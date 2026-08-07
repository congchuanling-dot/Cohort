package hermes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RecoverInterruptedRepairs 在 daemon 启动时收敛上次进程异常退出留下的状态。
// 存活的 repair lock 代表另一个 CLI worker 正在运行，绝不抢占；stale lock 会由 Store 清理。
func (s *Service) RecoverInterruptedRepairs() error {
	release, err := s.Store.AcquireGlobalLock()
	if err != nil {
		return err
	}
	repairs, err := s.Store.LoadRepairs()
	if err != nil {
		release()
		return err
	}
	changed := false
	var merged []RepairTask
	for index := range repairs.Repairs {
		task := &repairs.Repairs[index]
		if task.Status != RepairStatusRunning && task.Status != RepairStatusMerging {
			continue
		}
		active, lockErr := s.Store.RepairLockActive(task.ID)
		if lockErr != nil {
			release()
			return lockErr
		}
		if active {
			continue
		}
		if task.Status == RepairStatusMerging {
			if task.RepairCommit != "" {
				if _, ancestorErr := runGit(s.ProjectRoot, "merge-base", "--is-ancestor", task.RepairCommit, "HEAD"); ancestorErr == nil {
					task.Status = RepairStatusMerged
					task.MergeCommit, _ = runGit(s.ProjectRoot, "rev-parse", "HEAD")
					task.MergeCommit = strings.TrimSpace(task.MergeCommit)
					task.MergedAt = time.Now().UTC()
					task.FinishedAt = task.MergedAt
					task.Error = "recovered committed repair merge"
					task.UpdatedAt = task.MergedAt
					merged = append(merged, *task)
					changed = true
					continue
				}
			}
			if _, mergeErr := runGit(s.ProjectRoot, "rev-parse", "-q", "--verify", "MERGE_HEAD"); mergeErr == nil {
				if output, abortErr := runGit(s.ProjectRoot, "merge", "--abort"); abortErr != nil {
					release()
					return fmt.Errorf("abort interrupted repair merge: %w: %s", abortErr, strings.TrimSpace(output))
				}
			}
			if task.ApprovedAt.IsZero() {
				task.Status = RepairStatusReadyForReview
			} else {
				task.Status = RepairStatusApproved
			}
			task.Error = "recovered interrupted merge; main worktree merge was aborted"
		} else {
			task.Status = RepairStatusFailed
			task.Error = "recovered interrupted repair worker"
			task.FinishedAt = time.Now().UTC()
		}
		task.UpdatedAt = time.Now().UTC()
		changed = true
	}
	if !changed {
		release()
		return nil
	}
	if err := s.Store.SaveRepairs(repairs); err != nil {
		release()
		return err
	}
	release()
	for _, task := range merged {
		if task.VerificationRunID == "" {
			continue
		}
		if _, verifyErr := VerifyActionWithRun(s.Store, s.EvalStore, task.ActionID, task.VerificationRunID, true); verifyErr != nil {
			task.Error = "recovered merge but action resolution failed: " + verifyErr.Error()
			_ = UpdateRepair(s.Store, task)
			continue
		}
		task.Error = ""
		_ = UpdateRepair(s.Store, task)
		if cleanupErr := RemoveRepairWorktree(s.ProjectRoot, task); cleanupErr != nil {
			s.recordError(cleanupErr)
		}
	}
	return nil
}

