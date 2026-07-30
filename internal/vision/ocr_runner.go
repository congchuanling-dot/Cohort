package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// DefaultOCRTimeout 限制单次 Python OCR 推理时间，防止工具调用长期占用 Runner。
	DefaultOCRTimeout = 20 * time.Second
	// maxOCRHelperOutputBytes 限制异常输出进入工具上下文的体积。
	maxOCRHelperOutputBytes = 8 * 1024
)

// OCRRequest 是传给 OCR helper 的受控请求参数。
type OCRRequest struct {
	// ImagePath 是工具层确认位于 workspace 内的输入图片。
	ImagePath string
	// MinConfidence 过滤低置信度文字，取值语义由 Python helper 定义。
	MinConfidence float64
	// Enhance 请求 helper 对图片执行增强预处理，适合低对比度截图。
	Enhance bool
}

// OCRLine 是单行文字及其在原始图片中的矩形区域。
// BBox 使用 [x1, y1, x2, y2]，坐标空间由调用工具声明。
type OCRLine struct {
	// Index 是结果中的一基行号，缺失时 Go 侧会按顺序补齐。
	Index int `json:"index"`
	// Text 是识别出的文本内容。
	Text string `json:"text"`
	// Confidence 是 helper 给出的识别置信度。
	Confidence float64 `json:"confidence"`
	// BBox 是 [x1,y1,x2,y2] 图片局部像素框，不能直接当作系统屏幕坐标。
	BBox []int `json:"bbox"`
	// Center 是 BBox 中心点，仍处于同一图片局部坐标系。
	Center Point `json:"center"`
}

// Point 表示图片内像素坐标。
type Point struct {
	// X 是图片内横坐标。
	X int `json:"x"`
	// Y 是图片内纵坐标。
	Y int `json:"y"`
}

// OCRResult 是 Python helper 输出的稳定结构。
type OCRResult struct {
	// Status 只能是 success 或 error。
	Status string `json:"status"`
	// Width 是原始图片像素宽度。
	Width int `json:"width"`
	// Height 是原始图片像素高度。
	Height int `json:"height"`
	// Text 是 helper 汇总的完整识别文本。
	Text string `json:"text"`
	// Lines 是带位置的逐行结果。
	Lines []OCRLine `json:"lines"`
}

// OCRRunner 抽象 OCR 引擎，便于工具层测试并隔离 Python 进程实现。
type OCRRunner interface {
	Run(ctx context.Context, request OCRRequest) (OCRResult, error)
}

// ToolError 表示可以直接转换为结构化工具错误的 OCR 失败。
type ToolError struct {
	// Code 是工具层映射为稳定错误结果的机器可读编码。
	Code string
	// Message 是展示给用户和模型的错误摘要。
	Message string
	// Hint 是安全的下一步排障建议。
	Hint string
}

// Error 让 ToolError 实现 Go 的 error，同时保留结构化诊断字段。
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// PythonOCRRunner 使用受控 Python helper 调用 RapidOCR。
type PythonOCRRunner struct {
	// Python 是运行 OCR helper 的解释器。
	Python string
	// ScriptPath 是受版本管理的 browser_ocr.py 路径。
	ScriptPath string
	// Timeout 限制单次 OCR 推理。
	Timeout time.Duration
}

// NewPythonOCRRunner 创建使用默认超时的 Python OCR runner。
func NewPythonOCRRunner(python string, scriptPath string, timeout time.Duration) *PythonOCRRunner {
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	if timeout <= 0 {
		timeout = DefaultOCRTimeout
	}
	return &PythonOCRRunner{
		Python:     python,
		ScriptPath: scriptPath,
		Timeout:    timeout,
	}
}

