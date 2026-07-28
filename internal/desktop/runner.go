package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultTimeout 限制单次桌面 helper 调用，避免 Agent 长时间阻塞。
	DefaultTimeout       = 15 * time.Second
	maxHelperOutputBytes = 8 * 1024
)

// ToolError 是可安全返回给模型的桌面 helper 错误。
type ToolError struct {
	// Code 是可供上层稳定分支处理的机器可读错误码。
	Code string
	// Message 是可展示给模型和用户的错误概述。
	Message string
	// Hint 是安全的下一步修复建议，不含原始敏感输出。
	Hint string
}

// Error 让 ToolError 满足 Go 的 error 接口，同时保留结构化字段给工具层映射。
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// PythonDriver 通过受控 JSON 协议调用 macOS helper。
// Go 侧拥有工具边界和文件路径，Python 侧只调用平台 API。
type PythonDriver struct {
	// Python 是启动 helper 的 Python 可执行文件名或绝对路径。
	Python string
	// ScriptPath 是受版本管理的 macOS helper 脚本路径。
	ScriptPath string
	// Timeout 是单次 helper 进程的最长运行时间。
	Timeout time.Duration
}

// NewPythonDriver 使用安全默认值构造平台驱动。
// 真正的系统调用不会发生在这里，而在每次方法调用的 d.call 中。
func NewPythonDriver(python string, scriptPath string, timeout time.Duration) *PythonDriver {
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &PythonDriver{Python: python, ScriptPath: scriptPath, Timeout: timeout}
}

// Permissions 查询桌面自动化所需的系统权限。
func (d *PythonDriver) Permissions(ctx context.Context) (PermissionsResult, error) {
	var result PermissionsResult
	return result, d.call(ctx, "permissions", map[string]any{}, &result)
}

// ListWindows 枚举目标应用窗口，供上层先定位并激活正确进程。
func (d *PythonDriver) ListWindows(ctx context.Context, req ListWindowsRequest) (ListWindowsResult, error) {
	var result ListWindowsResult
	return result, d.call(ctx, "list_windows", req, &result)
}

// Activate 将指定应用或窗口带到前台。
func (d *PythonDriver) Activate(ctx context.Context, req ActivateRequest) (ActivateResult, error) {
	var result ActivateResult
	return result, d.call(ctx, "activate", req, &result)
}

// Screenshot 让 helper 生成窗口截图，路径始终由工具层提前限制在 workspace 内。
func (d *PythonDriver) Screenshot(ctx context.Context, req ScreenshotRequest) (ScreenshotResult, error) {
	var result ScreenshotResult
	return result, d.call(ctx, "screenshot", req, &result)
}

// AXSnapshot 读取目标应用的辅助功能树，供上层基于语义而非任意坐标操作界面。
func (d *PythonDriver) AXSnapshot(ctx context.Context, req AXSnapshotRequest) (AXSnapshotResult, error) {
	var result AXSnapshotResult
	return result, d.call(ctx, "ax_snapshot", req, &result)
}

// AXPress 对已核验的辅助功能节点执行受限 Press 动作。
func (d *PythonDriver) AXPress(ctx context.Context, req AXPressRequest) (AXPressResult, error) {
	var result AXPressResult
	return result, d.call(ctx, "ax_press", req, &result)
}

// AXFocus 聚焦已核验的可编辑辅助功能节点。
func (d *PythonDriver) AXFocus(ctx context.Context, req AXFocusRequest) (AXFocusResult, error) {
	var result AXFocusResult
	return result, d.call(ctx, "ax_focus", req, &result)
}

// Click 在已核验的辅助功能节点中心执行点击。
func (d *PythonDriver) Click(ctx context.Context, req ClickRequest) (ClickResult, error) {
	var result ClickResult
	return result, d.call(ctx, "click", req, &result)
}

// VisualClick 在截图映射后的物理屏幕坐标执行点击。
func (d *PythonDriver) VisualClick(ctx context.Context, req VisualClickRequest) (VisualClickResult, error) {
	var result VisualClickResult
	return result, d.call(ctx, "visual_click", req, &result)
}

// DoubleClick 在高层解析出的物理屏幕点执行双击。
func (d *PythonDriver) DoubleClick(ctx context.Context, req DoubleClickRequest) (DoubleClickResult, error) {
	var result DoubleClickResult
	return result, d.call(ctx, "double_click", req, &result)
}

