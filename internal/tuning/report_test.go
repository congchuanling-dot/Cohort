package tuning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/observability"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

func TestGenerateWritesTuningReport_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	sessionRoot := t.TempDir()
	store := session.NewStore(sessionRoot)
	sess, err := store.Create("tuning", t.TempDir(), "test-model")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 13, 0, 0, 0, time.UTC)
	events := []observability.Event{
		tuningEvent(base, "run_1", sess.ID, observability.EventRunStarted, 0, observability.SeverityInfo, nil),
		tuningEvent(base.Add(10*time.Millisecond), "run_1", sess.ID, observability.EventContextBuilt, 0, observability.SeverityInfo, map[string]any{
			"final_tokens":   25000,
			"final_chars":    90000,
			"final_messages": 12,
		}),
		tuningEvent(base.Add(15*time.Millisecond), "run_1", sess.ID, observability.EventToolRouteSelected, 1, observability.SeverityInfo, map[string]any{
			"mode":                  "adaptive",
			"reason":                "baseline",
			"selected_count":        15,
			"full_schema_count":     81,
			"selected_schema_bytes": 22000,
			"saved_schema_bytes":    55000,
		}),
		tuningEvent(base.Add(20*time.Millisecond), "run_1", sess.ID, observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{
			"message_count":     12,
			"tool_schema_count": 81,
			"request_chars":     77000,
		}),
		tuningEvent(base.Add(6020*time.Millisecond), "run_1", sess.ID, observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"duration_ms":     6000,
			"tool_call_count": 1,
			"usage": map[string]any{
				"input_tokens":  1000,
				"output_tokens": 100,
				"total_tokens":  1100,
			},
		}),
		tuningEvent(base.Add(6100*time.Millisecond), "run_1", sess.ID, observability.EventToolFinished, 1, observability.SeverityWarn, map[string]any{
			"tool":        "desktop_windows",
			"status":      "error",
			"duration_ms": 80,
			"error_code":  "desktop_not_ready",
		}),
		tuningEvent(base.Add(6200*time.Millisecond), "run_1", sess.ID, observability.EventToolFinished, 1, observability.SeverityInfo, map[string]any{
			"tool":        "ask_user",
			"status":      "success",
			"duration_ms": 20,
		}),
		tuningEvent(base.Add(6300*time.Millisecond), "run_1", sess.ID, observability.EventPermissionDecision, 1, observability.SeverityInfo, map[string]any{
			"decision": "approved",
		}),
		tuningEvent(base.Add(7000*time.Millisecond), "run_1", sess.ID, observability.EventRunFinished, 1, observability.SeverityInfo, map[string]any{
			"status":      "completed",
			"duration_ms": 7000,
		}),
	}
	writeTuningEvents(t, filepath.Join(store.SessionDir(sess.ID), traceview.ObservationLogFileName), events)

	report, err := Generate(workspace, Options{
		SessionRoot: sessionRoot,
		Limit:       10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunsScanned != 1 || report.SessionsScanned != 1 {
		t.Fatalf("report counts = %#v", report)
	}
	if report.LLMDurationMS != 6000 || report.ToolFailures != 1 || report.AskUserCalls != 1 || report.PermissionEvents != 1 {
		t.Fatalf("report summary = %#v", report)
	}
	if report.SchemaBloatRuns != 1 || report.RequestBloatRuns != 1 || report.ContextBloatRuns != 1 {
		t.Fatalf("bloat summary = %#v", report)
	}
	if report.AdaptiveRoutedRuns != 1 || report.SchemaBytesSaved != 55000 {
		t.Fatalf("adaptive routing summary = %#v", report)
	}
	if len(report.FailedTools) != 1 || report.FailedTools[0].Tool != "desktop_windows" {
		t.Fatalf("failed tools = %#v", report.FailedTools)
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(DefaultReportPath)))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"# Cohort Agent 调优报告",
		"schema_bloat_runs: 1",
		"adaptive_routed_runs: 1",
		"schema_bytes_saved: 55000",
		"`desktop_windows`",
		"主要瓶颈在 LLM 请求",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("report missing %q:\n%s", want, content)
		}
	}
	dashboard, err := os.ReadFile(report.DashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"日常 Agent 调优面板", "最慢 LLM 调用", "失败工具 Top"} {
		if !strings.Contains(string(dashboard), want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
}

func tuningEvent(at time.Time, runID string, sessionID string, eventType observability.EventType, turn int, severity observability.Severity, data map[string]any) observability.Event {
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

func writeTuningEvents(t *testing.T, path string, events []observability.Event) {
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
