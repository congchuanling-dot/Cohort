package contextmgr

import "cohort/internal/llm"

const (
	contextNotice                = "[Cohort context notice] Earlier conversation messages were omitted from this request. Full history is preserved in history.jsonl."
	longTermMemoryIndexNotice    = "[Cohort long-term memory index]"
	relevantLongTermMemoryNotice = "[Cohort relevant long-term memory]"
	sessionMemoryNotice          = "[Cohort session memory]"
	compactSummaryNotice         = "[Cohort compact summary]"
)

// Config 控制本轮模型请求前的确定性上下文压缩。
type Config struct {
	// MaxHistoryMessages 限制本轮请求最多携带多少条历史消息。
	// 超过后会按 message group 从旧到新裁剪，但不会修改 Runner.history 或 history.jsonl。
	MaxHistoryMessages int

	// KeepRecentToolResults 表示最近多少条 tool result 保持完整不压缩。
	// 最近工具结果通常最贴近当前任务，优先完整保留。
	KeepRecentToolResults int

	// MaxToolResultChars 是单条 tool result 允许进入请求的最大字符数。
	// 更旧且超过该阈值的 tool result 会被 Micro Compact 成头尾保留格式。
	MaxToolResultChars int

	// CompactedToolHeadChars 是工具结果压缩后保留的头部字符数。
	// 头部通常包含命令起始输出、文件开头或错误发生前的上下文。
	CompactedToolHeadChars int

	// CompactedToolTailChars 是工具结果压缩后保留的尾部字符数。
	// 尾部通常包含最终错误、测试总结、退出码附近日志等关键信息。
	CompactedToolTailChars int

	// MaxRequestChars 限制本轮 request messages 的估算字符总量。
	// Micro Compact 后如果仍超限，会继续按 group 裁剪旧历史。
	MaxRequestChars int

	// MaxSessionMemoryChars 限制 memory.md 注入请求前的最大字符数。
	// memory.md 是稳定事实，不应该无限增长；超过后会截断注入副本，不修改磁盘文件。
	MaxSessionMemoryChars int

	// MaxMemoryIndexChars 限制长期记忆索引注入请求前的最大字符数。
	// memory/index.md 只作为指针，不应该承载详细记忆。
	MaxMemoryIndexChars int

	// MaxRelevantMemoryChars 限制自动匹配到的长期记忆注入请求前的最大字符数。
	// 相关长期记忆来自 memory/index.md 指向的文件和默认 P0 memory 文件。
	MaxRelevantMemoryChars int

	// MaxRelevantMemoryEntries 限制单轮最多自动注入几个长期记忆条目。
	MaxRelevantMemoryEntries int

	// MaxCompactSummaryChars 限制 compact.md 注入请求前的最大字符数。
	// compact.md 承载长历史摘要，默认比 memory.md 更大；超过后只截断请求副本，不修改磁盘文件。
	MaxCompactSummaryChars int

	// ContextWindowTokens 是当前模型最大上下文窗口。
	// 它由 app 层根据当前模型名从内置表填充，不从用户配置读取。
	ContextWindowTokens int

	// MaxOutputTokens 是为模型回复预留的 token 数。
	// 输入上下文预算会从模型窗口里扣掉这部分，避免输入占满后导致输出失败。
	MaxOutputTokens int

	// SafetyTokens 是上下文估算安全余量。
	// 当前 token 估算较粗，保留安全余量可以降低请求超窗风险。
	SafetyTokens int

	// CompactTriggerRatio 是触发压缩的上下文占用比例。
	// 默认固定为 0.70，表示估算输入达到可用输入预算 70% 后才开始压缩。
	CompactTriggerRatio float64

	// EnableMicroCompact 控制是否启用旧工具结果压缩。
	// 关闭后仍会执行 group trim，但 tool result 内容不会被头尾压缩。
	EnableMicroCompact bool
}

