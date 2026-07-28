package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/desktop"
	"cohort/internal/llm"
	"cohort/internal/vision"
)

const (
	// defaultDesktopWindowLimit 是一次窗口枚举的默认返回上限。
	defaultDesktopWindowLimit = 50
	// maxDesktopWindowLimit 防止模型请求无界窗口列表。
	maxDesktopWindowLimit = 100
	// defaultDesktopAXDepth 是辅助功能树读取的默认深度。
	defaultDesktopAXDepth = 8
	// maxDesktopAXDepth 限制递归快照的最深层级。
	maxDesktopAXDepth = 12
	// defaultDesktopAXNodes 是辅助功能树默认节点上限。
	defaultDesktopAXNodes = 300
	// maxDesktopAXNodes 是辅助功能树可返回的最大节点数。
	maxDesktopAXNodes = 500
	// defaultDesktopScreenshotDir 是工作区内桌面截图和 manifest 的固定目录。
	defaultDesktopScreenshotDir    = ".cohort/desktop/screenshots"
	defaultDesktopOCRMinConfidence = 0.5
	defaultDesktopOCRMaxLines      = 80
	defaultDesktopOCRMaxChars      = 8000
	maxDesktopOCRLines             = 200
	maxDesktopOCRChars             = 12000
)

// desktopScreenshotManifest 是截图局部 bbox 映射为物理屏幕坐标所需的不可变证据。
// 视觉点击必须读取并校验它，不能仅凭模型传来的图片路径和坐标执行。
type desktopScreenshotManifest struct {
	// Version 允许未来安全演进 manifest 格式。
	Version int `json:"version"`
	// ImagePath 将 manifest 绑定到唯一截图文件。
	ImagePath string `json:"image_path"`
	// PID 将截图绑定到产生它的应用进程。
	PID int `json:"pid"`
	// WindowID 将截图绑定到进程中的具体窗口。
	WindowID string `json:"window_id"`
	// Width 是截图像素宽度。
	Width int `json:"width"`
	// Height 是截图像素高度。
	Height int `json:"height"`
	// WindowBounds 是截图时窗口在物理屏幕中的边界。
	WindowBounds desktop.Bounds `json:"window_bounds"`
	// CoordinateSpace 说明 OCR bbox 使用的截图局部坐标系。
	CoordinateSpace string `json:"coordinate_space"`
	// ScreenCoordinateSpace 说明 WindowBounds 使用的物理屏幕坐标系。
	ScreenCoordinateSpace string `json:"screen_coordinate_space"`
	// CreatedAt 是截图生成时间，主要用于审计和排查旧图复用。
	CreatedAt string `json:"created_at"`
}

// DesktopPermissions 检查桌面感知所需的 macOS 权限。
type DesktopPermissions struct {
	// driver 查询系统权限而不执行任何桌面输入。
	driver desktop.Driver
}

// NewDesktopPermissions 创建只读权限探测工具。
func NewDesktopPermissions(driver desktop.Driver) *DesktopPermissions {
	return &DesktopPermissions{driver: driver}
}

func (t *DesktopPermissions) Name() string { return ToolNameDesktopPermissions }

func (t *DesktopPermissions) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Check macOS accessibility and screen-recording permissions required for read-only desktop sensing. Run this before desktop screenshots or AX snapshots when desktop capability is uncertain.",
		Parameters:  objectSchema(map[string]any{}),
	}}
}

func (t *DesktopPermissions) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	result, err := t.driver.Permissions(ctx)
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"platform":         result.Platform,
			"accessibility":    result.Accessibility,
			"screen_recording": result.ScreenRecording,
			"input_monitoring": result.InputMonitoring,
			"missing":          result.Missing,
			"hints":            result.Hints,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopWindows 枚举可见普通应用窗口，供模型定位目标 PID。
type DesktopWindows struct {
	// driver 枚举可见窗口。
	driver desktop.Driver
}

// NewDesktopWindows 创建窗口枚举工具。
func NewDesktopWindows(driver desktop.Driver) *DesktopWindows {
	return &DesktopWindows{driver: driver}
}

func (t *DesktopWindows) Name() string { return ToolNameDesktopWindows }

func (t *DesktopWindows) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "List visible desktop application windows. Use this before desktop activation or sensing to obtain the target PID and window ID.",
		Parameters: objectSchema(map[string]any{
			"app_name": stringProp("Optional case-insensitive application-name substring filter."),
			"title":    stringProp("Optional case-insensitive window-title substring filter."),
			"limit":    intProp("Maximum windows to return. Default 50, max 100.", defaultDesktopWindowLimit),
		}),
	}}
}

