package hermes

import (
	"testing"
	"time"

	"cohort/internal/evaluation"
)

func TestSyncActionsPreservesResolvedStatusAndAlerts_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	index := evaluation.StabilityIndex{
		GeneratedAt: time.Now().UTC(),
		ActionItems: []evaluation.ActionItem{{
			ID: "a1", Scope: "case", Severity: "high", Category: "flaky", Title: "治理不稳定 case",
			SuiteID: "stateful", CaseID: "create_config", RunID: "r1", Evidence: "stability=50%",
		}},
		Summary: evaluation.StabilitySummary{Runs: 1, ActionItems: 1},
	}
	queue, alerts, err := SyncActions(store, index)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Actions) != 1 || len(alerts) != 1 {
		t.Fatalf("queue=%#v alerts=%#v", queue.Actions, alerts)
	}
	evalStore := evaluation.NewStore(root)
	if _, err := evalStore.SaveResult(evaluation.RunResult{
		RunID:     "verify-r1",
		SuiteID:   "stateful",
		StartedAt: time.Now().UTC().Add(time.Second),
		Gate:      &evaluation.GateResult{Passed: true},
		Cases:     []evaluation.CaseResult{{CaseID: "create_config", Passed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := VerifyActionWithRun(store, evalStore, "a1", "verify-r1", true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != QueueStatusResolved {
		t.Fatalf("status = %s", resolved.Status)
	}
	queue, alerts, err = SyncActions(store, index)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("resolved existing action should not alert again: %#v", alerts)
	}
	if len(queue.Actions) != 1 || queue.Actions[0].Status != QueueStatusResolved || queue.Actions[0].Occurrences != 1 {
		t.Fatalf("resolved action not preserved: %#v", queue.Actions)
	}
	open, critical, high := CountOpen(queue)
	if open != 0 || critical != 0 || high != 0 {
		t.Fatalf("open counts = %d/%d/%d", open, critical, high)
	}
}
