package plan

import (
	"os"
	"strings"
	"testing"
)

func TestPlanRequiresEvidenceBeforeCompletion_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	state, err := store.Create("P0", []string{"implement", "verify"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Steps) != 2 || state.Steps[0].Status != StepPending {
		t.Fatalf("state = %#v", state)
	}
	if _, err := store.VerifyStep(1, ""); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("VerifyStep empty evidence err = %v, want evidence error", err)
	}
	state, err = store.StartStep(1)
	if err != nil {
		t.Fatal(err)
	}
	if state.Steps[0].Status != StepInProgress {
		t.Fatalf("step status = %s, want in_progress", state.Steps[0].Status)
	}
	state, err = store.VerifyStep(1, "go test ./internal/plan")
	if err != nil {
		t.Fatal(err)
	}
	if state.Steps[0].Status != StepCompleted || state.Steps[0].Evidence == "" {
		t.Fatalf("verified step = %#v", state.Steps[0])
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go test ./internal/plan") {
		t.Fatalf("plan file missing evidence:\n%s", data)
	}
}