func (t *DesktopWindows) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	limit := clampDesktopLimit(asInt(call.Args["limit"], defaultDesktopWindowLimit), defaultDesktopWindowLimit, maxDesktopWindowLimit)
	result, err := t.driver.ListWindows(ctx, desktop.ListWindowsRequest{
		AppName: strings.TrimSpace(asString(call.Args["app_name"])),
		Title:   strings.TrimSpace(asString(call.Args["title"])),
		Limit:   limit,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"windows":          result.Windows,
			"count":            len(result.Windows),
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopActivate 将一个 PID 对应的应用带到前台，并验证结果。
type DesktopActivate struct {
	// driver 负责把指定应用带到前台。
	driver desktop.Driver
}

// NewDesktopActivate 创建前台激活工具。
func NewDesktopActivate(driver desktop.Driver) *DesktopActivate {
	return &DesktopActivate{driver: driver}
}

func (t *DesktopActivate) Name() string { return ToolNameDesktopActivate }

func (t *DesktopActivate) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Activate a desktop application by PID and verify it became frontmost. Always run this before desktop screenshots or any future desktop input action.",
		Parameters: objectSchema(map[string]any{
			"pid":       intProp("Target application PID from desktop_windows.", 0),
			"window_id": stringProp("Optional window ID for result correlation; macOS activation is application-scoped."),
		}, "pid"),
	}}
}

func (t *DesktopActivate) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	result, err := t.driver.Activate(ctx, desktop.ActivateRequest{
		PID:      pid,
		WindowID: strings.TrimSpace(asString(call.Args["window_id"])),
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":   agent.ToolStatusSuccess,
			"pid":      result.PID,
			"active":   result.Active,
			"verified": result.Verified,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopScreenshot 捕获指定 PID 的一个窗口，并由 Go 侧固定保存路径。
type DesktopScreenshot struct {
	// driver 执行平台截图。
	driver desktop.Driver
	// workspaceTool 约束截图落盘位置。
	workspaceTool
}

// NewDesktopScreenshot 创建受工作区路径约束的截图工具。
func NewDesktopScreenshot(driver desktop.Driver, workspace string) *DesktopScreenshot {
	return &DesktopScreenshot{driver: driver, workspaceTool: newWorkspaceTool(workspace)}
}

func (t *DesktopScreenshot) Name() string { return ToolNameDesktopScreenshot }

func (t *DesktopScreenshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Capture a visible window for a target PID and save it under the workspace. Activate the PID first. The returned OCR coordinates are screenshot-local, not screen coordinates.",
		Parameters: objectSchema(map[string]any{
			"pid":       intProp("Target application PID from desktop_windows.", 0),
			"window_id": stringProp("Optional visible window ID. If empty, captures the first visible window for this PID."),
		}, "pid"),
	}}
}

func (t *DesktopScreenshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	dir := filepath.Join(t.workspace, defaultDesktopScreenshotDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_save_failed",
				fmt.Sprintf("create desktop screenshot directory: %v", err),
				"请检查 workspace 是否可写。",
			),
			NextPrompt: "\n",
		}, nil
	}
	outputPath := filepath.Join(dir, fmt.Sprintf("desktop_screenshot_%d.png", time.Now().UnixNano()))
	result, err := t.driver.Screenshot(ctx, desktop.ScreenshotRequest{
		PID:        pid,
		WindowID:   strings.TrimSpace(asString(call.Args["window_id"])),
		OutputPath: outputPath,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_save_failed",
				fmt.Sprintf("desktop helper did not create the requested screenshot: %v", err),
				"请确认已授予屏幕录制权限，并重新调用 desktop_permissions 检查。",
			),
			NextPrompt: "\n",
		}, nil
	}
	manifestPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".json"
	manifest := desktopScreenshotManifest{
		Version:               1,
		ImagePath:             outputPath,
		PID:                   result.PID,
		WindowID:              result.WindowID,
		Width:                 result.Width,
		Height:                result.Height,
		WindowBounds:          result.Bounds,
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeDesktopScreenshotManifest(manifestPath, manifest); err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_manifest_failed",
				fmt.Sprintf("write desktop screenshot manifest: %v", err),
				"请检查 workspace 是否可写；没有 manifest 的截图不能用于 desktop_visual_click。",
			),
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":                  agent.ToolStatusSuccess,
			"image_path":              outputPath,
			"manifest_path":           manifestPath,
			"width":                   result.Width,
			"height":                  result.Height,
			"window_id":               result.WindowID,
			"pid":                     result.PID,
			"window_bounds":           result.Bounds,
			"coordinate_space":        desktop.CoordinateSpaceScreenshotLocal,
			"screen_coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"bytes":                   info.Size(),
		},
		NextPrompt: "\n",
	}, nil
}

