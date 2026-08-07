package hermes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RepairRunner func(context.Context, RepairTask, QueueAction) (RepairExecution, error)
type RepairVerifier func(context.Context, RepairTask, QueueAction, string) (RepairValidation, error)

func CreateRepair(store Store, projectRoot string, actionID string, cfg AutoRepairConfig) (RepairTask, error) {
	release, err := store.AcquireGlobalLock()
	if err != nil {
		return RepairTask{}, err
	}
	defer release()
	action, err := findAction(store, actionID)
	if err != nil {
		return RepairTask{}, err
	}
	if action.Status == QueueStatusResolved || action.Status == QueueStatusDismissed {
		return RepairTask{}, fmt.Errorf("action %q is %s and cannot be repaired", action.ID, action.Status)
	}
	repairs, err := store.LoadRepairs()
	if err != nil {
		return RepairTask{}, err
	}
	for _, repair := range repairs.Repairs {
		if repair.ActionFingerprint == action.Fingerprint && repairActive(repair.Status) {
			return RepairTask{}, fmt.Errorf("action %q already has active repair %q", action.ID, repair.ID)
		}
	}
	baseCommit, err := runGit(projectRoot, "rev-parse", "HEAD")
	if err != nil {
		return RepairTask{}, fmt.Errorf("resolve repair base commit: %w", err)
	}
	now := time.Now().UTC()
	suffix := sanitizeFileID(firstNonEmptyString(action.CaseID, action.Category, "action"))
	id := fmt.Sprintf("repair_%s_%s", now.Format("20060102T150405.000000000"), suffix)
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	task := RepairTask{
		ID:                id,
		ActionID:          action.ID,
		ActionFingerprint: action.Fingerprint,
		Status:            RepairStatusQueued,
		Severity:          action.Severity,
		SuiteID:           action.SuiteID,
		CaseID:            action.CaseID,
		SourceRunID:       action.RunID,
		BaseCommit:        strings.TrimSpace(baseCommit),
		Branch:            "cohort/repair/" + strings.ReplaceAll(id, "_", "-"),
		WorktreePath:      filepath.Join(store.WorktreesDir(), id),
		ArtifactDir:       filepath.Join(store.RepairArtifactsDir(), id),
		MaxAttempts:       maxAttempts,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	repairs.Repairs = append(repairs.Repairs, task)
	if err := store.SaveRepairs(repairs); err != nil {
		return RepairTask{}, err
	}
	_ = store.AppendEvent(Event{Type: "repair_created", Severity: action.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func FindRepair(store Store, id string) (RepairTask, error) {
	repairs, err := store.LoadRepairs()
	if err != nil {
		return RepairTask{}, err
	}
	for _, repair := range repairs.Repairs {
		if repair.ID == id {
			return repair, nil
		}
	}
	return RepairTask{}, fmt.Errorf("repair %q not found", id)
}

func UpdateRepair(store Store, task RepairTask) error {
	release, err := store.AcquireGlobalLock()
	if err != nil {
		return err
	}
	defer release()
	repairs, err := store.LoadRepairs()
	if err != nil {
		return err
	}
	for i := range repairs.Repairs {
		if repairs.Repairs[i].ID == task.ID {
			task.UpdatedAt = time.Now().UTC()
			repairs.Repairs[i] = task
			return store.SaveRepairs(repairs)
		}
	}
	return fmt.Errorf("repair %q not found", task.ID)
}

func PrepareRepairWorktree(projectRoot string, task RepairTask) error {
	if strings.TrimSpace(task.WorktreePath) == "" || strings.TrimSpace(task.Branch) == "" || strings.TrimSpace(task.BaseCommit) == "" {
		return errors.New("repair worktree metadata is incomplete")
	}
	if info, err := os.Stat(task.WorktreePath); err == nil && info.IsDir() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(task.WorktreePath), 0755); err != nil {
		return err
	}
	output, err := runGit(projectRoot, "worktree", "add", "-b", task.Branch, task.WorktreePath, task.BaseCommit)
	if err != nil {
		return fmt.Errorf("create repair worktree: %w: %s", err, strings.TrimSpace(output))
	}
	return nil
}

// ResetRepairWorktree 清除失败尝试留下的所有 tracked、untracked 和 ignored 内容。
// Repair worktree 是 Hermes 独占的隔离目录，因此重试必须从固定 BaseCommit 开始，
// 防止上一轮被拒绝的文件逃逸到下一轮安全检查或验证结果中。
func ResetRepairWorktree(task RepairTask) error {
	if _, err := runGit(task.WorktreePath, "reset", "--hard", task.BaseCommit); err != nil {
		return fmt.Errorf("reset repair worktree: %w", err)
	}
	if _, err := runGit(task.WorktreePath, "clean", "-ffdx"); err != nil {
		return fmt.Errorf("clean repair worktree: %w", err)
	}
	return nil
}

// CleanupRepairRuntimeArtifacts 只清理隔离 worktree 中未跟踪/ignored 的 Eval 运行产物。
// git clean 不会删除 tracked 文件，因此任何对仓库内受保护文件的修改仍会在二次检查中被拒绝。
func CleanupRepairRuntimeArtifacts(task RepairTask) error {
	if _, err := runGit(task.WorktreePath, "clean", "-ffdx", "--", ".cohort/evals"); err != nil {
		return fmt.Errorf("clean repair verification artifacts: %w", err)
	}
	return nil
}

func InspectRepairWorktree(task RepairTask, cfg AutoRepairConfig) ([]string, string, error) {
	if _, err := runGit(task.WorktreePath, "add", "-N", "--", "."); err != nil {
		return nil, "", fmt.Errorf("prepare untracked files for diff: %w", err)
	}
	ignoredStatus, err := runGit(task.WorktreePath, "status", "--porcelain=v1", "-z", "--ignored=matching")
	if err != nil {
		return nil, "", err
	}
	for _, path := range parsePorcelainPaths(ignoredStatus) {
		if protectedRepairPath(path, cfg.ProtectedPaths) {
			return nil, "", fmt.Errorf("repair changed protected or ignored path %q", path)
		}
	}
	status, err := runGit(task.WorktreePath, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, "", err
	}
	files := parsePorcelainPaths(status)
	if len(files) == 0 {
		return nil, "", errors.New("repair produced no file changes")
	}
	maxFiles := cfg.MaxChangedFiles
	if maxFiles <= 0 {
		maxFiles = 12
	}
	if len(files) > maxFiles {
		return nil, "", fmt.Errorf("repair changed %d files, limit is %d", len(files), maxFiles)
	}
	for _, path := range files {
		if protectedRepairPath(path, cfg.ProtectedPaths) {
			return nil, "", fmt.Errorf("repair changed protected path %q", path)
		}
		info, statErr := os.Lstat(filepath.Join(task.WorktreePath, filepath.FromSlash(path)))
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("repair created or changed symlink %q", path)
		}
	}
	diff, err := runGit(task.WorktreePath, "diff", "--binary", "--no-ext-diff", "--")
	if err != nil {
		return nil, "", err
	}
	maxDiff := cfg.MaxDiffBytes
	if maxDiff <= 0 {
		maxDiff = 512 * 1024
	}
	if len(diff) > maxDiff {
		return nil, "", fmt.Errorf("repair diff is %d bytes, limit is %d", len(diff), maxDiff)
	}
	return files, diff, nil
}

func WriteRepairDiff(task RepairTask, diff string) (string, error) {
	if err := os.MkdirAll(task.ArtifactDir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(task.ArtifactDir, "diff.patch")
	if err := os.WriteFile(path, []byte(diff), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func CommitRepair(task RepairTask) (string, error) {
	if len(task.ChangedFiles) == 0 {
		return "", errors.New("repair has no changed files to commit")
	}
	args := append([]string{"add", "--"}, task.ChangedFiles...)
	if output, err := runGit(task.WorktreePath, args...); err != nil {
		return "", fmt.Errorf("stage repair files: %w: %s", err, strings.TrimSpace(output))
	}
	message := "cohort repair: " + task.ActionID
	if output, err := runGit(task.WorktreePath,
		"-c", "user.name=Cohort Repair Worker",
		"-c", "user.email=cohort-repair@localhost",
		"commit", "-m", message); err != nil {
		return "", fmt.Errorf("commit repair branch: %w: %s", err, strings.TrimSpace(output))
	}
	commit, err := runGit(task.WorktreePath, "rev-parse", "HEAD")
	return strings.TrimSpace(commit), err
}

func ApproveRepair(store Store, id string) (RepairTask, error) {
	task, err := FindRepair(store, id)
	if err != nil {
		return RepairTask{}, err
	}
	if task.Status != RepairStatusReadyForReview {
		return RepairTask{}, fmt.Errorf("repair %q must be ready_for_review, got %s", id, task.Status)
	}
	task.Status = RepairStatusApproved
	task.ApprovedAt = time.Now().UTC()
	if err := UpdateRepair(store, task); err != nil {
		return RepairTask{}, err
	}
	_ = store.AppendEvent(Event{Type: "repair_approved", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func RejectRepair(store Store, id string, reason string) (RepairTask, error) {
	task, err := FindRepair(store, id)
	if err != nil {
		return RepairTask{}, err
	}
	if task.Status != RepairStatusReadyForReview && task.Status != RepairStatusApproved {
		return RepairTask{}, fmt.Errorf("repair %q cannot be rejected from %s", id, task.Status)
	}
	task.Status = RepairStatusRejected
	task.RejectedAt = time.Now().UTC()
	task.Error = strings.TrimSpace(reason)
	if err := UpdateRepair(store, task); err != nil {
		return RepairTask{}, err
	}
	_ = store.AppendEvent(Event{Type: "repair_rejected", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func RemoveRepairWorktree(projectRoot string, task RepairTask) error {
	if strings.TrimSpace(task.WorktreePath) == "" {
		return removeRepairBranch(projectRoot, task.Branch, false)
	}
	if _, err := os.Stat(task.WorktreePath); err == nil {
		output, removeErr := runGit(projectRoot, "worktree", "remove", "--force", task.WorktreePath)
		if removeErr != nil {
			return fmt.Errorf("remove repair worktree: %w: %s", removeErr, strings.TrimSpace(output))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeRepairBranch(projectRoot, task.Branch, false)
}

func CancelRepair(store Store, projectRoot string, id string, reason string) (RepairTask, error) {
	release, err := store.AcquireRepairLock(id)
	if err != nil {
		return RepairTask{}, err
	}
	defer release()
	task, err := FindRepair(store, id)
	if err != nil {
		return RepairTask{}, err
	}
	switch task.Status {
	case RepairStatusQueued, RepairStatusFailed, RepairStatusReadyForReview, RepairStatusApproved, RepairStatusRejected:
	default:
		return RepairTask{}, fmt.Errorf("repair %q cannot be cancelled from %s", id, task.Status)
	}
	task.Status = RepairStatusCancelled
	task.Error = strings.TrimSpace(reason)
	task.FinishedAt = time.Now().UTC()
	if err := UpdateRepair(store, task); err != nil {
		return RepairTask{}, err
	}
	if cleanupErr := discardRepairWorktree(projectRoot, task); cleanupErr != nil {
		task.Error = firstNonEmptyString(task.Error, cleanupErr.Error())
		_ = UpdateRepair(store, task)
		return task, cleanupErr
	}
	_ = store.AppendEvent(Event{Type: "repair_cancelled", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func discardRepairWorktree(projectRoot string, task RepairTask) error {
	if strings.TrimSpace(task.WorktreePath) != "" {
		if _, err := os.Stat(task.WorktreePath); err == nil {
			output, removeErr := runGit(projectRoot, "worktree", "remove", "--force", task.WorktreePath)
			if removeErr != nil {
				return fmt.Errorf("discard repair worktree: %w: %s", removeErr, strings.TrimSpace(output))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return removeRepairBranch(projectRoot, task.Branch, true)
}

func removeRepairBranch(projectRoot string, branch string, force bool) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil
	}
	if _, err := runGit(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return nil
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	output, err := runGit(projectRoot, "branch", flag, branch)
	if err != nil {
		return fmt.Errorf("remove repair branch: %w: %s", err, strings.TrimSpace(output))
	}
	return nil
}

func findAction(store Store, id string) (QueueAction, error) {
	queue, err := store.LoadQueue()
	if err != nil {
		return QueueAction{}, err
	}
	for _, action := range queue.Actions {
		if action.ID == id || action.Fingerprint == id {
			return action, nil
		}
	}
	return QueueAction{}, fmt.Errorf("action %q not found", id)
}

func repairActive(status string) bool {
	switch status {
	case RepairStatusQueued, RepairStatusRunning, RepairStatusReadyForReview, RepairStatusApproved, RepairStatusMerging:
		return true
	default:
		return false
	}
}

func protectedRepairPath(path string, patterns []string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(filepath.Clean(strings.TrimSpace(pattern)))
		if pattern == "." || pattern == "" {
			continue
		}
		if clean == pattern || strings.HasPrefix(clean, strings.TrimSuffix(pattern, "/")+"/") {
			return true
		}
		if matched, _ := filepath.Match(pattern, clean); matched {
			return true
		}
	}
	return false
}

func parsePorcelainPaths(raw string) []string {
	entries := strings.Split(raw, "\x00")
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		path = filepath.ToSlash(path)
		if path != "" && !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		if entry[2] != ' ' {
			continue
		}
		add(entry[3:])
		if strings.ContainsAny(status, "RC") && index+1 < len(entries) {
			index++
			add(entries[index])
		}
	}
	sort.Strings(files)
	return files
}

func runGit(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func parseInt(value string) int {
	result, _ := strconv.Atoi(strings.TrimSpace(value))
	return result
}
