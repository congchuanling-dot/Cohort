package hermes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cohort/internal/evaluation"
)

func TestDispatchRepairsClaimsTaskBeforeStartingGoroutine_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.AutoRepair.Enabled = true
	service.Config.AutoRepair.MaxConcurrent = 1
	service.Config.AutoRepair.TestCommands = nil
	config, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.AutoRepair = service.Config.AutoRepair
	if err := store.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return RepairExecution{}, os.WriteFile(filepath.Join(task.WorktreePath, "bug.go"), []byte("package sample\n\nfunc Value() int { return 2 }\n"), 0644)
	}
	service.RepairVerifier = func(context.Context, RepairTask, QueueAction, string) (RepairValidation, error) {
		return RepairValidation{Passed: true, GatePassed: true, EvalRunIDs: []string{"run"}}, nil
	}
	service.DispatchRepairs(context.Background())
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("repair did not start")
	}
	service.DispatchRepairs(context.Background())
	close(release)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if calls.Load() != 1 {
			t.Fatalf("runner calls=%d", calls.Load())
		}
		repairs, loadErr := store.LoadRepairs()
		if loadErr == nil && len(repairs.Repairs) == 1 && repairs.Repairs[0].Status == RepairStatusReadyForReview && !service.repairRunning(task.ID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("repair did not finish")
}

func TestRepairWorkerUsesIsolatedWorktreeAndStopsForReview_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.AutoRepair.TestCommands = nil
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		if task.WorktreePath == root {
			t.Fatal("repair runner received main worktree")
		}
		if err := os.WriteFile(filepath.Join(task.WorktreePath, "bug.go"), []byte("package sample\n\nfunc Value() int { return 2 }\n"), 0644); err != nil {
			return RepairExecution{}, err
		}
		return RepairExecution{Summary: "fixed Value"}, nil
	}
	service.RepairVerifier = func(ctx context.Context, task RepairTask, action QueueAction, phase string) (RepairValidation, error) {
		return RepairValidation{
			Passed: true, GatePassed: true,
			EvalRunIDs: []string{"eval-verify"},
			StartedAt:  time.Now().UTC(), FinishedAt: time.Now().UTC(),
		}, nil
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair); err == nil {
		t.Fatal("duplicate active repair unexpectedly created")
	}
	task, err = service.RunRepair(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != RepairStatusReadyForReview || task.RepairCommit == "" || len(task.ChangedFiles) != 1 || task.ChangedFiles[0] != "bug.go" {
		t.Fatalf("task=%#v", task)
	}
	mainData, err := os.ReadFile(filepath.Join(root, "bug.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainData), "return 2") {
		t.Fatal("repair modified main worktree before approval")
	}
	if _, err := service.MergeRepair(context.Background(), task.ID); err == nil {
		t.Fatal("unapproved repair unexpectedly merged")
	}
}

func TestRepairWorkerRejectsProtectedPath_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		return RepairExecution{}, os.WriteFile(filepath.Join(task.WorktreePath, ".env"), []byte("SECRET=bad"), 0644)
	}
	service.RepairVerifier = func(context.Context, RepairTask, QueueAction, string) (RepairValidation, error) {
		t.Fatal("verifier must not run for protected diff")
		return RepairValidation{}, nil
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.RunRepair(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "protected") || task.Status != RepairStatusFailed {
		t.Fatalf("task=%#v err=%v", task, err)
	}
}

func TestParsePorcelainPathsIncludesRenameEndpointsAndSpaces_BitsUT(t *testing.T) {
	paths := parsePorcelainPaths("R  safe.go\x00.env\x00??  leading and trailing \x00")
	want := []string{" leading and trailing ", ".env", "safe.go"}
	if len(paths) != len(want) {
		t.Fatalf("paths=%q", paths)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("paths=%q want=%q", paths, want)
		}
	}
}

