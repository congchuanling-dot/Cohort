package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/browser"
	"cohort/internal/llm"
	"cohort/internal/vision"
)

const (
	// defaultBrowserScanChars 限制普通 DOM 文本进入模型上下文的默认长度。
	defaultBrowserScanChars = 12000
	// defaultBrowserDOMSummaryChars 限制结构化 DOM 摘要默认长度。
	defaultBrowserDOMSummaryChars = 20000
	// defaultBrowserJSReturnChars 限制页面脚本返回值的默认长度。
	defaultBrowserJSReturnChars    = 8000
	defaultBrowserSnapshotItems    = 80
	defaultBrowserWaitTimeoutMS    = 10000
	defaultBrowserWaitIntervalMS   = 200
	defaultBrowserScreenshotDir    = ".cohort/screenshots"
	defaultBrowserOCRMinConfidence = 0.5
	defaultBrowserOCRMaxLines      = 80
	defaultBrowserOCRMaxChars      = 8000
	maxBrowserOCRLines             = 200
	maxBrowserOCRChars             = 12000
)

// BrowserTabs 把浏览器标签页列表暴露给模型。
// 模型不知道当前浏览器状态时，应先调用这个工具再决定打开或扫描哪个 tab。
type BrowserTabs struct {
	// client 是浏览器桥能力接口，用来读取当前标签页列表。
	client browser.Client
}

func NewBrowserTabs(client browser.Client) *BrowserTabs {
	return &BrowserTabs{client: client}
}

func (t *BrowserTabs) Name() string { return ToolNameBrowserTabs }

func (t *BrowserTabs) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "List current Chrome tabs visible to the Cohort Browser Bridge.",
		Parameters:  objectSchema(map[string]any{}),
	}}
}

func (t *BrowserTabs) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabs, err := t.client.Tabs(ctx)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{
		Data:       map[string]any{"status": agent.ToolStatusSuccess, "tabs": tabs},
		NextPrompt: "\n",
	}, nil
}

// BrowserOpen 打开一个 URL，或把指定 tab 导航到目标 URL。
// 查天气这类任务会先把搜索 URL 交给这个工具，再用 browser_scan 读取结果页。
type BrowserOpen struct {
	// client 是浏览器桥能力接口，用来打开或导航标签页。
	client browser.Client
}

func NewBrowserOpen(client browser.Client) *BrowserOpen {
	return &BrowserOpen{client: client}
}

func (t *BrowserOpen) Name() string { return ToolNameBrowserOpen }

func (t *BrowserOpen) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Open a URL in Chrome or navigate an existing tab. Use this before browser_scan for web lookup tasks.",
		Parameters: objectSchema(map[string]any{
			"url":    stringProp("Absolute http/https URL to open"),
			"tab_id": stringProp("Optional tab ID. If empty, opens a new active tab."),
			"active": boolProp("Whether the tab should become active", true),
		}, "url"),
	}}
}

func (t *BrowserOpen) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	rawURL, err := normalizeBrowserURL(asString(call.Args["url"]))
	if err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_url",
				err.Error(),
				"请提供完整的 http/https URL，例如 https://www.google.com/search?q=重庆明日天气。",
			),
			NextPrompt: "\n",
		}, nil
	}
	tabID := asString(call.Args["tab_id"])
	active := asBool(call.Args["active"], true)

	result, err := t.client.Open(ctx, rawURL, tabID, active)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserScan 读取当前或指定 tab 的页面正文。
// 页面文本会进入模型上下文，所以 max_chars 必须有默认上限。
type BrowserScan struct {
	// client 是浏览器桥能力接口，用来读取页面标题、URL 和正文。
	client browser.Client
}

func NewBrowserScan(client browser.Client) *BrowserScan {
	return &BrowserScan{client: client}
}

func (t *BrowserScan) Name() string { return ToolNameBrowserScan }

func (t *BrowserScan) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read title, URL, and visible text from the current or specified Chrome tab. Prefer this over OCR for normal web pages.",
		Parameters: objectSchema(map[string]any{
			"tab_id":    stringProp("Optional tab ID. If empty, scans the active tab."),
			"max_chars": intProp("Maximum text characters to return. Default 12000.", defaultBrowserScanChars),
		}),
	}}
}

func (t *BrowserScan) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	maxChars := asInt(call.Args["max_chars"], defaultBrowserScanChars)
	if maxChars <= 0 {
		maxChars = defaultBrowserScanChars
	}

	result, err := t.client.Scan(ctx, tabID, maxChars)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserDOMSummary 返回低噪声 DOM/表单/iframe/shadowRoot 摘要。
// 它比 browser_scan 更结构化，适合 scan/snapshot 不足但 DOM 仍可访问的页面。
type BrowserDOMSummary struct {
	// client 是浏览器桥能力接口，用来读取页面 DOM 摘要。
	client browser.Client
}

func NewBrowserDOMSummary(client browser.Client) *BrowserDOMSummary {
	return &BrowserDOMSummary{client: client}
}

func (t *BrowserDOMSummary) Name() string { return ToolNameBrowserDOMSummary }

func (t *BrowserDOMSummary) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Return a compact low-noise DOM summary for the current or specified Chrome tab, including forms, current non-sensitive field values, same-origin iframes, open shadow roots, and fixed overlays. Use when browser_scan/browser_snapshot is insufficient but DOM is still available; prefer this before screenshot/OCR.",
		Parameters: objectSchema(map[string]any{
			"tab_id":                 stringProp("Optional tab ID. If empty, summarizes the active tab."),
			"max_chars":              intProp("Maximum summary characters to return. Default 20000.", defaultBrowserDOMSummaryChars),
			"include_iframes":        boolProp("Include same-origin iframe body summaries and iframe metadata. Default true.", true),
			"include_shadow_dom":     boolProp("Include open shadowRoot summaries when readable. Default true.", true),
			"include_form_values":    boolProp("Include current non-sensitive form field values. Password values are never returned. Default true.", true),
			"include_fixed_overlays": boolProp("Include fixed/sticky overlay summaries. Default true.", true),
		}),
	}}
}

