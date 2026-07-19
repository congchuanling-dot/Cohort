package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/contextmgr"
	"cohert/internal/llm"
	"cohert/internal/session"
)

func TestRunnerCompactSessionMemoryWritesMemoryFile_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "# Session Memory\n\n## 用户目标\n\n- 继续实现上下文管理"}},
	}
	runner := &Runner{
		Client:       client,
		SessionStore: &store,
		sessionID:    sess.ID,
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "我要实现上下文管理"},
			{Role: llm.RoleAssistant, Content: "已完成第一层压缩"},
		},
	}

	result, err := runner.CompactSessionMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryFileName)
	if result.Path != wantPath {
		t.Fatalf("memory path = %q, want %q", result.Path, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "继续实现上下文管理") {
		t.Fatalf("memory file does not contain generated content:\n%s", string(data))
	}
	if len(client.requests) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if len(req.Tools) != 0 {
		t.Fatalf("compact request tools = %d, want 0", len(req.Tools))
	}
	if !strings.Contains(req.Messages[0].Content, "会话历史") {
		t.Fatalf("compact prompt missing history section:\n%s", req.Messages[0].Content)
	}
}

func TestRunnerCompactSessionMemoryRequiresActiveSession_BitsUT(t *testing.T) {
	runner := &Runner{
		Client: &contextRecordingClient{},
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		},
	}

	_, err := runner.CompactSessionMemory(context.Background())
	if err == nil {
		t.Fatal("expected compact to require active session")
	}
}
