package browser

import (
	"context"
	"fmt"
)

// UnavailableClient 在 bridge server 启动失败时使用。
// 这样 Cohort 仍能启动，模型调用浏览器工具时会拿到明确错误，而不是整个 Agent 崩掉。
type UnavailableClient struct {
	// Err 是 bridge 初始化失败的原始错误；为空时退化为未连接错误。
	Err error
}

func NewUnavailableClient(err error) *UnavailableClient {
	return &UnavailableClient{Err: err}
}

func (c *UnavailableClient) Tabs(ctx context.Context) ([]Tab, error) {
	return nil, c.error()
}

func (c *UnavailableClient) Open(ctx context.Context, url string, tabID string, active bool) (OpenResult, error) {
	return OpenResult{}, c.error()
}

func (c *UnavailableClient) Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error) {
	return PageSnapshot{}, c.error()
}

func (c *UnavailableClient) DOMSummary(ctx context.Context, tabID string, maxChars int, includeIframes bool, includeShadowDOM bool, includeFormValues bool, includeFixedOverlays bool) (DOMSummary, error) {
	return DOMSummary{}, c.error()
}

func (c *UnavailableClient) ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error) {
	return ExecuteJSResult{}, c.error()
}

func (c *UnavailableClient) CDP(ctx context.Context, tabID string, method string, params map[string]any, noMonitor bool) (CDPResult, error) {
	return CDPResult{}, c.error()
}

func (c *UnavailableClient) Click(ctx context.Context, tabID string, x float64, y float64, noMonitor bool) (ClickResult, error) {
	return ClickResult{}, c.error()
}

func (c *UnavailableClient) Type(ctx context.Context, tabID string, text string, clear bool, noMonitor bool) (TypeResult, error) {
	return TypeResult{}, c.error()
}

func (c *UnavailableClient) PressKey(ctx context.Context, tabID string, key string, noMonitor bool) (PressKeyResult, error) {
	return PressKeyResult{}, c.error()
}

func (c *UnavailableClient) Snapshot(ctx context.Context, tabID string, maxElements int) (InteractiveSnapshot, error) {
	return InteractiveSnapshot{}, c.error()
}

func (c *UnavailableClient) Screenshot(ctx context.Context, tabID string, format string, fullPage bool, quality int) (ScreenshotResult, error) {
	return ScreenshotResult{}, c.error()
}

func (c *UnavailableClient) Wait(ctx context.Context, tabID string, mode string, params map[string]any, timeoutMS int, intervalMS int) (WaitResult, error) {
	return WaitResult{}, c.error()
}

func (c *UnavailableClient) error() error {
	if c == nil || c.Err == nil {
		return ErrNotConnected
	}
	return fmt.Errorf("browser bridge unavailable: %w", c.Err)
}
