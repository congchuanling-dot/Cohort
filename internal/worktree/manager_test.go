package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerCreatesInspectsCommitsAndRemovesWorktree_BitsUT(t *testing.T) {
	root := initRepo(t)
	base := gitOutput(t, root, "rev-parse", "HEAD")
	manager, err := NewManager(root, filepath.Join(root, ".cohort", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		ID:         "candidate-1",
		BaseCommit: base,
		Branch:     "cohort/test/candidate-1",
		Path:       filepath.Join(manager.RootDir, "candidate-1"),
	}
	if err := manager.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Path, "feature.txt"), []byte("implemented\n"), 0644); err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.Inspect(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Files) != 1 || inspection.Files[0] != "feature.txt" || inspection.TreeHash == "" {
		t.Fatalf("inspection = %#v", inspection)
	}
	commit, err := manager.Commit(context.Background(), spec, "implement feature")
	if err != nil {
		t.Fatal(err)
	}
	if commit == base {
		t.Fatalf("commit = base %s", base)
	}
	if err := manager.Remove(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestManagerMergesDependencyCommit_BitsUT(t *testing.T) {
	root := initRepo(t)
	base := gitOutput(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "dependency.txt"), []byte("dependency\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "dependency.txt")
	gitRun(t, root, "commit", "-m", "dependency")
	dependency := gitOutput(t, root, "rev-parse", "HEAD")

	manager, err := NewManager(root, filepath.Join(root, ".cohort", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		ID:         "candidate-2",
		BaseCommit: base,
		Branch:     "cohort/test/candidate-2",
		Path:       filepath.Join(manager.RootDir, "candidate-2"),
	}
	if err := manager.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := manager.MergeCommits(context.Background(), spec, []string{dependency}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(spec.Path, "dependency.txt"))
	if err != nil || string(data) != "dependency\n" {
		t.Fatalf("merged dependency = %q, err=%v", data, err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init")
	gitRun(t, root, "config", "user.email", "worktree-test@example.com")
	gitRun(t, root, "config", "user.name", "Worktree Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "README.md")
	gitRun(t, root, "commit", "-m", "base")
	return root
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
