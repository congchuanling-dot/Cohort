package browser

import "context"

const (
	// DefaultListenAddr 是 Cohort 浏览器桥默认监听地址。
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
	// ID 是 Chrome tab 的唯一标识，由插件侧上报。
	ID string `json:"id"`
	// Title 是标签页当前标题。
	Title string `json:"title"`
	// URL 是标签页当前页面地址。
	URL string `json:"url"`
	// Active 表示该 tab 是否是当前窗口活动标签页。
	Active bool `json:"active"`
	// WindowID 是 Chrome 窗口标识，用于区分多窗口场景。
	WindowID int `json:"windowId,omitempty"`
}

// PageSnapshot 是 browser_scan 返回给模型的页面快照。
// Text 已经在插件层按 max_chars 截断，避免一次网页扫描撑爆上下文。
type PageSnapshot struct {
	// Status 是浏览器扫描命令的执行状态。
	Status string `json:"status"`
	// TabID 是被扫描的标签页 ID。
	TabID string `json:"tab_id"`
	// Title 是页面标题。
	Title string `json:"title"`
	// URL 是页面当前地址。
	URL string `json:"url"`
	// Text 是插件提取的页面可见文本。
	Text string `json:"text"`
	// Truncated 表示 Text 是否因 max_chars 限制被截断。
	Truncated bool `json:"truncated"`
	// CharCount 是截断前页面文本字符数。
	CharCount int `json:"char_count"`
	// Omitted 是因截断省略掉的字符数。
	Omitted int `json:"omitted"`
}

// DOMFieldSummary 是 browser_dom_summary 返回的表单字段摘要。
// password 字段永远不返回明文 value，只通过 ValuePresent 表示是否已有输入。
type DOMFieldSummary struct {
	// Selector 是插件建议的 CSS selector。
	Selector string `json:"selector,omitempty"`
	// Tag 是字段标签，例如 input、textarea 或 select。
	Tag string `json:"tag"`
	// Type 是输入类型，例如 text、password、checkbox。
	Type string `json:"type,omitempty"`
	// Name 是字段 name 属性。
	Name string `json:"name,omitempty"`
	// ID 是字段 id 属性。
	ID string `json:"id,omitempty"`
	// Placeholder 是字段 placeholder 文案。
	Placeholder string `json:"placeholder,omitempty"`
	// Value 是非敏感字段的当前值；password 等敏感字段不返回。
	Value string `json:"value,omitempty"`
	// ValuePresent 表示字段当前是否有值。
	ValuePresent bool `json:"value_present,omitempty"`
	// Checked 表示 checkbox/radio 是否选中。
	Checked bool `json:"checked,omitempty"`
	// Disabled 表示字段是否禁用。
	Disabled bool `json:"disabled,omitempty"`
}

// DOMFormSummary 是 browser_dom_summary 返回的表单摘要。
type DOMFormSummary struct {
	// Selector 是插件建议的 CSS selector。
	Selector string `json:"selector,omitempty"`
	// ID 是表单 id 属性。
	ID string `json:"id,omitempty"`
	// Name 是表单 name 属性。
	Name string `json:"name,omitempty"`
	// Method 是表单 method。
	Method string `json:"method,omitempty"`
	// Action 是表单 action。
	Action string `json:"action,omitempty"`
	// Fields 是表单内字段摘要。
	Fields []DOMFieldSummary `json:"fields,omitempty"`
}

// DOMFrameSummary 描述页面 iframe 摘要。
type DOMFrameSummary struct {
	// Src 是 iframe src。
	Src string `json:"src,omitempty"`
	// Title 是 iframe title。
	Title string `json:"title,omitempty"`
	// SameOrigin 表示 iframe 是否同源可读。
	SameOrigin bool `json:"same_origin"`
	// Included 表示同源 iframe 内容是否已合入 summary。
	Included bool `json:"included"`
}

// DOMSummary 是 browser_dom_summary 返回的低噪声 DOM/表单摘要。
type DOMSummary struct {
	// Status 是命令执行状态。
	Status string `json:"status"`
	// TabID 是生成摘要的标签页 ID。
	TabID string `json:"tab_id"`
	// Title 是页面标题。
	Title string `json:"title"`
	// URL 是页面当前地址。
	URL string `json:"url"`
	// Summary 是裁剪后的低噪声 HTML/文本摘要。
	Summary string `json:"summary"`
	// Forms 是页面表单摘要。
	Forms []DOMFormSummary `json:"forms,omitempty"`
	// Iframes 是 iframe 摘要。
	Iframes []DOMFrameSummary `json:"iframes,omitempty"`
	// ShadowRoots 是读取到的 open shadowRoot 数量。
	ShadowRoots int `json:"shadow_roots,omitempty"`
	// FixedOverlays 是识别到的 fixed/sticky 浮层数量。
	FixedOverlays int `json:"fixed_overlays,omitempty"`
	// Truncated 表示 Summary 是否因 max_chars 限制被截断。
	Truncated bool `json:"truncated"`
	// CharCount 是截断前摘要字符数。
	CharCount int `json:"char_count"`
	// Omitted 是因截断省略掉的字符数。
	Omitted int `json:"omitted"`
}

