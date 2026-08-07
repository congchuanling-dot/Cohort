package cli

import (
	"os"
	"path/filepath"
	"testing"

	"cohort/internal/evaluation"
)

func TestRepairSuitePathUsesMainStoreAndRejectsTraversal_BitsUT(t *testing.T) {
	store := evaluation.NewStore(t.TempDir())
	if err := os.MkdirAll(store.SuitesDir(), 0755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(store.SuitesDir(), "custom-suite.json")
	if err := os.WriteFile(want, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	actual, err := repairSuitePath(store, "custom-suite")
	if err != nil {
		t.Fatal(err)
	}
	if actual != want {
		t.Fatalf("path=%q want=%q", actual, want)
	}
	for _, invalid := range []string{"../custom-suite", "/tmp/custom-suite", `folder\custom-suite`} {
		if _, err := repairSuitePath(store, invalid); err == nil {
			t.Fatalf("suite id %q unexpectedly accepted", invalid)
		}
	}
}