// writeDesktopScreenshotManifest 以格式化 JSON 写入截图旁车文件，便于人工审计坐标来源。
func writeDesktopScreenshotManifest(path string, manifest desktopScreenshotManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// DesktopAXSnapshot 返回目标应用的受限 AX 控件树。
type DesktopAXSnapshot struct {
	// driver 读取平台辅助功能树。
	driver desktop.Driver
}

// NewDesktopAXSnapshot 创建只读且有深度/节点上限的 AX 快照工具。
func NewDesktopAXSnapshot(driver desktop.Driver) *DesktopAXSnapshot {
	return &DesktopAXSnapshot{driver: driver}
}

func (t *DesktopAXSnapshot) Name() string { return ToolNameDesktopAXSnapshot }

func (t *DesktopAXSnapshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read a bounded macOS Accessibility control tree for a target PID. Prefer this semantic snapshot before screenshot/OCR; secure text values are never returned.",
		Parameters: objectSchema(map[string]any{
			"pid":               intProp("Target application PID from desktop_windows.", 0),
			"max_depth":         intProp("Maximum tree depth. Default 8, max 12.", defaultDesktopAXDepth),
			"max_nodes":         intProp("Maximum returned nodes. Default 300, max 500.", defaultDesktopAXNodes),
			"include_zero_size": boolProp("Include zero-size structural nodes. Default false.", false),
		}, "pid"),
	}}
}

func (t *DesktopAXSnapshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	result, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        clampDesktopLimit(asInt(call.Args["max_depth"], defaultDesktopAXDepth), defaultDesktopAXDepth, maxDesktopAXDepth),
		MaxNodes:        clampDesktopLimit(asInt(call.Args["max_nodes"], defaultDesktopAXNodes), defaultDesktopAXNodes, maxDesktopAXNodes),
		IncludeZeroSize: asBool(call.Args["include_zero_size"], false),
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"pid":              result.PID,
			"coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"root":             result.Root,
			"node_count":       result.NodeCount,
			"truncated":        result.Truncated,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopOCR 对已经保存到 workspace 的桌面截图执行只读 OCR。
type DesktopOCR struct {
	// workspaceTool 解析并限制图片路径。
	workspaceTool
	// runner 是可替换的 OCR 引擎接口。
	runner vision.OCRRunner
}

// NewDesktopOCR 构造默认 Python OCR runner，并从项目根目录定位 helper 脚本。
func NewDesktopOCR(workspace string) *DesktopOCR {
	workspaceRoot := newWorkspaceTool(workspace).workspace
	scriptPath := filepath.Join("scripts", "browser_ocr.py")
	if root := findGitRoot(workspaceRoot); root != "" {
		scriptPath = filepath.Join(root, "scripts", "browser_ocr.py")
	} else if absolutePath, err := filepath.Abs(scriptPath); err == nil {
		scriptPath = absolutePath
	}
	return NewDesktopOCRWithRunner(
		workspace,
		vision.NewPythonOCRRunner("python3", scriptPath, vision.DefaultOCRTimeout),
	)
}

// NewDesktopOCRWithRunner 允许测试或未来实现注入不同 OCR 引擎。
func NewDesktopOCRWithRunner(workspace string, runner vision.OCRRunner) *DesktopOCR {
	return &DesktopOCR{workspaceTool: newWorkspaceTool(workspace), runner: runner}
}

func (t *DesktopOCR) Name() string { return ToolNameDesktopOCR }

func (t *DesktopOCR) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Run read-only OCR on a desktop screenshot already saved under the workspace. Bounding boxes are screenshot-local and must be converted by a later controlled tool before any input action.",
		Parameters: objectSchema(map[string]any{
			"image_path":     stringProp("Required screenshot path under the workspace, usually returned by desktop_screenshot."),
			"min_confidence": floatProp("Minimum OCR confidence from 0 to 1. Default 0.5.", defaultDesktopOCRMinConfidence),
			"max_lines":      intProp("Maximum OCR lines to return. Default 80, max 200.", defaultDesktopOCRMaxLines),
			"max_chars":      intProp("Maximum OCR text characters to return. Default 8000, max 12000.", defaultDesktopOCRMaxChars),
			"enhance":        boolProp("Apply contrast and scale preprocessing. Default false.", false),
		}, "image_path"),
	}}
}