// Run 调用 Python helper，并把 JSON 结果转为稳定 Go 类型。
func (r *PythonOCRRunner) Run(ctx context.Context, request OCRRequest) (OCRResult, error) {
	if strings.TrimSpace(request.ImagePath) == "" {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_image_required",
			Message: "OCR image path is empty",
			Hint:    "请提供 workspace 内的图片路径，或让 browser_ocr 自动截取当前浏览器视口。",
		}
	}
	if strings.TrimSpace(r.ScriptPath) == "" {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_helper_missing",
			Message: "browser OCR helper path is not configured",
			Hint:    "请重新安装 Cohort，或设置 COHORT_BROWSER_OCR_HELPER_PATH / COHORT_RUNTIME_SCRIPTS_DIR 指向随包提供的 helper。",
		}
	}
	if _, err := os.Stat(r.ScriptPath); err != nil {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_helper_missing",
			Message: fmt.Sprintf("browser OCR helper is unavailable: %v", err),
			Hint:    "请重新安装 Cohort，或设置 COHORT_BROWSER_OCR_HELPER_PATH / COHORT_RUNTIME_SCRIPTS_DIR 指向随包提供的 helper。",
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	args := []string{
		r.ScriptPath,
		"--image", request.ImagePath,
		"--min-confidence", fmt.Sprintf("%.4f", request.MinConfidence),
	}
	if request.Enhance {
		args = append(args, "--enhance")
	}
	cmd := exec.CommandContext(runCtx, r.Python, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	result, parseErr := parseOCRResult(stdout.Bytes())
	if parseErr == nil && result.Status == "error" {
		return OCRResult{}, resultError(stdout.Bytes())
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_timeout",
			Message: fmt.Sprintf("OCR helper exceeded the %s timeout", r.Timeout),
			Hint:    "请使用更小的图片或关闭 full_page；复杂长图可以先裁剪后重试。",
		}
	}
	if err != nil {
		detail := compactHelperOutput(stderr.String())
		if detail == "" {
			detail = compactHelperOutput(stdout.String())
		}
		if strings.Contains(detail, "No module named") || strings.Contains(detail, "ModuleNotFoundError") {
			return OCRResult{}, &ToolError{
				Code:    "browser_ocr_dependency_missing",
				Message: "Python OCR dependencies are missing: " + detail,
				Hint:    "请手动安装依赖：python3 -m pip install rapidocr-onnxruntime pillow numpy。",
			}
		}
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_runner_failed",
			Message: fmt.Sprintf("OCR helper failed: %v%s", err, detailSuffix(detail)),
			Hint:    "请检查 Python、RapidOCR 依赖和图片文件是否可读；不要在工具执行中自动安装依赖。",
		}
	}
	if parseErr != nil {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_invalid_output",
			Message: "OCR helper returned invalid JSON: " + compactHelperOutput(stdout.String()),
			Hint:    "请检查 scripts/browser_ocr.py 输出；它必须只向 stdout 输出一份 JSON 结果。",
		}
	}
	if result.Status != "success" {
		return OCRResult{}, &ToolError{
			Code:    "browser_ocr_invalid_output",
			Message: "OCR helper returned an unknown status: " + result.Status,
			Hint:    "请检查 scripts/browser_ocr.py 的 JSON status 字段。",
		}
	}
	return result, nil
}

// parseOCRResult 校验 helper JSON 形状并补齐缺失的行索引。
// 这里早期拒绝非法 bbox，避免后续视觉点击把损坏坐标当作可信定位。
func parseOCRResult(data []byte) (OCRResult, error) {
	var result OCRResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&result); err != nil {
		return OCRResult{}, err
	}
	if result.Status == "error" {
		return result, nil
	}
	if result.Status != "success" {
		return OCRResult{}, fmt.Errorf("unexpected status %q", result.Status)
	}
	if result.Width < 0 || result.Height < 0 {
		return OCRResult{}, errors.New("negative image size")
	}
	for index := range result.Lines {
		line := &result.Lines[index]
		if len(line.BBox) != 4 {
			return OCRResult{}, fmt.Errorf("line %d has invalid bbox length", index)
		}
		if line.Index <= 0 {
			line.Index = index + 1
		}
	}
	return result, nil
}

// resultError 解析 helper 的 error JSON，并为缺失字段补上安全默认值。
func resultError(data []byte) error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return &ToolError{
			Code:    "browser_ocr_invalid_output",
			Message: "OCR helper returned an invalid error payload",
			Hint:    "请检查 scripts/browser_ocr.py 输出。",
		}
	}
	if strings.TrimSpace(payload.Code) == "" {
		payload.Code = "browser_ocr_runner_failed"
	}
	if strings.TrimSpace(payload.Message) == "" {
		payload.Message = "OCR helper returned an unspecified error"
	}
	if strings.TrimSpace(payload.Hint) == "" {
		payload.Hint = "请检查 Python、RapidOCR 依赖和图片文件是否可读。"
	}
	return &ToolError{Code: payload.Code, Message: payload.Message, Hint: payload.Hint}
}

// compactHelperOutput 截断异常输出，防止依赖堆栈过度占用模型上下文。
func compactHelperOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxOCRHelperOutputBytes {
		return value
	}
	return value[:maxOCRHelperOutputBytes] + "...[truncated]"
}

// detailSuffix 在存在诊断文本时提供可读分隔符。
func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}
