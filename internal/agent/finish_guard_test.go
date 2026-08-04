package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"cohort/internal/llm"
)

type recordingToolRunner struct {
	calls []ToolCallContext
}

func (r *recordingToolRunner) Schemas() []llm.ToolSchema {
	return nil
}

func (r *recordingToolRunner) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	r.calls = append(r.calls, call)
	return Outcome{Data: map[string]any{"status": ToolStatusSuccess}}, nil
}

func TestRunnerParsesTextToolUseFallback_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{Content: `我需要读文件。
<tool_use>{"name":"file_read","arguments":{"path":"tmp.txt"}}</tool_use>`},
			{Content: "已读取"},
		},
	}
	tools := &recordingToolRunner{}
	runner := &Runner{
		Client:   client,
		Tools:    tools,
		MaxTurns: 3,
	}

	result, err := runner.Run(context.Background(), "读取 README", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done; requests=%d tool_calls=%d history=%#v", result.Status, len(client.requests), len(tools.calls), runner.history)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(tools.calls))
	}
	call := tools.calls[0]
	if call.Name != "file_read" || call.Args["path"] != "tmp.txt" {
		t.Fatalf("call = %#v, want file_read tmp.txt", call)
	}
	if len(runner.history) < 2 || strings.Contains(runner.history[1].Content, "<tool_use>") {
		t.Fatalf("assistant history kept raw tool_use block: %#v", runner.history)
	}
}

func TestRunnerRetriesInvalidTextToolUseOnce_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{Content: `<tool_use>{"name":"file_read","arguments":`},
			{Content: `<tool_use>{"name":"file_read","arguments":{"path":"tmp.txt"}}</tool_use>`},
			{Content: "ok"},
		},
	}
	tools := &recordingToolRunner{}
	runner := &Runner{
		Client:   client,
		Tools:    tools,
		MaxTurns: 3,
	}

	result, err := runner.Run(context.Background(), "读取 README", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) < 2 {
		t.Fatalf("requests = %d, want retry", len(client.requests))
	}
	retryMessages := client.requests[1].Messages
	last := retryMessages[len(retryMessages)-1]
	if !strings.Contains(last.Content, "TEXT TOOL_USE PARSE ERROR") {
		t.Fatalf("retry hint = %q, want parse error hint", last.Content)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("tool calls = %d, want 1 after retry", len(tools.calls))
	}
}

func TestRunnerFinishGuardRetriesEmptyResponse_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{Content: ""},
			{Content: "已完成：没有需要修改的内容。"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 2,
	}

	result, err := runner.Run(context.Background(), "实现一个空回复守卫", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want finish guard retry", len(client.requests))
	}
	second := client.requests[1].Messages
	last := second[len(second)-1]
	if !strings.Contains(last.Content, "FINISH GUARD") || !strings.Contains(last.Content, "没有返回任何文本") {
		t.Fatalf("finish guard hint = %q", last.Content)
	}
}

func TestFinishGuardDetectsTruncatedRaw_BitsUT(t *testing.T) {
	decision := evaluateFinishGuard("总结即可", &llm.Response{
		Content: "这是一段被截断的回答",
		Raw:     `{"choices":[{"finish_reason":"length"}]}`,
	})
	if decision.Allow || decision.Reason != "truncated_response" {
		t.Fatalf("decision = %#v, want truncated block", decision)
	}
}
