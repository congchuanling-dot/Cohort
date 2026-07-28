package llm

import (
	"strings"
	"testing"
)

func TestBuildAnthropicMessagesConvertsToolCalls_BitsUT(t *testing.T) {
	system, messages, err := buildAnthropicMessages("system prompt", []Message{
		{Role: RoleUser, Content: "read file"},
		{Role: RoleAssistant, Content: "I'll read it.", ToolCalls: []ToolCall{{
			ID: "toolu_1",
			Function: ToolFunction{
				Name:      "file_read",
				Arguments: `{"path":"README.md"}`,
			},
		}}},
		{Role: RoleTool, ToolCallID: "toolu_1", Content: "content"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if system != "system prompt" {
		t.Fatalf("system = %q, want system prompt", system)
	}
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(messages))
	}
	assistant := messages[1]
	if assistant.Role != RoleAssistant || len(assistant.Content) != 2 {
		t.Fatalf("assistant message = %#v, want text + tool_use", assistant)
	}
	toolUse := assistant.Content[1]
	if toolUse.Type != "tool_use" || toolUse.ID != "toolu_1" || toolUse.Name != "file_read" {
		t.Fatalf("tool use block = %#v, want file_read tool_use", toolUse)
	}
	input := toolUse.Input.(map[string]any)
	if input["path"] != "README.md" {
		t.Fatalf("tool input = %#v, want README path", input)
	}
	toolResult := messages[2]
	if toolResult.Role != RoleUser || len(toolResult.Content) != 1 || toolResult.Content[0].ToolUseID != "toolu_1" {
		t.Fatalf("tool result message = %#v, want user tool_result", toolResult)
	}
}

func TestParseAnthropicJSONConvertsToolUse_BitsUT(t *testing.T) {
	body := strings.NewReader(`{
		"content": [
			{"type": "text", "text": "Need a file."},
			{"type": "tool_use", "id": "toolu_1", "name": "file_read", "input": {"path": "README.md"}}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 5,
			"cache_creation_input_tokens": 2,
			"cache_read_input_tokens": 3
		}
	}`)

	resp, err := parseAnthropicJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Need a file." {
		t.Fatalf("content = %q, want text", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_1" || call.Function.Name != "file_read" || call.Function.Arguments != `{"path": "README.md"}` {
		t.Fatalf("tool call = %#v, want converted Anthropic tool_use", call)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 || resp.Usage.TotalTokens != 15 || resp.Usage.CacheCreationInputTokens != 2 || resp.Usage.CacheReadInputTokens != 3 {
		t.Fatalf("usage = %#v, want parsed Anthropic usage", resp.Usage)
	}
}

func TestParseAnthropicSSETextAndToolUse_BitsUT(t *testing.T) {
	sse := strings.NewReader(strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":8,"output_tokens":0,"cache_read_input_tokens":4}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		"",
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"file_read","input":{}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":6}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n"))

	out := make(chan Event, 10)
	resp, err := parseAnthropicSSE(sse, out)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Content != "Hi" {
		t.Fatalf("content = %q, want Hi", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_1" || call.Function.Name != "file_read" || call.Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("tool call = %#v, want streamed tool_use", call)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 6 || resp.Usage.TotalTokens != 14 || resp.Usage.CacheReadInputTokens != 4 {
		t.Fatalf("usage = %#v, want streamed Anthropic usage", resp.Usage)
	}
	first := <-out
	if first.Type != EventText || first.Text != "Hi" {
		t.Fatalf("first event = %#v, want text Hi", first)
	}
	select {
	case event := <-out:
		t.Fatalf("unexpected extra event: %#v", event)
	default:
	}
}

func TestAnthropicMessagesURL_BitsUT(t *testing.T) {
	tests := map[string]string{
		"":                                "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com":       "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/v1":    "https://api.anthropic.com/v1/messages",
		"https://proxy.test/v1/messages/": "https://proxy.test/v1/messages",
	}
	for input, want := range tests {
		if got := anthropicMessagesURL(input); got != want {
			t.Fatalf("anthropicMessagesURL(%q) = %q, want %q", input, got, want)
		}
	}
}
