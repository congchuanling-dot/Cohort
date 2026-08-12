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
		testEvent(base.Add(3*time.Millisecond), runID, sessionID, observability.EventContextBuilt, 1, observability.SeverityInfo, map[string]any{
			"final_messages": 3, "final_tokens": 100, "final_chars": 400, "usable_input_tokens": 1000,
			"trigger_reason": "below_compact_trigger_threshold",
		}),
		testEvent(base.Add(4*time.Millisecond), runID, sessionID, observability.EventToolRouteSelected, 1, observability.SeverityInfo, map[string]any{"mode": "adaptive", "selected_count": 15, "full_schema_count": 81, "saved_schema_bytes": 55000}),
		testEvent(base.Add(5*time.Millisecond), runID, sessionID, observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{"message_count": 3, "tool_schema_count": 15}),
		testEvent(base.Add(505*time.Millisecond), runID, sessionID, observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"status": "success", "duration_ms": 500, "tool_call_count": 1, "content_chars": 10,
			"usage": map[string]any{"input_tokens": 120, "output_tokens": 10, "total_tokens": 130},
		}),
		testEvent(base.Add(506*time.Millisecond), runID, sessionID, observability.EventToolStarted, 1, observability.SeverityInfo, map[string]any{
			"tool": "apply_patch", "tool_call_id": "call_1", "args_hash": "sha256:test", "args_summary": `{"path":"main.go"}`,
		}),
		testEvent(base.Add(706*time.Millisecond), runID, sessionID, observability.EventToolFinished, 1, observability.SeverityInfo, map[string]any{"tool": "apply_patch", "tool_call_id": "call_1", "status": "success", "duration_ms": 200}),
		testEvent(base.Add(707*time.Millisecond), runID, sessionID, observability.EventFileChanged, 1, observability.SeverityInfo, map[string]any{"tool": "apply_patch", "tool_call_id": "call_1", "path": "/workspace/main.go"}),
		testEvent(base.Add(710*time.Millisecond), runID, sessionID, observability.EventRunFinished, 1, observability.SeverityInfo, map[string]any{"status": "completed", "duration_ms": 710}),
	}
	view := RunView{SessionID: sessionID, RunID: runID, Events: events}
	graph := view.CausalGraph()
	if graph.Summary.LLMNodes != 1 || graph.Summary.ToolNodes != 1 || graph.Summary.FileChanges != 1 {
		t.Fatalf("graph summary = %#v", graph.Summary)
	}
	for _, node := range graph.Nodes {
		if node.Kind == "context" || node.Kind == "turn" {
			t.Fatalf("ordinary context/turn must be an LLM attribute, got node %#v", node)
		}
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
	llmNode, toolNode := graphNodeByID(graph, "llm-1"), graphNodeByID(graph, "tool-call_1")
	if llmNode.Execution.TokenUsage == nil || llmNode.Execution.TokenUsage.Source != UsageSourceProviderReported ||
		llmNode.Execution.TokenUsage.Total != 130 || llmNode.Execution.Attributes["context_messages"] != "3" {
		t.Fatalf("LLM execution detail = %#v", llmNode.Execution)
	}
	if toolNode.Execution.ParametersSummary != `{"path":"main.go"}` || toolNode.Execution.ParametersHash != "sha256:test" ||
		toolNode.Execution.OutputSummary == "" || len(toolNode.Execution.Evidence) < 2 {
		t.Fatalf("tool execution detail = %#v", toolNode.Execution)
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

func graphNodeByID(graph Graph, id string) GraphNode {
	for _, node := range graph.Nodes {
		if node.ID == id {
			return node
		}
	}
	return GraphNode{}
}

func TestReceiptLedgerSeparatesProviderUsageFromEstimate_BitsUT(t *testing.T) {
	base := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	events := []observability.Event{
		testEvent(base, "run_receipt", "session_receipt", observability.EventContextBuilt, 1, observability.SeverityInfo, map[string]any{
			"final_tokens": 900,
		}),
		testEvent(base.Add(time.Millisecond), "run_receipt", "session_receipt", observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{
			"message_count": 4, "request_chars": 3600, "tool_schema_count": 81,
		}),
		testEvent(base.Add(100*time.Millisecond), "run_receipt", "session_receipt", observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"status": "success", "duration_ms": 99, "usage": map[string]any{
				"input_tokens": 1000, "output_tokens": 50, "total_tokens": 1050, "cache_read_input_tokens": 800,
			},
		}),
		testEvent(base.Add(101*time.Millisecond), "run_receipt", "session_receipt", observability.EventContextBuilt, 2, observability.SeverityInfo, map[string]any{
			"final_tokens": 1200,
		}),
		testEvent(base.Add(102*time.Millisecond), "run_receipt", "session_receipt", observability.EventLLMRequestStarted, 2, observability.SeverityInfo, map[string]any{
			"message_count": 6, "request_chars": 4800, "tool_schema_count": 81,
		}),
		testEvent(base.Add(200*time.Millisecond), "run_receipt", "session_receipt", observability.EventLLMResponseFinished, 2, observability.SeverityInfo, map[string]any{
			"status": "success", "duration_ms": 98,
		}),
	}
	ledger := (RunView{SessionID: "session_receipt", RunID: "run_receipt", Events: events}).ReceiptLedger()
	if ledger.UsageSource != UsageSourceProviderReported || ledger.ProviderTurns != 1 || ledger.UnavailableTurns != 1 {
		t.Fatalf("ledger sources = %#v", ledger)
	}
	if ledger.InputTokens != 1000 || ledger.OutputTokens != 50 || ledger.TotalTokens != 1050 || ledger.CacheReadTokens != 800 {
		t.Fatalf("ledger totals = %#v", ledger)
	}
	if len(ledger.Receipts) != 2 || ledger.Receipts[0].EstimatedInputTokens != 900 ||
		ledger.Receipts[0].UsageSource != UsageSourceProviderReported ||
		ledger.Receipts[1].UsageSource != UsageSourceUnavailable ||
		ledger.Receipts[1].EstimatedInputTokens != 1200 {
		t.Fatalf("receipts = %#v", ledger.Receipts)
	}
	if ledger.EstimatedCostUSD != nil || ledger.CostPricingSource != "not_configured" {
		t.Fatalf("unconfigured cost must stay unavailable: %#v", ledger)
	}
}

func TestReceiptLedgerDoesNotTreatRedactedUsageAsProviderNumbers_BitsUT(t *testing.T) {
	event := testEvent(time.Now(), "run_redacted", "session_redacted", observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
		"status": "success",
		"usage": map[string]any{
			"input_tokens": map[string]any{"redacted": true, "hash": "sha256:secret"},
			"total_tokens": map[string]any{"redacted": true, "hash": "sha256:secret"},
		},
	})
	ledger := (RunView{SessionID: "session_redacted", RunID: "run_redacted", Events: []observability.Event{event}}).ReceiptLedger()
	if ledger.ProviderTurns != 0 || ledger.UnavailableTurns != 1 || ledger.TotalTokens != 0 ||
		ledger.Receipts[0].UsageSource != UsageSourceUnavailable {
		t.Fatalf("redacted usage leaked into numeric ledger: %#v", ledger)
	}
}