// DefaultConfig 返回第一版 Context Manager 的保守默认值。
func DefaultConfig() Config {
	return Config{
		MaxHistoryMessages:       40,
		KeepRecentToolResults:    2,
		MaxToolResultChars:       12000,
		CompactedToolHeadChars:   4000,
		CompactedToolTailChars:   4000,
		MaxRequestChars:          100000,
		MaxSessionMemoryChars:    20000,
		MaxMemoryIndexChars:      12000,
		MaxRelevantMemoryChars:   16000,
		MaxRelevantMemoryEntries: 3,
		MaxCompactSummaryChars:   60000,
		MaxOutputTokens:          4096,
		SafetyTokens:             4000,
		CompactTriggerRatio:      0.70,
		EnableMicroCompact:       true,
	}
}

// Normalize 补齐非法或缺失配置，避免零值导致把上下文全部裁掉。
func (c Config) Normalize() Config {
	defaults := DefaultConfig()
	if c.MaxHistoryMessages <= 0 {
		c.MaxHistoryMessages = defaults.MaxHistoryMessages
	}
	if c.KeepRecentToolResults < 0 {
		c.KeepRecentToolResults = defaults.KeepRecentToolResults
	}
	if c.MaxToolResultChars <= 0 {
		c.MaxToolResultChars = defaults.MaxToolResultChars
	}
	if c.CompactedToolHeadChars < 0 {
		c.CompactedToolHeadChars = defaults.CompactedToolHeadChars
	}
	if c.CompactedToolTailChars < 0 {
		c.CompactedToolTailChars = defaults.CompactedToolTailChars
	}
	if c.MaxRequestChars <= 0 {
		c.MaxRequestChars = defaults.MaxRequestChars
	}
	if c.MaxSessionMemoryChars <= 0 {
		c.MaxSessionMemoryChars = defaults.MaxSessionMemoryChars
	}
	if c.MaxMemoryIndexChars <= 0 {
		c.MaxMemoryIndexChars = defaults.MaxMemoryIndexChars
	}
	if c.MaxRelevantMemoryChars <= 0 {
		c.MaxRelevantMemoryChars = defaults.MaxRelevantMemoryChars
	}
	if c.MaxRelevantMemoryEntries <= 0 {
		c.MaxRelevantMemoryEntries = defaults.MaxRelevantMemoryEntries
	}
	if c.MaxCompactSummaryChars <= 0 {
		c.MaxCompactSummaryChars = defaults.MaxCompactSummaryChars
	}
	if c.ContextWindowTokens <= 0 {
		c.ContextWindowTokens = defaultContextWindowTokens
	}
	if c.MaxOutputTokens < 0 {
		c.MaxOutputTokens = defaults.MaxOutputTokens
	}
	if c.SafetyTokens < 0 {
		c.SafetyTokens = defaults.SafetyTokens
	}
	if c.CompactTriggerRatio <= 0 || c.CompactTriggerRatio > 1 {
		c.CompactTriggerRatio = defaults.CompactTriggerRatio
	}
	return c
}

// Manager 构造发给模型的可见上下文，不修改 Runner.history 或 history.jsonl。
type Manager struct {
	// Config 是本 Manager 使用的上下文预算和压缩策略。
	Config Config
	// MemoryRoot 是长期记忆根目录，通常是 workspace/memory。
	// 为空时不注入长期记忆索引，避免测试或无 workspace 场景产生隐式文件依赖。
	MemoryRoot string
}

// BuildInput 是一次请求前上下文构造所需的信息。
type BuildInput struct {
	// Messages 是从 Runner.history 复制出来的完整消息列表。
	// Build 会再次复制后处理，调用方传入的切片不会被修改。
	Messages []llm.Message

	// SessionID 是当前 session 标识，预留给后续 memory.md、compact.md 和状态文件使用。
	SessionID string

	// SessionDir 是当前 session 在磁盘上的目录，预留给后续读取 session memory 或 compact summary。
	SessionDir string
}

// BuildResult 是压缩后的请求消息和统计信息。
type BuildResult struct {
	// Messages 是本轮真正发给 LLM Client 的消息。
	// 它可能包含 context notice、压缩后的 tool result，且可能少于原始历史。
	Messages []llm.Message

	// Stats 描述本次构造过程发生的压缩、裁剪和告警。
	Stats Stats
}