// RightClick 在高层解析出的物理屏幕点执行右键点击。
func (d *PythonDriver) RightClick(ctx context.Context, req RightClickRequest) (RightClickResult, error) {
	var result RightClickResult
	return result, d.call(ctx, "right_click", req, &result)
}

// PressKey 向已激活应用发送由工具层白名单控制的按键。
func (d *PythonDriver) PressKey(ctx context.Context, req PressKeyRequest) (PressKeyResult, error) {
	var result PressKeyResult
	return result, d.call(ctx, "press_key", req, &result)
}

// TypeText 向当前焦点输入文本；是否允许视觉焦点由上层令牌控制。
func (d *PythonDriver) TypeText(ctx context.Context, req TypeTextRequest) (TypeTextResult, error) {
	var result TypeTextResult
	return result, d.call(ctx, "type_text", req, &result)
}

// Scroll 在已激活应用中发送受限滚轮事件。
func (d *PythonDriver) Scroll(ctx context.Context, req ScrollRequest) (ScrollResult, error) {
	var result ScrollResult
	return result, d.call(ctx, "scroll", req, &result)
}

// Drag 在已激活应用中从一个受控物理点拖拽到另一个物理点。
func (d *PythonDriver) Drag(ctx context.Context, req DragRequest) (DragResult, error) {
	var result DragResult
	return result, d.call(ctx, "drag", req, &result)
}

// ClipboardWrite 写入系统剪贴板，不读取或返回原剪贴板内容。
func (d *PythonDriver) ClipboardWrite(ctx context.Context, req ClipboardWriteRequest) (ClipboardWriteResult, error) {
	var result ClipboardWriteResult
	return result, d.call(ctx, "clipboard_write", req, &result)
}

// ClipboardPaste 对已激活应用发送 Cmd+V。
func (d *PythonDriver) ClipboardPaste(ctx context.Context, req ClipboardPasteRequest) (ClipboardPasteResult, error) {
	var result ClipboardPasteResult
	return result, d.call(ctx, "clipboard_paste", req, &result)
}

// MenuSelect 通过 AX 菜单栏选择受控菜单路径。
func (d *PythonDriver) MenuSelect(ctx context.Context, req MenuSelectRequest) (MenuSelectResult, error) {
	var result MenuSelectResult
	return result, d.call(ctx, "menu_select", req, &result)
}

// FileDialog 在当前文件对话框中跳转路径并可选确认。
func (d *PythonDriver) FileDialog(ctx context.Context, req FileDialogRequest) (FileDialogResult, error) {
	var result FileDialogResult
	return result, d.call(ctx, "file_dialog", req, &result)
}

// WindowMove 通过 AX 移动目标窗口。
func (d *PythonDriver) WindowMove(ctx context.Context, req WindowMoveRequest) (WindowMoveResult, error) {
	var result WindowMoveResult
	return result, d.call(ctx, "window_move", req, &result)
}

// WindowResize 通过 AX 调整目标窗口尺寸。
func (d *PythonDriver) WindowResize(ctx context.Context, req WindowResizeRequest) (WindowResizeResult, error) {
	var result WindowResizeResult
	return result, d.call(ctx, "window_resize", req, &result)
}

// helperEnvelope 是 Go 与 Python helper 之间固定的一层 JSON 返回协议。
// 无论成功或失败，helper 都必须把业务数据放在该封装内，避免 Go 侧猜测 stderr。
type helperEnvelope struct {
	// Status 只能是 success 或 error。
	Status string `json:"status"`
	// Code 是 error 状态下的结构化错误码。
	Code string `json:"code"`
	// Message 是 helper 的错误说明。
	Message string `json:"message"`
	// Hint 是 helper 提供的修复建议。
	Hint string `json:"hint"`
	// Data 是 success 状态下延迟反序列化的具体结果。
	Data json.RawMessage `json:"data"`
}

