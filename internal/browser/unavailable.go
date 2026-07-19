package browser

import (
	"context"
	"fmt"
)

// UnavailableClient 在 bridge server 启动失败时使用。
// 这样 Cohert 仍能启动，模型调用浏览器工具时会拿到明确错误，而不是整个 Agent 崩掉。
type UnavailableClient struct {
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

func (c *UnavailableClient) ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error) {
	return ExecuteJSResult{}, c.error()
}

func (c *UnavailableClient) error() error {
	if c == nil || c.Err == nil {
		return ErrNotConnected
	}
	return fmt.Errorf("browser bridge unavailable: %w", c.Err)
}
