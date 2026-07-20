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

// CDPResult 是 browser_cdp 的底层返回结构。
// Result 保留 CDP 原始返回值，方便调试 Runtime.evaluate、Page.captureScreenshot 等不同命令。
type CDPResult struct {
	Status string         `json:"status"`
	TabID  string         `json:"tab_id"`
	Method string         `json:"method"`
	Result map[string]any `json:"result,omitempty"`
	Diff   string         `json:"diff,omitempty"`
}

// Point 表示 viewport 坐标，不是屏幕物理坐标。
// Chrome Debugger Protocol 的 Input.dispatchMouseEvent 使用的就是 viewport 坐标。
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Rect 是元素在 viewport 中的边界框。
// browser_click_element / browser_type_element 会先用 JS 读取 rect，再取中心点交给 CDP。
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
}

// ClickResult 是 browser_click 和 browser_click_element 的稳定返回结构。
type ClickResult struct {
	Status    string `json:"status"`
	TabID     string `json:"tab_id"`
	ClickedAt Point  `json:"clicked_at"`
	Diff      string `json:"diff,omitempty"`
}

// ElementClickResult 是按 selector 点击元素后的返回结构。
type ElementClickResult struct {
	Status    string `json:"status"`
	TabID     string `json:"tab_id"`
	Selector  string `json:"selector"`
	Rect      Rect   `json:"rect"`
	ClickedAt Point  `json:"clicked_at"`
	Diff      string `json:"diff,omitempty"`
}

// TypeResult 是 browser_type 和 browser_type_element 的稳定返回结构。
type TypeResult struct {
	Status string `json:"status"`
	TabID  string `json:"tab_id"`
	Text   string `json:"text,omitempty"`
	Clear  bool   `json:"clear,omitempty"`
	Diff   string `json:"diff,omitempty"`
}

// ElementTypeResult 是按 selector 聚焦元素并输入文本后的返回结构。
type ElementTypeResult struct {
	Status   string `json:"status"`
	TabID    string `json:"tab_id"`
	Selector string `json:"selector"`
	Rect     Rect   `json:"rect"`
	TypedAt  Point  `json:"typed_at"`
	Text     string `json:"text,omitempty"`
	Clear    bool   `json:"clear,omitempty"`
	Diff     string `json:"diff,omitempty"`
}

// PressKeyResult 是 browser_press_key 的稳定返回结构。
// Key 支持 Enter、Escape、Tab、ArrowUp、Cmd+Enter 等高层按键名称。
type PressKeyResult struct {
	Status    string   `json:"status"`
	TabID     string   `json:"tab_id"`
	Key       string   `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
	Diff      string   `json:"diff,omitempty"`
}

// InteractiveElement 是 browser_snapshot 返回的可交互元素摘要。
// 只保留模型决策需要的低噪声字段，避免回传完整 DOM。
type InteractiveElement struct {
	Index     int     `json:"index"`
	Tag       string  `json:"tag"`
	Text      string  `json:"text,omitempty"`
	AriaLabel string  `json:"aria_label,omitempty"`
	Title     string  `json:"title,omitempty"`
	Role      string  `json:"role,omitempty"`
	Class     string  `json:"class,omitempty"`
	Selector  string  `json:"selector,omitempty"`
	Rect      Rect    `json:"rect"`
	Visible   bool    `json:"visible"`
	Disabled  bool    `json:"disabled"`
	Href      string  `json:"href,omitempty"`
	Type      string  `json:"type,omitempty"`
	Name      string  `json:"name,omitempty"`
	ID        string  `json:"id,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

// InteractiveSnapshot 是 browser_snapshot 返回的页面交互摘要。
type InteractiveSnapshot struct {
	Status    string               `json:"status"`
	TabID     string               `json:"tab_id"`
	Title     string               `json:"title"`
	URL       string               `json:"url"`
	Elements  []InteractiveElement `json:"elements"`
	Count     int                  `json:"count"`
	Truncated bool                 `json:"truncated,omitempty"`
}

// WaitResult 是浏览器等待类工具的稳定返回结构。
// status 为 success 表示条件满足，timeout 表示页面在限定时间内没有达到目标状态。
type WaitResult struct {
	Status           string `json:"status"`
	TabID            string `json:"tab_id"`
	Mode             string `json:"mode"`
	Matched          bool   `json:"matched"`
	ElapsedMS        int    `json:"elapsed_ms"`
	ReadyState       string `json:"ready_state,omitempty"`
	TabStatus        string `json:"tab_status,omitempty"`
	URL              string `json:"url,omitempty"`
	Title            string `json:"title,omitempty"`
	Selector         string `json:"selector,omitempty"`
	State            string `json:"state,omitempty"`
	Exists           bool   `json:"exists,omitempty"`
	Visible          bool   `json:"visible,omitempty"`
	Rect             *Rect  `json:"rect,omitempty"`
	Text             string `json:"text,omitempty"`
	TextLength       int    `json:"text_length,omitempty"`
	StableMS         int    `json:"stable_ms,omitempty"`
	StableForMS      int    `json:"stable_for_ms,omitempty"`
	InteractiveCount int    `json:"interactive_count,omitempty"`
}

// Client 是工具层依赖的浏览器能力接口。
// internal/tools 只关心这个接口，不需要知道底层是 Chrome 插件、CDP 还是以后别的桥接方案。
type Client interface {
	Tabs(ctx context.Context) ([]Tab, error)
	Open(ctx context.Context, url string, tabID string, active bool) (OpenResult, error)
	Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error)
	ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error)
	CDP(ctx context.Context, tabID string, method string, params map[string]any, noMonitor bool) (CDPResult, error)
	Click(ctx context.Context, tabID string, x float64, y float64, noMonitor bool) (ClickResult, error)
	Type(ctx context.Context, tabID string, text string, clear bool, noMonitor bool) (TypeResult, error)
	PressKey(ctx context.Context, tabID string, key string, noMonitor bool) (PressKeyResult, error)
	Snapshot(ctx context.Context, tabID string, maxElements int) (InteractiveSnapshot, error)
	Wait(ctx context.Context, tabID string, mode string, params map[string]any, timeoutMS int, intervalMS int) (WaitResult, error)
}
