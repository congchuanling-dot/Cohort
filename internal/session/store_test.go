package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
