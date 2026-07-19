package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/contextmgr"
	"cohert/internal/llm"
)

type contextRecordingClient struct {
	responses []llm.Response
	requests  []llm.ChatRequest
}

func (c *contextRecordingClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.requests = append(c.requests, req)
	response := llm.Response{}
	if len(c.responses) > 0 {
		response = c.responses[0]
		c.responses = c.responses[1:]
	}
	out := make(chan llm.Event, 2)
	if response.Content != "" {
		out <- llm.Event{Type: llm.EventText, Text: response.Content}
	}
	out <- llm.Event{Type: llm.EventDone, Response: &response}
	close(out)
	return out, nil
}

type contextFakeTools struct {
	result string
}

func (contextFakeTools) Schemas() []llm.ToolSchema {
	return nil
}

func (t contextFakeTools) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	return Outcome{Data: t.result, NextPrompt: "\n"}, nil
}

func TestRunnerUsesContextManagerForModelRequest_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "ok"}},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     3,
			KeepRecentToolResults:  1,
			MaxToolResultChars:     1000,
			CompactedToolHeadChars: 100,
			CompactedToolTailChars: 100,
			MaxRequestChars:        10000,
			ContextWindowTokens:    25,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "old user"},
			{Role: llm.RoleAssistant, Content: "old answer"},
			{Role: llm.RoleUser, Content: "middle user"},
		},
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "latest user", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	requestMessages := client.requests[0].Messages
	if len(requestMessages) != 3 {
		t.Fatalf("request messages = %d, want 3: %#v", len(requestMessages), requestMessages)
	}
	if requestMessages[0].Content != "[Cohert context notice] Earlier conversation messages were omitted from this request. Full history is preserved in history.jsonl." {
		t.Fatalf("first request message = %#v, want context notice", requestMessages[0])
	}
	if requestMessages[2].Content != "latest user" {
		t.Fatalf("latest user input was not preserved: %#v", requestMessages)
	}
	if len(runner.history) != 5 {
		t.Fatalf("runner history = %d, want full 5 messages", len(runner.history))
	}
	if runner.history[0].Content != "old user" {
		t.Fatalf("full history was modified: %#v", runner.history)
	}
}

func TestRunnerLogsContextStats_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "ok"}},
	}
	logDir := t.TempDir()
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
		LogDir:   logDir,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     10,
			KeepRecentToolResults:  1,
			MaxToolResultChars:     1000,
			CompactedToolHeadChars: 100,
			CompactedToolTailChars: 100,
			MaxRequestChars:        10000,
			ContextWindowTokens:    1000000,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
	}

	_, err := runner.Run(context.Background(), "hello", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(logDir, contextStatsLogFileName))
	if err != nil {
		t.Fatal(err)
	}
	logLine := string(data)
	for _, want := range []string{`"original_messages":1`, `"final_messages":1`, `"trigger_reason":"below_compact_trigger_threshold"`} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("context stats log does not contain %q:\n%s", want, logLine)
		}
	}
	if strings.Contains(logLine, "hello") {
		t.Fatalf("context stats log leaked message content:\n%s", logLine)
	}
}

func TestRunnerRebuildsContextAfterToolResult_BitsUT(t *testing.T) {
	longResult := strings.Repeat("A", 40) + strings.Repeat("Z", 40)
	client := &contextRecordingClient{
		responses: []llm.Response{
			{
				ToolCalls: []llm.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: llm.ToolFunction{
						Name:      "code_run",
						Arguments: `{}`,
					},
				}},
			},
			{Content: "done"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: longResult},
		MaxTurns: 2,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     20,
			KeepRecentToolResults:  0,
			MaxToolResultChars:     30,
			CompactedToolHeadChars: 5,
			CompactedToolTailChars: 5,
			MaxRequestChars:        10000,
			ContextWindowTokens:    80,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "run command", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	secondRequest := client.requests[1].Messages
	var compactedTool string
	for _, message := range secondRequest {
		if message.Role == llm.RoleTool {
			compactedTool = message.Content
		}
	}
	if !strings.Contains(compactedTool, "[tool result compacted]") {
		t.Fatalf("second request did not receive compacted tool result:\n%#v", secondRequest)
	}
	for _, message := range runner.history {
		if message.Role == llm.RoleTool && message.Content != longResult {
			t.Fatalf("full history tool result was modified: %#v", message)
		}
	}
}
