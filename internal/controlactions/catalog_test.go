package controlactions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/controlplane"
)

func TestSnapshotProviderAggregatesProjectWithoutMutatingRepository_BitsUT(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("cohort\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")
	provider := SnapshotProvider(filepath.Join(root, ".cohort", "config.yaml"))
	value, err := provider(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := value.(DashboardSnapshot)
	if !ok {
		t.Fatalf("snapshot type = %T", value)
	}
	if snapshot.Project.Root != root || snapshot.Project.Head == "" || snapshot.Project.Dirty {
		t.Fatalf("project snapshot = %#v", snapshot.Project)
	}
	if snapshot.Counts.Deliveries != 0 || snapshot.Delivery.ByStatus == nil {
		t.Fatalf("resource snapshot = %#v", snapshot)
	}
	for _, resource := range []string{"deliveries", "hermes", "evaluations", "traces"} {
		if _, err := NewResourceProvider(filepath.Join(root, "config.yaml"))(context.Background(), root, resource, nil); err != nil {
			t.Fatalf("resource %s: %v", resource, err)
		}
	}
	command := exec.Command("git", "-C", root, "status", "--porcelain=v1", "--untracked-files=all")
	if output, err := command.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("snapshot mutated repository: %q err=%v", output, err)
	}
}

func TestCatalogExposesStableSystemAction_BitsUT(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	spec, exists := catalog.Get("system.ping")
	if !exists || spec.Risk != controlplane.RiskRead {
		t.Fatalf("system action = %#v exists=%t", spec, exists)
	}
	merge, exists := catalog.Get("delivery.merge")
	if !exists || merge.Risk != controlplane.RiskDanger || merge.ConfirmationText != "MERGE" {
		t.Fatalf("delivery merge action = %#v exists=%t", merge, exists)
	}
	if actions := catalog.List(); len(actions) < 40 {
		t.Fatalf("catalog only exposes %d actions", len(actions))
	}
	root := t.TempDir()
	if _, err := projectPath(root, "../outside.json"); err == nil {
		t.Fatal("expected project path escape to be rejected")
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
