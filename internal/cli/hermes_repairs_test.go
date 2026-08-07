package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/evaluation"
	"cohort/internal/hermes"
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

func TestHermesAutoRepairShortcutTogglesConfig_BitsUT(t *testing.T) {
	store := hermes.NewStore(t.TempDir())
	var out bytes.Buffer
	if err := hermesAutoRepair(store, []string{"on"}, &out); err != nil {
		t.Fatal(err)
	}
	config, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.AutoRepair.Enabled {
		t.Fatal("auto repair was not enabled")
	}
	out.Reset()
	if toggleErr := hermesAutoRepair(store, []string{"off"}, &out); toggleErr != nil {
		t.Fatal(toggleErr)
	}
	config, err = store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.AutoRepair.Enabled {
		t.Fatal("auto repair was not disabled")
	}
}

func TestHermesReviewShortcutPrintsRepairAndDiff_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := hermes.NewStore(root)
	diffPath := filepath.Join(root, "diff.patch")
	diff := "diff --git a/a.go b/a.go\n+fixed\n"
	if err := os.WriteFile(diffPath, []byte(diff), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := hermes.RepairTask{
		ID:           "repair-1",
		ActionID:     "action-1",
		Status:       hermes.RepairStatusReadyForReview,
		Severity:     hermes.AlertSeverityHigh,
		CreatedAt:    now,
		UpdatedAt:    now,
		DiffPath:     diffPath,
		ChangedFiles: []string{"a.go"},
	}
	if err := store.SaveRepairs(hermes.Repairs{Repairs: []hermes.RepairTask{task}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := hermesRepairReview(store, "repair-1", &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, `"id": "repair-1"`) || !strings.Contains(text, "--- diff ---") || !strings.Contains(text, "+fixed") {
		t.Fatalf("review output missing repair metadata or diff:\n%s", text)
	}
}
