package agent

import (
	"context"
	"path/filepath"
	"time"

	"cohort/internal/contextmgr"
	"cohort/internal/llm"
	"cohort/internal/observability"
)

const observationLogFileName = "run.log.jsonl"

func (r *Runner) observationBus() observability.Bus {
	if r.Observability != nil {
		return r.Observability
	}
	path := r.observationLogPath()
	if path == "" {
		return observability.NoopBus{}
	}
	return observability.NewBus(observability.NewJSONLSink(path))
}

func (r *Runner) observationLogPath() string {
	if sessionDir := r.sessionDir(); sessionDir != "" {
		return filepath.Join(sessionDir, observationLogFileName)
	}
	if r.LogDir != "" {
		return filepath.Join(r.LogDir, observationLogFileName)
	}
	return ""
}

func (r *Runner) emitObservation(ctx context.Context, bus observability.Bus, runID string, eventType observability.EventType, turn int, severity observability.Severity, data map[string]any) {
	if bus == nil {
		return
	}
	bus.Emit(ctx, observability.NewEvent(
		eventType,
		runID,
		r.sessionID,
		turn,
		r.SessionCWD,
		"runner",
		severity,
		data,
	))
}

func promptSummary(input string) map[string]any {
	summary := observability.SummarizeText(input)
	return map[string]any{
		"chars": summary.Chars,
		"lines": summary.Lines,
		"hash":  summary.Hash,
	}
}

func contextStatsData(stats contextmgr.Stats) map[string]any {
	return map[string]any{
		"original_messages":         stats.OriginalMessages,
		"final_messages":            stats.FinalMessages,
		"original_chars":            stats.OriginalChars,
		"final_chars":               stats.FinalChars,
		"original_tokens":           stats.OriginalTokens,
		"final_tokens":              stats.FinalTokens,
		"usable_input_tokens":       stats.UsableInputTokens,
		"compact_trigger_tokens":    stats.CompactTriggerTokens,
		"skipped_compact":           stats.SkippedCompact,
		"trigger_reason":            stats.TriggerReason,
		"trimmed_messages":          stats.TrimmedMessages,
		"compacted_tool_results":    stats.CompactedToolResults,
		"omitted_tool_result_chars": stats.OmittedToolResultChars,
		"inserted_notice":           stats.InsertedNotice,
		"injected_session_memory":   stats.InjectedSessionMemory,
		"session_memory_chars":      stats.SessionMemoryChars,
		"session_memory_truncated":  stats.SessionMemoryTruncated,
		"injected_relevant_memory":  stats.InjectedRelevantMemory,
		"relevant_memory_chars":     stats.RelevantMemoryChars,
		"relevant_memory_entries":   stats.RelevantMemoryEntries,
		"relevant_memory_truncated": stats.RelevantMemoryTruncated,
		"injected_compact_summary":  stats.InjectedCompactSummary,
		"compact_summary_chars":     stats.CompactSummaryChars,
		"compact_summary_truncated": stats.CompactSummaryTruncated,
		"warning_count":             len(stats.Warnings),
		"relevant_memory_hit_count": len(stats.RelevantMemoryHitLogs),
	}
}

func llmRequestData(messages []llm.Message, tools []llm.ToolSchema, system string) map[string]any {
	return map[string]any{
		"message_count":     len(messages),
		"tool_schema_count": len(tools),
		"system_chars":      len([]rune(system)),
		"request_chars":     messagesChars(messages),
	}
}

func llmResponseData(resp *llm.Response, duration time.Duration) map[string]any {
	data := map[string]any{
		"duration_ms": duration.Milliseconds(),
	}
	if resp == nil {
		data["status"] = ToolStatusError
		return data
	}
	data["status"] = ToolStatusSuccess
	data["content_chars"] = len([]rune(resp.Content))
	data["tool_call_count"] = len(resp.ToolCalls)
	data["raw_chars"] = len([]rune(resp.Raw))
	return data
}

func toolStartedData(call llm.ToolCall, turn, index int, args map[string]any, parseErr error) map[string]any {
	data := map[string]any{
		"tool":         call.Function.Name,
		"tool_call_id": call.ID,
		"turn":         turn,
		"index":        index,
	}
	if parseErr != nil {
		data["args_parse_error"] = parseErr.Error()
		data["arguments_summary"] = promptSummary(call.Function.Arguments)
		return data
	}
	data["args_hash"] = stableArgsHash(args)
	data["args_summary"] = redactArgsSummary(args)
	return data
}

func toolFinishedData(call llm.ToolCall, outcome Outcome, duration time.Duration) map[string]any {
	resultChars, truncated, errorCode := outcomeAuditShape(outcome.Data)
	data := map[string]any{
		"tool":         call.Function.Name,
		"tool_call_id": call.ID,
		"status":       outcomeStatus(outcome),
		"duration_ms":  duration.Milliseconds(),
		"result_chars": resultChars,
		"truncated":    truncated,
	}
	if errorCode != "" {
		data["error_code"] = errorCode
	}
	if outcome.ShouldExit {
		data["should_exit"] = true
	}
	applyObservationAudit(data, outcome.Audit)
	return data
}

func applyObservationAudit(data map[string]any, audit map[string]any) {
	if len(audit) == 0 {
		return
	}
	for _, key := range []string{"external", "server", "mcp_tool", "risk", "permission_decision"} {
		if value, ok := audit[key]; ok {
			data[key] = value
		}
	}
}

func messagesChars(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += len([]rune(message.Role))
		total += len([]rune(message.Content))
		total += len([]rune(message.ToolCallID))
		total += len([]rune(message.Name))
		for _, call := range message.ToolCalls {
			total += len([]rune(call.ID))
			total += len([]rune(call.Type))
			total += len([]rune(call.Function.Name))
			total += len([]rune(call.Function.Arguments))
		}
	}
	return total
}
