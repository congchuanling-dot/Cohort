package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsValidatedPlanAndEvents_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	fixed := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }
	item, err := store.CreateDraft("add bounded delivery", strings.Repeat("a", 40), false, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	contract, graph := validPlanFixture(item)
	item, err = store.SavePlan(item.ID, contract, graph)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusPlanned || item.ContractHash == "" || item.GraphHash == "" {
		t.Fatalf("planned delivery = %#v", item)
	}
	storedContract, storedGraph, err := store.LoadPlan(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedContract.Criteria[0].ID != "AC-1" || len(storedGraph.Nodes) != 2 {
		t.Fatalf("stored plan = %#v %#v", storedContract, storedGraph)
	}
	events, err := os.ReadFile(filepath.Join(store.RootDir, item.ID, eventsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "DeliveryCreated") || !strings.Contains(string(events), "ContractCompiled") {
		t.Fatalf("events = %s", events)
	}
}

func TestGraphRejectsUnorderedOverlappingWriteSets_BitsUT(t *testing.T) {
	item := Delivery{ID: "delivery_test", BaseCommit: strings.Repeat("b", 40), RequirementHash: HashString("test")}
	contract, graph := validPlanFixture(item)
	graph.Nodes[1].DeclaredWrites = []string{"internal/delivery/**"}
	graph.Nodes[1].Dependencies = nil
	if err := ValidateGraph(graph, contract); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("ValidateGraph err = %v, want overlapping write set error", err)
	}
	graph.Nodes[1].Dependencies = []string{graph.Nodes[0].ID}
	if err := ValidateGraph(graph, contract); err != nil {
		t.Fatalf("ordered graph rejected: %v", err)
	}
}

func TestPlanServiceMarksInvalidPlannerOutputFailed_BitsUT(t *testing.T) {
	root := initDeliveryGitRepo(t)
	store := NewStore(root)
	service := PlanService{Store: store}
	item, _, _, err := service.Plan(context.Background(), "invalid plan", func(_ context.Context, request PlanRequest) (PlanDraft, error) {
		contract, graph := validPlanFixture(request.Delivery)
		graph.Nodes[0].Dependencies = []string{"missing"}
		return PlanDraft{Contract: contract, Graph: graph}, nil
	})
	if err == nil {
		t.Fatal("Plan succeeded, want invalid graph error")
	}
	stored, loadErr := store.Load(item.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if stored.Status != StatusFailed || !strings.Contains(stored.Error, "unknown node") {
		t.Fatalf("failed delivery = %#v", stored)
	}
}

func TestParsePlanDraftRejectsUnknownFields_BitsUT(t *testing.T) {
	_, err := ParsePlanDraft(`{"contract":{"unknown":true},"graph":{}}`)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParsePlanDraft err = %v, want unknown field", err)
	}
}

func TestDeliveryStateMachineRejectsUnsafeSkip_BitsUT(t *testing.T) {
	if err := ValidateTransition(StatusPlanned, StatusVerified); err == nil {
		t.Fatal("planned -> verified transition unexpectedly allowed")
	}
	if err := ValidateTransition(StatusReadyForReview, StatusApproved); err != nil {
		t.Fatalf("ready_for_review -> approved rejected: %v", err)
	}
}

func validPlanFixture(item Delivery) (AcceptanceContract, TaskGraph) {
	contract := AcceptanceContract{
		SchemaVersion:   SchemaVersion,
		RequirementHash: item.RequirementHash,
		BaseCommit:      item.BaseCommit,
		Summary:         "Implement delivery state and tests.",
		Criteria: []Criterion{{
			ID:             "AC-1",
			Statement:      "Delivery state is persisted and validated.",
			Mandatory:      true,
			Verification:   VerifyCommand,
			TargetPaths:    []string{"internal/delivery/**"},
			EvidencePolicy: EvidenceExecution,
			GateIDs:        []string{"gate-unit"},
		}},
		AllowedScope:   []string{"internal/delivery/**", "docs/**"},
		ForbiddenScope: []string{".git/**"},
		RiskProfile:    RiskProfile{Level: RiskMedium},
		RequiredGates: []GateSpec{{
			ID:             "gate-unit",
			Name:           "delivery unit tests",
			Kind:           "command",
			Command:        []string{"go", "test", "./internal/delivery"},
			Paths:          []string{"internal/delivery/**"},
			Mandatory:      true,
			TimeoutSeconds: 300,
		}},
	}
	graph := TaskGraph{
		SchemaVersion: SchemaVersion,
		DeliveryID:    item.ID,
		BaseCommit:    item.BaseCommit,
		Nodes: []TaskNode{
			{
				ID:             "delivery-core",
				Title:          "Implement delivery core",
				Objective:      "Persist and validate delivery state.",
				Role:           RoleBuilder,
				Status:         NodePending,
				ReadSet:        []string{"internal/delivery/**"},
				DeclaredWrites: []string{"internal/delivery/**"},
				Criteria:       []string{"AC-1"},
				Risk:           RiskMedium,
				CandidateCount: 1,
				Budget:         DefaultNodeBudget(),
			},
			{
				ID:             "delivery-docs",
				Title:          "Document delivery",
				Objective:      "Document the persistent delivery contract.",
				Role:           RoleBuilder,
				Status:         NodePending,
				Dependencies:   []string{"delivery-core"},
				ReadSet:        []string{"docs/**"},
				DeclaredWrites: []string{"docs/**"},
				Criteria:       []string{"AC-1"},
				Risk:           RiskLow,
				CandidateCount: 1,
				Budget:         DefaultNodeBudget(),
			},
		},
	}
	return contract, graph
}

func initDeliveryGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runTestGit(t, root, "init")
	runTestGit(t, root, "config", "user.email", "delivery-test@example.com")
	runTestGit(t, root, "config", "user.name", "Delivery Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", "README.md")
	runTestGit(t, root, "commit", "-m", "init")
	return root
}

func runTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	output, err := runGitText(context.Background(), root, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