func (t *BrowserDOMSummary) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	maxChars := asInt(call.Args["max_chars"], defaultBrowserDOMSummaryChars)
	if maxChars <= 0 {
		maxChars = defaultBrowserDOMSummaryChars
	}
	result, err := t.client.DOMSummary(
		ctx,
		tabID,
		maxChars,
		asBool(call.Args["include_iframes"], true),
		asBool(call.Args["include_shadow_dom"], true),
		asBool(call.Args["include_form_values"], true),
		asBool(call.Args["include_fixed_overlays"], true),
	)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserExecuteJS 在当前或指定 tab 的页面上下文里执行 JavaScript。
// 第一版复用插件 execute_js 命令，Go 层负责把返回结构稳定成 js_return/new_tabs。
type BrowserExecuteJS struct {
	// client 是浏览器桥能力接口，用来在页面上下文执行脚本。
	client browser.Client
}

func NewBrowserExecuteJS(client browser.Client) *BrowserExecuteJS {
	return &BrowserExecuteJS{client: client}
}

func (t *BrowserExecuteJS) Name() string { return ToolNameBrowserExecuteJS }

func (t *BrowserExecuteJS) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Execute JavaScript in the current or specified Chrome tab. Use simple expressions for reading page state, or explicit return for multi-line scripts.",
		Parameters: objectSchema(map[string]any{
			"tab_id":           stringProp("Optional tab ID. If empty, executes in the active tab."),
			"script":           stringProp("JavaScript to execute in the page. Simple expressions like document.title are automatically returned."),
			"no_monitor":       boolProp("Disable page-change monitoring. Reserved for future richer diff support.", false),
			"max_return_chars": intProp("Maximum return characters. Default 8000.", defaultBrowserJSReturnChars),
		}, "script"),
	}}
}

func (t *BrowserExecuteJS) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	script := normalizeBrowserScript(asString(call.Args["script"]))
	if script == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_script",
				"browser_execute_js requires a non-empty script",
				"请提供要执行的 JavaScript，例如 document.title 或 return document.body.innerText。",
			),
			NextPrompt: "\n",
		}, nil
	}
	maxReturnChars := asInt(call.Args["max_return_chars"], defaultBrowserJSReturnChars)
	if maxReturnChars <= 0 {
		maxReturnChars = defaultBrowserJSReturnChars
	}
	noMonitor := asBool(call.Args["no_monitor"], false)

	result, err := t.client.ExecuteJS(ctx, tabID, script, noMonitor, maxReturnChars)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserCDP 是原始 Chrome Debugger Protocol 入口。
// 它主要用于调试和补齐新能力；常见点击和输入应优先使用更安全的封装工具。
type BrowserCDP struct {
	// client 是浏览器桥能力接口，用来透传 CDP 命令。
	client browser.Client
}

func NewBrowserCDP(client browser.Client) *BrowserCDP {
	return &BrowserCDP{client: client}
}

func (t *BrowserCDP) Name() string { return ToolNameBrowserCDP }

func (t *BrowserCDP) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Send one Chrome DevTools Protocol command to the current or specified Chrome tab. Prefer browser_click/browser_type for normal interactions.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, uses the active tab."),
			"method":     stringProp("CDP method name, for example Runtime.evaluate or Input.dispatchMouseEvent."),
			"params":     objectProp("CDP command parameters."),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "method"),
	}}
}

func (t *BrowserCDP) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	method := strings.TrimSpace(asString(call.Args["method"]))
	if method == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_cdp_method",
				"browser_cdp requires a non-empty method",
				"请提供 CDP method，例如 Runtime.evaluate、Page.bringToFront 或 Input.dispatchMouseEvent。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.client.CDP(ctx, tabID, method, asObject(call.Args["params"]), asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserClick 在 viewport 坐标执行真实鼠标点击。
// x/y 来自 getBoundingClientRect 或用户明确给出的页面坐标，不是屏幕坐标。
type BrowserClick struct {
	// client 是浏览器桥能力接口，用来发送真实鼠标点击。
	client browser.Client
}

func NewBrowserClick(client browser.Client) *BrowserClick {
	return &BrowserClick{client: client}
}

func (t *BrowserClick) Name() string { return ToolNameBrowserClick }

func (t *BrowserClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Click a viewport coordinate in Chrome using CDP mouse events. Use browser_click_element when a CSS selector is available.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, clicks in the active tab."),
			"x":          numberProp("Viewport X coordinate from getBoundingClientRect."),
			"y":          numberProp("Viewport Y coordinate from getBoundingClientRect."),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "x", "y"),
	}}
}

func (t *BrowserClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	x := asFloat(call.Args["x"], math.NaN())
	y := asFloat(call.Args["y"], math.NaN())
	if math.IsNaN(x) || math.IsNaN(y) {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_click_point",
				"browser_click requires numeric x and y",
				"请传入 viewport 坐标，例如 {\"x\": 120, \"y\": 300}。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.client.Click(ctx, tabID, x, y, asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserClickElement 用 CSS selector 定位元素，再走 CDP 坐标点击。
// JS 只负责找元素和算 rect；真正点击仍由 CDP 产生真实输入事件。
type BrowserClickElement struct {
	// client 是浏览器桥能力接口，用来定位元素并发送点击。
	client browser.Client
}

func NewBrowserClickElement(client browser.Client) *BrowserClickElement {
	return &BrowserClickElement{client: client}
}

func (t *BrowserClickElement) Name() string { return ToolNameBrowserClickElement }

func (t *BrowserClickElement) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find an element by CSS selector, compute its viewport center, then click it with CDP mouse events.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, uses the active tab."),
			"selector":   stringProp("CSS selector for the target element."),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "selector"),
	}}
}

func (t *BrowserClickElement) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	selector := strings.TrimSpace(asString(call.Args["selector"]))
	if selector == "" {
		return badSelectorOutcome(), nil
	}
	target, err := locateElementTarget(ctx, t.client, tabID, selector, false)
	if err != nil {
		return browserToolError(err), nil
	}
	point := target.Point
	click, err := t.client.Click(ctx, tabID, point.X, point.Y, asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	waitResult, waitErr := waitAfterBrowserAction(ctx, t.client, click.TabID)
	result := browser.ElementClickResult{
		Status:    click.Status,
		TabID:     click.TabID,
		Selector:  selector,
		Rect:      target.Rect,
		ClickedAt: click.ClickedAt,
		Hit:       target.Hit,
		Diff:      click.Diff,
	}
	if waitErr == nil && waitResult.Status != "" {
		result.Diff = appendBrowserDiff(result.Diff, "auto_wait_"+waitResult.Status)
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserType 向当前焦点输入文本。聚焦由 browser_click 或 browser_type_element 负责。
type BrowserType struct {
	// client 是浏览器桥能力接口，用来向当前焦点输入文本。
	client browser.Client
}

func NewBrowserType(client browser.Client) *BrowserType {
	return &BrowserType{client: client}
}

func (t *BrowserType) Name() string { return ToolNameBrowserType }

func (t *BrowserType) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Type text into the currently focused page element using CDP keyboard input. Focus the element first with browser_click or browser_type_element.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, types in the active tab."),
			"text":       stringProp("Text to insert into the focused element."),
			"clear":      boolProp("Select all existing text and delete it before typing.", false),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "text"),
	}}
}

