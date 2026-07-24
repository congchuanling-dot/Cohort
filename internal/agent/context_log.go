package agent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"cohert/internal/contextmgr"
)

const contextStatsLogFileName = "context.log"

type contextStatsLogEntry struct {
	// SessionID 是本条日志对应的会话标识。
	SessionID string `json:"session_id"`
	// OriginalMessages 是压缩前的历史消息数量。
	OriginalMessages int `json:"original_messages"`
	// FinalMessages 是最终发给模型的消息数量。
	FinalMessages int `json:"final_messages"`
	// OriginalChars 是压缩前消息的估算字符数。
	OriginalChars int `json:"original_chars"`
	// FinalChars 是压缩后消息的估算字符数。
	FinalChars int `json:"final_chars"`
	// OriginalTokens 是压缩前消息的估算 token 数。
	OriginalTokens int `json:"original_tokens"`
	// FinalTokens 是压缩后消息的估算 token 数。
	FinalTokens int `json:"final_tokens"`
	// UsableInputTokens 是扣除输出预留和安全余量后的输入预算。
	UsableInputTokens int `json:"usable_input_tokens"`
	// CompactTriggerTokens 是本轮开始压缩的 token 阈值。
	CompactTriggerTokens int `json:"compact_trigger_tokens"`
	// SkippedCompact 表示本轮是否因低于阈值跳过压缩。
	SkippedCompact bool `json:"skipped_compact"`
	// TriggerReason 记录触发或跳过压缩的原因。
	TriggerReason string `json:"trigger_reason"`
	// TrimmedMessages 是被 group trim 或协议清理移除的消息数量。
	TrimmedMessages int `json:"trimmed_messages"`
	// CompactedToolResults 是被 Micro Compact 压缩过的工具结果数量。
	CompactedToolResults int `json:"compacted_tool_results"`
	// OmittedToolResultChars 是工具结果压缩省略的字符数。
	OmittedToolResultChars int `json:"omitted_tool_result_chars"`
	// InsertedNotice 表示是否插入上下文省略提示。
	InsertedNotice bool `json:"inserted_notice"`
	// InjectedSessionMemory 表示是否注入 session memory。
	InjectedSessionMemory bool `json:"injected_session_memory"`
	// SessionMemoryChars 是注入请求的 session memory 字符数。
	SessionMemoryChars int `json:"session_memory_chars"`
	// SessionMemoryTruncated 表示注入的 session memory 是否被截断。
	SessionMemoryTruncated bool `json:"session_memory_truncated"`
	// InjectedRelevantMemory 表示是否注入相关长期记忆。
	InjectedRelevantMemory bool `json:"injected_relevant_memory"`
	// RelevantMemoryChars 是注入请求的相关长期记忆字符数。
	RelevantMemoryChars int `json:"relevant_memory_chars"`
	// RelevantMemoryFiles 是注入请求的相关长期记忆文件数量。
	RelevantMemoryFiles int `json:"relevant_memory_files"`
	// RelevantMemoryTruncated 表示相关长期记忆是否被截断。
	RelevantMemoryTruncated bool `json:"relevant_memory_truncated"`
	// InjectedCompactSummary 表示是否注入 compact summary。
	InjectedCompactSummary bool `json:"injected_compact_summary"`
	// CompactSummaryChars 是注入请求的 compact summary 字符数。
	CompactSummaryChars int `json:"compact_summary_chars"`
	// CompactSummaryTruncated 表示注入的 compact summary 是否被截断。
	CompactSummaryTruncated bool `json:"compact_summary_truncated"`
	// Warnings 记录协议修复或异常历史的警告。
	Warnings []string `json:"warnings,omitempty"`
}

// logContextStats 把 Context Manager 的压缩决策写入 JSONL debug 日志。
//
// 这里故意不记录 message 内容，只记录数字、布尔值和触发原因：
//   - 避免把用户上下文复制进日志；
//   - 避免日志因为工具结果而膨胀；
//   - 方便用 rg/jq 排查“本轮到底有没有压缩、怎么压的”。
//
// 日志失败不影响主流程。上下文管理日志是观测能力，不应该让模型请求失败。
func (r *Runner) logContextStats(stats contextmgr.Stats) {
	if r.LogDir == "" {
		return
	}
	entry := contextStatsLogEntry{
		SessionID:               r.sessionID,
		OriginalMessages:        stats.OriginalMessages,
		FinalMessages:           stats.FinalMessages,
		OriginalChars:           stats.OriginalChars,
		FinalChars:              stats.FinalChars,
		OriginalTokens:          stats.OriginalTokens,
		FinalTokens:             stats.FinalTokens,
		UsableInputTokens:       stats.UsableInputTokens,
		CompactTriggerTokens:    stats.CompactTriggerTokens,
		SkippedCompact:          stats.SkippedCompact,
		TriggerReason:           stats.TriggerReason,
		TrimmedMessages:         stats.TrimmedMessages,
		CompactedToolResults:    stats.CompactedToolResults,
		OmittedToolResultChars:  stats.OmittedToolResultChars,
		InsertedNotice:          stats.InsertedNotice,
		InjectedSessionMemory:   stats.InjectedSessionMemory,
		SessionMemoryChars:      stats.SessionMemoryChars,
		SessionMemoryTruncated:  stats.SessionMemoryTruncated,
		InjectedRelevantMemory:  stats.InjectedRelevantMemory,
		RelevantMemoryChars:     stats.RelevantMemoryChars,
		RelevantMemoryFiles:     stats.RelevantMemoryFiles,
		RelevantMemoryTruncated: stats.RelevantMemoryTruncated,
		InjectedCompactSummary:  stats.InjectedCompactSummary,
		CompactSummaryChars:     stats.CompactSummaryChars,
		CompactSummaryTruncated: stats.CompactSummaryTruncated,
		Warnings:                append([]string(nil), stats.Warnings...),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(r.LogDir, 0755); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(r.LogDir, contextStatsLogFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return
	}
	_ = file.Close()
}