// RelevantMemoryHitLog 记录某条长期记忆为何被注入本轮请求，不包含记忆正文。
type RelevantMemoryHitLog struct {
	EntryID string   `json:"entry_id,omitempty"`
	Source  string   `json:"source"`
	Title   string   `json:"title,omitempty"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}

// Stats 记录本次请求前上下文压缩做了什么，便于测试和后续日志。
type Stats struct {
	// OriginalMessages 是输入 Build 前的原始消息数量。
	OriginalMessages int

	// FinalMessages 是 Build 后发给模型的消息数量。
	FinalMessages int

	// OriginalChars 是原始 messages 的估算字符数。
	OriginalChars int

	// FinalChars 是压缩和裁剪后的 messages 估算字符数。
	FinalChars int

	// OriginalTokens 是原始 messages 的估算 token 数。
	OriginalTokens int

	// FinalTokens 是 Build 后 request messages 的估算 token 数。
	FinalTokens int

	// UsableInputTokens 是扣除输出预留和安全余量后的可用输入 token 预算。
	UsableInputTokens int

	// CompactTriggerTokens 是触发压缩的 token 阈值。
	// 低于该值时不会执行 Micro Compact 或 Group Trim。
	CompactTriggerTokens int

	// SkippedCompact 表示本轮低于触发阈值，因此跳过压缩和裁剪。
	SkippedCompact bool

	// TriggerReason 记录本轮为什么跳过或触发压缩，便于后续日志和调试。
	TriggerReason string

	// TrimmedMessages 是本轮请求中被 group trim 或协议清理移除的消息数量。
	TrimmedMessages int

	// CompactedToolResults 是被 Micro Compact 压缩过内容的 tool result 数量。
	CompactedToolResults int

	// OmittedToolResultChars 是工具结果压缩后省略掉的字符数估算。
	OmittedToolResultChars int

	// InsertedNotice 表示是否插入了 context notice 提醒模型早期消息已省略。
	InsertedNotice bool

	// InjectedSessionMemory 表示本轮请求是否注入了 session memory。
	InjectedSessionMemory bool

	// SessionMemoryChars 是注入请求的 session memory 字符数，不包含消息角色等协议字段。
	SessionMemoryChars int

	// SessionMemoryTruncated 表示 memory.md 因超过 MaxSessionMemoryChars 而在请求副本中被截断。
	SessionMemoryTruncated bool

	// InjectedMemoryIndex 表示本轮请求是否注入了 memory/index.md。
	InjectedMemoryIndex bool

	// MemoryIndexChars 是注入请求的 memory/index.md 字符数。
	MemoryIndexChars int

	// MemoryIndexTruncated 表示 memory/index.md 因超过 MaxMemoryIndexChars 而在请求副本中被截断。
	MemoryIndexTruncated bool

	// InjectedRelevantMemory 表示本轮是否根据用户任务关键词自动注入相关长期记忆。
	InjectedRelevantMemory bool

	// RelevantMemoryChars 是注入请求的相关长期记忆字符数。
	RelevantMemoryChars int

	// RelevantMemoryEntries 是本轮自动注入的长期记忆条目数量。
	RelevantMemoryEntries int

	// RelevantMemoryTruncated 表示相关长期记忆因超过 MaxRelevantMemoryChars 而在请求副本中被截断。
	RelevantMemoryTruncated bool

	// RelevantMemoryHitLogs 记录本轮注入长期记忆的命中原因，不包含记忆正文。
	RelevantMemoryHitLogs []RelevantMemoryHitLog

	// InjectedCompactSummary 表示本轮请求是否注入了 compact.md。
	InjectedCompactSummary bool

	// CompactSummaryChars 是注入请求的 compact.md 摘要字符数，不包含消息角色等协议字段。
	CompactSummaryChars int

	// CompactSummaryTruncated 表示 compact.md 因超过 MaxCompactSummaryChars 而在请求副本中被截断。
	CompactSummaryTruncated bool

	// Warnings 记录协议修复或异常历史，例如孤立 tool result 被丢弃。
	Warnings []string
}
