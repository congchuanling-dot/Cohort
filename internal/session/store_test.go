package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/llm"
)

func TestStoreCreateWritesMeta(t *testing.T) {
	// 用临时目录做 session root，避免测试污染真实 temp/sessions。
	// t.TempDir() 会在测试结束后自动清理，适合验证文件落盘逻辑。
	root := t.TempDir()
	store := NewStore(root)

	// Create 当前只负责创建会话目录和 meta.json，不写 history.jsonl。
	// 这里传固定 title/cwd/model，是为了后面能明确断言 meta.json 内容没有写错字段。
	sess, err := store.Create("first task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Fatal("session id is empty")
	}
	// NewID 的前缀应该来自 CreatedAt。
	// 这个断言保护“ID 可读、按时间排序友好”的约定，避免以后误改成完全随机字符串。
	if !strings.HasPrefix(sess.ID, sess.CreatedAt.Format("20060102-150405")) {
		t.Fatalf("session id %q does not start with timestamp", sess.ID)
	}

	// 读取 meta.json，确认会话元信息完整落盘。
	// 这一步不是只检查文件存在，而是反序列化后逐字段检查，
	// 因为后续 session list/resume 都会依赖这些字段。
	metaPath := store.MetaPath(sess.ID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}

	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != sess.ID {
		t.Fatalf("meta id = %q, want %q", got.ID, sess.ID)
	}
	if got.Title != "first task" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.CWD != "/tmp/project" {
		t.Fatalf("cwd = %q", got.CWD)
	}
	if got.Model != "deepseek-v4-pro" {
		t.Fatalf("model = %q", got.Model)
	}

	// HistoryPath 先保证路径约定稳定；具体追加写入由 P0-012 的测试覆盖。
	// 当前 Create 不创建 history.jsonl，但 Runner 后续会通过同一个路径函数追加消息。
	// 所以这里先把路径规则锁住，避免后续实现 Runner 时写到另一个位置。
	wantHistoryPath := filepath.Join(root, sess.ID, HistoryFileName)
	if store.HistoryPath(sess.ID) != wantHistoryPath {
		t.Fatalf("history path = %q, want %q", store.HistoryPath(sess.ID), wantHistoryPath)
	}
}

func TestStoreAppendHistoryWritesJSONL(t *testing.T) {
	// 先创建 session，让 AppendHistory 有合法的 meta.json 可以刷新 UpdatedAt。
	root := t.TempDir()
	store := NewStore(root)
	sess, err := store.Create("first task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}

	// 连续追加两条消息，模拟 Runner 写入 user -> assistant 的最小链路。
	// 这里直接使用 llm.Message，是为了保证落盘格式和模型上下文类型保持一致。
	writeErr := store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleUser, Content: "你好"})
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	writeErr = store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "你好，我是 Cohort"})
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	// history.jsonl 应该是一行一个 HistoryEntry。
	// 用 Scanner 按行读，可以验证它不是一个大 JSON 数组。
	file, err := os.Open(store.HistoryPath(sess.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var entries []HistoryEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry HistoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("history entries = %d, want 2", len(entries))
	}
	if entries[0].SessionID != sess.ID || entries[1].SessionID != sess.ID {
		t.Fatalf("session ids = %q/%q, want %q", entries[0].SessionID, entries[1].SessionID, sess.ID)
	}
	if entries[0].Role != llm.RoleUser || entries[0].Message.Content != "你好" {
		t.Fatalf("first entry = %#v", entries[0])
	}
	if entries[1].Role != llm.RoleAssistant || entries[1].Message.Content != "你好，我是 Cohort" {
		t.Fatalf("second entry = %#v", entries[1])
	}
}

func TestStoreListAndLoadHistory(t *testing.T) {
	// 这个测试覆盖 P0-013/P0-014 的核心读路径：
	// session list 需要读取 meta.json 并统计消息数；
	// session resume 需要读取 history.jsonl 并还原成 []llm.Message。
	root := t.TempDir()
	store := NewStore(root)

	first, err := store.Create("first task", "/tmp/project-a", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create("second task", "/tmp/project-b", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}

	appendErr := store.AppendHistory(first.ID, llm.Message{Role: llm.RoleUser, Content: "第一条"})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	appendErr = store.AppendHistory(second.ID, llm.Message{Role: llm.RoleUser, Content: "第二条用户"})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	appendErr = store.AppendHistory(second.ID, llm.Message{Role: llm.RoleAssistant, Content: "第二条回复"})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	largeContent := strings.Repeat("x", 128*1024)
	appendErr = store.AppendHistory(second.ID, llm.Message{Role: llm.RoleAssistant, Content: largeContent})
	if appendErr != nil {
		t.Fatal(appendErr)
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("session summaries = %d, want 2", len(summaries))
	}
	// second 后追加历史，UpdatedAt 应该更新得更晚，所以列表里排在前面。
	if summaries[0].Session.ID != second.ID {
		t.Fatalf("first listed session = %q, want %q", summaries[0].Session.ID, second.ID)
	}
	if summaries[0].MessageCount != 3 {
		t.Fatalf("second message count = %d, want 3", summaries[0].MessageCount)
	}
	if summaries[1].Session.ID != first.ID || summaries[1].MessageCount != 1 {
		t.Fatalf("second listed summary = %#v, want first session with 1 message", summaries[1])
	}

	history, err := store.LoadHistory(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history messages = %d, want 3", len(history))
	}
	if history[0].Role != llm.RoleUser || history[0].Content != "第二条用户" {
		t.Fatalf("first restored message = %#v", history[0])
	}
	if history[1].Role != llm.RoleAssistant || history[1].Content != "第二条回复" {
		t.Fatalf("second restored message = %#v", history[1])
	}
	if history[2].Content != largeContent {
		t.Fatalf("large history message length = %d, want %d", len(history[2].Content), len(largeContent))
	}
}
