package tools

import (
	"context"
	"errors"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/browser"
)

type fakeBrowserClient struct {
	tabs             []browser.Tab
	openURL          string
	openTabID        string
	scanTabID        string
	scanMax          int
	executeTabID     string
	executeScript    string
	executeNoMonitor bool
	executeMaxReturn int
	cdpTabID         string
	cdpMethod        string
	cdpParams        map[string]any
	clickTabID       string
	clickX           float64
	clickY           float64
	typeTabID        string
	typeText         string
	typeClear        bool
	pressKeyTabID    string
	pressKey         string
	snapshotTabID    string
	snapshotMax      int
	waitTabID        string
	waitMode         string
	waitParams       map[string]any
	waitTimeoutMS    int
	waitIntervalMS   int
	err              error
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
