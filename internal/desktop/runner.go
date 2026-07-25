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
	Code    string
	Message string
	Hint    string
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// PythonDriver 通过受控 JSON 协议调用 macOS helper。
// Go 侧拥有工具边界和文件路径，Python 侧只调用平台 API。
type PythonDriver struct {
	Python     string
	ScriptPath string
	Timeout    time.Duration
}

func NewPythonDriver(python string, scriptPath string, timeout time.Duration) *PythonDriver {
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &PythonDriver{Python: python, ScriptPath: scriptPath, Timeout: timeout}
}

func (d *PythonDriver) Permissions(ctx context.Context) (PermissionsResult, error) {
	var result PermissionsResult
	return result, d.call(ctx, "permissions", map[string]any{}, &result)
}

func (d *PythonDriver) ListWindows(ctx context.Context, req ListWindowsRequest) (ListWindowsResult, error) {
	var result ListWindowsResult
	return result, d.call(ctx, "list_windows", req, &result)
}

func (d *PythonDriver) Activate(ctx context.Context, req ActivateRequest) (ActivateResult, error) {
	var result ActivateResult
	return result, d.call(ctx, "activate", req, &result)
}

func (d *PythonDriver) Screenshot(ctx context.Context, req ScreenshotRequest) (ScreenshotResult, error) {
	var result ScreenshotResult
	return result, d.call(ctx, "screenshot", req, &result)
}

func (d *PythonDriver) AXSnapshot(ctx context.Context, req AXSnapshotRequest) (AXSnapshotResult, error) {
	var result AXSnapshotResult
	return result, d.call(ctx, "ax_snapshot", req, &result)
}

func (d *PythonDriver) AXPress(ctx context.Context, req AXPressRequest) (AXPressResult, error) {
	var result AXPressResult
	return result, d.call(ctx, "ax_press", req, &result)
}

func (d *PythonDriver) PressKey(ctx context.Context, req PressKeyRequest) (PressKeyResult, error) {
	var result PressKeyResult
	return result, d.call(ctx, "press_key", req, &result)
}

type helperEnvelope struct {
	Status  string          `json:"status"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Hint    string          `json:"hint"`
	Data    json.RawMessage `json:"data"`
}

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
			Hint:    "请确认 scripts/desktop_darwin.py 随 Cohert 项目一起存在。",
		}
	}
	if _, err := os.Stat(d.ScriptPath); err != nil {
		return &ToolError{
			Code:    "desktop_helper_missing",
			Message: fmt.Sprintf("desktop helper is unavailable: %v", err),
			Hint:    "请确认 scripts/desktop_darwin.py 存在且当前运行目录是 Cohert 项目根目录。",
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

func compactHelperOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHelperOutputBytes {
		return value
	}
	return value[:maxHelperOutputBytes] + "...[truncated]"
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}
