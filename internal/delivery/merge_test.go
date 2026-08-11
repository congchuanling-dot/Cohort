package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cohort/internal/worktree"
)

func TestMergeServiceApprovesMergesAndPostVerifies_BitsUT(t *testing.T) {
	store, item := createReadyMergeDelivery(t, []string{"make", "test"})
	service := MergeService{Store: store}
	approval, err := service.Approve(item.ID, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if approval.ApprovedBy != "reviewer" {
		t.Fatalf("approval = %#v", approval)
	}
	state, err := service.Merge(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusVerified || state.MergeCommit == "" || len(state.PreMergeGates) != 1 || len(state.PostMergeGates) != 1 {
		t.Fatalf("merge state = %#v", state)
	}
	data, err := os.ReadFile(filepath.Join(item.ProjectRoot, "feature.txt"))
	if err != nil || string(data) != "delivered\n" {
		t.Fatalf("feature = %q, err=%v", data, err)
	}
	parents := strings.Fields(mustGitText(t, item.ProjectRoot, "show", "-s", "--format=%P", "HEAD"))
	if len(parents) != 2 {
		t.Fatalf("merge parents = %#v, want two", parents)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusVerified || stored.ApprovedAt.IsZero() || stored.VerifiedAt.IsZero() {
		t.Fatalf("stored delivery = %#v", stored)
	}
	report, err := store.BuildReviewReport(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(t.TempDir(), "review.html")
	if err := WriteReviewHTML(report, reportPath); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProofStatus != "fresh" || !strings.Contains(string(html), item.ID) || !strings.Contains(string(html), "Acceptance Coverage") {
		t.Fatalf("review report is incomplete: proof=%s html=%s", report.ProofStatus, html)
	}
}

func TestMergeServiceAbortsWhenGateMutatesMergeState_BitsUT(t *testing.T) {
	store, item := createReadyMergeDelivery(t, []string{"make", "mutate"})
	service := MergeService{Store: store}
	if _, err := service.Approve(item.ID, "reviewer"); err != nil {
		t.Fatal(err)
	}
	_, err := service.Merge(context.Background(), item.ID)
	if err == nil || !strings.Contains(err.Error(), "mutated") {
		t.Fatalf("merge err = %v", err)
	}
	if head := mustGitText(t, item.ProjectRoot, "rev-parse", "HEAD"); head != item.BaseCommit {
		t.Fatalf("HEAD = %s, want base %s", head, item.BaseCommit)
	}
	if status := mustGitText(t, item.ProjectRoot, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("workspace not restored: %s", status)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusApproved {
		t.Fatalf("delivery status = %s, want approved", stored.Status)
	}
}

func TestMergeServiceRecoversInterruptedMergeStages_BitsUT(t *testing.T) {
	t.Run("before commit", func(t *testing.T) {
		store, item := createReadyMergeDelivery(t, []string{"make", "test"})
		service := MergeService{Store: store}
		if _, err := service.Approve(item.ID, "reviewer"); err != nil {
			t.Fatal(err)
		}
		integration, err := store.LoadIntegration(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Transition(item.ID, StatusMerging, "fixture_crash", nil); err != nil {
			t.Fatal(err)
		}
		runTestGit(t, item.ProjectRoot, "merge", "--no-ff", "--no-commit", integration.Commit)
		recovered, err := service.Recover(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Status != StatusApproved || mustGitText(t, item.ProjectRoot, "rev-parse", "HEAD") != item.BaseCommit {
			t.Fatalf("pre-commit recovery = %#v", recovered)
		}
	})
	t.Run("after commit", func(t *testing.T) {
		store, item := createReadyMergeDelivery(t, []string{"make", "test"})
		service := MergeService{Store: store}
		if _, err := service.Approve(item.ID, "reviewer"); err != nil {
			t.Fatal(err)
		}
		integration, err := store.LoadIntegration(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Transition(item.ID, StatusMerging, "fixture_crash", nil); err != nil {
			t.Fatal(err)
		}
		runTestGit(t, item.ProjectRoot, "merge", "--no-ff", "--no-commit", integration.Commit)
		runTestGit(t, item.ProjectRoot, "commit", "-m", "interrupted merge")
		recovered, err := service.Recover(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Status != StatusVerified || recovered.MergeCommit == "" || len(recovered.PostMergeGates) != 1 {
			t.Fatalf("post-commit recovery = %#v", recovered)
		}
	})
}

func TestMergeServiceKeepsFailedPostMergeVerificationRecoverable_BitsUT(t *testing.T) {
	store, item := createReadyMergeDelivery(t, []string{"make", "postfail"})
	service := MergeService{Store: store}
	if _, err := service.Approve(item.ID, "reviewer"); err != nil {
		t.Fatal(err)
	}
	state, err := service.Merge(context.Background(), item.ID)
	if err == nil || state.Status != StatusMergedUnverified || state.MergeCommit == "" {
		t.Fatalf("post-merge failure state=%#v err=%v", state, err)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusMergedUnverified {
		t.Fatalf("delivery status = %s, want merged_unverified", stored.Status)
	}
	if err := os.Remove(filepath.Join(item.ProjectRoot, ".gate-once")); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Recover(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusVerified {
		t.Fatalf("recovered state = %#v", recovered)
	}
	report, err := store.BuildReviewReport(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProofStatus != "fresh" {
		t.Fatalf("recovered review proof = %s: %s", report.ProofStatus, report.ProofError)
	}
}

func TestMergeServiceSerializesConcurrentApprovals_BitsUT(t *testing.T) {
	store, item := createReadyMergeDelivery(t, []string{"make", "test"})
	service := MergeService{Store: store}
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	for _, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		go func(identity string) {
			defer wait.Done()
			_, err := service.Approve(item.ID, identity)
			results <- err
		}(reviewer)
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("approval successes = %d, want exactly one", successes)
	}
	approval, err := store.LoadApproval(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusApproved || approval.ApprovedBy == "" || !stored.ApprovedAt.Equal(approval.ApprovedAt) {
		t.Fatalf("approval=%#v delivery=%#v", approval, stored)
	}
}

func createReadyMergeDelivery(t *testing.T, command []string) (Store, Delivery) {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	runTestGit(t, repo, "init")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	makefile := "test:\n\t@test -f feature.txt\n\nmutate:\n\t@printf 'mutated\\n' > README.md\n\npostfail:\n\t@if [ -f .gate-once ]; then exit 1; else touch .gate-once; fi\n"
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".gate-once\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", ".")
	runTestGit(t, repo, "commit", "-m", "base")

	storeRoot := t.TempDir()
	store := NewStore(repo)
	store.RootDir = filepath.Join(storeRoot, "deliveries")
	store.WorktreeDir = filepath.Join(storeRoot, "worktrees")
	base := mustGitText(t, repo, "rev-parse", "HEAD")
	item, err := store.CreateDraft("deliver feature", base, false, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	contract := AcceptanceContract{
		SchemaVersion: SchemaVersion, RequirementHash: item.RequirementHash, BaseCommit: item.BaseCommit,
		Summary: "Deliver feature.", AllowedScope: []string{"feature.txt"},
		ForbiddenScope: []string{".git/**"}, RiskProfile: RiskProfile{Level: RiskLow},
		Criteria: []Criterion{{
			ID: "AC-1", Statement: "feature exists", Mandatory: true,
			Verification: VerifyCommand, TargetPaths: []string{"feature.txt"},
			EvidencePolicy: EvidenceExecution, GateIDs: []string{"gate-merge"},
		}},
		RequiredGates: []GateSpec{{
			ID: "gate-merge", Name: "merge gate", Kind: "command",
			Command: command, Paths: []string{"feature.txt"}, TimeoutSeconds: 30, Mandatory: true,
		}},
	}
	graph := TaskGraph{
		SchemaVersion: SchemaVersion, DeliveryID: item.ID, BaseCommit: item.BaseCommit,
		Nodes: []TaskNode{{
			ID: "feature", Title: "Deliver feature", Objective: "Create feature.txt.",
			Role: RoleBuilder, Status: NodePending, ReadSet: []string{"README.md"},
			DeclaredWrites: []string{"feature.txt"}, Criteria: []string{"AC-1"},
			Risk: RiskLow, CandidateCount: 1, Budget: DefaultNodeBudget(),
		}},
	}
	item, err = store.SavePlan(item.ID, contract, graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeRuntime(item.ID); err != nil {
		t.Fatal(err)
	}

	manager, err := worktree.NewManager(repo, filepath.Join(storeRoot, "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	spec := worktree.Spec{
		ID: "integration", BaseCommit: item.BaseCommit,
		Branch: "cohort/test/integration", Path: filepath.Join(storeRoot, "worktrees", "integration"),
	}
	if err := manager.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Path, "feature.txt"), []byte("delivered\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commit, err := manager.Commit(ctx, spec, "delivery candidate")
	if err != nil {
		t.Fatal(err)
	}
	treeHash, err := manager.TreeHash(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PublishArtifact(item.ID, ArtifactMeta{
		Kind: "gate_output", Producer: "fixture", BaseCommit: item.BaseCommit,
		TreeHash: treeHash, MediaType: "text/plain",
	}, []byte("fixture gate passed"))
	if err != nil {
		t.Fatal(err)
	}
	commandHash, err := GateCommandHash(contract.RequiredGates[0])
	if err != nil {
		t.Fatal(err)
	}
	environmentHash, err := EnvironmentHash(spec.Path)
	if err != nil {
		t.Fatal(err)
	}
	evidence := EvidenceEnvelope{
		SchemaVersion: SchemaVersion, ID: "evidence_fixture", DeliveryID: item.ID,
		CriterionIDs: []string{"AC-1"}, GateID: "gate-merge", Producer: "fixture",
		ContractHash: item.ContractHash, BaseCommit: item.BaseCommit, CandidateCommit: commit,
		TreeHash: treeHash, CommandHash: commandHash, EnvironmentHash: environmentHash,
		ExitCode: 0, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		ArtifactHash: artifact.ID, Status: EvidencePassed,
	}
	if err := store.SaveEvidence(item.ID, evidence); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIntegration(item.ID, IntegrationState{
		SchemaVersion: SchemaVersion, DeliveryID: item.ID, Status: IntegrationPassed,
		WorktreePath: spec.Path, Branch: spec.Branch, Commit: commit, TreeHash: treeHash,
		EvidenceIDs: []string{evidence.ID}, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []DeliveryStatus{StatusRunning, StatusIntegrating, StatusVerifying} {
		if _, err := store.Transition(item.ID, status, "fixture", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveVerification(item.ID, VerificationState{
		SchemaVersion: SchemaVersion, DeliveryID: item.ID, Round: 1,
		Status: VerificationPassed, TreeHash: treeHash,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(item.ID, StatusReadyForReview, "fixture", nil); err != nil {
		t.Fatal(err)
	}
	item, err = store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, item
}

func mustGitText(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := runGitText(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(output)
}
