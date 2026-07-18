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
