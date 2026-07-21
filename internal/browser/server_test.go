package browser

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBridgeTabsCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ready := tabsMessage{
		Type: messageTypeReady,
		Tabs: []Tab{{ID: "1", Title: "Example", URL: "https://example.com", Active: true}},
	}
	if writeErr := conn.WriteJSON(ready); writeErr != nil {
		t.Fatal(writeErr)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "tabs" {
			t.Errorf("command = %v, want tabs", command["command"])
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status": "success",
				"tabs": []Tab{{
					ID:     "2",
					Title:  "Weather",
					URL:    "https://www.google.com/search?q=weather",
					Active: true,
				}},
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tabs, err := bridge.Tabs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].ID != "2" {
		raw, _ := json.Marshal(tabs)
		t.Fatalf("tabs = %s, want tab id 2", raw)
	}

	<-done
}

func TestBridgeExecuteJSCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "execute_js" {
			t.Errorf("command = %v, want execute_js", command["command"])
			return
		}
		if command["tab_id"] != "7" || command["script"] != "return document.title" {
			t.Errorf("command payload = %+v", command)
			return
		}
		if command["no_monitor"] != true || command["max_return_chars"] != float64(99) {
			t.Errorf("command options = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status":    "success",
				"tab_id":    "7",
				"return":    "Example",
				"truncated": false,
				"diff":      "url and title unchanged",
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.ExecuteJS(ctx, "7", "return document.title", true, 99)
	if err != nil {
		t.Fatal(err)
	}
	if result.JSReturn != "Example" || result.TabID != "7" || result.NewTabs == nil {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeCDPCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "cdp" || command["method"] != "Runtime.evaluate" {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status": "success",
				"tab_id": "7",
				"method": "Runtime.evaluate",
				"result": map[string]any{"value": "Example"},
				"diff":   "monitor disabled",
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.CDP(ctx, "7", "Runtime.evaluate", map[string]any{"expression": "document.title"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.TabID != "7" || result.Method != "Runtime.evaluate" || result.Result["value"] != "Example" {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeClickCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "click" || command["x"] != float64(12) || command["y"] != float64(34) {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status":     "success",
				"tab_id":     "7",
				"clicked_at": map[string]any{"x": 12, "y": 34},
				"diff":       "url and title unchanged",
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.Click(ctx, "7", 12, 34, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.ClickedAt.X != 12 || result.ClickedAt.Y != 34 {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeTypeCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "type" || command["text"] != "hello" || command["clear"] != true {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status": "success",
				"tab_id": "7",
				"text":   "hello",
				"clear":  true,
				"diff":   "url and title unchanged",
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.Type(ctx, "7", "hello", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || !result.Clear {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgePressKeyCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "press_key" || command["key"] != "Cmd+Enter" {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status":    "success",
				"tab_id":    "7",
				"key":       "Enter",
				"modifiers": []string{"Meta"},
				"diff":      "focus input->body",
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.PressKey(ctx, "7", "Cmd+Enter", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Key != "Enter" || len(result.Modifiers) != 1 || result.Modifiers[0] != "Meta" {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeSnapshotCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "snapshot" || command["max_elements"] != float64(12) {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status": "success",
				"tab_id": "7",
				"title":  "demo",
				"url":    "https://example.com",
				"count":  1,
				"elements": []map[string]any{{
					"index":    1,
					"tag":      "button",
					"text":     "发送",
					"selector": "button:nth-of-type(1)",
					"visible":  true,
					"disabled": false,
				}},
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.Snapshot(ctx, "7", 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || len(result.Elements) != 1 || result.Elements[0].Text != "发送" {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeScreenshotCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "screenshot" || command["format"] != "png" || command["full_page"] != true || command["quality"] != float64(90) {
			t.Errorf("command payload = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status": "success",
				"tab_id": "7",
				"format": "png",
				"data":   "ZmFrZQ==",
				"width":  800,
				"height": 600,
				"scale":  1,
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.Screenshot(ctx, "7", "png", true, 90)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data != "ZmFrZQ==" || result.Width != 800 || result.Height != 600 {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}

func TestBridgeWaitCommandWithMockExtension(t *testing.T) {
	bridge := NewBridge("127.0.0.1:0", DefaultPath)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Close(context.Background())

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+bridge.Addr()+DefaultPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var command map[string]any
		if readErr := conn.ReadJSON(&command); readErr != nil {
			t.Errorf("read command: %v", readErr)
			return
		}
		if command["command"] != "wait" || command["mode"] != "selector" || command["selector"] != ".result" {
			t.Errorf("command payload = %+v", command)
			return
		}
		if command["timeout_ms"] != float64(5000) || command["interval_ms"] != float64(200) {
			t.Errorf("command timing = %+v", command)
			return
		}
		id := command["id"].(string)
		result := map[string]any{
			"type": messageTypeResult,
			"id":   id,
			"result": map[string]any{
				"status":     "success",
				"tab_id":     "7",
				"mode":       "selector",
				"matched":    true,
				"elapsed_ms": 400,
				"selector":   ".result",
				"state":      "visible",
				"exists":     true,
				"visible":    true,
			},
		}
		if writeErr := conn.WriteJSON(result); writeErr != nil {
			t.Errorf("write result: %v", writeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := bridge.Wait(ctx, "7", "selector", map[string]any{"selector": ".result", "state": "visible"}, 5000, 200)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "selector" || !result.Matched || result.Selector != ".result" {
		raw, _ := json.Marshal(result)
		t.Fatalf("result = %s", raw)
	}

	<-done
}
