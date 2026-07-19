package browser

import "context"

const (
	// DefaultListenAddr 是 Cohert 浏览器桥默认监听地址。
	// 插件里的 config.js 也指向这个端口，二者必须保持一致。
	//
	// 不使用 GA/TMWebDriver 常见的 18765/18766：
	DefaultListenAddr = "127.0.0.1:18777"

	// DefaultPath 是 WebSocket 路径。
	// 独立 path 可以避免以后同一个 HTTP server 上挂其他调试接口时发生冲突。
	DefaultPath = "/browser"
)

// Tab 描述 Chrome 里的一个可脚本化标签页。
// 当前插件只上报 http/https 页面，chrome://、file:// 等内部页面不会进入这里。
type Tab struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Active   bool   `json:"active"`
	WindowID int    `json:"windowId,omitempty"`
}

// PageSnapshot 是 browser_scan 返回给模型的页面快照。
// Text 已经在插件层按 max_chars 截断，避免一次网页扫描撑爆上下文。
type PageSnapshot struct {
	Status    string `json:"status"`
	TabID     string `json:"tab_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	CharCount int    `json:"char_count"`
	Omitted   int    `json:"omitted"`
}

// OpenResult 是 browser_open 打开或导航页面后的返回结果。
type OpenResult struct {
	Status string `json:"status"`
	TabID  string `json:"tab_id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// ExecuteJSResult 是 browser_execute_js 返回给模型的稳定结构。
// 插件内部当前返回字段名是 return；Go 层统一转换成 js_return，避免模型和插件协议强绑定。
type ExecuteJSResult struct {
	Status    string `json:"status"`
	TabID     string `json:"tab_id"`
	JSReturn  string `json:"js_return"`
	NewTabs   []Tab  `json:"new_tabs"`
	Truncated bool   `json:"truncated,omitempty"`
	Diff      string `json:"diff,omitempty"`
	Error     any    `json:"error,omitempty"`
}

// Client 是工具层依赖的浏览器能力接口。
// internal/tools 只关心这个接口，不需要知道底层是 Chrome 插件、CDP 还是以后别的桥接方案。
type Client interface {
	Tabs(ctx context.Context) ([]Tab, error)
	Open(ctx context.Context, url string, tabID string, active bool) (OpenResult, error)
	Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error)
	ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error)
}
