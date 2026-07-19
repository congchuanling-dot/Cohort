package tools

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/browser"
	"cohert/internal/llm"
)

const (
	defaultBrowserScanChars     = 12000
	defaultBrowserJSReturnChars = 8000
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
	// 插件侧使用 AsyncFunction 执行源码：纯表达式如果不写 return 会得到 undefined。
	// 为了满足 document.title 这类常见读页面场景，这里只把单行简单表达式包成 return。
	if strings.ContainsAny(script, ";\n\r") {
		return script
	}
	return "return (" + script + ")"
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
