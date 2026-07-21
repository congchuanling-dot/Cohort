package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/browser"
)

type fakeBrowserClient struct {
	// tabs 是 Tabs 方法返回的预设标签页列表。
	tabs []browser.Tab
	// openURL 记录 Open 方法收到的 URL。
	openURL string
	// openTabID 记录 Open 方法收到的 tab ID。
	openTabID string
	// scanTabID 记录 Scan 方法收到的 tab ID。
	scanTabID string
	// scanMax 记录 Scan 方法收到的最大字符数。
	scanMax int
	// executeTabID 记录 ExecuteJS 方法收到的 tab ID。
	executeTabID string
	// executeScript 记录 ExecuteJS 方法收到的脚本。
	executeScript string
	// executeNoMonitor 记录 ExecuteJS 是否关闭页面变化监控。
	executeNoMonitor bool
	// executeMaxReturn 记录 ExecuteJS 的最大返回字符数。
	executeMaxReturn int
	// cdpTabID 记录 CDP 方法收到的 tab ID。
	cdpTabID string
	// cdpMethod 记录 CDP 方法名。
	cdpMethod string
	// cdpParams 记录 CDP 参数。
	cdpParams map[string]any
	// clickTabID 记录 Click 方法收到的 tab ID。
	clickTabID string
	// clickX 记录 Click 方法收到的 viewport 横坐标。
	clickX float64
	// clickY 记录 Click 方法收到的 viewport 纵坐标。
	clickY float64
	// typeTabID 记录 Type 方法收到的 tab ID。
	typeTabID string
	// typeText 记录 Type 方法收到的输入文本。
	typeText string
	// typeClear 记录 Type 方法是否要求清空旧文本。
	typeClear bool
	// pressKeyTabID 记录 PressKey 方法收到的 tab ID。
	pressKeyTabID string
	// pressKey 记录 PressKey 方法收到的按键名称。
	pressKey string
	// snapshotTabID 记录 Snapshot 方法收到的 tab ID。
	snapshotTabID string
	// snapshotMax 记录 Snapshot 方法收到的最大元素数。
	snapshotMax int
	// screenshotTabID 记录 Screenshot 方法收到的 tab ID。
	screenshotTabID string
	// screenshotFormat 记录 Screenshot 方法收到的图片格式。
	screenshotFormat string
	// screenshotFull 记录 Screenshot 方法是否要求整页截图。
	screenshotFull bool
	// screenshotQuality 记录 Screenshot 方法收到的图片质量。
	screenshotQuality int
	// screenshotData 是 Screenshot 方法返回的预设 base64 图片数据。
	screenshotData string
	// waitTabID 记录 Wait 方法收到的 tab ID。
	waitTabID string
	// waitMode 记录 Wait 方法收到的等待模式。
	waitMode string
	// waitParams 记录 Wait 方法收到的等待参数。
	waitParams map[string]any
	// waitTimeoutMS 记录 Wait 方法收到的超时时间。
	waitTimeoutMS int
	// waitIntervalMS 记录 Wait 方法收到的轮询间隔。
	waitIntervalMS int
	// err 是 fake client 各方法统一返回的预设错误。
	err error
}

func (f *fakeBrowserClient) Tabs(ctx context.Context) ([]browser.Tab, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tabs, nil
}

func (f *fakeBrowserClient) Open(ctx context.Context, url string, tabID string, active bool) (browser.OpenResult, error) {
	if f.err != nil {
		return browser.OpenResult{}, f.err
	}
	f.openURL = url
	f.openTabID = tabID
	return browser.OpenResult{Status: agent.ToolStatusSuccess, TabID: "1", URL: url}, nil
}

func (f *fakeBrowserClient) Scan(ctx context.Context, tabID string, maxChars int) (browser.PageSnapshot, error) {
	if f.err != nil {
		return browser.PageSnapshot{}, f.err
	}
	f.scanTabID = tabID
	f.scanMax = maxChars
	return browser.PageSnapshot{Status: agent.ToolStatusSuccess, TabID: tabID, Text: "重庆明日天气"}, nil
}

