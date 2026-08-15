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

func TestWorkspaceSnapshotSkipsEngineArtifacts(t *testing.T) {
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

	// 用户工作区的真实未跟踪文件应进入快照。
	if err := os.WriteFile(filepath.Join(repo, "user.txt"), []byte("user\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// 引擎产物：普通文件 + 非普通文件（符号链接），均位于 .cohort/ 下。
	// 修复前，符号链接会触发 "unsupported untracked file type" 让整次快照失败。
	worktree := filepath.Join(repo, ".cohort", "replay-worktrees", "fork-1")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "trial.txt"), []byte("engine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, filepath.Join(repo, ".cohort", "nested-link")); err != nil {
		t.Fatal(err)
	}

	baseline := inspectGitBaseline(repo)
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := captureWorkspaceSnapshot(bundle, baseline)
	if snapshot.Error != "" {
		t.Fatalf("engine artifacts must not fail snapshot: %s", snapshot.Error)
	}
	if !snapshot.Available {
		t.Fatalf("snapshot should be available: %+v", snapshot)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0].Path != "user.txt" {
		t.Fatalf("only user workspace files should be captured, got %+v", snapshot.Untracked)
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
