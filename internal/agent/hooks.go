package agent

import (
	"context"

	"cohort/internal/hooks"
	"cohort/internal/observability"
)

func (r *Runner) emitHook(ctx context.Context, obs observability.Bus, runID string, eventType hooks.EventType, turn int, data map[string]any) {
	if r == nil || r.Hooks == nil {
		return
	}
	event := hooks.Event{
		Type:      eventType,
		RunID:     runID,
		SessionID: r.sessionID,
		Turn:      turn,
		Workspace: r.SessionCWD,
		Data:      data,
	}
	results := r.Hooks.Emit(ctx, event)
	if len(results) == 0 {
		return
	}
	summary := hooks.ResultsSummary(results)
	if summary == nil {
		return
	}
	summary["hook_event"] = string(eventType)
	severity := observability.SeverityInfo
	if failed, ok := summary["failed"].(int); ok && failed > 0 {
		severity = observability.SeverityWarn
	}
	r.emitObservation(ctx, obs, runID, observability.EventHookDispatched, turn, severity, summary)
}

func isFileMutationTool(name string) bool {
	return name == "file_write" || name == "file_patch"
}
