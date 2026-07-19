package agent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"cohert/internal/contextmgr"
)

const contextStatsLogFileName = "context.log"

type contextStatsLogEntry struct {
	SessionID               string   `json:"session_id"`
	OriginalMessages        int      `json:"original_messages"`
	FinalMessages           int      `json:"final_messages"`
	OriginalChars           int      `json:"original_chars"`
	FinalChars              int      `json:"final_chars"`
	OriginalTokens          int      `json:"original_tokens"`
	FinalTokens             int      `json:"final_tokens"`
	UsableInputTokens       int      `json:"usable_input_tokens"`
	CompactTriggerTokens    int      `json:"compact_trigger_tokens"`
	SkippedCompact          bool     `json:"skipped_compact"`
	TriggerReason           string   `json:"trigger_reason"`
	TrimmedMessages         int      `json:"trimmed_messages"`
	CompactedToolResults    int      `json:"compacted_tool_results"`
	OmittedToolResultChars  int      `json:"omitted_tool_result_chars"`
	InsertedNotice          bool     `json:"inserted_notice"`
	InjectedSessionMemory   bool     `json:"injected_session_memory"`
	SessionMemoryChars      int      `json:"session_memory_chars"`
	SessionMemoryTruncated  bool     `json:"session_memory_truncated"`
	InjectedCompactSummary  bool     `json:"injected_compact_summary"`
	CompactSummaryChars     int      `json:"compact_summary_chars"`
	CompactSummaryTruncated bool     `json:"compact_summary_truncated"`
	Warnings                []string `json:"warnings,omitempty"`
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
