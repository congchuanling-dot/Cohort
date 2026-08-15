package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/contextmgr"
	"cohort/internal/llm"
	"cohort/internal/observability"
)

const observationLogFileName = "run.log.jsonl"

const langfusePreviewChars = 2000

func (r *Runner) observationBus() observability.Bus {
	if r.Observability != nil {
		return r.Observability
	}
	sinks := make([]observability.Sink, 0, 1+len(r.ObservationSinks))
	path := r.observationLogPath()
	if path != "" {
		sinks = append(sinks, observability.NewJSONLSink(path))
	}
	sinks = append(sinks, r.ObservationSinks...)
	if len(sinks) == 0 {
		return observability.NoopBus{}
	}
	return observability.NewBus(sinks...)
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
		"context_window_tokens":     stats.ContextWindowTokens,
		"context_window_source":     stats.ContextWindowSource,
		"capability_version":        stats.CapabilityVersion,
		"capability_confidence":     stats.CapabilityConfidence,
		"max_output_tokens":         stats.MaxOutputTokens,
		"safety_tokens":             stats.SafetyTokens,
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

func toolRouteDecisionData(decision ToolRouteDecision) map[string]any {
	return map[string]any{
		"mode":                  decision.Mode,
		"reason":                decision.Reason,
		"full_schema_count":     decision.FullSchemaCount,
		"selected_count":        decision.SelectedCount,
		"selected_groups":       decision.SelectedGroups,
		"selected_external":     decision.SelectedExternal,
		"full_schema_bytes":     decision.FullSchemaBytes,
		"selected_schema_bytes": decision.SelectedBytes,
		"saved_schema_bytes":    decision.SavedSchemaBytes,
		"escalated":             decision.Escalated,
		"failure_count":         decision.FailureCount,
	}
}

func llmResponseData(resp *llm.Response, duration time.Duration, messages []llm.Message, tools []llm.ToolSchema, system string) map[string]any {
	data := map[string]any{
		"duration_ms": duration.Milliseconds(),
		"langfuse_input": map[string]any{
			"system":   previewString(system, langfusePreviewChars),
			"messages": langfuseMessagePreviews(messages),
			"tools":    langfuseToolPreviews(tools),
		},
	}
	if resp == nil {
		data["status"] = ToolStatusError
		return data
	}
	data["status"] = ToolStatusSuccess
	data["content_chars"] = len([]rune(resp.Content))
	data["tool_call_count"] = len(resp.ToolCalls)
	data["raw_chars"] = len([]rune(resp.Raw))
	data["langfuse_output"] = map[string]any{
		"message":    previewString(resp.Content, langfusePreviewChars),
		"tool_calls": langfuseToolCallPreviews(resp.ToolCalls),
	}
	if !resp.Usage.IsZero() {
		data["usage"] = usageData(resp.Usage)
	}
	return data
}

func langfuseMessagePreviews(messages []llm.Message) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{
			"role":          message.Role,
			"message":       previewString(message.Content, langfusePreviewChars),
			"content_chars": len([]rune(message.Content)),
		}
		if message.Name != "" {
			item["name"] = message.Name
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			item["tool_calls"] = langfuseToolCallPreviews(message.ToolCalls)
		}
		result = append(result, item)
	}
	return result
}

func langfuseToolPreviews(tools []llm.ToolSchema) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"name":        tool.Function.Name,
			"description": previewString(tool.Function.Description, 240),
		})
	}
	return result
}

func langfuseToolCallPreviews(calls []llm.ToolCall) []map[string]any {
	result := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		result = append(result, map[string]any{
			"id":        call.ID,
			"name":      call.Function.Name,
			"arguments": previewJSON(call.Function.Arguments, langfusePreviewChars),
		})
	}
	return result
}

func previewJSON(value string, maxChars int) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return redactValue(decoded, "")
	}
	return previewString(value, maxChars)
}

func previewString(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len([]rune(value)) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "...[truncated]"
}

func usageData(usage llm.Usage) map[string]any {
	data := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  usage.NormalizedTotal(),
	}
	if usage.CacheCreationInputTokens > 0 {
		data["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		data["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
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
	resultChars, truncated, errorCode, errorMessage := outcomeAuditShape(outcome.Data)
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
	if errorMessage != "" {
		data["error_message"] = errorMessage
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
