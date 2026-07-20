package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/browser"
	"cohert/internal/llm"
)

const (
	defaultBrowserScanChars      = 12000
	defaultBrowserJSReturnChars  = 8000
	defaultBrowserSnapshotItems  = 80
	defaultBrowserWaitTimeoutMS  = 10000
	defaultBrowserWaitIntervalMS = 200
)

// BrowserTabs 把浏览器标签页列表暴露给模型。
// 模型不知道当前浏览器状态时，应先调用这个工具再决定打开或扫描哪个 tab。
type BrowserTabs struct {
	client browser.Client
}

func NewBrowserTabs(client browser.Client) *BrowserTabs {
	return &BrowserTabs{client: client}
}

func (t *BrowserTabs) Name() string { return ToolNameBrowserTabs }

func (t *BrowserTabs) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "List current Chrome tabs visible to the Cohert Browser Bridge.",
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

// BrowserExecuteJS 在当前或指定 tab 的页面上下文里执行 JavaScript。
// 第一版复用插件 execute_js 命令，Go 层负责把返回结构稳定成 js_return/new_tabs。
type BrowserExecuteJS struct {
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
	rect, err := locateElementRect(ctx, t.client, tabID, selector)
	if err != nil {
		return browserToolError(err), nil
	}
	point := centerPoint(rect)
	click, err := t.client.Click(ctx, tabID, point.X, point.Y, asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	result := browser.ElementClickResult{
		Status:    click.Status,
		TabID:     click.TabID,
		Selector:  selector,
		Rect:      rect,
		ClickedAt: click.ClickedAt,
		Diff:      click.Diff,
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserType 向当前焦点输入文本。聚焦由 browser_click 或 browser_type_element 负责。
type BrowserType struct {
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

// BrowserTypeElement 先按 selector 定位并点击聚焦，再输入文本。
type BrowserTypeElement struct {
	client browser.Client
}

func NewBrowserTypeElement(client browser.Client) *BrowserTypeElement {
	return &BrowserTypeElement{client: client}
}

func (t *BrowserTypeElement) Name() string { return ToolNameBrowserTypeElement }

func (t *BrowserTypeElement) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find an input-like element by CSS selector, click it with CDP to focus, then type text with CDP keyboard input.",
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
	rect, err := locateElementRect(ctx, t.client, tabID, selector)
	if err != nil {
		return browserToolError(err), nil
	}
	point := centerPoint(rect)
	if _, err := t.client.Click(ctx, tabID, point.X, point.Y, true); err != nil {
		return browserToolError(err), nil
	}
	typed, err := t.client.Type(ctx, tabID, text, asBool(call.Args["clear"], false), asBool(call.Args["no_monitor"], false))
	if err != nil {
		return browserToolError(err), nil
	}
	result := browser.ElementTypeResult{
		Status:   typed.Status,
		TabID:    typed.TabID,
		Selector: selector,
		Rect:     rect,
		TypedAt:  point,
		Text:     typed.Text,
		Clear:    typed.Clear,
		Diff:     typed.Diff,
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

// BrowserPressKey 发送 Enter、Escape、Tab、Cmd+Enter 等真实键盘按键。
// 它封装 CDP Input.dispatchKeyEvent，避免模型手写底层 keyDown/keyUp 参数。
type BrowserPressKey struct {
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

// BrowserWaitForStable 等待页面进入短时间稳定状态。
// 它适合页面 load 已完成但前端 JS 仍在持续渲染的场景。
type BrowserWaitForStable struct {
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

func browserToolError(err error) agent.Outcome {
	code := "browser_error"
	hint := "确认 Chrome 已安装 Cohert Browser Bridge 插件，并且 Cohert 正在运行；插件会连接 ws://127.0.0.1:18777/browser。"
	if errors.Is(err, browser.ErrNotConnected) {
		code = "browser_not_connected"
		hint = "请在 Chrome 的 chrome://extensions 中加载 assert/cohert_browser_bridge，并打开任意 http/https 页面；然后重试。"
	}
	return agent.Outcome{
		Data:       agent.NewToolError(code, err.Error(), hint),
		NextPrompt: "\n",
	}
}

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

func locateElementRect(ctx context.Context, client browser.Client, tabID string, selector string) (browser.Rect, error) {
	selectorJSON, err := json.Marshal(selector)
	if err != nil {
		return browser.Rect{}, err
	}
	// 这里让 JS 只做两件事：滚动目标元素到 viewport 内，并读取真实渲染后的 rect。
	// 不在 JS 里调用 element.click()，避免产生 isTrusted=false 的合成点击事件。
	script := fmt.Sprintf(`const selector = %s;
const element = document.querySelector(selector);
if (!element) {
  throw new Error("element not found: " + selector);
}
element.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
await new Promise((resolve) => requestAnimationFrame(() => resolve()));
const rect = element.getBoundingClientRect();
const style = getComputedStyle(element);
if (rect.width <= 0 || rect.height <= 0 || style.display === "none" || style.visibility === "hidden") {
  throw new Error("element is not visible: " + selector);
}
return {
  x: rect.x,
  y: rect.y,
  width: rect.width,
  height: rect.height,
  top: rect.top,
  right: rect.right,
  bottom: rect.bottom,
  left: rect.left
};`, string(selectorJSON))
	result, err := client.ExecuteJS(ctx, tabID, script, true, 2000)
	if err != nil {
		return browser.Rect{}, err
	}
	if result.Status == "error" {
		return browser.Rect{}, fmt.Errorf("browser element lookup error: %v", result.Error)
	}
	var rect browser.Rect
	if err := json.Unmarshal([]byte(result.JSReturn), &rect); err != nil {
		return browser.Rect{}, fmt.Errorf("decode element rect: %w", err)
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return browser.Rect{}, fmt.Errorf("element has invalid rect: width=%v height=%v", rect.Width, rect.Height)
	}
	return rect, nil
}

func centerPoint(rect browser.Rect) browser.Point {
	return browser.Point{
		X: rect.Left + rect.Width/2,
		Y: rect.Top + rect.Height/2,
	}
}

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

func looksLikeBrowserJSONCommand(script string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(script), &payload); err != nil {
		return false
	}
	_, hasCmd := payload["cmd"]
	_, hasCommand := payload["command"]
	return hasCmd || hasCommand
}

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