func (t *DesktopOCR) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if t.runner == nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_unavailable",
				"OCR runner is not configured",
				"请确认 Cohort 随附的 scripts/browser_ocr.py 可用，并已配置 Python OCR 环境。",
			),
			NextPrompt: "\n",
		}, nil
	}
	imagePath, pathErr := t.resolveImagePath(strings.TrimSpace(asString(call.Args["image_path"])))
	if pathErr != nil {
		return agent.Outcome{Data: *pathErr, NextPrompt: "\n"}, nil
	}
	minConfidence := asFloat(call.Args["min_confidence"], defaultDesktopOCRMinConfidence)
	if math.IsNaN(minConfidence) || minConfidence < 0 || minConfidence > 1 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_bad_min_confidence",
				"min_confidence must be between 0 and 1",
				"请传入 0 到 1 之间的数值，默认 0.5。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.runner.Run(ctx, vision.OCRRequest{
		ImagePath:     imagePath,
		MinConfidence: minConfidence,
		Enhance:       asBool(call.Args["enhance"], false),
	})
	if err != nil {
		var ocrErr *vision.ToolError
		if errors.As(err, &ocrErr) {
			return agent.Outcome{
				Data: agent.NewToolError(
					strings.Replace(ocrErr.Code, "browser_ocr_", "desktop_ocr_", 1),
					ocrErr.Message,
					"请检查桌面截图、Python OCR 依赖和 helper 状态；不要在工具执行中自动安装依赖。",
				),
				NextPrompt: "\n",
			}, nil
		}
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_failed",
				err.Error(),
				"请检查图片是否可读、Python OCR helper 是否可用，并根据错误决定是否重试。",
			),
			NextPrompt: "\n",
		}, nil
	}
	maxLines := clampDesktopLimit(asInt(call.Args["max_lines"], defaultDesktopOCRMaxLines), defaultDesktopOCRMaxLines, maxDesktopOCRLines)
	maxChars := clampDesktopLimit(asInt(call.Args["max_chars"], defaultDesktopOCRMaxChars), defaultDesktopOCRMaxChars, maxDesktopOCRChars)
	lines := result.Lines
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	text, textTruncated := truncateBrowserOCRText(joinOCRLines(lines), maxChars)
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"image_path":       imagePath,
			"coordinate_space": desktop.CoordinateSpaceScreenshotLocal,
			"width":            result.Width,
			"height":           result.Height,
			"text":             text,
			"lines":            lines,
			"line_count":       len(lines),
			"total_lines":      len(result.Lines),
			"truncated":        truncated || textTruncated,
		},
		NextPrompt: "\n",
	}, nil
}

// resolveImagePath 同时验证词法路径和符号链接真实路径均留在 workspace 内。
func (t *DesktopOCR) resolveImagePath(rawPath string) (string, *agent.ToolErrorData) {
	if rawPath == "" {
		err := agent.NewToolError(
			"desktop_ocr_image_required",
			"desktop_ocr requires an image_path returned by desktop_screenshot",
			"请先调用 desktop_screenshot，再将其 image_path 传给 desktop_ocr。",
		)
		return "", &err
	}
	path := t.resolve(rawPath)
	rel, err := filepath.Rel(t.workspace, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		toolErr := agent.NewToolError(
			"desktop_ocr_path_outside_workspace",
			"desktop OCR image_path must stay inside the configured workspace",
			"请提供 desktop_screenshot 返回的 workspace 内路径。",
		)
		return "", &toolErr
	}
	if _, err := os.Stat(path); err != nil {
		toolErr := agent.NewToolError(
			"desktop_ocr_image_not_found",
			fmt.Sprintf("desktop OCR image is unavailable: %v", err),
			"请确认截图仍存在，或重新调用 desktop_screenshot。",
		)
		return "", &toolErr
	}
	realWorkspace, workspaceErr := filepath.EvalSymlinks(t.workspace)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if workspaceErr == nil && pathErr == nil {
		realRel, err := filepath.Rel(realWorkspace, realPath)
		if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			toolErr := agent.NewToolError(
				"desktop_ocr_path_outside_workspace",
				"desktop OCR image resolves outside the configured workspace",
				"图片路径不能通过符号链接逃出 workspace；请复制图片到 workspace 后重试。",
			)
			return "", &toolErr
		}
	}
	return path, nil
}

// desktopBadPIDOutcome 统一提示模型先通过 desktop_windows 获取有效目标进程。
func desktopBadPIDOutcome(toolName string) agent.Outcome {
	return agent.Outcome{
		Data: agent.NewToolError(
			"desktop_bad_pid",
			toolName+" requires a positive pid",
			"请先调用 desktop_windows，选择目标窗口返回的 pid。",
		),
		NextPrompt: "\n",
	}
}

// desktopToolError 将平台结构化错误转换为通用工具错误，其余 error 使用保守诊断提示。
func desktopToolError(err error) agent.Outcome {
	var toolErr *desktop.ToolError
	if errors.As(err, &toolErr) {
		return agent.Outcome{
			Data:       agent.NewToolError(toolErr.Code, toolErr.Message, toolErr.Hint),
			NextPrompt: "\n",
		}
	}
	return agent.Outcome{
		Data: agent.NewToolError(
			"desktop_failed",
			err.Error(),
			"请检查 desktop 权限、目标 PID 和 macOS helper 状态，再决定是否重试。",
		),
		NextPrompt: "\n",
	}
}

// clampDesktopLimit 对模型给出的数量参数应用默认值与硬上限。
func clampDesktopLimit(value int, fallback int, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
