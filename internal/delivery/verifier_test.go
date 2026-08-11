package delivery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestVerifierCouncilPassesIndependentReports_BitsUT(t *testing.T) {
	store, item := createVerifyingDelivery(t)
	verifier := func(_ context.Context, _ Delivery, _ AcceptanceContract, _ IntegrationState, role AgentRole) (VerifierReport, error) {
		return VerifierReport{
			Role:     role,
			Verdict:  VerdictPass,
			Score:    96,
			Summary:  "criteria satisfied",
			Findings: []Finding{},
		}, nil
	}
	state, err := (VerifierCouncil{Store: store, MaxParallel: 2}).Run(context.Background(), item.ID, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != VerificationPassed || len(state.Reports) != 2 || len(state.Findings) != 0 {
		t.Fatalf("verification = %#v", state)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusReadyForReview {
		t.Fatalf("delivery status = %s, want ready_for_review", stored.Status)
	}
}

func TestVerifierCouncilRequestsRevisionForHighFinding_BitsUT(t *testing.T) {
	store, item := createVerifyingDelivery(t)
	verifier := func(_ context.Context, _ Delivery, _ AcceptanceContract, _ IntegrationState, role AgentRole) (VerifierReport, error) {
		report := VerifierReport{Role: role, Verdict: VerdictPass, Score: 90, Summary: "checked"}
		if role == RoleCorrectnessVerifier {
			report.Verdict = VerdictFail
			report.Score = 45
			report.Findings = []Finding{{
				CriterionID: "AC-1",
				Severity:    SeverityHigh,
				Confidence:  0.95,
				File:        "internal/delivery/runtime_store.go",
				Line:        10,
				Claim:       "completion count ignores passed nodes",
				Evidence:    []string{"the loop does not increment for NodePassed"},
				FixHint:     "increment only for NodePassed",
			}}
		}
		return report, nil
	}
	state, err := (VerifierCouncil{Store: store, MaxParallel: 2}).Run(context.Background(), item.ID, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != VerificationFailed || len(state.Findings) != 1 {
		t.Fatalf("verification = %#v", state)
	}
	if state.Findings[0].Fingerprint == "" || state.Findings[0].ID == "" {
		t.Fatalf("finding identity = %#v", state.Findings[0])
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusNeedsRevision {
		t.Fatalf("delivery status = %s, want needs_revision", stored.Status)
	}
}

func TestValidateVerifierReportRejectsUnsupportedFindingPath_BitsUT(t *testing.T) {
	contract := AcceptanceContract{Criteria: []Criterion{{ID: "AC-1"}}}
	report := VerifierReport{
		Role:    RoleSpecVerifier,
		Verdict: VerdictFail,
		Score:   20,
		Summary: "bad",
		Findings: []Finding{{
			CriterionID: "AC-1",
			Severity:    SeverityHigh,
			Confidence:  1,
			File:        "../outside",
			Claim:       "outside",
			Evidence:    []string{"path"},
		}},
	}
	err := ValidateVerifierReport(report, contract)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("ValidateVerifierReport err = %v", err)
	}
}

func createVerifyingDelivery(t *testing.T) (Store, Delivery) {
	t.Helper()
	root := initRunnableDeliveryRepo(t)
	store := NewStore(root)
	base, _, err := RepositoryState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateDraft("verify candidate", base, false, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	contract, graph := validPlanFixture(item)
	item, err = store.SavePlan(item.ID, contract, graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(item.ID, StatusRunning, "test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(item.ID, StatusIntegrating, "test", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIntegration(item.ID, IntegrationState{
		SchemaVersion: SchemaVersion,
		DeliveryID:    item.ID,
		Status:        IntegrationPassed,
		BaseCommit:    base,
		Commit:        "candidate",
		TreeHash:      "treehash",
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	item, err = store.Transition(item.ID, StatusVerifying, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return store, item
}