func (t *BrowserType) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	text := asString(call.Args["text"])
	if text == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_type_text",
				"browser_type requires non-empty text",
				"请提供要输入的文本。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.client.Type(ctx, tabID, text, asBool(call.Args["clear"], false), asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserTypeElement 先按 selector 定位并无副作用聚焦，再输入文本。
type BrowserTypeElement struct {
	// client 是浏览器桥能力接口，用来定位输入元素并发送文本。
	client browser.Client
}

func NewBrowserTypeElement(client browser.Client) *BrowserTypeElement {
	return &BrowserTypeElement{client: client}
}

func (t *BrowserTypeElement) Name() string { return ToolNameBrowserTypeElement }

func (t *BrowserTypeElement) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find an input-like element by CSS selector, focus it without clicking or submitting, then type text with CDP keyboard input.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, uses the active tab."),
			"selector":   stringProp("CSS selector for the target input element."),
			"text":       stringProp("Text to insert into the element."),
			"clear":      boolProp("Select all existing text and delete it before typing.", false),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "selector", "text"),
	}}
}

func (t *BrowserTypeElement) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	selector := strings.TrimSpace(asString(call.Args["selector"]))
	if selector == "" {
		return badSelectorOutcome(), nil
	}
	text := asString(call.Args["text"])
	if text == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_type_text",
				"browser_type_element requires non-empty text",
				"请提供要输入的文本。",
			),
			NextPrompt: "\n",
		}, nil
	}
	target, err := locateElementTarget(ctx, t.client, tabID, selector, true)
	if err != nil {
		return browserToolError(err), nil
	}
	point := target.Point
	focused, focusErr := focusElementForTyping(ctx, t.client, tabID, selector)
	if focusErr != nil {
		return browserToolError(focusErr), nil
	}
	if !focused {
		return browserToolError(errors.New("browser input target did not receive focus")), nil
	}
	typed, err := t.client.Type(ctx, tabID, text, asBool(call.Args["clear"], false), asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	actual, verified, verifyErr := verifyElementTyped(ctx, t.client, tabID, selector, text, asBool(call.Args["clear"], false))
	if verifyErr != nil {
		return browserToolError(verifyErr), nil
	}
	result := browser.ElementTypeResult{
		Status:      typed.Status,
		TabID:       typed.TabID,
		Selector:    selector,
		Rect:        target.Rect,
		TypedAt:     point,
		FocusMethod: "dom_focus",
		Focused:     focused,
		Text:        typed.Text,
		Clear:       typed.Clear,
		Actual:      actual,
		Verified:    verified,
		Diff:        typed.Diff,
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// focusElementForTyping 聚焦已完成可见性和命中测试的可编辑元素。
// 聚焦本身不使用鼠标事件，避免坐标漂移或 label/button 默认行为触发表单提交；
// 随后的文字仍由 CDP Input.insertText 注入，页面可收到真实输入事件链。
func focusElementForTyping(ctx context.Context, client browser.Client, tabID string, selector string) (bool, error) {
	selectorJSON, err := json.Marshal(selector)
	if err != nil {
		return false, err
	}
	script := fmt.Sprintf(`const element = document.querySelector(%s);
if (!element) throw new Error("element not found before focus");
element.focus({ preventScroll: true });
await new Promise((resolve) => requestAnimationFrame(resolve));
return document.activeElement === element || element.contains(document.activeElement);`, string(selectorJSON))
	result, err := client.ExecuteJS(ctx, tabID, script, true, 1000)
	if err != nil {
		return false, err
	}
	if result.Status == "error" {
		return false, fmt.Errorf("browser input focus error: %v", result.Error)
	}
	var focused bool
	if err := json.Unmarshal([]byte(result.JSReturn), &focused); err != nil {
		return false, fmt.Errorf("decode browser input focus result: %w", err)
	}
	return focused, nil
}

// BrowserPressKey 发送 Enter、Escape、Tab、Cmd+Enter 等真实键盘按键。
// 它封装 CDP Input.dispatchKeyEvent，避免模型手写底层 keyDown/keyUp 参数。
type BrowserPressKey struct {
	// client 是浏览器桥能力接口，用来发送真实键盘按键。
	client browser.Client
}

func NewBrowserPressKey(client browser.Client) *BrowserPressKey {
	return &BrowserPressKey{client: client}
}

func (t *BrowserPressKey) Name() string { return ToolNameBrowserPressKey }

func (t *BrowserPressKey) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Press a real browser key or shortcut with CDP keyboard events. Use for Enter search, Escape close popup, Tab focus navigation, Cmd+Enter/Ctrl+Enter submit or send.",
		Parameters: objectSchema(map[string]any{
			"tab_id":     stringProp("Optional tab ID. If empty, presses key in the active tab."),
			"key":        stringProp("Key or shortcut, for example Enter, Escape, Tab, ArrowUp, ArrowDown, Cmd+Enter, Ctrl+Enter, Meta+A."),
			"no_monitor": boolProp("Disable lightweight page-change monitoring.", false),
		}, "key"),
	}}
}

func (t *BrowserPressKey) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	key := strings.TrimSpace(asString(call.Args["key"]))
	if key == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_key",
				"browser_press_key requires a non-empty key",
				"请提供按键名称，例如 Enter、Escape、Tab、Cmd+Enter 或 Ctrl+Enter。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.client.PressKey(ctx, tabID, key, asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserSnapshot 返回当前页面的可交互元素摘要。
// 它是“找按钮/输入框”的高层工具，减少模型反复写 Runtime.evaluate 探测 DOM。
type BrowserSnapshot struct {
	// client 是浏览器桥能力接口，用来生成页面交互元素摘要。
	client browser.Client
}

