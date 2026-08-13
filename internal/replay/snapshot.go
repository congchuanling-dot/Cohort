package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	snapshotPatchName = "workspace.patch"
	snapshotFilesDir  = "workspace-files"
	maxSnapshotBytes  = 20 << 20
	maxSnapshotFile   = 5 << 20
)

func captureWorkspaceSnapshot(bundleDir string, git GitBaseline) WorkspaceSnapshot {
	if !git.Available {
		return WorkspaceSnapshot{Error: "git baseline unavailable"}
	}
	snapshot := WorkspaceSnapshot{Available: true}
	if !git.Dirty {
		return snapshot
	}
	run := func(args ...string) ([]byte, error) {
		command := exec.Command("git", append([]string{"-C", git.Root}, args...)...)
		var output bytes.Buffer
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
		}
		return output.Bytes(), nil
	}
	patch, err := run("diff", "--binary", "--no-ext-diff", "HEAD", "--")
	if err != nil {
		return WorkspaceSnapshot{Error: err.Error()}
	}
	if len(patch) > maxSnapshotBytes {
		return WorkspaceSnapshot{Error: "tracked diff exceeds 20 MiB snapshot limit"}
	}
	if len(patch) > 0 {
		patchPath := filepath.Join(bundleDir, snapshotPatchName)
		if err := os.WriteFile(patchPath, patch, 0600); err != nil {
			return WorkspaceSnapshot{Error: err.Error()}
		}
		snapshot.PatchFile = snapshotPatchName
		snapshot.PatchHash = StableHash(patch)
		snapshot.TotalBytes += int64(len(patch))
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return WorkspaceSnapshot{Error: err.Error()}
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		relative := filepath.Clean(strings.TrimSpace(string(raw)))
		if relative == "." || relative == "" {
			continue
		}
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return WorkspaceSnapshot{Error: "untracked path escapes repository: " + relative}
		}
		source := filepath.Join(git.Root, relative)
		info, err := os.Lstat(source)
		if err != nil {
			return WorkspaceSnapshot{Error: err.Error()}
		}
		if !info.Mode().IsRegular() {
			return WorkspaceSnapshot{Error: "unsupported untracked file type: " + relative}
		}
		if info.Size() > maxSnapshotFile || snapshot.TotalBytes+info.Size() > maxSnapshotBytes {
			return WorkspaceSnapshot{Error: "untracked content exceeds snapshot limit"}
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return WorkspaceSnapshot{Error: err.Error()}
		}
		destination := filepath.Join(bundleDir, snapshotFilesDir, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return WorkspaceSnapshot{Error: err.Error()}
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			return WorkspaceSnapshot{Error: err.Error()}
		}
		snapshot.Untracked = append(snapshot.Untracked, SnapshotFile{
			Path: filepath.ToSlash(relative),
			Hash: StableHash(data),
			Size: info.Size(),
		})
		snapshot.TotalBytes += info.Size()
	}
	return snapshot
}

func ApplyWorkspaceSnapshot(ctx context.Context, worktree string, bundleDir string, snapshot WorkspaceSnapshot) error {
	if !snapshot.Available {
		return errors.New("workspace snapshot is unavailable")
	}
	if snapshot.PatchFile != "" {
		patchPath := filepath.Join(bundleDir, snapshot.PatchFile)
		data, err := os.ReadFile(patchPath)
		if err != nil {
			return err
		}
		if StableHash(data) != snapshot.PatchHash {
			return errors.New("workspace patch hash mismatch")
		}
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(runCtx, "git", "apply", "--binary", "--whitespace=nowarn", patchPath)
		command.Dir = worktree
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("apply workspace patch: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	for _, file := range snapshot.Untracked {
		relative := filepath.Clean(file.Path)
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("snapshot path escapes worktree: " + file.Path)
		}
		source := filepath.Join(bundleDir, snapshotFilesDir, relative)
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if int64(len(data)) != file.Size || StableHash(data) != file.Hash {
			return errors.New("snapshot file hash mismatch: " + file.Path)
		}
		destination := filepath.Join(worktree, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
