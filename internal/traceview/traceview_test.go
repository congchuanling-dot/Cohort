package traceview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cohort/internal/observability"
	"cohort/internal/session"
)

func TestLoadLatestSummarizesRunLog_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	sess, err := store.Create("trace test", t.TempDir(), "test-model")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	events := []observability.Event{
		testEvent(base, "run_old", sess.ID, observability.EventRunStarted, 0, observability.SeverityInfo, nil),
		testEvent(base.Add(10*time.Millisecond), "run_old", sess.ID, observability.EventRunFinished, 0, observability.SeverityInfo, map[string]any{"status": "completed", "duration_ms": 10}),
		testEvent(base.Add(time.Second), "run_new", sess.ID, observability.EventRunStarted, 0, observability.SeverityInfo, nil),
		testEvent(base.Add(1100*time.Millisecond), "run_new", sess.ID, observability.EventContextBuilt, 0, observability.SeverityInfo, map[string]any{
			"final_tokens":   1200,
			"final_chars":    4800,
			"final_messages": 6,
		}),
		testEvent(base.Add(1200*time.Millisecond), "run_new", sess.ID, observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{
			"message_count":     6,
			"tool_schema_count": 81,
			"request_chars":     77000,
			"last_unused_field": "ignored",
		}),
		testEvent(base.Add(1700*time.Millisecond), "run_new", sess.ID, observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"duration_ms":     500,
			"tool_call_count": 1,
			"content_chars":   20,
			"raw_chars":       100,
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 20,
				"total_tokens":  120,
			},
		}),
		testEvent(base.Add(1800*time.Millisecond), "run_new", sess.ID, observability.EventToolFinished, 1, observability.SeverityWarn, map[string]any{
			"tool":         "browser_open",
			"status":       "error",
			"duration_ms":  90,
			"error_code":   "bridge_unavailable",
			"result_chars": 120,
		}),
		testEvent(base.Add(2*time.Second), "run_new", sess.ID, observability.EventRunFinished, 1, observability.SeverityInfo, map[string]any{
			"status":      "completed",
			"duration_ms": 1000,
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 20,
				"total_tokens":  120,
			},
		}),
	}
	writeEvents(t, filepath.Join(store.SessionDir(sess.ID), ObservationLogFileName), events)

	view, err := LoadLatest(root)
	if err != nil {
		t.Fatal(err)
	}
	if view.RunID != "run_new" {
		t.Fatalf("RunID = %q, want run_new", view.RunID)
	}
	summary := view.Summary()
	if summary.SessionID != sess.ID || summary.Status != "completed" {
		t.Fatalf("summary identity/status = %#v", summary)
	}
	if summary.LLMCalls != 1 || summary.LLMDurationMS != 500 {
		t.Fatalf("llm summary = %#v", summary)
	}
	if summary.ToolCalls != 1 || summary.ToolFailures != 1 || summary.ToolDurationMS != 90 {
		t.Fatalf("tool summary = %#v", summary)
	}
	if summary.LastToolSchemaCount != 81 || summary.LastRequestChars != 77000 {
		t.Fatalf("request summary = %#v", summary)
	}
	if summary.TotalTokens != 120 || summary.InputTokens != 100 || summary.OutputTokens != 20 {
		t.Fatalf("usage summary = %#v", summary)
	}
	if len(summary.Gaps) == 0 || summary.Gaps[0].GapMS < 500 {
		t.Fatalf("gaps = %#v, want largest gap from llm latency", summary.Gaps)
	}
}

func testEvent(at time.Time, runID string, sessionID string, eventType observability.EventType, turn int, severity observability.Severity, data map[string]any) observability.Event {
	return observability.Event{
		SchemaVersion: observability.SchemaVersion,
		EventID:       string(eventType) + "_" + runID,
		EventType:     eventType,
		Time:          at,
		RunID:         runID,
		SessionID:     sessionID,
		Turn:          turn,
		Workspace:     "/tmp/workspace",
		Source:        "runner",
		Severity:      severity,
		Data:          data,
	}
}

func writeEvents(t *testing.T, path string, events []observability.Event) {
	t.Helper()
	var data []byte
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
