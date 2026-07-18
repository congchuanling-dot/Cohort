package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"cohert/internal/llm"
	"cohert/internal/session"
)

type fakeClient struct {
	response llm.Response
}

func (c fakeClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	out := make(chan llm.Event, 2)
	out <- llm.Event{Type: llm.EventText, Text: c.response.Content}
	out <- llm.Event{Type: llm.EventDone, Response: &c.response}
	close(out)
	return out, nil
}

type fakeToolRunner struct{}

func (fakeToolRunner) Schemas() []llm.ToolSchema {
	return nil
}

func (fakeToolRunner) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	return Outcome{}, nil
}

type recordingClient struct {
	response llm.Response
	requests []llm.ChatRequest
}

func (c *recordingClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.requests = append(c.requests, req)
	out := make(chan llm.Event, 2)
	out <- llm.Event{Type: llm.EventText, Text: c.response.Content}
	out <- llm.Event{Type: llm.EventDone, Response: &c.response}
	close(out)
	return out, nil
}

func TestRunnerWritesHistoryJSONL(t *testing.T) {
	// 这个测试验证 P0-012 的核心行为：
	// Runner 不只维护内存 history，也会把 user/assistant 消息追加到 history.jsonl。
	store := session.NewStore(t.TempDir())
	runner := &Runner{
		Client:       fakeClient{response: llm.Response{Content: "收到"}},
		Tools:        fakeToolRunner{},
		MaxTurns:     1,
		SessionStore: &store,
		SessionCWD:   "/tmp/project",
		SessionModel: "deepseek-v4-pro",
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "你好", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if runner.sessionID == "" {
		t.Fatal("runner session id is empty")
	}

	// Runner 第一次收到用户输入时会创建 meta.json。
	// 标题来自第一条用户输入，后续 session list 会展示这个标题。
	meta, err := store.LoadMeta(runner.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "你好" {
		t.Fatalf("session title = %q, want %q", meta.Title, "你好")
	}
	if meta.CWD != "/tmp/project" {
		t.Fatalf("session cwd = %q, want %q", meta.CWD, "/tmp/project")
	}
	if meta.Model != "deepseek-v4-pro" {
		t.Fatalf("session model = %q, want %q", meta.Model, "deepseek-v4-pro")
	}

	// history.jsonl 至少要包含两行：用户输入和模型最终回复。
	entries := readHistoryEntries(t, store.HistoryPath(runner.sessionID))
	if len(entries) != 2 {
		t.Fatalf("history entries = %d, want 2", len(entries))
	}
	if entries[0].Role != llm.RoleUser || entries[0].Message.Content != "你好" {
		t.Fatalf("first entry = %#v", entries[0])
	}
	if entries[1].Role != llm.RoleAssistant || entries[1].Message.Content != "收到" {
		t.Fatalf("second entry = %#v", entries[1])
	}
}

func TestRunnerResumeSessionContinuesExistingHistory(t *testing.T) {
	// 这个测试验证 P0-014/P0-015 的关键约定：
	// resume 后 Runner 会把旧 history 发给模型，并继续追加到同一个 history.jsonl。
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("old task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	appendErr := store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleUser, Content: "旧问题"})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	appendErr = store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "旧回答"})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	history, err := store.LoadHistory(sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	client := &recordingClient{response: llm.Response{Content: "新回答"}}
	runner := &Runner{
		Client:       client,
		Tools:        fakeToolRunner{},
		MaxTurns:     1,
		SessionStore: &store,
		SessionCWD:   "/tmp/project",
		SessionModel: "deepseek-v4-pro",
	}
	runner.ResumeSession(sess.ID, history)

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "新问题", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if runner.sessionID != sess.ID {
		t.Fatalf("runner session id = %q, want %q", runner.sessionID, sess.ID)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	if len(client.requests[0].Messages) != 3 {
		t.Fatalf("request messages = %d, want old 2 + new user", len(client.requests[0].Messages))
	}
	if client.requests[0].Messages[0].Content != "旧问题" ||
		client.requests[0].Messages[1].Content != "旧回答" ||
		client.requests[0].Messages[2].Content != "新问题" {
		t.Fatalf("request messages = %#v", client.requests[0].Messages)
	}

	entries := readHistoryEntries(t, store.HistoryPath(sess.ID))
	if len(entries) != 4 {
		t.Fatalf("history entries = %d, want 4", len(entries))
	}
	if entries[2].Role != llm.RoleUser || entries[2].Message.Content != "新问题" {
		t.Fatalf("third entry = %#v", entries[2])
	}
	if entries[3].Role != llm.RoleAssistant || entries[3].Message.Content != "新回答" {
		t.Fatalf("fourth entry = %#v", entries[3])
	}
}

func readHistoryEntries(t *testing.T, path string) []session.HistoryEntry {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var entries []session.HistoryEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry session.HistoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}
