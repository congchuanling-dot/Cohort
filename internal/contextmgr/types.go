package contextmgr

import "cohert/internal/llm"

const contextNotice = "[Cohert context notice] Earlier conversation messages were omitted from this request. Full history is preserved in history.jsonl."

// Config 控制本轮模型请求前的确定性上下文压缩。
type Config struct {
	MaxHistoryMessages     int
	KeepRecentToolResults  int
	MaxToolResultChars     int
	CompactedToolHeadChars int
	CompactedToolTailChars int
	MaxRequestChars        int
	EnableMicroCompact     bool
}

// DefaultConfig 返回第一版 Context Manager 的保守默认值。
func DefaultConfig() Config {
	return Config{
		MaxHistoryMessages:     40,
		KeepRecentToolResults:  2,
		MaxToolResultChars:     12000,
		CompactedToolHeadChars: 4000,
		CompactedToolTailChars: 4000,
		MaxRequestChars:        100000,
		EnableMicroCompact:     true,
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
	return c
}

// Manager 构造发给模型的可见上下文，不修改 Runner.history 或 history.jsonl。
type Manager struct {
	Config Config
}

// BuildInput 是一次请求前上下文构造所需的信息。
type BuildInput struct {
	Messages   []llm.Message
	SessionID  string
	SessionDir string
}

// BuildResult 是压缩后的请求消息和统计信息。
type BuildResult struct {
	Messages []llm.Message
	Stats    Stats
}

// Stats 记录本次请求前上下文压缩做了什么，便于测试和后续日志。
type Stats struct {
	OriginalMessages       int
	FinalMessages          int
	OriginalChars          int
	FinalChars             int
	TrimmedMessages        int
	CompactedToolResults   int
	OmittedToolResultChars int
	InsertedNotice         bool
	Warnings               []string
}
