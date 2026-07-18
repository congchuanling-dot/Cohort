package session

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"cohert/internal/llm"
)

const (
	// DefaultRootDir 是本地会话默认保存目录。
	// 后续每一次对话都会在这个目录下创建一个独立子目录。
	//
	// 这里先放在 temp/ 下，是因为当前 Cohert 还处在本地 MVP 阶段：
	// 1. 不依赖数据库，直接用文件就能观察和调试。
	// 2. 不默认写入用户 HOME，避免早期开发时产生难清理的全局数据。
	// 3. 后续如果要做全局安装，可以再把这个默认值迁到 ~/.cohert/sessions。
	DefaultRootDir = "temp/sessions"

	// MetaFileName 保存会话元信息。
	// 这个文件只放标题、工作目录、模型名、创建时间等轻量信息，方便快速列会话。
	//
	// 注意：meta.json 不保存完整上下文。它的定位类似“索引卡片”，用于快速展示：
	// 这个 session 是什么时候创建的、在哪个目录创建的、用的哪个模型。
	MetaFileName = "meta.json"
	// HistoryFileName 保存会话消息流水，一行一个 HistoryEntry。
	// 使用 jsonl 是为了追加写入简单，Agent 每产生一条消息就可以落一行。
	//
	// history.jsonl 才是真正恢复上下文需要读取的文件。
	// 选择 jsonl 而不是一个大 JSON 数组，是为了避免每次追加消息都读写整个文件。
	HistoryFileName = "history.jsonl"
)

// Session 描述一次连续对话的元信息。
// 真正的消息历史不放在这里，而是追加写入同目录下的 history.jsonl。
//
// 可以把 Session 理解成“会话目录的说明书”：
// 它告诉 Cohert 这个目录属于哪次会话、会话发生在哪个工作目录、使用哪个模型。
// 但它不保存 user/assistant/tool 的每轮消息，这样会话列表读取会很轻。
type Session struct {
	// ID 是会话唯一标识，也会作为 temp/sessions/<id> 的目录名。
	// 用 ID 做目录名可以避免标题重名，也方便后续通过 `cohert resume <id>` 精确恢复。
	ID string `json:"id"`
	// Title 是会话标题，第一版可以用用户第一条输入截断生成。
	// 它只服务展示，不参与模型上下文，所以标题生成得不好也不会影响 Agent 行为。
	Title string `json:"title"`
	// CWD 记录用户启动 Cohert 时所在的工作目录，恢复会话时要回到这个上下文。
	// 这对文件工具很重要：同一句 “读取 README” 在不同目录下会指向不同文件。
	CWD string `json:"cwd"`
	// Model 记录本会话使用的模型，后续做 resume 或排查问题时能知道请求过哪个模型。
	// 后面如果支持同一个项目切换模型，这个字段能帮助解释输出差异。
	Model string `json:"model"`
	// CreatedAt 是会话首次创建时间。
	// 它不会随着后续对话改变，适合用来追溯 session 的起点。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是会话最后更新时间，后续 session list 会按这个字段排序。
	// 每次追加历史或保存元信息时都应该刷新它，让最近使用的会话排在前面。
	UpdatedAt time.Time `json:"updated_at"`
}

// Summary 是 session list 使用的轻量展示结构。
// 它把 meta.json 里的 Session 元信息和 history.jsonl 的消息行数放在一起，
// 这样 CLI 列表不需要加载完整 Message 内容，也能告诉用户这个会话大概有多少上下文。
type Summary struct {
	// Session 是 meta.json 中的会话元信息。
	Session Session
	// MessageCount 是 history.jsonl 的有效行数。
	// 一行对应一条 HistoryEntry，包含 user/assistant/tool 三类消息。
	MessageCount int
}

// HistoryEntry 是 history.jsonl 中的一行记录。
// Message 保存真正发给模型或从工具返回的消息；外层字段用于恢复、排序和后续做对话树。
//
// 为什么不直接只存 llm.Message：
// 1. llm.Message 只关心模型协议，不应该背负本地存储字段。
// 2. SessionID、Time、ParentID 属于 Cohert 本地运行时信息，放外层更清晰。
// 3. 后续如果做分支、回退、审计，可以只扩展 HistoryEntry，不破坏 LLM 类型。
type HistoryEntry struct {
	// ID 是单条历史记录的唯一标识。
	// 它用于定位某一条消息，后续做“从某条消息继续”或“删除某条之后的历史”会用到。
	ID string `json:"id"`
	// ParentID 预留给后续分支/回退功能使用；当前线性会话可以先为空。
	// 例如用户从旧回答重新提问时，新消息可以指向原来的父节点，形成对话树。
	ParentID string `json:"parent_id,omitempty"`
	// SessionID 表示这条消息属于哪个会话。
	// 虽然文件路径里已经有 sessionID，行内再存一份可以让单条记录脱离路径后仍可识别来源。
	SessionID string `json:"session_id"`
	// Time 是这条消息写入 history.jsonl 的时间。
	// 它不是模型返回时间，而是 Cohert 本地落盘时间，用于排查执行顺序。
	Time time.Time `json:"time"`
	// Role 冗余保存 message.role，方便不展开 Message 时快速筛选 user/assistant/tool。
	// 这个字段是有意冗余：列表、统计、调试时不需要解析完整 Message。
	Role string `json:"role"`
	// Message 是真正的模型消息，包含 role、content、tool_calls、tool_call_id 等字段。
	// 恢复上下文时，Runner 会按 history.jsonl 顺序把这些 Message 重新装回 r.history。
	Message llm.Message `json:"message"`
}

// New 创建一个新的 Session 元信息。
// 它只构造内存对象，不创建目录、不写文件；落盘由 Store.Create/SaveMeta 负责。
//
// 这里故意不接触文件系统，是为了让“构造数据”和“写入磁盘”分开：
// New 只负责填字段，Store 才负责路径、目录权限和 JSON 序列化。
func New(title string, cwd string, model string) Session {
	now := time.Now()
	return Session{
		ID:        NewID(now),
		Title:     title,
		CWD:       cwd,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewID 生成本地可读的会话 ID，格式类似 20260718-153012-a1b2c3d4。
// 前半段方便人按时间识别，后半段随机值避免同一秒创建多个会话时冲突。
//
// 这里不用纯 UUID，是为了人肉查看 temp/sessions 目录时能立刻看出大概创建时间。
// 这里也不用纯时间戳，是为了并发或快速连续创建 session 时不会撞目录。
func NewID(now time.Time) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return now.Format("20060102-150405")
	}
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}