func (f *fakeBrowserClient) ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (browser.ExecuteJSResult, error) {
	if f.err != nil {
		return browser.ExecuteJSResult{}, f.err
	}
	f.executeTabID = tabID
	f.executeScript = script
	f.executeNoMonitor = noMonitor
	f.executeMaxReturn = maxReturnChars
	return browser.ExecuteJSResult{
		Status:   agent.ToolStatusSuccess,
		TabID:    tabID,
		JSReturn: "Example",
		NewTabs:  []browser.Tab{},
	}, nil
}

func (f *fakeBrowserClient) CDP(ctx context.Context, tabID string, method string, params map[string]any, noMonitor bool) (browser.CDPResult, error) {
	if f.err != nil {
		return browser.CDPResult{}, f.err
	}
	f.cdpTabID = tabID
	f.cdpMethod = method
	f.cdpParams = params
	return browser.CDPResult{Status: agent.ToolStatusSuccess, TabID: tabID, Method: method, Result: map[string]any{}}, nil
}

func (f *fakeBrowserClient) Click(ctx context.Context, tabID string, x float64, y float64, noMonitor bool) (browser.ClickResult, error) {
	if f.err != nil {
		return browser.ClickResult{}, f.err
	}
	f.clickTabID = tabID
	f.clickX = x
	f.clickY = y
	return browser.ClickResult{Status: agent.ToolStatusSuccess, TabID: tabID, ClickedAt: browser.Point{X: x, Y: y}}, nil
}

func (f *fakeBrowserClient) Type(ctx context.Context, tabID string, text string, clear bool, noMonitor bool) (browser.TypeResult, error) {
	if f.err != nil {
		return browser.TypeResult{}, f.err
	}
	f.typeTabID = tabID
	f.typeText = text
	f.typeClear = clear
	return browser.TypeResult{Status: agent.ToolStatusSuccess, TabID: tabID, Text: text, Clear: clear}, nil
}

func (f *fakeBrowserClient) PressKey(ctx context.Context, tabID string, key string, noMonitor bool) (browser.PressKeyResult, error) {
	if f.err != nil {
		return browser.PressKeyResult{}, f.err
	}
	f.pressKeyTabID = tabID
	f.pressKey = key
	return browser.PressKeyResult{Status: agent.ToolStatusSuccess, TabID: tabID, Key: key}, nil
}

func (f *fakeBrowserClient) Snapshot(ctx context.Context, tabID string, maxElements int) (browser.InteractiveSnapshot, error) {
	if f.err != nil {
		return browser.InteractiveSnapshot{}, f.err
	}
	f.snapshotTabID = tabID
	f.snapshotMax = maxElements
	return browser.InteractiveSnapshot{
		Status: agent.ToolStatusSuccess,
		TabID:  tabID,
		Elements: []browser.InteractiveElement{{
			Index:    1,
			Tag:      "button",
			Text:     "发送",
			Selector: "button:nth-of-type(1)",
			Visible:  true,
		}},
		Count: 1,
	}, nil
}

func (f *fakeBrowserClient) Screenshot(ctx context.Context, tabID string, format string, fullPage bool, quality int) (browser.ScreenshotResult, error) {
	if f.err != nil {
		return browser.ScreenshotResult{}, f.err
	}
	f.screenshotTabID = tabID
	f.screenshotFormat = format
	f.screenshotFull = fullPage
	f.screenshotQuality = quality
	data := f.screenshotData
	if data == "" {
		data = base64.StdEncoding.EncodeToString([]byte("fake image"))
	}
	return browser.ScreenshotResult{
		Status: agent.ToolStatusSuccess,
		TabID:  tabID,
		Format: format,
		Data:   data,
		Width:  800,
		Height: 600,
	}, nil
}

func (f *fakeBrowserClient) Wait(ctx context.Context, tabID string, mode string, params map[string]any, timeoutMS int, intervalMS int) (browser.WaitResult, error) {
	if f.err != nil {
		return browser.WaitResult{}, f.err
	}
	f.waitTabID = tabID
	f.waitMode = mode
	f.waitParams = params
	f.waitTimeoutMS = timeoutMS
	f.waitIntervalMS = intervalMS
	return browser.WaitResult{Status: agent.ToolStatusSuccess, TabID: tabID, Mode: mode, Matched: true}, nil
}