func TestVerifyMergeIntegrityPreservesExistingStabilityDiff_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	path := filepath.Join(root, ".cohort", "evals", "stability", "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("baseline\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runRepairGit(t, root, "add", path)
	runRepairGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "stability baseline")
	if err := os.WriteFile(path, []byte("existing generated report\n"), 0644); err != nil {
		t.Fatal(err)
	}
	unstaged, err := runGit(root, "diff", "--binary", "--no-ext-diff", "--")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMergeIntegrity(root, "", "", unstaged); err != nil {
		t.Fatalf("unchanged existing diff rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("verifier changed report\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyMergeIntegrity(root, "", "", unstaged); err == nil {
		t.Fatal("verifier stability mutation unexpectedly accepted")
	}
}

func TestApprovedRepairMergesThenResolvesAction_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.AutoRepair.TestCommands = nil
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		return RepairExecution{}, os.WriteFile(filepath.Join(task.WorktreePath, "bug.go"), []byte("package sample\n\nfunc Value() int { return 2 }\n"), 0644)
	}
	service.RepairVerifier = func(ctx context.Context, task RepairTask, action QueueAction, phase string) (RepairValidation, error) {
		result := evaluation.RunResult{
			RunID: "verify-" + phase, SuiteID: action.SuiteID, StartedAt: time.Now().UTC().Add(time.Second),
			Gate:  &evaluation.GateResult{Passed: true},
			Cases: []evaluation.CaseResult{{CaseID: action.CaseID, Passed: true}},
		}
		phaseStore := evaluation.NewStore(task.WorktreePath)
		if _, err := phaseStore.SaveResult(result); err != nil {
			return RepairValidation{}, err
		}
		return RepairValidation{Passed: true, GatePassed: true, EvalRunIDs: []string{result.RunID}}, nil
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.RunRepair(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = ApproveRepair(store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.MergeRepair(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != RepairStatusMerged || task.MergeCommit == "" || task.VerificationRunID != "verify-merge" {
		t.Fatalf("task=%#v", task)
	}
	data, err := os.ReadFile(filepath.Join(root, "bug.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "return 2") {
		t.Fatalf("main data=%s", data)
	}
	resolved, err := findAction(store, action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != QueueStatusResolved || resolved.ResolvedFromRun != "verify-merge" {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestMergeValidationFailureAbortsMainWorktree_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.AutoRepair.TestCommands = nil
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		return RepairExecution{}, os.WriteFile(filepath.Join(task.WorktreePath, "bug.go"), []byte("package sample\n\nfunc Value() int { return 2 }\n"), 0644)
	}
	service.RepairVerifier = func(ctx context.Context, task RepairTask, action QueueAction, phase string) (RepairValidation, error) {
		if phase == "merge" {
			return RepairValidation{Passed: false, GatePassed: false, Error: "merge regression"}, errors.New("merge regression")
		}
		return RepairValidation{Passed: true, GatePassed: true, EvalRunIDs: []string{"worktree-run"}}, nil
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.RunRepair(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApproveRepair(store, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = service.MergeRepair(context.Background(), task.ID)
	if err == nil || task.Status != RepairStatusApproved {
		t.Fatalf("task=%#v err=%v", task, err)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "bug.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(data), "return 2") {
		t.Fatalf("failed merge remained in main worktree: %s", data)
	}
}

func TestRecoverInterruptedRepairClearsStaleLock_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	config, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	task, err := CreateRepair(store, root, action.ID, config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = RepairStatusRunning
	task.Attempt = 1
	if err := UpdateRepair(store, task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.repairLockPath(task.ID)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.repairLockPath(task.ID), []byte("99999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverInterruptedRepairs(); err != nil {
		t.Fatal(err)
	}
	recovered, err := FindRepair(store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != RepairStatusFailed || !strings.Contains(recovered.Error, "interrupted") {
		t.Fatalf("recovered=%#v", recovered)
	}
	if _, err := os.Stat(store.repairLockPath(task.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale lock remains: %v", err)
	}
}

func TestRecoverCommittedMergeResolvesAction_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.AutoRepair.TestCommands = nil
	service.RepairRunner = func(ctx context.Context, task RepairTask, action QueueAction) (RepairExecution, error) {
		return RepairExecution{}, os.WriteFile(filepath.Join(task.WorktreePath, "bug.go"), []byte("package sample\n\nfunc Value() int { return 2 }\n"), 0644)
	}
	service.RepairVerifier = func(context.Context, RepairTask, QueueAction, string) (RepairValidation, error) {
		return RepairValidation{Passed: true, GatePassed: true, EvalRunIDs: []string{"worktree-run"}}, nil
	}
	task, err := CreateRepair(store, root, action.ID, service.Config.AutoRepair)
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.RunRepair(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = ApproveRepair(store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	runRepairGit(t, root, "merge", "--no-ff", "--no-commit", task.Branch)
	runRepairGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "interrupted merge")
	if _, err := service.EvalStore.SaveResult(evaluation.RunResult{
		RunID: "recover-run", SuiteID: action.SuiteID, StartedAt: time.Now().UTC().Add(time.Second),
		Gate:  &evaluation.GateResult{Passed: true},
		Cases: []evaluation.CaseResult{{CaseID: action.CaseID, Passed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	task.Status = RepairStatusMerging
	task.VerificationRunID = "recover-run"
	task.MergeValidation = RepairValidation{Passed: true, GatePassed: true, EvalRunIDs: []string{"recover-run"}}
	if err := UpdateRepair(store, task); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverInterruptedRepairs(); err != nil {
		t.Fatal(err)
	}
	recovered, err := FindRepair(store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != RepairStatusMerged || recovered.MergeCommit == "" || recovered.Error != "" {
		t.Fatalf("recovered=%#v", recovered)
	}
	resolved, err := findAction(store, action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != QueueStatusResolved || resolved.ResolvedFromRun != "recover-run" {
		t.Fatalf("action=%#v", resolved)
	}
}

func initRepairGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runRepairGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sample\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bug.go"), []byte("package sample\n\nfunc Value() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runRepairGit(t, root, "add", ".")
	runRepairGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	return root
}

func seedRepairAction(t *testing.T, store Store) QueueAction {
	t.Helper()
	now := time.Now().UTC()
	action := QueueAction{
		ID: "action-1", Fingerprint: "failure\x00core\x00case\x00fix", Status: QueueStatusOpen,
		Severity: "high", Category: "failure", Title: "fix bug",
		SuiteID: "core", CaseID: "case", RunID: "run-1",
		FirstSeenAt: now, LastSeenAt: now, LastStatusAt: now,
	}
	if err := store.SaveQueue(Queue{Actions: []QueueAction{action}}); err != nil {
		t.Fatal(err)
	}
	return action
}

func runRepairGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