func (s *Service) RunRepair(ctx context.Context, repairID string) (RepairTask, error) {
	if s.RepairRunner == nil {
		return RepairTask{}, errors.New("hermes repair runner is not configured")
	}
	if s.RepairVerifier == nil {
		return RepairTask{}, errors.New("hermes repair verifier is not configured")
	}
	release, err := s.Store.AcquireRepairLock(repairID)
	if err != nil {
		return RepairTask{}, err
	}
	defer release()
	task, err := FindRepair(s.Store, repairID)
	if err != nil {
		return RepairTask{}, err
	}
	if task.Status != RepairStatusQueued && task.Status != RepairStatusFailed {
		return RepairTask{}, fmt.Errorf("repair %q cannot run from status %s", task.ID, task.Status)
	}
	if task.Attempt >= task.MaxAttempts {
		return RepairTask{}, fmt.Errorf("repair %q exhausted %d attempts", task.ID, task.MaxAttempts)
	}
	action, err := findAction(s.Store, task.ActionID)
	if err != nil {
		return RepairTask{}, err
	}
	cfg, err := s.currentAutoRepairConfig()
	if err != nil {
		return RepairTask{}, err
	}
	s.setRepairRunning(task.ID, true)
	defer s.setRepairRunning(task.ID, false)
	task.Status = RepairStatusRunning
	task.Attempt++
	task.StartedAt = time.Now().UTC()
	task.FinishedAt = time.Time{}
	task.Error = ""
	if err := UpdateRepair(s.Store, task); err != nil {
		return RepairTask{}, err
	}
	_ = UpdateActionStatusNoError(s.Store, action.ID, QueueStatusInProgress)
	_ = s.Store.AppendEvent(Event{Type: "repair_started", Severity: task.Severity, SourceID: task.ID, Data: task})

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := PrepareRepairWorktree(s.ProjectRoot, task); err != nil {
		return s.failRepair(task, err)
	}
	if task.Attempt > 1 {
		if err := ResetRepairWorktree(task); err != nil {
			return s.failRepair(task, err)
		}
	}
	execution, err := s.RepairRunner(runCtx, task, action)
	task.Execution = execution
	if err != nil {
		return s.failRepair(task, fmt.Errorf("repair agent: %w", err))
	}
	files, diff, err := InspectRepairWorktree(task, cfg)
	if err != nil {
		return s.failRepair(task, err)
	}
	task.ChangedFiles = files
	task.DiffBytes = len(diff)
	task.DiffPath, err = WriteRepairDiff(task, diff)
	if err != nil {
		return s.failRepair(task, err)
	}
	validation, verifyErr := s.RepairVerifier(runCtx, task, action, "worktree")
	task.Validation = validation
	if verifyErr != nil {
		return s.failRepair(task, fmt.Errorf("repair verification: %w", verifyErr))
	}
	if !validation.Passed || !validation.GatePassed {
		return s.failRepair(task, errors.New("repair verification did not pass"))
	}
	if err := CleanupRepairRuntimeArtifacts(task); err != nil {
		return s.failRepair(task, err)
	}
	// Verifier 本身会执行项目脚本和 Eval；提交前重新检查，消除验证期间生成或篡改文件的 TOCTOU。
	files, diff, err = InspectRepairWorktree(task, cfg)
	if err != nil {
		return s.failRepair(task, fmt.Errorf("post-verification diff inspection: %w", err))
	}
	task.ChangedFiles = files
	task.DiffBytes = len(diff)
	task.DiffPath, err = WriteRepairDiff(task, diff)
	if err != nil {
		return s.failRepair(task, err)
	}
	task.RepairCommit, err = CommitRepair(task)
	if err != nil {
		return s.failRepair(task, err)
	}
	task.Status = RepairStatusReadyForReview
	task.Summary = firstNonEmptyString(execution.Summary, "repair completed and passed validation")
	task.FinishedAt = time.Now().UTC()
	task.Error = ""
	if err := UpdateRepair(s.Store, task); err != nil {
		return task, err
	}
	s.markRepairFinished()
	_ = s.Store.AppendEvent(Event{Type: "repair_ready_for_review", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func (s *Service) MergeRepair(ctx context.Context, repairID string) (RepairTask, error) {
	if s.RepairVerifier == nil {
		return RepairTask{}, errors.New("hermes repair verifier is not configured")
	}
	release, err := s.Store.AcquireRepairLock(repairID)
	if err != nil {
		return RepairTask{}, err
	}
	defer release()
	task, err := FindRepair(s.Store, repairID)
	if err != nil {
		return RepairTask{}, err
	}
	cfg, err := s.currentAutoRepairConfig()
	if err != nil {
		return RepairTask{}, err
	}
	if cfg.RequireApproval && task.Status != RepairStatusApproved {
		return RepairTask{}, fmt.Errorf("repair %q requires approval before merge", task.ID)
	}
	if !cfg.RequireApproval && task.Status != RepairStatusReadyForReview && task.Status != RepairStatusApproved {
		return RepairTask{}, fmt.Errorf("repair %q cannot merge from status %s", task.ID, task.Status)
	}
	if dirty, err := unsafeProjectChanges(s.ProjectRoot); err != nil {
		return RepairTask{}, err
	} else if len(dirty) > 0 {
		return RepairTask{}, fmt.Errorf("main worktree has unrelated changes: %s", strings.Join(dirty, ", "))
	}
	unstagedBefore, err := runGit(s.ProjectRoot, "diff", "--binary", "--no-ext-diff", "--")
	if err != nil {
		return RepairTask{}, err
	}
	action, err := findAction(s.Store, task.ActionID)
	if err != nil {
		return RepairTask{}, err
	}
	task.Status = RepairStatusMerging
	if err := UpdateRepair(s.Store, task); err != nil {
		return RepairTask{}, err
	}
	output, mergeErr := runGit(s.ProjectRoot, "merge", "--no-ff", "--no-commit", task.Branch)
	if mergeErr != nil {
		task.Status = RepairStatusFailed
		task.Error = "merge repair branch: " + strings.TrimSpace(output)
		_ = UpdateRepair(s.Store, task)
		return task, fmt.Errorf("merge repair branch: %w: %s", mergeErr, strings.TrimSpace(output))
	}
	stagedBefore, err := runGit(s.ProjectRoot, "diff", "--cached", "--binary", "--no-ext-diff", "--")
	if err != nil {
		_, _ = runGit(s.ProjectRoot, "merge", "--abort")
		return task, err
	}
	stagedNamesBefore, err := runGit(s.ProjectRoot, "diff", "--cached", "--name-only", "-z", "--")
	if err != nil {
		_, _ = runGit(s.ProjectRoot, "merge", "--abort")
		return task, err
	}
	mergedTask := task
	mergedTask.WorktreePath = s.ProjectRoot
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	validation, verifyErr := s.RepairVerifier(verifyCtx, mergedTask, action, "merge")
	task.MergeValidation = validation
	if verifyErr == nil && validation.Passed && validation.GatePassed {
		verifyErr = verifyMergeIntegrity(s.ProjectRoot, stagedBefore, stagedNamesBefore, unstagedBefore)
	}
	if verifyErr != nil || !validation.Passed || !validation.GatePassed || len(validation.EvalRunIDs) == 0 {
		_, _ = runGit(s.ProjectRoot, "merge", "--abort")
		task.Status = RepairStatusApproved
		task.Error = firstNonEmptyString(errorText(verifyErr), validation.Error, "merged repair verification failed")
		_ = UpdateRepair(s.Store, task)
		return task, errors.New(task.Error)
	}
	task.VerificationRunID = validation.EvalRunIDs[len(validation.EvalRunIDs)-1]
	if err := UpdateRepair(s.Store, task); err != nil {
		_, _ = runGit(s.ProjectRoot, "merge", "--abort")
		task.Status = RepairStatusApproved
		task.Error = "persist merge validation: " + err.Error()
		_ = UpdateRepair(s.Store, task)
		return task, errors.New(task.Error)
	}
	message := "merge cohort repair " + task.ID
	output, err = runGit(s.ProjectRoot,
		"-c", "user.name=Cohort Repair Worker",
		"-c", "user.email=cohort-repair@localhost",
		"commit", "-m", message)
	if err != nil {
		_, _ = runGit(s.ProjectRoot, "merge", "--abort")
		task.Status = RepairStatusApproved
		task.Error = "commit repair merge: " + strings.TrimSpace(output)
		_ = UpdateRepair(s.Store, task)
		return task, fmt.Errorf("commit repair merge: %w: %s", err, strings.TrimSpace(output))
	}
	task.MergeCommit, err = runGit(s.ProjectRoot, "rev-parse", "HEAD")
	if err != nil {
		return task, err
	}
	task.MergeCommit = strings.TrimSpace(task.MergeCommit)
	if _, err := VerifyActionWithRun(s.Store, s.EvalStore, task.ActionID, task.VerificationRunID, true); err != nil {
		task.Status = RepairStatusMerged
		task.MergedAt = time.Now().UTC()
		task.Error = "merged but action resolution failed: " + err.Error()
		_ = UpdateRepair(s.Store, task)
		return task, errors.New(task.Error)
	}
	task.Status = RepairStatusMerged
	task.MergedAt = time.Now().UTC()
	task.FinishedAt = task.MergedAt
	task.Error = ""
	if err := UpdateRepair(s.Store, task); err != nil {
		return task, err
	}
	_ = RemoveRepairWorktree(s.ProjectRoot, task)
	s.markRepairFinished()
	_ = s.Store.AppendEvent(Event{Type: "repair_merged", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, nil
}

func (s *Service) DispatchRepairs(ctx context.Context) {
	cfg, err := s.currentAutoRepairConfig()
	if err != nil {
		s.recordError(err)
		return
	}
	if !cfg.Enabled || s.RepairRunner == nil || s.RepairVerifier == nil {
		return
	}
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	s.mu.Lock()
	available := maxConcurrent - len(s.runningRepairs)
	s.mu.Unlock()
	if available <= 0 {
		return
	}
	repairs, err := s.Store.LoadRepairs()
	if err != nil {
		s.recordError(err)
		return
	}
	now := time.Now().UTC()
	for _, task := range repairs.Repairs {
		if available <= 0 {
			return
		}
		eligible := task.Status == RepairStatusQueued ||
			(task.Status == RepairStatusFailed && task.Attempt < task.MaxAttempts && now.Sub(task.UpdatedAt) >= time.Minute)
		if !eligible || !s.claimRepair(task.ID) {
			continue
		}
		available--
		repairID := task.ID
		go func() {
			defer s.releaseRepairClaim(repairID)
			if _, err := s.RunRepair(ctx, repairID); err != nil {
				s.recordError(fmt.Errorf("repair %s: %w", repairID, err))
			}
		}()
	}
	if available <= 0 {
		return
	}
	queue, err := s.Store.LoadQueue()
	if err != nil {
		s.recordError(err)
		return
	}
	activeFingerprints := map[string]bool{}
	for _, task := range repairs.Repairs {
		if repairActive(task.Status) || (task.Status == RepairStatusFailed && task.Attempt < task.MaxAttempts) {
			activeFingerprints[task.ActionFingerprint] = true
		}
	}
	for _, action := range queue.Actions {
		if available <= 0 {
			break
		}
		if action.Status == QueueStatusResolved || action.Status == QueueStatusDismissed ||
			severityRank(action.Severity) < severityRank(cfg.MinSeverity) ||
			activeFingerprints[action.Fingerprint] {
			continue
		}
		task, createErr := CreateRepair(s.Store, s.ProjectRoot, action.ID, cfg)
		if createErr != nil {
			s.recordError(createErr)
			continue
		}
		activeFingerprints[action.Fingerprint] = true
		available--
		if !s.claimRepair(task.ID) {
			continue
		}
		go func(repairID string) {
			defer s.releaseRepairClaim(repairID)
			if _, err := s.RunRepair(ctx, repairID); err != nil {
				s.recordError(fmt.Errorf("repair %s: %w", repairID, err))
			}
		}(task.ID)
	}
}

func (s *Service) currentAutoRepairConfig() (AutoRepairConfig, error) {
	config, err := s.Store.LoadConfig()
	if err != nil {
		return AutoRepairConfig{}, fmt.Errorf("load auto repair config: %w", err)
	}
	return config.AutoRepair, nil
}

func (s *Service) claimRepair(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runningRepairs[id] {
		return false
	}
	s.runningRepairs[id] = true
	status, _ := s.Store.LoadStatus()
	status.RunningRepairs = s.runningRepairIDsLocked()
	_ = s.Store.SaveStatus(status)
	return true
}

func (s *Service) releaseRepairClaim(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.runningRepairs[id] {
		return
	}
	delete(s.runningRepairs, id)
	status, _ := s.Store.LoadStatus()
	status.RunningRepairs = s.runningRepairIDsLocked()
	_ = s.Store.SaveStatus(status)
}

func (s *Service) failRepair(task RepairTask, err error) (RepairTask, error) {
	task.Status = RepairStatusFailed
	task.Error = err.Error()
	task.FinishedAt = time.Now().UTC()
	_ = UpdateRepair(s.Store, task)
	s.markRepairFinished()
	_ = s.Store.AppendEvent(Event{Type: "repair_failed", Severity: task.Severity, SourceID: task.ID, Data: task})
	return task, err
}

func (s *Service) setRepairRunning(id string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		s.runningRepairs[id] = true
	} else {
		delete(s.runningRepairs, id)
	}
	status, _ := s.Store.LoadStatus()
	status.RunningRepairs = s.runningRepairIDsLocked()
	_ = s.Store.SaveStatus(status)
}

func (s *Service) repairRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runningRepairs[id]
}

func (s *Service) runningRepairIDsLocked() []string {
	ids := make([]string, 0, len(s.runningRepairs))
	for id := range s.runningRepairs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) markRepairFinished() {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, _ := s.Store.LoadStatus()
	status.LastRepairAt = time.Now().UTC()
	status.RunningRepairs = s.runningRepairIDsLocked()
	_ = s.Store.SaveStatus(status)
}

func unsafeProjectChanges(projectRoot string) ([]string, error) {
	raw, err := runGit(projectRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var unsafe []string
	for _, path := range parsePorcelainPaths(raw) {
		clean := filepath.ToSlash(path)
		if strings.HasPrefix(clean, ".cohort/hermes/") || strings.HasPrefix(clean, ".cohort/evals/stability/") {
			continue
		}
		unsafe = append(unsafe, clean)
	}
	sort.Strings(unsafe)
	return unsafe, nil
}

// verifyMergeIntegrity 保证验证器测试的 staged 内容就是随后提交的内容。
// Eval 产物允许写入 .cohort/evals；任何 tracked 修改、额外源码或 staged diff 变化都中止合并。
func verifyMergeIntegrity(projectRoot string, stagedBefore string, stagedNamesBefore string, unstagedBefore string) error {
	stagedAfter, err := runGit(projectRoot, "diff", "--cached", "--binary", "--no-ext-diff", "--")
	if err != nil {
		return err
	}
	if stagedAfter != stagedBefore {
		return errors.New("merge verifier changed the staged repair diff")
	}
	unstaged, err := runGit(projectRoot, "diff", "--binary", "--no-ext-diff", "--")
	if err != nil {
		return err
	}
	if unstaged != unstagedBefore {
		return errors.New("merge verifier changed the pre-existing unstaged diff")
	}
	raw, err := runGit(projectRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	stagedPaths := map[string]bool{}
	for _, path := range strings.Split(stagedNamesBefore, "\x00") {
		if path != "" {
			stagedPaths[filepath.ToSlash(path)] = true
		}
	}
	var unexpected []string
	for _, path := range parsePorcelainPaths(raw) {
		clean := filepath.ToSlash(path)
		if strings.HasPrefix(clean, ".cohort/hermes/") || strings.HasPrefix(clean, ".cohort/evals/") {
			continue
		}
		// 当前 merge 自身的 staged 文件已由 staged diff 对比覆盖。
		if stagedPaths[clean] {
			continue
		}
		unexpected = append(unexpected, clean)
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return fmt.Errorf("merge verifier created unexpected files: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

func UpdateActionStatusNoError(store Store, id string, status string) error {
	_, err := UpdateActionStatus(store, id, status)
	return err
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func RepairArtifactPath(task RepairTask, name string) string {
	return filepath.Join(task.ArtifactDir, name)
}

func EnsureRepairArtifactDir(task RepairTask) error {
	return os.MkdirAll(task.ArtifactDir, 0755)
}