func NewBrowserSnapshot(client browser.Client) *BrowserSnapshot {
	return &BrowserSnapshot{client: client}
}

func (t *BrowserSnapshot) Name() string { return ToolNameBrowserSnapshot }

func (t *BrowserSnapshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Return a compact snapshot of visible interactive elements in the current page: index, tag, text, aria-label, title, role, class summary, suggested selector, rect, visible, and disabled. Use before clicking or typing when selector is unknown.",
		Parameters: objectSchema(map[string]any{
			"tab_id":       stringProp("Optional tab ID. If empty, snapshots the active tab."),
			"max_elements": intProp("Maximum interactive elements to return. Default 80.", defaultBrowserSnapshotItems),
		}),
	}}
}

func (t *BrowserSnapshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	maxElements := asInt(call.Args["max_elements"], defaultBrowserSnapshotItems)
	if maxElements <= 0 {
		maxElements = defaultBrowserSnapshotItems
	}
	result, err := t.client.Snapshot(ctx, tabID, maxElements)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserWaitForLoad 等待页面基础加载完成。
// browser_open 之后优先调用它，避免页面还没出来就 browser_scan 导致误判。
type BrowserWaitForLoad struct {
	// client 是浏览器桥能力接口，用来等待页面加载状态。
	client browser.Client
}

func NewBrowserWaitForLoad(client browser.Client) *BrowserWaitForLoad {
	return &BrowserWaitForLoad{client: client}
}

func (t *BrowserWaitForLoad) Name() string { return ToolNameBrowserWaitForLoad }

func (t *BrowserWaitForLoad) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait until the current or specified Chrome tab reports loaded and the page document is interactive or complete. Use after browser_open or navigation before scanning.",
		Parameters: objectSchema(map[string]any{
			"tab_id":      stringProp("Optional tab ID. If empty, waits on the active tab."),
			"timeout_ms":  intProp("Maximum wait time in milliseconds. Default 10000.", defaultBrowserWaitTimeoutMS),
			"interval_ms": intProp("Polling interval in milliseconds. Default 200.", defaultBrowserWaitIntervalMS),
		}),
	}}
}

func (t *BrowserWaitForLoad) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	result, err := runBrowserWait(ctx, t.client, asString(call.Args["tab_id"]), "load", map[string]any{}, call.Args)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserWaitForSelector 等待元素出现、可见、隐藏或消失。
// 它比等文本更通用，适合等待搜索框、结果列表、弹窗和按钮状态。
type BrowserWaitForSelector struct {
	// client 是浏览器桥能力接口，用来等待元素状态变化。
	client browser.Client
}

func NewBrowserWaitForSelector(client browser.Client) *BrowserWaitForSelector {
	return &BrowserWaitForSelector{client: client}
}

func (t *BrowserWaitForSelector) Name() string { return ToolNameBrowserWaitForSelector }

func (t *BrowserWaitForSelector) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait for a CSS selector to become attached, visible, hidden, or detached. Use after clicks or page opens before deciding an element is missing.",
		Parameters: objectSchema(map[string]any{
			"tab_id":      stringProp("Optional tab ID. If empty, waits on the active tab."),
			"selector":    stringProp("CSS selector to wait for."),
			"state":       stringProp("Target state: attached, visible, hidden, or detached. Default visible."),
			"timeout_ms":  intProp("Maximum wait time in milliseconds. Default 10000.", defaultBrowserWaitTimeoutMS),
			"interval_ms": intProp("Polling interval in milliseconds. Default 200.", defaultBrowserWaitIntervalMS),
		}, "selector"),
	}}
}

func (t *BrowserWaitForSelector) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	selector := strings.TrimSpace(asString(call.Args["selector"]))
	if selector == "" {
		return badSelectorOutcome(), nil
	}
	state := strings.TrimSpace(asString(call.Args["state"]))
	if state == "" {
		state = "visible"
	}
	params := map[string]any{"selector": selector, "state": state}
	result, err := runBrowserWait(ctx, t.client, asString(call.Args["tab_id"]), "selector", params, call.Args)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserWaitForText 等待页面正文出现指定文本。
// 它适合等待“登录成功”“提交成功”“搜索结果”这类异步文案。
type BrowserWaitForText struct {
	// client 是浏览器桥能力接口，用来等待页面文本出现。
	client browser.Client
}

func NewBrowserWaitForText(client browser.Client) *BrowserWaitForText {
	return &BrowserWaitForText{client: client}
}

func (t *BrowserWaitForText) Name() string { return ToolNameBrowserWaitForText }

func (t *BrowserWaitForText) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait until the page visible text contains a target string. Use after async actions before concluding the result is absent.",
		Parameters: objectSchema(map[string]any{
			"tab_id":      stringProp("Optional tab ID. If empty, waits on the active tab."),
			"text":        stringProp("Text that should appear in document.body.innerText."),
			"timeout_ms":  intProp("Maximum wait time in milliseconds. Default 10000.", defaultBrowserWaitTimeoutMS),
			"interval_ms": intProp("Polling interval in milliseconds. Default 200.", defaultBrowserWaitIntervalMS),
		}, "text"),
	}}
}

func (t *BrowserWaitForText) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	text := strings.TrimSpace(asString(call.Args["text"]))
	if text == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_wait_text",
				"browser_wait_for_text requires non-empty text",
				"请提供要等待出现的页面文字，例如 登录成功、提交成功 或 搜索结果。",
			),
			NextPrompt: "\n",
		}, nil
	}
	params := map[string]any{"text": text}
	result, err := runBrowserWait(ctx, t.client, asString(call.Args["tab_id"]), "text", params, call.Args)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserWaitForURL 等待页面 URL 命中指定条件。
// 登录跳转、搜索结果页、详情页导航这类场景优先用它，而不是只等 stable 后猜状态。
type BrowserWaitForURL struct {
	// client 是浏览器桥能力接口，用来等待 URL 命中条件。
	client browser.Client
}

func NewBrowserWaitForURL(client browser.Client) *BrowserWaitForURL {
	return &BrowserWaitForURL{client: client}
}

func (t *BrowserWaitForURL) Name() string { return ToolNameBrowserWaitForURL }

