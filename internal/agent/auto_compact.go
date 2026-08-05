package agent

import (
	"context"
	"strings"
	"time"

	"cohort/internal/contextmgr"
	"cohort/internal/llm"
	"cohort/internal/observability"
)

func (r *Runner) buildRequestMessagesMaybeAutoCompact(ctx context.Context, obs observability.Bus, runID string, turn int) ([]llm.Message, contextmgr.Stats) {
	messages, stats := r.buildRequestMessagesWithStats()
	if !r.shouldAutoCompact(stats) {
		return messages, stats
	}
	sessionDir := strings.TrimSpace(r.sessionDir())
	if sessionDir == "" {
		return messages, stats
	}
	state, err := contextmgr.LoadContextState(sessionDir)
	if err != nil {
		r.emitObservation(ctx, obs, runID, observability.EventCompactFinished, turn, observability.SeverityWarn, map[string]any{
			"kind":  "auto_full_compact",
			"error": err.Error(),
		})
		return messages, stats
	}
	limit := r.ContextManager.Config.Normalize().AutoCompactFailureLimit
	if state.AutoCompactDisabled || state.AutoCompactConsecutiveFails >= limit {
		state.AutoCompactDisabled = true
		_ = contextmgr.SaveContextState(sessionDir, state)
		return messages, stats
	}

	state.AutoCompactAttempts++
	state.LastAutoCompactAttemptAt = time.Now().UTC()
	_ = contextmgr.SaveContextState(sessionDir, state)
	r.emitObservation(ctx, obs, runID, observability.EventCompactStarted, turn, observability.SeverityInfo, map[string]any{
		"kind":             "auto_full_compact",
		"final_tokens":     stats.FinalTokens,
		"usable_tokens":    stats.UsableInputTokens,
		"trimmed_messages": stats.TrimmedMessages,
	})
	result, compactErr := r.FullCompactSession(ctx)
	if compactErr != nil {
		state.AutoCompactConsecutiveFails++
		state.LastAutoCompactError = compactErr.Error()
		if state.AutoCompactConsecutiveFails >= limit {
			state.AutoCompactDisabled = true
		}
		_ = contextmgr.SaveContextState(sessionDir, state)
		r.emitObservation(ctx, obs, runID, observability.EventCompactFinished, turn, observability.SeverityWarn, map[string]any{
			"kind":                 "auto_full_compact",
			"status":               ToolStatusError,
			"error":                compactErr.Error(),
			"consecutive_failures": state.AutoCompactConsecutiveFails,
			"disabled":             state.AutoCompactDisabled,
		})
		return messages, stats
	}
	state.AutoCompactSuccesses++
	state.AutoCompactConsecutiveFails = 0
	state.LastAutoCompactError = ""
	state.LastAutoCompactSuccessAt = time.Now().UTC()
	state.LastCompactPath = result.Path
	_ = contextmgr.SaveContextState(sessionDir, state)
	r.emitObservation(ctx, obs, runID, observability.EventCompactFinished, turn, observability.SeverityInfo, map[string]any{
		"kind":      "auto_full_compact",
		"status":    ToolStatusSuccess,
		"path":      result.Path,
		"chars":     result.Chars,
		"backed_up": result.BackedUp,
	})
	return r.buildRequestMessagesWithStats()
}

func (r *Runner) shouldAutoCompact(stats contextmgr.Stats) bool {
	if r == nil || r.ContextManager == nil || r.Client == nil {
		return false
	}
	cfg := r.ContextManager.Config.Normalize()
	if !cfg.EnableAutoCompact {
		return false
	}
	if stats.UsableInputTokens <= 0 || stats.CompactTriggerTokens <= 0 {
		return false
	}
	if stats.TrimmedMessages > 0 {
		return true
	}
	return stats.FinalTokens >= stats.UsableInputTokens
}
