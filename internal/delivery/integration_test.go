package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/worktree"
)

func TestIntegratorMergesSelectedCommitAndProducesFreshEvidence_BitsUT(t *testing.T) {
	root := initIntegrationRepo(t)
	store := NewStore(root)
	base, dirty, err := RepositoryState(context.Background(), root)
	if err != nil || dirty {
		t.Fatalf("repository state: base=%s dirty=%t err=%v", base, dirty, err)
	}
	item, err := store.CreateDraft("add integrated feature", base, false, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	contract := AcceptanceContract{
		Summary:      "Add a feature file and verify the Go module.",
		AllowedScope: []string{"feature.txt"},
		RiskProfile:  RiskProfile{Level: RiskLow},
		Criteria: []Criterion{{
			ID:             "AC-1",
			Statement:      "The complete Go module passes tests.",
			Mandatory:      true,
			Verification:   VerifyCommand,
			EvidencePolicy: EvidenceExecution,
			GateIDs:        []string{"gate-go"},
		}},
		RequiredGates: []GateSpec{{
			ID:             "gate-go",
			Name:           "go tests",
			Kind:           "command",
			Command:        []string{"go", "test", "./..."},
			Mandatory:      true,
			TimeoutSeconds: 120,
		}},
	}
	graph := TaskGraph{Nodes: []TaskNode{{
		ID:             "feature",
		Title:          "Add feature",
		Objective:      "Create the requested feature file.",
		Role:           RoleBuilder,
		DeclaredWrites: []string{"feature.txt"},
		Criteria:       []string{"AC-1"},
		Risk:           RiskLow,
		CandidateCount: 1,
		Budget:         DefaultNodeBudget(),
	}}}
	item, err = store.SavePlan(item.ID, contract, graph)
	if err != nil {
		t.Fatal(err)
	}
	contract, graph, err = store.LoadPlan(item.ID)
	if err != nil {
		t.Fatal(err)
	}

	manager, err := worktree.NewManager(root, store.WorktreeDir)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		ID:           "feature-c1",
		NodeID:       "feature",
		BaseCommit:   base,
		Branch:       "cohort/test/feature-c1",
		WorktreePath: filepath.Join(store.WorktreeDir, item.ID, "builders", "feature", "feature-c1"),
	}
	spec := worktree.Spec{ID: candidate.ID, BaseCommit: base, Branch: candidate.Branch, Path: candidate.WorktreePath}
	if err := manager.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Path, "feature.txt"), []byte("integrated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.Inspect(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Commit, err = manager.Commit(context.Background(), spec, "feature")
	if err != nil {
		t.Fatal(err)
	}
	candidate.TreeHash = inspection.TreeHash
	candidate.ActualWrites = inspection.Files
	candidate.Status = CandidatePassed

	if _, err := store.InitializeRuntime(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNode(item.ID, "feature", "test", os.Getpid(), []Candidate{candidate}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCandidate(item.ID, "feature", candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteNode(item.ID, "feature", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(item.ID, StatusRunning, "test_started", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(item.ID, StatusIntegrating, "test_builders_finished", nil); err != nil {
		t.Fatal(err)
	}

	state, err := (Integrator{Store: store}).Run(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != IntegrationPassed || state.TreeHash == "" || len(state.EvidenceIDs) != 1 {
		t.Fatalf("integration state = %#v", state)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusVerifying {
		t.Fatalf("delivery status = %s, want verifying", stored.Status)
	}
	evidence, err := store.LoadEvidence(item.ID, state.EvidenceIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	environmentHash, err := EnvironmentHash(state.WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvidenceFreshness(evidence, stored, state.TreeHash, contract.RequiredGates[0], environmentHash); err != nil {
		t.Fatal(err)
	}
	evidence.TreeHash = "stale"
	if err := VerifyEvidenceFreshness(evidence, stored, state.TreeHash, contract.RequiredGates[0], environmentHash); err == nil || !strings.Contains(err.Error(), "tree hash") {
		t.Fatalf("stale evidence err = %v", err)
	}
}

func TestValidateGateCommandRejectsShell_BitsUT(t *testing.T) {
	err := ValidateGateCommand(GateSpec{Kind: "command", Command: []string{"sh", "-c", "go test ./..."}})
	if err == nil || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("ValidateGateCommand err = %v", err)
	}
}

func initIntegrationRepo(t *testing.T) string {
	t.Helper()
	root := initRunnableDeliveryRepo(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module integrationfixture\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package integrationfixture\n\nfunc Value() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package integrationfixture\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", "go.mod", "main.go", "main_test.go")
	runTestGit(t, root, "commit", "-m", "go fixture")
	return root
}