func (t *BrowserWaitForURL) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait until the current page URL matches a condition. Use after clicks, form submit, login, search, or navigation when the expected result is a URL change.",
		Parameters: objectSchema(map[string]any{
			"tab_id":       stringProp("Optional tab ID. If empty, waits on the active tab."),
			"url_contains": stringProp("Optional substring that the URL should contain."),
			"url_exact":    stringProp("Optional exact URL that should match."),
			"url_matches":  stringProp("Optional JavaScript regular expression pattern that the URL should match."),
			"timeout_ms":   intProp("Maximum wait time in milliseconds. Default 10000.", defaultBrowserWaitTimeoutMS),
			"interval_ms":  intProp("Polling interval in milliseconds. Default 200.", defaultBrowserWaitIntervalMS),
		}),
	}}
}

func (t *BrowserWaitForURL) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	contains := strings.TrimSpace(asString(call.Args["url_contains"]))
	exact := strings.TrimSpace(asString(call.Args["url_exact"]))
	matches := strings.TrimSpace(asString(call.Args["url_matches"]))
	if contains == "" && exact == "" && matches == "" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_wait_url",
				"browser_wait_for_url requires url_contains, url_exact, or url_matches",
				"请至少提供一种 URL 匹配条件，例如 {\"url_contains\":\"/search\"}。",
			),
			NextPrompt: "\n",
		}, nil
	}
	params := map[string]any{
		"url_contains": contains,
		"url_exact":    exact,
		"url_matches":  matches,
	}
	result, err := runBrowserWait(ctx, t.client, asString(call.Args["tab_id"]), "url", params, call.Args)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserWaitForStable 等待页面进入短时间稳定状态。
// 它适合页面 load 已完成但前端 JS 仍在持续渲染的场景。
type BrowserWaitForStable struct {
	// client 是浏览器桥能力接口，用来等待页面轻量状态稳定。
	client browser.Client
}

func NewBrowserWaitForStable(client browser.Client) *BrowserWaitForStable {
	return &BrowserWaitForStable{client: client}
}

func (t *BrowserWaitForStable) Name() string { return ToolNameBrowserWaitForStable }

func (t *BrowserWaitForStable) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait until URL, title, body text length, and interactive element count stay stable for a short period. Use before browser_scan when pages render asynchronously.",
		Parameters: objectSchema(map[string]any{
			"tab_id":      stringProp("Optional tab ID. If empty, waits on the active tab."),
			"stable_ms":   intProp("How long the lightweight page state must stay unchanged. Default 800.", 800),
			"timeout_ms":  intProp("Maximum wait time in milliseconds. Default 10000.", defaultBrowserWaitTimeoutMS),
			"interval_ms": intProp("Polling interval in milliseconds. Default 200.", defaultBrowserWaitIntervalMS),
		}),
	}}
}

func (t *BrowserWaitForStable) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	params := map[string]any{"stable_ms": asInt(call.Args["stable_ms"], 800)}
	result, err := runBrowserWait(ctx, t.client, asString(call.Args["tab_id"]), "stable", params, call.Args)
	if err != nil {
		return browserToolError(err), nil
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserScreenshot 截取当前浏览器页面，并把图片保存到 workspace。
// 工具结果只返回路径和尺寸，不把 base64 图片塞进模型上下文。
type BrowserScreenshot struct {
	// client 是浏览器桥能力接口，用来请求页面截图。
	client browser.Client
	// workspace 是截图文件保存的根目录。
	workspace string
}

func NewBrowserScreenshot(client browser.Client, workspace string) *BrowserScreenshot {
	return &BrowserScreenshot{client: client, workspace: workspace}
}

func (t *BrowserScreenshot) Name() string { return ToolNameBrowserScreenshot }

func (t *BrowserScreenshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Capture a browser screenshot and save it under the workspace. Use only when visual evidence is needed or DOM text is unavailable; the tool returns an image path instead of base64.",
		Parameters: objectSchema(map[string]any{
			"tab_id":    stringProp("Optional tab ID. If empty, captures the active tab."),
			"format":    stringProp("Image format: png, jpeg, or webp. Default png."),
			"full_page": boolProp("Capture the full page instead of current viewport.", false),
			"quality":   intProp("Quality for jpeg/webp, 1-100. Default 90.", 90),
		}),
	}}
}

func (t *BrowserScreenshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tabID := asString(call.Args["tab_id"])
	format := strings.ToLower(strings.TrimSpace(asString(call.Args["format"])))
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_bad_screenshot_format",
				"browser_screenshot format must be png, jpeg, or webp",
				"请使用 png、jpeg 或 webp。默认 png 即可。",
			),
			NextPrompt: "\n",
		}, nil
	}
	quality := asInt(call.Args["quality"], 90)
	if quality <= 0 {
		quality = 90
	}
	result, err := t.client.Screenshot(ctx, tabID, format, asBool(call.Args["full_page"], false), quality)
	if err != nil {
		return browserToolError(err), nil
	}
	imagePath, size, err := t.saveScreenshot(result)
	if err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_screenshot_save_failed",
				err.Error(),
				"截图已从浏览器返回，但保存到 workspace 失败；请检查 workspace 权限。",
			),
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":     result.Status,
			"tab_id":     result.TabID,
			"image_path": imagePath,
			"format":     result.Format,
			"width":      result.Width,
			"height":     result.Height,
			"bytes":      size,
		},
		NextPrompt: "\n",
	}, nil
}