// call 是所有平台方法共享的子进程边界。
//
// 它负责平台检查、请求 JSON 序列化、超时、stdout 协议解析和错误脱敏；
// 各公开方法只决定命令名、请求类型和结果类型，避免安全策略分散。
func (d *PythonDriver) call(ctx context.Context, command string, request any, target any) error {
	if runtime.GOOS != "darwin" {
		return &ToolError{
			Code:    "desktop_platform_unsupported",
			Message: "desktop computer-use M1 currently supports macOS only",
			Hint:    "请在 macOS 上运行，或等待 Windows/Linux desktop driver 实现。",
		}
	}
	if strings.TrimSpace(d.ScriptPath) == "" {
		return &ToolError{
			Code:    "desktop_helper_missing",
			Message: "desktop helper path is not configured",
			Hint:    "请确认 scripts/desktop_darwin.py 随 Cohort 项目一起存在。",
		}
	}
	if _, err := os.Stat(d.ScriptPath); err != nil {
		return &ToolError{
			Code:    "desktop_helper_missing",
			Message: fmt.Sprintf("desktop helper is unavailable: %v", err),
			Hint:    "请确认 scripts/desktop_darwin.py 存在且当前运行目录是 Cohort 项目根目录。",
		}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal desktop helper request: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, d.Python, d.ScriptPath, "--command", command, "--json", string(payload))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	envelope, parseErr := parseHelperEnvelope(stdout.Bytes())
	if parseErr == nil && envelope.Status == "error" {
		return envelope.toolError()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return &ToolError{
			Code:    "desktop_helper_timeout",
			Message: fmt.Sprintf("desktop helper exceeded the %s timeout", d.Timeout),
			Hint:    "请缩小窗口快照范围、降低 AX 节点上限，或检查目标应用是否无响应。",
		}
	}
	if runErr != nil {
		detail := compactHelperOutput(stderr.String())
		if detail == "" {
			detail = compactHelperOutput(stdout.String())
		}
		if strings.Contains(detail, "No module named") || strings.Contains(detail, "ModuleNotFoundError") {
			return &ToolError{
				Code:    "desktop_dependency_missing",
				Message: "macOS desktop helper dependencies are missing: " + detail,
				Hint:    "请手动安装依赖：python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices。",
			}
		}
		return &ToolError{
			Code:    "desktop_helper_failed",
			Message: fmt.Sprintf("desktop helper failed: %v%s", runErr, detailSuffix(detail)),
			Hint:    "请检查 macOS 权限、Python PyObjC 依赖和目标应用状态；不要在工具执行中自动安装依赖。",
		}
	}
	if parseErr != nil {
		return &ToolError{
			Code:    "desktop_helper_invalid_output",
			Message: "desktop helper returned invalid JSON: " + compactHelperOutput(stdout.String()),
			Hint:    "请检查 scripts/desktop_darwin.py；它必须只向 stdout 输出一份 JSON 结果。",
		}
	}
	if envelope.Status != "success" {
		return &ToolError{
			Code:    "desktop_helper_invalid_output",
			Message: "desktop helper returned an unknown status: " + envelope.Status,
			Hint:    "请检查 scripts/desktop_darwin.py 的 JSON status 字段。",
		}
	}
	if len(envelope.Data) == 0 {
		return errors.New("desktop helper returned empty data")
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("decode desktop helper data: %w", err)
	}
	return nil
}

// parseHelperEnvelope 只接受 helper 协议规定的两种状态，尽早拒绝脚本误输出。
func parseHelperEnvelope(data []byte) (helperEnvelope, error) {
	var envelope helperEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return helperEnvelope{}, err
	}
	if envelope.Status != "success" && envelope.Status != "error" {
		return helperEnvelope{}, fmt.Errorf("unexpected helper status %q", envelope.Status)
	}
	return envelope, nil
}

// toolError 将 helper 的 error 包装为调用方可以安全序列化的结构化错误。
func (e helperEnvelope) toolError() error {
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = "desktop_helper_failed"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "desktop helper returned an unspecified error"
	}
	hint := strings.TrimSpace(e.Hint)
	if hint == "" {
		hint = "请检查 macOS 权限、PyObjC 依赖和目标应用状态。"
	}
	return &ToolError{Code: code, Message: message, Hint: hint}
}

// compactHelperOutput 限制诊断文本大小，防止异常堆栈淹没模型上下文或工具结果。
func compactHelperOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHelperOutputBytes {
		return value
	}
	return value[:maxHelperOutputBytes] + "...[truncated]"
}

// detailSuffix 仅在诊断文本非空时添加分隔符，保持错误消息可读。
func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}
