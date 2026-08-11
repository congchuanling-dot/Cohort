package traceview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		testEvent(base.Add(1150*time.Millisecond), "run_new", sess.ID, observability.EventToolRouteSelected, 1, observability.SeverityInfo, map[string]any{
			"mode":                  "adaptive",
			"reason":                "intent_match",
			"selected_count":        15,
			"full_schema_count":     81,
			"selected_schema_bytes": 22000,
			"saved_schema_bytes":    55000,
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
		testEvent(base.Add(1900*time.Millisecond), "run_new", sess.ID, observability.EventToolFinished, 1, observability.SeverityInfo, map[string]any{
			"tool":         "computer_file_dialog",
			"status":       "error",
			"duration_ms":  20,
			"error_code":   "desktop_action_confirmation_required",
			"result_chars": 0,
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
	if summary.ToolCalls != 2 || summary.ToolFailures != 1 || summary.ToolDurationMS != 110 {
		t.Fatalf("tool summary = %#v", summary)
	}
	if summary.LastToolSchemaCount != 81 || summary.LastRequestChars != 77000 {
		t.Fatalf("request summary = %#v", summary)
	}
	if summary.LastToolRouteMode != "adaptive" || summary.LastFullSchemaCount != 81 ||
		summary.LastSchemaBytes != 22000 || summary.LastSavedSchemaBytes != 55000 {
		t.Fatalf("route summary = %#v", summary)
	}
	if summary.TotalTokens != 120 || summary.InputTokens != 100 || summary.OutputTokens != 20 {
		t.Fatalf("usage summary = %#v", summary)
	}
	if len(summary.Gaps) == 0 || summary.Gaps[0].GapMS < 500 {
		t.Fatalf("gaps = %#v, want largest gap from llm latency", summary.Gaps)
	}
}

func TestCausalGraphLinksToolArtifactsAndComputesCriticalPath_BitsUT(t *testing.T) {
	base := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	const (
		runID     = "run_graph"
		sessionID = "session_graph"
	)
	events := []observability.Event{
		testEvent(base, runID, sessionID, observability.EventRunStarted, 0, observability.SeverityInfo, nil),
		testEvent(base.Add(time.Millisecond), runID, sessionID, observability.EventUserPromptSubmitted, 0, observability.SeverityInfo, map[string]any{"chars": 42, "raw": "must-not-leak"}),
		testEvent(base.Add(2*time.Millisecond), runID, sessionID, observability.EventTurnStarted, 1, observability.SeverityInfo, nil),
		testEvent(base.Add(3*time.Millisecond), runID, sessionID, observability.EventContextBuilt, 1, observability.SeverityInfo, map[string]any{"final_messages": 3, "final_tokens": 100, "final_chars": 400}),
		testEvent(base.Add(4*time.Millisecond), runID, sessionID, observability.EventToolRouteSelected, 1, observability.SeverityInfo, map[string]any{"mode": "adaptive", "selected_count": 15, "full_schema_count": 81, "saved_schema_bytes": 55000}),
		testEvent(base.Add(5*time.Millisecond), runID, sessionID, observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{"message_count": 3, "tool_schema_count": 15}),
		testEvent(base.Add(505*time.Millisecond), runID, sessionID, observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{"status": "success", "duration_ms": 500, "tool_call_count": 1, "content_chars": 10}),
		testEvent(base.Add(506*time.Millisecond), runID, sessionID, observability.EventToolStarted, 1, observability.SeverityInfo, map[string]any{"tool": "apply_patch", "tool_call_id": "call_1"}),
		testEvent(base.Add(706*time.Millisecond), runID, sessionID, observability.EventToolFinished, 1, observability.SeverityInfo, map[string]any{"tool": "apply_patch", "tool_call_id": "call_1", "status": "success", "duration_ms": 200}),
		testEvent(base.Add(707*time.Millisecond), runID, sessionID, observability.EventFileChanged, 1, observability.SeverityInfo, map[string]any{"tool": "apply_patch", "tool_call_id": "call_1", "path": "/workspace/main.go"}),
		testEvent(base.Add(710*time.Millisecond), runID, sessionID, observability.EventRunFinished, 1, observability.SeverityInfo, map[string]any{"status": "completed", "duration_ms": 710}),
	}
	view := RunView{SessionID: sessionID, RunID: runID, Events: events}
	graph := view.CausalGraph()
	if graph.Summary.LLMNodes != 1 || graph.Summary.ToolNodes != 1 || graph.Summary.FileChanges != 1 {
		t.Fatalf("graph summary = %#v", graph.Summary)
	}
	if graph.CriticalPathMS != 700 {
		t.Fatalf("critical path = %dms, want 700ms", graph.CriticalPathMS)
	}
	hasWriteEdge := false
	for _, edge := range graph.Edges {
		if edge.From == "tool-call_1" && strings.HasPrefix(edge.To, "artifact-") && edge.Relation == "writes" {
			hasWriteEdge = true
		}
	}
	if !hasWriteEdge {
		t.Fatalf("graph edges = %#v, want tool -> artifact write edge", graph.Edges)
	}

	output := filepath.Join(t.TempDir(), "causal.html")
	if _, err := WriteGraphHTML(view, output); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Causal Trace Graph") || !strings.Contains(string(html), "main.go") {
		t.Fatalf("graph HTML missing expected content")
	}
	if strings.Contains(string(html), "must-not-leak") {
		t.Fatalf("graph HTML leaked raw event data")
	}
	inMemory, err := GraphHTML(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inMemory), "Causal Trace Graph") || strings.Contains(string(inMemory), "must-not-leak") {
		t.Fatal("in-memory graph HTML did not preserve the redacted graph view")
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
