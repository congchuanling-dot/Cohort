package delivery

import (
	"context"
	"testing"
)

func TestRevisionServiceBuildsScopedRepairAndReturnsToIntegration_BitsUT(t *testing.T) {
	store, item := createVerifyingDelivery(t)
	verifier := func(_ context.Context, _ Delivery, _ AcceptanceContract, _ IntegrationState, role AgentRole) (VerifierReport, error) {
		report := VerifierReport{Role: role, Verdict: VerdictPass, Score: 90, Summary: "checked"}
		if role == RoleCorrectnessVerifier {
			report.Verdict = VerdictFail
			report.Score = 40
			report.Findings = []Finding{{
				CriterionID: "AC-1",
				Severity:    SeverityHigh,
				Confidence:  0.98,
				File:        "internal/delivery/runtime_store.go",
				Line:        20,
				Claim:       "completion count is incorrect",
				Evidence:    []string{"NodePassed is not counted"},
				FixHint:     "count only NodePassed entries",
			}}
		}
		return report, nil
	}
	if _, err := (VerifierCouncil{Store: store}).Run(context.Background(), item.ID, verifier); err != nil {
		t.Fatal(err)
	}
	worker := func(_ context.Context, _ Delivery, _ AcceptanceContract, node TaskNode, candidate Candidate) (WorkerResult, error) {
		if len(node.DeclaredWrites) != 1 || node.DeclaredWrites[0] != "internal/delivery/runtime_store.go" {
			t.Fatalf("revision write set = %#v", node.DeclaredWrites)
		}
		if candidate.BaseCommit != "candidate" {
			t.Fatalf("revision base = %q, want integration commit", candidate.BaseCommit)
		}
		return WorkerResult{
			Summary:      "fixed",
			Commit:       "revision-commit",
			TreeHash:     "revision-tree",
			ActualWrites: []string{"internal/delivery/runtime_store.go"},
			Diff:         []byte("diff"),
			Result:       []byte(`{"status":"fixed"}`),
		}, nil
	}
	record, err := (RevisionService{Store: store}).Run(context.Background(), item.ID, worker)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != RevisionPassed || record.Candidate.Commit != "revision-commit" {
		t.Fatalf("revision = %#v", record)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusIntegrating {
		t.Fatalf("delivery status = %s, want integrating", stored.Status)
	}
	commits, err := store.RevisionCommits(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0] != "revision-commit" {
		t.Fatalf("revision commits = %#v", commits)
	}
}

func TestSelectCandidatePrefersSmallerVerifiedPatch_BitsUT(t *testing.T) {
	selected := SelectCandidate([]Candidate{
		{ID: "large", DiffBytes: 1000, ActualWrites: []string{"a", "b"}, Tokens: 100},
		{ID: "small", DiffBytes: 200, ActualWrites: []string{"a"}, Tokens: 500},
	})
	if selected.ID != "small" {
		t.Fatalf("selected = %#v", selected)
	}
}
