package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Manager struct {
	ProjectRoot string
	RootDir     string
}

type Spec struct {
	ID         string
	BaseCommit string
	Branch     string
	Path       string
}

type Inspection struct {
	Files     []string
	Diff      []byte
	DiffBytes int
	TreeHash  string
}

func NewManager(projectRoot string, rootDir string) (Manager, error) {
	projectRoot, err := filepath.Abs(strings.TrimSpace(projectRoot))
	if err != nil {
		return Manager{}, err
	}
	if strings.TrimSpace(rootDir) == "" {
		return Manager{}, errors.New("worktree root directory is required")
	}
	rootDir, err = filepath.Abs(rootDir)
	if err != nil {
		return Manager{}, err
	}
	return Manager{ProjectRoot: filepath.Clean(projectRoot), RootDir: filepath.Clean(rootDir)}, nil
}

func (m Manager) Prepare(ctx context.Context, spec Spec) error {
	if err := m.validateSpec(spec); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(spec.Path); err == nil {
		if removeErr := m.Remove(ctx, spec); removeErr != nil {
			return removeErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = m.git(ctx, m.ProjectRoot, "branch", "-D", spec.Branch)
	output, err := m.git(ctx, m.ProjectRoot, "worktree", "add", "--detach", spec.Path, spec.BaseCommit)
	if err != nil {
		return fmt.Errorf("create worktree: %w: %s", err, output)
	}
	output, err = m.git(ctx, spec.Path, "switch", "-c", spec.Branch)
	if err != nil {
		_ = m.Remove(ctx, spec)
		return fmt.Errorf("create worktree branch: %w: %s", err, output)
	}
	return nil
}

func (m Manager) Inspect(ctx context.Context, spec Spec) (Inspection, error) {
	if err := m.validateSpec(spec); err != nil {
		return Inspection{}, err
	}
	if _, err := os.Stat(spec.Path); err != nil {
		return Inspection{}, err
	}
	if output, err := m.git(ctx, spec.Path, "add", "--all", "--"); err != nil {
		return Inspection{}, fmt.Errorf("stage worktree for inspection: %w: %s", err, output)
	}
	names, err := m.gitBytes(ctx, spec.Path, "diff", "--cached", "--name-only", "-z", "--")
	if err != nil {
		return Inspection{}, err
	}
	files := splitNUL(names)
	for index := range files {
		files[index] = filepath.ToSlash(files[index])
	}
	sort.Strings(files)
	diff, err := m.gitBytes(ctx, spec.Path, "diff", "--cached", "--binary", "--no-ext-diff", "--")
	if err != nil {
		return Inspection{}, err
	}
	treeHash, err := m.git(ctx, spec.Path, "write-tree")
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{
		Files:     files,
		Diff:      diff,
		DiffBytes: len(diff),
		TreeHash:  strings.TrimSpace(treeHash),
	}, nil
}

func (m Manager) MergeCommits(ctx context.Context, spec Spec, commits []string) error {
	if err := m.validateSpec(spec); err != nil {
		return err
	}
	for _, commit := range commits {
		commit = strings.TrimSpace(commit)
		if commit == "" {
			return errors.New("dependency commit is empty")
		}
		output, err := m.git(
			ctx,
			spec.Path,
			"-c", "user.name=Cohort Delivery Worker",
			"-c", "user.email=cohort-delivery@localhost",
			"merge", "--no-edit", commit,
		)
		if err != nil {
			_, _ = m.git(ctx, spec.Path, "merge", "--abort")
			return fmt.Errorf("merge dependency commit %s: %w: %s", commit, err, output)
		}
	}
	return nil
}

func (m Manager) Head(ctx context.Context, spec Spec) (string, error) {
	if err := m.validateSpec(spec); err != nil {
		return "", err
	}
	head, err := m.git(ctx, spec.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(head), nil
}

func (m Manager) Commit(ctx context.Context, spec Spec, message string) (string, error) {
	inspection, err := m.Inspect(ctx, spec)
	if err != nil {
		return "", err
	}
	if len(inspection.Files) == 0 {
		return "", errors.New("worktree has no changes to commit")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "cohort delivery " + spec.ID
	}
	output, err := m.git(
		ctx,
		spec.Path,
		"-c", "user.name=Cohort Delivery Worker",
		"-c", "user.email=cohort-delivery@localhost",
		"commit", "-m", message,
	)
	if err != nil {
		return "", fmt.Errorf("commit worktree: %w: %s", err, output)
	}
	commit, err := m.git(ctx, spec.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

func (m Manager) Remove(ctx context.Context, spec Spec) error {
	if err := m.validateSpec(spec); err != nil {
		return err
	}
	if _, err := os.Stat(spec.Path); errors.Is(err, os.ErrNotExist) {
		_, _ = m.git(ctx, m.ProjectRoot, "worktree", "prune")
		return nil
	}
	output, err := m.git(ctx, m.ProjectRoot, "worktree", "remove", "--force", spec.Path)
	if err != nil {
		return fmt.Errorf("remove worktree: %w: %s", err, output)
	}
	return nil
}

func (m Manager) validateSpec(spec Spec) error {
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.BaseCommit) == "" || strings.TrimSpace(spec.Branch) == "" {
		return errors.New("worktree id, base commit, and branch are required")
	}
	path, err := filepath.Abs(strings.TrimSpace(spec.Path))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(m.RootDir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("worktree path escapes configured root")
	}
	if strings.ContainsAny(spec.Branch, " ~^:?*[\\") || strings.HasPrefix(spec.Branch, "-") || strings.Contains(spec.Branch, "..") {
		return errors.New("invalid worktree branch")
	}
	return nil
}

func (m Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	data, err := m.gitBytes(ctx, dir, args...)
	return string(data), err
}

func (m Manager) gitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(runCtx, "git", args...)
	command.Dir = dir
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.Bytes(), nil
}

func splitNUL(data []byte) []string {
	var values []string
	for _, item := range bytes.Split(data, []byte{0}) {
		value := strings.TrimSpace(string(item))
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