func (t *BrowserScreenshot) saveScreenshot(result browser.ScreenshotResult) (string, int, error) {
	if strings.TrimSpace(result.Data) == "" {
		return "", 0, errors.New("browser screenshot returned empty image data")
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return "", 0, fmt.Errorf("decode screenshot image: %w", err)
	}
	ext := result.Format
	if ext == "jpeg" {
		ext = "jpg"
	}
	dir := filepath.Join(t.workspace, defaultBrowserScreenshotDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, fmt.Sprintf("browser_screenshot_%d.%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", 0, err
	}
	return path, len(data), nil
}

// BrowserOCR 对浏览器截图或 workspace 内图片执行只读 OCR。
// OCR bbox 始终以截图左上角为原点，不能直接用于系统级鼠标点击。
type BrowserOCR struct {
	client browser.Client
	workspaceTool
	runner vision.OCRRunner
}

func NewBrowserOCR(client browser.Client, workspace string) *BrowserOCR {
	scriptPath := resolveRuntimeScriptPath(workspace, browserOCRHelperFileName)
	if absolutePath, err := filepath.Abs(scriptPath); err == nil {
		scriptPath = absolutePath
	}
	return NewBrowserOCRWithRunner(
		client,
		workspace,
		vision.NewPythonOCRRunner("python3", scriptPath, vision.DefaultOCRTimeout),
	)
}

// NewBrowserOCRWithRunner 允许测试或未来 OCR 引擎注入受控 runner。
func NewBrowserOCRWithRunner(client browser.Client, workspace string, runner vision.OCRRunner) *BrowserOCR {
	return &BrowserOCR{
		client:        client,
		workspaceTool: newWorkspaceTool(workspace),
		runner:        runner,
	}
}

func (t *BrowserOCR) Name() string { return ToolNameBrowserOCR }

func (t *BrowserOCR) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Run read-only OCR on an image under the workspace, or capture the current browser viewport first when image_path is empty. Return text and screenshot-local bounding boxes. Use only after browser_scan and browser_dom_summary cannot read the needed text; OCR bounding boxes are not system-screen coordinates.",
		Parameters: objectSchema(map[string]any{
			"image_path":     stringProp("Optional image path under the workspace. If empty, capture the current browser viewport first."),
			"tab_id":         stringProp("Optional tab ID used only when image_path is empty."),
			"full_page":      boolProp("Capture the full page when image_path is empty. Default false.", false),
			"min_confidence": floatProp("Minimum OCR confidence from 0 to 1. Default 0.5.", defaultBrowserOCRMinConfidence),
			"max_lines":      intProp("Maximum OCR lines to return. Default 80, max 200.", defaultBrowserOCRMaxLines),
			"max_chars":      intProp("Maximum OCR text characters to return. Default 8000, max 12000.", defaultBrowserOCRMaxChars),
			"enhance":        boolProp("Apply contrast and scale preprocessing. Default false because it can harm clear text.", false),
		}),
	}}
}

func (t *BrowserOCR) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if t.runner == nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_ocr_unavailable",
				"OCR runner is not configured",
				"请确认 Cohort 随附的 scripts/browser_ocr.py 可用，并已配置 Python 运行环境。",
			),
			NextPrompt: "\n",
		}, nil
	}

	imagePath := strings.TrimSpace(asString(call.Args["image_path"]))
	var screenshot browser.ScreenshotResult
	capturedScreenshot := imagePath == ""
	if capturedScreenshot {
		var err error
		screenshot, err = t.client.Screenshot(ctx, asString(call.Args["tab_id"]), "png", asBool(call.Args["full_page"], false), 90)
		if err != nil {
			return browserToolError(err), nil
		}
		var saveErr error
		imagePath, _, saveErr = (&BrowserScreenshot{workspace: t.workspace}).saveScreenshot(screenshot)
		if saveErr != nil {
			return agent.Outcome{
				Data: agent.NewToolError(
					"browser_ocr_screenshot_save_failed",
					saveErr.Error(),
					"浏览器截图已返回但无法保存到 workspace；请检查 workspace 权限。",
				),
				NextPrompt: "\n",
			}, nil
		}
	}

	resolvedPath, pathErr := t.resolveOCRImagePath(imagePath)
	if pathErr != nil {
		return agent.Outcome{
			Data:       *pathErr,
			NextPrompt: "\n",
		}, nil
	}

	minConfidence := asFloat(call.Args["min_confidence"], defaultBrowserOCRMinConfidence)
	if minConfidence < 0 || minConfidence > 1 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_ocr_bad_min_confidence",
				"min_confidence must be between 0 and 1",
				"请传入 0 到 1 之间的数值，默认 0.5。",
			),
			NextPrompt: "\n",
		}, nil
	}

	result, err := t.runner.Run(ctx, vision.OCRRequest{
		ImagePath:     resolvedPath,
		MinConfidence: minConfidence,
		Enhance:       asBool(call.Args["enhance"], false),
	})
	if err != nil {
		var ocrErr *vision.ToolError
		if errors.As(err, &ocrErr) {
			return agent.Outcome{
				Data:       agent.NewToolError(ocrErr.Code, ocrErr.Message, ocrErr.Hint),
				NextPrompt: "\n",
			}, nil
		}
		return agent.Outcome{
			Data: agent.NewToolError(
				"browser_ocr_failed",
				err.Error(),
				"请检查图片是否可读、Python OCR helper 是否可用，并根据错误决定是否重试。",
			),
			NextPrompt: "\n",
		}, nil
	}

	maxLines := clampBrowserOCRLimit(asInt(call.Args["max_lines"], defaultBrowserOCRMaxLines), defaultBrowserOCRMaxLines, maxBrowserOCRLines)
	maxChars := clampBrowserOCRLimit(asInt(call.Args["max_chars"], defaultBrowserOCRMaxChars), defaultBrowserOCRMaxChars, maxBrowserOCRChars)
	lines := result.Lines
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	text, textTruncated := truncateBrowserOCRText(joinOCRLines(lines), maxChars)
	truncated = truncated || textTruncated

	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"image_path":       resolvedPath,
		"coordinate_space": "screenshot-local",
		"width":            result.Width,
		"height":           result.Height,
		"text":             text,
		"lines":            lines,
		"line_count":       len(lines),
		"total_lines":      len(result.Lines),
		"truncated":        truncated,
	}
	if capturedScreenshot {
		data["tab_id"] = screenshot.TabID
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *BrowserOCR) resolveOCRImagePath(rawPath string) (string, *agent.ToolErrorData) {
	path := t.resolve(rawPath)
	rel, err := filepath.Rel(t.workspace, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		err := agent.NewToolError(
			"browser_ocr_path_outside_workspace",
			"OCR image_path must stay inside the configured workspace",
			"请提供 workspace 内图片的相对路径，或省略 image_path 让 browser_ocr 自动截图。",
		)
		return "", &err
	}
	if _, err := os.Stat(path); err != nil {
		toolErr := agent.NewToolError(
			"browser_ocr_image_not_found",
			fmt.Sprintf("OCR image is unavailable: %v", err),
			"请确认图片已保存到 workspace，或省略 image_path 让 browser_ocr 自动截图。",
		)
		return "", &toolErr
	}

	realWorkspace, workspaceErr := filepath.EvalSymlinks(t.workspace)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if workspaceErr == nil && pathErr == nil {
		realRel, err := filepath.Rel(realWorkspace, realPath)
		if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			toolErr := agent.NewToolError(
				"browser_ocr_path_outside_workspace",
				"OCR image_path resolves outside the configured workspace",
				"图片路径不能通过符号链接逃出 workspace；请复制图片到 workspace 后重试。",
			)
			return "", &toolErr
		}
	}
	return path, nil
}

