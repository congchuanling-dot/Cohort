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
	if result.BackedUp {
		t.Fatal("did not expect backup on first compact")
	}
}

func TestRunnerCompactSessionMemoryBacksUpExistingMemory_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	memoryPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryFileName)
	if err := os.WriteFile(memoryPath, []byte("old memory\n"), 0644); err != nil {
		t.Fatal(err)
	}
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "new memory"}},
	}
	runner := &Runner{
		Client:       client,
		SessionStore: &store,
		sessionID:    sess.ID,
		history:      []llm.Message{{Role: llm.RoleUser, Content: "继续"}},
	}

	result, err := runner.CompactSessionMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.BackedUp {
		t.Fatal("expected existing memory to be backed up")
	}
	wantBackupPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryBackupFileName)
	if result.BackupPath != wantBackupPath {
		t.Fatalf("backup path = %q, want %q", result.BackupPath, wantBackupPath)
	}
	backup, err := os.ReadFile(wantBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "old memory\n" {
		t.Fatalf("backup content = %q, want old memory", string(backup))
	}
	current, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "new memory\n" {
		t.Fatalf("current memory = %q, want new memory", string(current))
	}
}

func TestRunnerFullCompactSessionWritesCompactSummary_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("full compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "<analysis>drop me</analysis>\n<summary>\n1. Primary Request and Intent:\n\n- continue long task\n</summary>"}},
	}
	runner := &Runner{
		Client:       client,
		SessionStore: &store,
		sessionID:    sess.ID,
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "我要做 full compact"},
			{Role: llm.RoleAssistant, Content: "已确认手动触发"},
		},
	}

	result, err := runner.FullCompactSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.CompactSummaryFileName)
	if result.Path != wantPath {
		t.Fatalf("compact path = %q, want %q", result.Path, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "analysis") {
		t.Fatalf("compact summary should not contain analysis:\n%s", content)
	}
	if !strings.Contains(content, "continue long task") {
		t.Fatalf("compact summary missing generated content:\n%s", content)
	}
	if len(client.requests) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if len(req.Tools) != 0 {
		t.Fatalf("full compact request tools = %d, want 0", len(req.Tools))
	}
	if !strings.Contains(req.Messages[0].Content, "full compact") {
		t.Fatalf("full compact prompt missing compact instruction:\n%s", req.Messages[0].Content)
	}
	if result.BackedUp {
		t.Fatal("did not expect backup on first full compact")
	}
}

func TestRunnerFullCompactSessionBacksUpExistingCompactSummary_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("full compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	compactPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.CompactSummaryFileName)
	if err := os.WriteFile(compactPath, []byte("old compact\n"), 0644); err != nil {
		t.Fatal(err)
	}
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "<summary>new compact</summary>"}},
	}
	runner := &Runner{
		Client:       client,
		SessionStore: &store,
		sessionID:    sess.ID,
		history:      []llm.Message{{Role: llm.RoleUser, Content: "继续"}},
	}

	result, err := runner.FullCompactSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.BackedUp {
		t.Fatal("expected existing compact summary to be backed up")
	}
	wantBackupPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.CompactSummaryBackupFileName)
	if result.BackupPath != wantBackupPath {
		t.Fatalf("backup path = %q, want %q", result.BackupPath, wantBackupPath)
	}
	backup, err := os.ReadFile(wantBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "old compact\n" {
		t.Fatalf("backup content = %q, want old compact", string(backup))
	}
	current, err := os.ReadFile(compactPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "new compact\n" {
		t.Fatalf("current compact = %q, want new compact", string(current))
	}
}

func TestRunnerLoadSessionMemory_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("memory task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	memoryPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryFileName)
	if err := os.WriteFile(memoryPath, []byte("session facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		SessionStore: &store,
		sessionID:    sess.ID,
	}

	snapshot, err := runner.LoadSessionMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Exists {
		t.Fatal("expected session memory to exist")
	}
	if snapshot.Path != memoryPath {
		t.Fatalf("path = %q, want %q", snapshot.Path, memoryPath)
	}
	if snapshot.Content != "session facts" {
		t.Fatalf("content = %q, want session facts", snapshot.Content)
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