// OpenResult 是 browser_open 打开或导航页面后的返回结果。
type OpenResult struct {
	// Status 是打开或导航命令的执行状态。
	Status string `json:"status"`
	// TabID 是打开或导航后的标签页 ID。
	TabID string `json:"tab_id"`
	// Title 是目标页面标题。
	Title string `json:"title"`
	// URL 是最终页面地址。
	URL string `json:"url"`
}

// ExecuteJSResult 是 browser_execute_js 返回给模型的稳定结构。
// 插件内部当前返回字段名是 return；Go 层统一转换成 js_return，避免模型和插件协议强绑定。
type ExecuteJSResult struct {
	// Status 是 JS 执行命令的执行状态。
	Status string `json:"status"`
	// TabID 是执行脚本的标签页 ID。
	TabID string `json:"tab_id"`
	// JSReturn 是脚本返回值的字符串化结果。
	JSReturn string `json:"js_return"`
	// NewTabs 是脚本执行期间新打开的标签页列表。
	NewTabs []Tab `json:"new_tabs"`
	// Truncated 表示 JSReturn 是否被最大返回长度截断。
	Truncated bool `json:"truncated,omitempty"`
	// Diff 是插件侧页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
	// Error 是脚本执行异常时的原始错误信息。
	Error any `json:"error,omitempty"`
}