func clampBrowserOCRLimit(value int, fallback int, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func joinOCRLines(lines []vision.OCRLine) string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}
	return strings.Join(texts, "\n")
}

func truncateBrowserOCRText(text string, maxChars int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, false
	}
	return string(runes[:maxChars]) + "\n...[truncated]...", true
}

func browserToolError(err error) agent.Outcome {
	code := "browser_error"
	hint := "确认 Chrome 已安装 Cohort Browser Bridge 插件，并且 Cohort 正在运行；插件会连接 ws://127.0.0.1:18777/browser。"
	if errors.Is(err, browser.ErrNotConnected) {
		code = "browser_not_connected"
		hint = "请运行 cohort extension open，在 Chrome 的 chrome://extensions 中加载输出的 unpacked extension 目录，并打开任意 http/https 页面；然后重试。"
	}
	return agent.Outcome{
		Data:       agent.NewToolError(code, err.Error(), hint),
		NextPrompt: "\n",
	}
}

// runBrowserWait 统一解析等待工具的超时和轮询间隔，避免每种等待模式有不同零值行为。
func runBrowserWait(ctx context.Context, client browser.Client, tabID string, mode string, params map[string]any, args map[string]any) (browser.WaitResult, error) {
	timeoutMS := asInt(args["timeout_ms"], defaultBrowserWaitTimeoutMS)
	if timeoutMS <= 0 {
		timeoutMS = defaultBrowserWaitTimeoutMS
	}
	intervalMS := asInt(args["interval_ms"], defaultBrowserWaitIntervalMS)
	if intervalMS <= 0 {
		intervalMS = defaultBrowserWaitIntervalMS
	}
	return client.Wait(ctx, tabID, mode, params, timeoutMS, intervalMS)
}

// badSelectorOutcome 返回一致的 CSS selector 参数错误，提示模型重新发现页面元素。
func badSelectorOutcome() agent.Outcome {
	return agent.Outcome{
		Data: agent.NewToolError(
			"browser_bad_selector",
			"selector is required",
			"请提供 CSS selector，例如 button[type=submit] 或 input[name=q]。",
		),
		NextPrompt: "\n",
	}
}

// elementTarget 是页面脚本定位、滚动和命中测试后的元素几何信息。
// 随后的真实点击仍通过 CDP 发送，避免 element.click() 生成不受信任事件。
type elementTarget struct {
	// Rect 是目标元素的 viewport 边界框。
	Rect browser.Rect `json:"rect"`
	// Point 是适合点击或聚焦的 viewport 坐标。
	Point browser.Point `json:"point"`
	// Hit 描述 Point 命中的实际 DOM 元素。
	Hit string `json:"hit"`
	// Value 是定位时读取到的元素当前值或文本。
	Value string `json:"value"`
}

// elementTypeVerification 是真实键盘输入后重新读取 DOM 得到的验证结果。
type elementTypeVerification struct {
	// Actual 是输入后从 DOM 读取到的实际值。
	Actual string `json:"actual"`
	// Verified 表示 Actual 是否满足本次输入预期。
	Verified bool `json:"verified"`
}

// locateElementTarget 在页面内验证 selector、可见性、可编辑性和实际命中点。
//
// 这里的 JavaScript 只做只读定位和滚动；不会调用 element.click()。
// 这样上层可在浏览器桥中发送真实 CDP 输入事件，并保留更接近用户操作的语义。
func locateElementTarget(ctx context.Context, client browser.Client, tabID string, selector string, requireEditable bool) (elementTarget, error) {
	selectorJSON, err := json.Marshal(selector)
	if err != nil {
		return elementTarget{}, err
	}
	// JS 只负责定位、滚动、重测和判断哪个 viewport 点真正落在目标元素上。
	// 真正点击仍由 CDP 完成，避免 element.click() 产生 isTrusted=false 的合成事件。
	script := fmt.Sprintf(`const selector = %s;
const requireEditable = %v;
const element = document.querySelector(selector);
if (!element) {
  throw new Error("element not found: " + selector);
}
element.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
const rect = element.getBoundingClientRect();
const style = getComputedStyle(element);
const visible = rect.width > 0
  && rect.height > 0
  && style.display !== "none"
  && style.visibility !== "hidden"
  && Number(style.opacity || "1") > 0;
if (!visible) {
  throw new Error("element is not visible: " + selector);
}
if (style.pointerEvents === "none") {
  throw new Error("element has pointer-events:none: " + selector);
}
const tag = element.tagName.toLowerCase();
const editable = tag === "input"
  || tag === "textarea"
  || tag === "select"
  || element.isContentEditable
  || element.getAttribute("contenteditable") === "true";
if (requireEditable && !editable) {
  throw new Error("element is not editable: " + selector);
}
const disabled = !!element.disabled || element.getAttribute("aria-disabled") === "true" || element.closest("[aria-disabled='true']");
if (disabled) {
  throw new Error("element is disabled: " + selector);
}
const describe = (node) => {
  if (!node || node.nodeType !== 1) return "";
  const id = node.id ? "#" + node.id : "";
  const cls = node.classList && node.classList.length ? "." + Array.from(node.classList).slice(0, 3).join(".") : "";
  return node.tagName.toLowerCase() + id + cls;
};
const readValue = () => {
  if (tag === "input" || tag === "textarea" || tag === "select") return element.value || "";
  return element.innerText || element.textContent || "";
};
const clamp = (value, min, max) => Math.max(min, Math.min(max, value));
const x1 = clamp(rect.left + Math.min(6, rect.width / 4), 0, window.innerWidth - 1);
const x2 = clamp(rect.left + rect.width / 2, 0, window.innerWidth - 1);
const x3 = clamp(rect.right - Math.min(6, rect.width / 4), 0, window.innerWidth - 1);
const y1 = clamp(rect.top + Math.min(6, rect.height / 4), 0, window.innerHeight - 1);
const y2 = clamp(rect.top + rect.height / 2, 0, window.innerHeight - 1);
const y3 = clamp(rect.bottom - Math.min(6, rect.height / 4), 0, window.innerHeight - 1);
const candidates = [
  { x: x2, y: y2 },
  { x: x1, y: y2 },
  { x: x3, y: y2 },
  { x: x2, y: y1 },
  { x: x2, y: y3 },
  { x: x1, y: y1 },
  { x: x3, y: y1 },
  { x: x1, y: y3 },
  { x: x3, y: y3 }
];
let blockedBy = "";
for (const point of candidates) {
  const hit = document.elementFromPoint(point.x, point.y);
  if (!blockedBy && hit) blockedBy = describe(hit);
  if (hit && (hit === element || element.contains(hit))) {
    return {
      rect: {
        x: rect.x,
        y: rect.y,
        width: rect.width,
        height: rect.height,
        top: rect.top,
        right: rect.right,
        bottom: rect.bottom,
        left: rect.left
      },
      point,
      hit: describe(hit),
      value: readValue()
    };
  }
}
throw new Error("element is covered or not hit-testable: " + selector + (blockedBy ? "; top element: " + blockedBy : ""));
`, string(selectorJSON), requireEditable)
	result, err := client.ExecuteJS(ctx, tabID, script, true, 3000)
	if err != nil {
		return elementTarget{}, err
	}
	if result.Status == "error" {
		return elementTarget{}, fmt.Errorf("browser element lookup error: %v", result.Error)
	}
	var target elementTarget
	if err := json.Unmarshal([]byte(result.JSReturn), &target); err != nil {
		return elementTarget{}, fmt.Errorf("decode element target: %w", err)
	}
	if target.Rect.Width <= 0 || target.Rect.Height <= 0 {
		return elementTarget{}, fmt.Errorf("element has invalid rect: width=%v height=%v", target.Rect.Width, target.Rect.Height)
	}
	if math.IsNaN(target.Point.X) || math.IsNaN(target.Point.Y) {
		return elementTarget{}, errors.New("element has invalid click point")
	}
	return target, nil
}

