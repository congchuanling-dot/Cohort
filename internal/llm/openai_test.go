package llm

import (
	"strings"
	"testing"
)

func TestParseOpenAISSEText(t *testing.T) {
	sse := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n"))

	out := make(chan Event, 10)
	resp, err := parseOpenAISSE(sse, out)
	if err != nil {
		t.Fatalf("parseOpenAISSE error: %v", err)
	}

	if resp.Content != "你好" {
		t.Fatalf("response content = %q, want %q", resp.Content, "你好")
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("tool calls count = %d, want 0", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 5 || resp.Usage.CacheReadInputTokens != 1 {
		t.Fatalf("usage = %#v, want OpenAI usage", resp.Usage)
	}

	first := <-out
	if first.Type != EventText || first.Text != "你" {
		t.Fatalf("first event = %#v, want text event 你", first)
	}
	second := <-out
	if second.Type != EventText || second.Text != "好" {
		t.Fatalf("second event = %#v, want text event 好", second)
	}
	select {
	case event := <-out:
		t.Fatalf("unexpected extra event: %#v", event)
	default:
	}
}

func TestParseOpenAIJSONUsage_BitsUT(t *testing.T) {
	body := strings.NewReader(`{
		"choices": [
			{"message": {"content": "hello"}}
		],
		"usage": {
			"prompt_tokens": 7,
			"completion_tokens": 4,
			"total_tokens": 11
		}
	}`)

	resp, err := parseOpenAIJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q, want hello", resp.Content)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 4 || resp.Usage.TotalTokens != 11 {
		t.Fatalf("usage = %#v, want parsed OpenAI usage", resp.Usage)
	}
}

func TestParseOpenAISSEToolCalls(t *testing.T) {
	sse := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file","arguments":"{\"path\""}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"_read","arguments":":\"README.md\"}"}}]}}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n"))

	out := make(chan Event, 10)
	resp, err := parseOpenAISSE(sse, out)
	if err != nil {
		t.Fatalf("parseOpenAISSE error: %v", err)
	}

	if resp.Content != "" {
		t.Fatalf("response content = %q, want empty", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls count = %d, want 1", len(resp.ToolCalls))
	}

	call := resp.ToolCalls[0]
	if call.ID != "call_1" {
		t.Fatalf("tool call id = %q, want %q", call.ID, "call_1")
	}
	if call.Type != "function" {
		t.Fatalf("tool call type = %q, want %q", call.Type, "function")
	}
	if call.Function.Name != "file_read" {
		t.Fatalf("tool call name = %q, want %q", call.Function.Name, "file_read")
	}
	if call.Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("tool call arguments = %q, want %q", call.Function.Arguments, `{"path":"README.md"}`)
	}

	select {
	case event := <-out:
		t.Fatalf("unexpected text event: %#v", event)
	default:
	}
}
