package replay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceSnapshotRestoresTrackedAndUntrackedFiles(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Replay Test")
	runGit(t, repo, "config", "user.email", "replay@test.invalid")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	baseline := inspectGitBaseline(repo)
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := captureWorkspaceSnapshot(bundle, baseline)
	if !snapshot.Available || len(snapshot.Untracked) != 1 || snapshot.PatchFile == "" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	target := filepath.Join(t.TempDir(), "target")
	runGit(t, "", "clone", repo, target)
	if err := ApplyWorkspaceSnapshot(context.Background(), target, bundle, snapshot); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(target, "tracked.txt"), "changed\n")
	assertFileContent(t, filepath.Join(target, "untracked.txt"), "new\n")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("%s: expected %q, got %q", path, expected, string(data))
	}
}