// verifyElementTyped 在真实输入后读取元素值，确认 clear/append 语义符合预期。
func verifyElementTyped(ctx context.Context, client browser.Client, tabID string, selector string, expected string, clear bool) (string, bool, error) {
	selectorJSON, err := json.Marshal(selector)
	if err != nil {
		return "", false, err
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return "", false, err
	}
	script := fmt.Sprintf(`const selector = %s;
const expected = %s;
const clear = %v;
const element = document.querySelector(selector);
if (!element) {
  throw new Error("element not found after typing: " + selector);
}
const tag = element.tagName.toLowerCase();
const isValueElement = tag === "input" || tag === "textarea" || tag === "select";
const actual = isValueElement ? (element.value || "") : (element.innerText || element.textContent || "");
try {
  element.dispatchEvent(new Event("input", { bubbles: true }));
  element.dispatchEvent(new Event("change", { bubbles: true }));
} catch (_err) {}
return {
  actual,
  verified: clear ? actual === expected : actual.includes(expected)
};`, string(selectorJSON), string(expectedJSON), clear)
	result, err := client.ExecuteJS(ctx, tabID, script, true, 2000)
	if err != nil {
		return "", false, err
	}
	if result.Status == "error" {
		return "", false, fmt.Errorf("browser input verification error: %v", result.Error)
	}
	var verification elementTypeVerification
	if err := json.Unmarshal([]byte(result.JSReturn), &verification); err != nil {
		return "", false, fmt.Errorf("decode input verification: %w", err)
	}
	return verification.Actual, verification.Verified, nil
}

// waitAfterBrowserAction 在有明确标签页 ID 时等待短暂稳定，减少点击后过早判定失败。
func waitAfterBrowserAction(ctx context.Context, client browser.Client, tabID string) (browser.WaitResult, error) {
	if tabID == "" {
		return browser.WaitResult{}, nil
	}
	return client.Wait(ctx, tabID, "stable", map[string]any{"stable_ms": 500}, 1500, 150)
}

// appendBrowserDiff 在两个非空变化摘要之间加入可读分隔符。
func appendBrowserDiff(diff string, extra string) string {
	if extra == "" {
		return diff
	}
	if diff == "" {
		return extra
	}
	return diff + ", " + extra
}

// centerPoint 计算视口矩形中心，用于没有更可靠命中点时的点击回退。
func centerPoint(rect browser.Rect) browser.Point {
	return browser.Point{
		X: rect.Left + rect.Width/2,
		Y: rect.Top + rect.Height/2,
	}
}

// normalizeBrowserURL 清理模型常见的 Markdown 包裹并严格限制为绝对 http/https URL。
func normalizeBrowserURL(raw string) (string, error) {
	// 模型有时会把 URL 当成 Markdown 代码片段，传成 " `https://...` "。
	// 工具层做一次轻量清洗，避免浏览器能力因为格式噪音失败。
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`\"'")
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("browser_open requires an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("browser_open only supports http/https URLs")
	}
	return raw, nil
}

// normalizeBrowserScript 为单行读取表达式补 return，但不篡改带控制流或 JSON 命令的脚本。
func normalizeBrowserScript(script string) string {
	script = strings.TrimSpace(script)
	if script == "" || hasExplicitJSControl(script) {
		return script
	}
	if looksLikeBrowserJSONCommand(script) {
		return script
	}
	// 插件侧使用 AsyncFunction 执行源码：纯表达式如果不写 return 会得到 undefined。
	// 为了满足 document.title 这类常见读页面场景，这里只把单行简单表达式包成 return。
	if strings.ContainsAny(script, ";\n\r") {
		return script
	}
	return "return (" + script + ")"
}

// looksLikeBrowserJSONCommand 识别插件内部的 JSON 命令格式，保持其原样透传。
func looksLikeBrowserJSONCommand(script string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(script), &payload); err != nil {
		return false
	}
	_, hasCmd := payload["cmd"]
	_, hasCommand := payload["command"]
	return hasCmd || hasCommand
}

// hasExplicitJSControl 检测脚本是否已显式包含语句或 return，避免再套表达式包装。
func hasExplicitJSControl(script string) bool {
	lower := strings.ToLower(strings.TrimSpace(script))
	prefixes := []string{
		"return ",
		"return\n",
		"if ",
		"for ",
		"while ",
		"switch ",
		"try ",
		"const ",
		"let ",
		"var ",
		"function ",
		"class ",
		"import ",
		"throw ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.HasPrefix(lower, "return;")
}