func TestBrowserOpenRejectsNonHTTPURL(t *testing.T) {
	tool := NewBrowserOpen(&fakeBrowserClient{})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"url": "file:///tmp/a.html"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(agent.ToolErrorData)
	if data.Code != "browser_bad_url" {
		t.Fatalf("code = %q, want browser_bad_url", data.Code)
	}
}

func TestBrowserCDPCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserCDP(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"tab_id": "9",
			"method": "Runtime.evaluate",
			"params": map[string]any{"expression": "document.title"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.cdpTabID != "9" || client.cdpMethod != "Runtime.evaluate" || client.cdpParams["expression"] != "document.title" {
		t.Fatalf("cdp call = tab %q method %q params %+v", client.cdpTabID, client.cdpMethod, client.cdpParams)
	}
}

func TestBrowserClickCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserClick(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "x": 12.5, "y": 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.clickTabID != "5" || client.clickX != 12.5 || client.clickY != 40 {
		t.Fatalf("click call = tab %q x %v y %v", client.clickTabID, client.clickX, client.clickY)
	}
}

func TestBrowserTypeCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserType(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "text": "hello", "clear": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.typeTabID != "5" || client.typeText != "hello" || !client.typeClear {
		t.Fatalf("type call = tab %q text %q clear %v", client.typeTabID, client.typeText, client.typeClear)
	}
}

func TestBrowserPressKeyCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserPressKey(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "key": "Cmd+Enter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.pressKeyTabID != "5" || client.pressKey != "Cmd+Enter" {
		t.Fatalf("press key call = tab %q key %q", client.pressKeyTabID, client.pressKey)
	}
}

func TestBrowserSnapshotCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserSnapshot(client)
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "max_elements": 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.snapshotTabID != "5" || client.snapshotMax != 12 {
		t.Fatalf("snapshot call = tab %q max %d", client.snapshotTabID, client.snapshotMax)
	}
	data := outcome.Data.(browser.InteractiveSnapshot)
	if len(data.Elements) != 1 || data.Elements[0].Text != "发送" {
		t.Fatalf("snapshot result = %+v", data)
	}
}

func TestBrowserScreenshotSavesImage(t *testing.T) {
	client := &fakeBrowserClient{
		screenshotData: base64.StdEncoding.EncodeToString([]byte("png bytes")),
	}
	tool := NewBrowserScreenshot(client, t.TempDir())
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "format": "png", "full_page": true, "quality": 80},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.screenshotTabID != "5" || client.screenshotFormat != "png" || !client.screenshotFull || client.screenshotQuality != 80 {
		t.Fatalf("screenshot call = tab %q format %q full %v quality %d", client.screenshotTabID, client.screenshotFormat, client.screenshotFull, client.screenshotQuality)
	}
	data := outcome.Data.(map[string]any)
	imagePath := data["image_path"].(string)
	content, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "png bytes" {
		t.Fatalf("saved screenshot = %q", string(content))
	}
}

func TestBrowserWaitForLoadCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserWaitForLoad(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "5", "timeout_ms": 1234, "interval_ms": 250},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitTabID != "5" || client.waitMode != "load" || client.waitTimeoutMS != 1234 || client.waitIntervalMS != 250 {
		t.Fatalf("wait call = tab %q mode %q timeout %d interval %d", client.waitTabID, client.waitMode, client.waitTimeoutMS, client.waitIntervalMS)
	}
}

func TestBrowserWaitForSelectorCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserWaitForSelector(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"selector": ".result", "state": "attached"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitMode != "selector" || client.waitParams["selector"] != ".result" || client.waitParams["state"] != "attached" {
		t.Fatalf("wait params = mode %q params %+v", client.waitMode, client.waitParams)
	}
}

func TestBrowserWaitForTextCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserWaitForText(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"text": "提交成功"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitMode != "text" || client.waitParams["text"] != "提交成功" {
		t.Fatalf("wait params = mode %q params %+v", client.waitMode, client.waitParams)
	}
}

func TestBrowserWaitForURLCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserWaitForURL(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"url_contains": "/search", "timeout_ms": 4321},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitMode != "url" || client.waitParams["url_contains"] != "/search" || client.waitTimeoutMS != 4321 {
		t.Fatalf("wait url params = mode %q params %+v timeout %d", client.waitMode, client.waitParams, client.waitTimeoutMS)
	}
}

func TestBrowserWaitForStableCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserWaitForStable(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"stable_ms": 900},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitMode != "stable" || client.waitParams["stable_ms"] != 900 {
		t.Fatalf("wait params = mode %q params %+v", client.waitMode, client.waitParams)
	}
}

func TestBrowserOpenCallsClient(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserOpen(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"url":    "https://www.google.com/search?q=%E9%87%8D%E5%BA%86%E6%98%8E%E6%97%A5%E5%A4%A9%E6%B0%94",
			"tab_id": "7",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.openURL == "" || client.openTabID != "7" {
		t.Fatalf("openURL=%q openTabID=%q", client.openURL, client.openTabID)
	}
}

func TestBrowserOpenNormalizesMarkdownURL(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserOpen(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"url": " `https://www.weather.com.cn/weather/101040100.shtml` ",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.openURL != "https://www.weather.com.cn/weather/101040100.shtml" {
		t.Fatalf("openURL = %q", client.openURL)
	}
}

func TestBrowserScanUsesDefaultMaxChars(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserScan(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"tab_id": "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.scanTabID != "3" {
		t.Fatalf("scanTabID = %q, want 3", client.scanTabID)
	}
	if client.scanMax != defaultBrowserScanChars {
		t.Fatalf("scanMax = %d, want %d", client.scanMax, defaultBrowserScanChars)
	}
}

func TestBrowserExecuteJSWrapsSimpleExpression(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserExecuteJS(client)
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"tab_id":     "3",
			"script":     "document.title",
			"no_monitor": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.executeTabID != "3" {
		t.Fatalf("executeTabID = %q, want 3", client.executeTabID)
	}
	if client.executeScript != "return (document.title)" {
		t.Fatalf("executeScript = %q", client.executeScript)
	}
	if !client.executeNoMonitor {
		t.Fatal("executeNoMonitor = false, want true")
	}
	if client.executeMaxReturn != defaultBrowserJSReturnChars {
		t.Fatalf("executeMaxReturn = %d, want %d", client.executeMaxReturn, defaultBrowserJSReturnChars)
	}
	data := outcome.Data.(browser.ExecuteJSResult)
	if data.JSReturn != "Example" || data.NewTabs == nil {
		t.Fatalf("result = %+v, want js return and non-nil new_tabs", data)
	}
}

func TestBrowserExecuteJSKeepsJSONCommandRaw(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserExecuteJS(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"script": `{"cmd":"cdp","method":"Runtime.evaluate","params":{"expression":"document.title"}}`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.executeScript != `{"cmd":"cdp","method":"Runtime.evaluate","params":{"expression":"document.title"}}` {
		t.Fatalf("executeScript = %q", client.executeScript)
	}
}

func TestBrowserExecuteJSKeepsExplicitReturn(t *testing.T) {
	client := &fakeBrowserClient{}
	tool := NewBrowserExecuteJS(client)
	_, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"script":           "return document.body.innerText",
			"max_return_chars": 42,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.executeScript != "return document.body.innerText" {
		t.Fatalf("executeScript = %q", client.executeScript)
	}
	if client.executeMaxReturn != 42 {
		t.Fatalf("executeMaxReturn = %d, want 42", client.executeMaxReturn)
	}
}

func TestBrowserExecuteJSRejectsEmptyScript(t *testing.T) {
	tool := NewBrowserExecuteJS(&fakeBrowserClient{})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"script": " \n "},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(agent.ToolErrorData)
	if data.Code != "browser_bad_script" {
		t.Fatalf("code = %q, want browser_bad_script", data.Code)
	}
}

func TestBrowserToolReturnsNotConnectedError(t *testing.T) {
	tool := NewBrowserTabs(&fakeBrowserClient{err: browser.ErrNotConnected})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(agent.ToolErrorData)
	if data.Code != "browser_not_connected" {
		t.Fatalf("code = %q, want browser_not_connected", data.Code)
	}
}

func TestBrowserToolReturnsGenericError(t *testing.T) {
	tool := NewBrowserTabs(&fakeBrowserClient{err: errors.New("boom")})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(agent.ToolErrorData)
	if data.Code != "browser_error" {
		t.Fatalf("code = %q, want browser_error", data.Code)
	}
}