// CDPResult 是 browser_cdp 的底层返回结构。
// Result 保留 CDP 原始返回值，方便调试 Runtime.evaluate、Page.captureScreenshot 等不同命令。
type CDPResult struct {
	// Status 是 CDP 命令执行状态。
	Status string `json:"status"`
	// TabID 是执行 CDP 命令的标签页 ID。
	TabID string `json:"tab_id"`
	// Method 是透传给 Chrome Debugger Protocol 的方法名。
	Method string `json:"method"`
	// Result 是 CDP 原始响应体。
	Result map[string]any `json:"result,omitempty"`
	// Diff 是插件侧页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// Point 表示 viewport 坐标，不是屏幕物理坐标。
// Chrome Debugger Protocol 的 Input.dispatchMouseEvent 使用的就是 viewport 坐标。
type Point struct {
	// X 是 viewport 内的横坐标。
	X float64 `json:"x"`
	// Y 是 viewport 内的纵坐标。
	Y float64 `json:"y"`
}

// Rect 是元素在 viewport 中的边界框。
// browser_click_element / browser_type_element 会先用 JS 读取 rect，再取中心点交给 CDP。
type Rect struct {
	// X 是元素边界框左上角的 viewport 横坐标。
	X float64 `json:"x"`
	// Y 是元素边界框左上角的 viewport 纵坐标。
	Y float64 `json:"y"`
	// Width 是元素边界框宽度。
	Width float64 `json:"width"`
	// Height 是元素边界框高度。
	Height float64 `json:"height"`
	// Top 是元素边界框顶部坐标。
	Top float64 `json:"top"`
	// Right 是元素边界框右侧坐标。
	Right float64 `json:"right"`
	// Bottom 是元素边界框底部坐标。
	Bottom float64 `json:"bottom"`
	// Left 是元素边界框左侧坐标。
	Left float64 `json:"left"`
}

// ClickResult 是 browser_click 和 browser_click_element 的稳定返回结构。
type ClickResult struct {
	// Status 是点击命令执行状态。
	Status string `json:"status"`
	// TabID 是执行点击的标签页 ID。
	TabID string `json:"tab_id"`
	// ClickedAt 是实际发送鼠标事件的 viewport 坐标。
	ClickedAt Point `json:"clicked_at"`
	// Diff 是点击后页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// ElementClickResult 是按 selector 点击元素后的返回结构。
type ElementClickResult struct {
	// Status 是点击命令执行状态。
	Status string `json:"status"`
	// TabID 是执行点击的标签页 ID。
	TabID string `json:"tab_id"`
	// Selector 是用于定位目标元素的 CSS selector。
	Selector string `json:"selector"`
	// Rect 是目标元素点击前的 viewport 边界框。
	Rect Rect `json:"rect"`
	// ClickedAt 是实际点击的 viewport 坐标。
	ClickedAt Point `json:"clicked_at"`
	// Hit 描述点击点命中的实际 DOM 元素。
	Hit string `json:"hit,omitempty"`
	// Diff 是点击后页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// TypeResult 是 browser_type 和 browser_type_element 的稳定返回结构。
type TypeResult struct {
	// Status 是输入命令执行状态。
	Status string `json:"status"`
	// TabID 是执行输入的标签页 ID。
	TabID string `json:"tab_id"`
	// Text 是本次输入的文本。
	Text string `json:"text,omitempty"`
	// Clear 表示输入前是否清空了已有文本。
	Clear bool `json:"clear,omitempty"`
	// Diff 是输入后页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// ElementTypeResult 是按 selector 聚焦元素并输入文本后的返回结构。
type ElementTypeResult struct {
	// Status 是输入命令执行状态。
	Status string `json:"status"`
	// TabID 是执行输入的标签页 ID。
	TabID string `json:"tab_id"`
	// Selector 是用于定位目标输入元素的 CSS selector。
	Selector string `json:"selector"`
	// Rect 是目标元素输入前的 viewport 边界框。
	Rect Rect `json:"rect"`
	// TypedAt 是聚焦目标元素时点击的 viewport 坐标。
	TypedAt Point `json:"typed_at"`
	// Text 是本次输入的文本。
	Text string `json:"text,omitempty"`
	// Clear 表示输入前是否清空了已有文本。
	Clear bool `json:"clear,omitempty"`
	// Actual 是输入后从 DOM 读取到的实际值。
	Actual string `json:"actual,omitempty"`
	// Verified 表示 Actual 是否符合本次输入期望。
	Verified bool `json:"verified,omitempty"`
	// Diff 是输入后页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// PressKeyResult 是 browser_press_key 的稳定返回结构。
// Key 支持 Enter、Escape、Tab、ArrowUp、Cmd+Enter 等高层按键名称。
type PressKeyResult struct {
	// Status 是按键命令执行状态。
	Status string `json:"status"`
	// TabID 是执行按键的标签页 ID。
	TabID string `json:"tab_id"`
	// Key 是发送给浏览器的高层按键或组合键名称。
	Key string `json:"key"`
	// Modifiers 是解析出的修饰键列表，例如 Cmd 或 Ctrl。
	Modifiers []string `json:"modifiers,omitempty"`
	// Diff 是按键后页面变化监控返回的轻量摘要。
	Diff string `json:"diff,omitempty"`
}

// InteractiveElement 是 browser_snapshot 返回的可交互元素摘要。
// 只保留模型决策需要的低噪声字段，避免回传完整 DOM。
type InteractiveElement struct {
	// Index 是元素在本次 snapshot 结果中的序号。
	Index int `json:"index"`
	// Tag 是元素标签名。
	Tag string `json:"tag"`
	// Text 是元素可见文本摘要。
	Text string `json:"text,omitempty"`
	// AriaLabel 是元素的 aria-label 辅助文本。
	AriaLabel string `json:"aria_label,omitempty"`
	// Title 是元素 title 属性。
	Title string `json:"title,omitempty"`
	// Role 是元素 ARIA role。
	Role string `json:"role,omitempty"`
	// Class 是元素 class 的低噪声摘要。
	Class string `json:"class,omitempty"`
	// Selector 是插件建议的 CSS selector。
	Selector string `json:"selector,omitempty"`
	// Rect 是元素的 viewport 边界框。
	Rect Rect `json:"rect"`
	// Visible 表示元素当前是否可见。
	Visible bool `json:"visible"`
	// Disabled 表示元素当前是否禁用。
	Disabled bool `json:"disabled"`
	// Href 是链接元素的目标地址。
	Href string `json:"href,omitempty"`
	// Type 是输入控件的 type 属性。
	Type string `json:"type,omitempty"`
	// Name 是元素 name 属性。
	Name string `json:"name,omitempty"`
	// ID 是元素 id 属性。
	ID string `json:"id,omitempty"`
	// Score 是插件侧排序使用的交互相关性分数。
	Score float64 `json:"score,omitempty"`
}

// InteractiveSnapshot 是 browser_snapshot 返回的页面交互摘要。
type InteractiveSnapshot struct {
	// Status 是 snapshot 命令执行状态。
	Status string `json:"status"`
	// TabID 是生成 snapshot 的标签页 ID。
	TabID string `json:"tab_id"`
	// Title 是页面标题。
	Title string `json:"title"`
	// URL 是页面当前地址。
	URL string `json:"url"`
	// Elements 是可交互元素摘要列表。
	Elements []InteractiveElement `json:"elements"`
	// Count 是返回的元素数量。
	Count int `json:"count"`
	// Truncated 表示元素列表是否被 max_elements 截断。
	Truncated bool `json:"truncated,omitempty"`
}

// ScreenshotResult 是浏览器截图的底层返回结构。
// Data 是插件返回的 base64 图片数据，工具层会落盘后只把路径返回给模型。
type ScreenshotResult struct {
	// Status 是截图命令执行状态。
	Status string `json:"status"`
	// TabID 是截图来源标签页 ID。
	TabID string `json:"tab_id"`
	// Format 是图片编码格式，例如 png、jpeg 或 webp。
	Format string `json:"format"`
	// Data 是插件返回的 base64 图片数据。
	Data string `json:"data,omitempty"`
	// Width 是截图像素宽度。
	Width int `json:"width,omitempty"`
	// Height 是截图像素高度。
	Height int `json:"height,omitempty"`
	// Scale 是截图使用的设备像素比。
	Scale int `json:"scale,omitempty"`
}

// WaitResult 是浏览器等待类工具的稳定返回结构。
// status 为 success 表示条件满足，timeout 表示页面在限定时间内没有达到目标状态。
type WaitResult struct {
	// Status 是等待命令状态，success 或 timeout。
	Status string `json:"status"`
	// TabID 是执行等待的标签页 ID。
	TabID string `json:"tab_id"`
	// Mode 是等待模式，例如 load、selector、text、url 或 stable。
	Mode string `json:"mode"`
	// Matched 表示等待条件是否在超时前满足。
	Matched bool `json:"matched"`
	// ElapsedMS 是本次等待实际耗时毫秒数。
	ElapsedMS int `json:"elapsed_ms"`
	// ReadyState 是页面 document.readyState。
	ReadyState string `json:"ready_state,omitempty"`
	// TabStatus 是 Chrome tab.status。
	TabStatus string `json:"tab_status,omitempty"`
	// URL 是等待结束时的页面地址。
	URL string `json:"url,omitempty"`
	// Title 是等待结束时的页面标题。
	Title string `json:"title,omitempty"`
	// URLContains 是 url 模式使用的包含匹配条件。
	URLContains string `json:"url_contains,omitempty"`
	// URLExact 是 url 模式使用的精确匹配条件。
	URLExact string `json:"url_exact,omitempty"`
	// URLMatches 是 url 模式使用的正则匹配条件。
	URLMatches string `json:"url_matches,omitempty"`
	// Selector 是 selector 模式等待的 CSS selector。
	Selector string `json:"selector,omitempty"`
	// State 是 selector 模式等待的目标状态。
	State string `json:"state,omitempty"`
	// Exists 表示 selector 等待结束时元素是否存在。
	Exists bool `json:"exists,omitempty"`
	// Visible 表示 selector 等待结束时元素是否可见。
	Visible bool `json:"visible,omitempty"`
	// Rect 是 selector 等待结束时元素的边界框。
	Rect *Rect `json:"rect,omitempty"`
	// Text 是 text 模式等待的目标文本。
	Text string `json:"text,omitempty"`
	// TextLength 是等待结束时页面正文文本长度。
	TextLength int `json:"text_length,omitempty"`
	// StableMS 是 stable 模式要求稳定持续的毫秒数。
	StableMS int `json:"stable_ms,omitempty"`
	// StableForMS 是页面当前已稳定的毫秒数。
	StableForMS int `json:"stable_for_ms,omitempty"`
	// InteractiveCount 是等待结束时页面可交互元素数量。
	InteractiveCount int `json:"interactive_count,omitempty"`
}

// Client 是工具层依赖的浏览器能力接口。
// internal/tools 只关心这个接口，不需要知道底层是 Chrome 插件、CDP 还是以后别的桥接方案。
type Client interface {
	Tabs(ctx context.Context) ([]Tab, error)
	Open(ctx context.Context, url string, tabID string, active bool) (OpenResult, error)
	Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error)
	DOMSummary(ctx context.Context, tabID string, maxChars int, includeIframes bool, includeShadowDOM bool, includeFormValues bool, includeFixedOverlays bool) (DOMSummary, error)
	ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error)
	CDP(ctx context.Context, tabID string, method string, params map[string]any, noMonitor bool) (CDPResult, error)
	Click(ctx context.Context, tabID string, x float64, y float64, noMonitor bool) (ClickResult, error)
	Type(ctx context.Context, tabID string, text string, clear bool, noMonitor bool) (TypeResult, error)
	PressKey(ctx context.Context, tabID string, key string, noMonitor bool) (PressKeyResult, error)
	Snapshot(ctx context.Context, tabID string, maxElements int) (InteractiveSnapshot, error)
	Screenshot(ctx context.Context, tabID string, format string, fullPage bool, quality int) (ScreenshotResult, error)
	Wait(ctx context.Context, tabID string, mode string, params map[string]any, timeoutMS int, intervalMS int) (WaitResult, error)
}
