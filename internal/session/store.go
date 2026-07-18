package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Store 管理本地 session 目录。
// 它只负责文件路径和落盘，不负责 Agent 运行逻辑。
//
// 当前阶段只实现创建会话和保存 meta.json；
// history.jsonl 的追加写入会在 P0-012 接入 Runner 时补上。
//
// 这层存在的目的，是把“文件怎么组织”从 Runner 里拿出来：
// Runner 只关心什么时候产生了 user/assistant/tool 消息，
// Store 负责这些消息最终应该写到哪个目录、哪个文件。
type Store struct {
	// RootDir 是所有会话目录的根路径，例如 temp/sessions。
	// 每个会话都会在 RootDir 下创建一个以 Session.ID 命名的子目录。
	RootDir string
}

// NewStore 创建 session 存储器。
// rootDir 为空时使用 DefaultRootDir，调用方可以在测试里传 t.TempDir() 隔离文件。
//
// filepath.Clean 会规整路径里的多余分隔符和 "."，让后续路径拼接结果更稳定。
// 例如 "temp//sessions/" 会被规整为 "temp/sessions"。
func NewStore(rootDir string) Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	return Store{RootDir: filepath.Clean(rootDir)}
}

// Create 创建会话目录并写入 meta.json。
//
// 这个方法会完成三件事：
// 1. cwd 为空时读取当前进程工作目录。
// 2. 生成 Session ID 和元信息。
// 3. 创建 temp/sessions/<session_id>/ 并写入 meta.json。
//
// 注意：它不会创建 history.jsonl。历史文件应该由 Runner 在追加第一条消息时自然创建。
// 这样空会话目录里只有 meta.json，不会出现一个没有任何消息的空 history 文件。
func (s Store) Create(title string, cwd string, model string) (Session, error) {
	if cwd == "" {
		var err error
		// 调用方没有显式传 cwd 时，使用当前进程目录作为会话工作目录。
		// 后续恢复 session 时，这个目录会成为文件工具解析相对路径的依据。
		cwd, err = os.Getwd()
		if err != nil {
			return Session{}, err
		}
	}
	sess := New(title, cwd, model)
	// 先创建目录，再写 meta.json。MkdirAll 可以重复调用，
	// 即使父目录 temp/sessions 不存在，也会一并创建出来。
	if err := os.MkdirAll(s.SessionDir(sess.ID), 0755); err != nil {
		return Session{}, err
	}
	if err := s.SaveMeta(sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// SessionDir 返回某个 session 的目录。
// 例如 RootDir=temp/sessions、sessionID=abc 时返回 temp/sessions/abc。
//
// 所有路径函数都集中放在 Store 上，是为了避免其他包手写路径字符串。
// 这样后续目录结构变更时，只需要改 Store，不需要全项目搜索替换。
func (s Store) SessionDir(sessionID string) string {
	return filepath.Join(s.RootDir, sessionID)
}

// MetaPath 返回 meta.json 路径。
// 这个文件用于快速读取会话列表，不需要扫描完整 history.jsonl。
//
// 后续 `cohert session list` 只需要读取每个 session 目录下的 meta.json，
// 不需要把所有历史消息都加载进内存。
func (s Store) MetaPath(sessionID string) string {
	return filepath.Join(s.SessionDir(sessionID), MetaFileName)
}

// HistoryPath 返回 history.jsonl 路径。
// 后续 Runner 会往这个文件追加 HistoryEntry，一行一条消息。
//
// 这里暂时只提供路径，不提供 AppendHistory 方法，是因为当前任务先定义数据结构；
// 真正追加写入需要和 Runner 的消息产生时机一起设计，放到 P0-012 更合适。
func (s Store) HistoryPath(sessionID string) string {
	return filepath.Join(s.SessionDir(sessionID), HistoryFileName)
}

// SaveMeta 保存会话元信息。
// 每次保存都会刷新 UpdatedAt，保证后续会话列表能按最近使用时间排序。
//
// 这里使用 MarshalIndent 而不是紧凑 JSON，是因为 meta.json 是人会打开看的文件；
// history.jsonl 后续会更偏机器追加读取，可以保持一行一条。
func (s Store) SaveMeta(sess Session) error {
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.SessionDir(sess.ID), 0755); err != nil {
		return err
	}
	// 0644 表示当前用户可读写，其他用户只读。
	// 这里不保存密钥，只保存 session 元信息，所以不需要更严格的 0600。
	return os.WriteFile(s.MetaPath(sess.ID), data, 0644)
}