func TestContextCapacityUsesProviderReceiptAndExplainsWaterfall_BitsUT(t *testing.T) {
	base := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	events := []observability.Event{
		testEvent(base, "run_capacity", "session_capacity", observability.EventContextBuilt, 1, observability.SeverityInfo, map[string]any{
			"original_tokens": 800, "final_tokens": 1000, "original_messages": 4, "final_messages": 6,
			"context_window_tokens": 2000, "usable_input_tokens": 1800, "compact_trigger_tokens": 1260,
			"context_window_source": "test_registry", "capability_version": "v1", "capability_confidence": "verified",
			"relevant_memory_chars": 200, "omitted_tool_result_chars": 100, "trigger_reason": "below_compact_trigger_threshold",
		}),
		testEvent(base.Add(time.Millisecond), "run_capacity", "session_capacity", observability.EventLLMRequestStarted, 1, observability.SeverityInfo, map[string]any{
			"message_count": 6, "request_chars": 4000,
		}),
		testEvent(base.Add(2*time.Millisecond), "run_capacity", "session_capacity", observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"status": "success", "usage": map[string]any{"input_tokens": 1700, "output_tokens": 20, "total_tokens": 1720},
		}),
	}
	report := (RunView{SessionID: "session_capacity", RunID: "run_capacity", Events: events}).ContextCapacity("test-model")
	if report.Capability.ContextWindowTokens != 2000 || report.Capability.Source != "test_registry" ||
		report.State != "critical" || len(report.Turns) != 1 {
		t.Fatalf("capacity report = %#v", report)
	}
	turn := report.Turns[0]
	if turn.MeasurementSource != UsageSourceProviderReported || turn.EffectiveInputTokens != 1700 ||
		turn.EstimatedInputTokens != 1000 || turn.OccupancyRatio < 0.94 {
		t.Fatalf("capacity turn = %#v", turn)
	}
	if report.Calibration.Samples != 1 || report.Calibration.AverageActualRatio != 1.7 ||
		len(turn.Waterfall) < 4 || len(report.RecommendedActions) == 0 {
		t.Fatalf("capacity calibration/waterfall = %#v", report)
	}
}

func TestGovernanceReportIncludesEnforcedAndPendingInterventions_BitsUT(t *testing.T) {
	base := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	events := []observability.Event{
		testEvent(base, "run_governance", "session_governance", observability.EventContextBuilt, 1, observability.SeverityWarn, map[string]any{
			"final_tokens": 950, "usable_input_tokens": 1000, "trimmed_messages": 2,
			"trigger_reason": "over_usable_input_budget",
		}),
		testEvent(base.Add(time.Millisecond), "run_governance", "session_governance", observability.EventGovernanceIntervention, 2, observability.SeverityWarn, map[string]any{
			"policy_id": "tool.repeated_identical_failure", "action": "circuit_break",
			"reason": "same call failed twice", "enforcement": "enforced",
		}),
	}
	report := (RunView{SessionID: "session_governance", RunID: "run_governance", Events: events}).Governance("unknown")
	if report.State != "action_required" || len(report.Policies) != 4 || len(report.Interventions) != 3 {
		t.Fatalf("governance report = %#v", report)
	}
	var enforcedCircuit, pendingCompact bool
	for _, item := range report.Interventions {
		if item.PolicyID == "tool.repeated_identical_failure" && item.Enforcement == "enforced" {
			enforcedCircuit = true
		}
		if item.PolicyID == "context.capacity" && item.Status == "pending" && item.Action == "full_compact" {
			pendingCompact = true
		}
	}
	if !enforcedCircuit || !pendingCompact {
		t.Fatalf("interventions = %#v", report.Interventions)
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
