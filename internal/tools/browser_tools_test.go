package tools

import (
	"context"
	"errors"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/browser"
)

type fakeBrowserClient struct {
	tabs      []browser.Tab
	openURL   string
	openTabID string
	scanTabID string
	scanMax   int
	err       error
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
