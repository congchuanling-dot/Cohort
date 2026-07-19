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
