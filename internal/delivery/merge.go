package delivery

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

	"cohort/internal/worktree"
)

const (
	approvalFile = "approval.json"
	mergeFile    = "merge.json"
)

type ApprovalRecord struct {
	SchemaVersion     int       `json:"schema_version"`
	DeliveryID        string    `json:"delivery_id"`
	ApprovedBy        string    `json:"approved_by"`
	ApprovedAt        time.Time `json:"approved_at"`
	ContractHash      string    `json:"contract_hash"`
	IntegrationCommit string    `json:"integration_commit"`
	IntegrationTree   string    `json:"integration_tree"`
	VerificationRound int       `json:"verification_round"`
}

type MergeState struct {
	SchemaVersion     int            `json:"schema_version"`
	DeliveryID        string         `json:"delivery_id"`
	Status            DeliveryStatus `json:"status"`
	BaseCommit        string         `json:"base_commit"`
	IntegrationCommit string         `json:"integration_commit"`
	MergeCommit       string         `json:"merge_commit,omitempty"`
	TreeHash          string         `json:"tree_hash,omitempty"`
	PreMergeGates     []GateResult   `json:"pre_merge_gates,omitempty"`
	PostMergeGates    []GateResult   `json:"post_merge_gates,omitempty"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        time.Time      `json:"finished_at,omitempty"`
	Error             string         `json:"error,omitempty"`
}

type MergeService struct {
	Store Store
}

func (s MergeService) Approve(deliveryID string, approvedBy string) (ApprovalRecord, error) {
	item, contract, integration, verification, err := s.reviewInputs(deliveryID)
	if err != nil {
		return ApprovalRecord{}, err
	}
	if item.Status != StatusReadyForReview {
		return ApprovalRecord{}, fmt.Errorf("delivery status is %s, want %s", item.Status, StatusReadyForReview)
	}
	if verification.Status != VerificationPassed || verification.TreeHash != integration.TreeHash {
		return ApprovalRecord{}, errors.New("current integration tree lacks passing semantic verification")
	}
	if err := validateIntegrationEvidence(s.Store, item, contract, integration); err != nil {
		return ApprovalRecord{}, err
	}
	approvedBy = strings.TrimSpace(approvedBy)
	if approvedBy == "" {
		return ApprovalRecord{}, errors.New("approver identity is required")
	}
	record := ApprovalRecord{
		SchemaVersion:     SchemaVersion,
		DeliveryID:        deliveryID,
		ApprovedBy:        approvedBy,
		ApprovedAt:        time.Now().UTC(),
		ContractHash:      item.ContractHash,
		IntegrationCommit: integration.Commit,
		IntegrationTree:   integration.TreeHash,
		VerificationRound: verification.Round,
	}
	if _, err := s.Store.ApproveDelivery(record); err != nil {
		return ApprovalRecord{}, err
	}
	return record, nil
}

func (s MergeService) Merge(ctx context.Context, deliveryID string) (MergeState, error) {
	item, contract, integration, verification, err := s.reviewInputs(deliveryID)
	if err != nil {
		return MergeState{}, err
	}
	approval, err := s.Store.LoadApproval(deliveryID)
	if err != nil {
		return MergeState{}, err
	}
	if item.Status != StatusApproved {
		return MergeState{}, fmt.Errorf("delivery status is %s, want %s", item.Status, StatusApproved)
	}
	if approval.ContractHash != item.ContractHash ||
		approval.IntegrationCommit != integration.Commit ||
		approval.IntegrationTree != integration.TreeHash ||
		approval.VerificationRound != verification.Round {
		return MergeState{}, errors.New("approval is stale for current contract, integration, or verification")
	}
	if err := validateIntegrationEvidence(s.Store, item, contract, integration); err != nil {
		return MergeState{}, err
	}
	head, err := runDeliveryGitText(ctx, item.ProjectRoot, "rev-parse", "HEAD")
	if err != nil {
		return MergeState{}, err
	}
	if head != item.BaseCommit {
		return MergeState{}, fmt.Errorf("main workspace HEAD moved from base %s to %s; replan or rebase delivery", item.BaseCommit, head)
	}
	status, err := runDeliveryGitText(ctx, item.ProjectRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return MergeState{}, err
	}
	if status != "" {
		return MergeState{}, errors.New("main workspace is dirty; transactional merge refused")
	}
	state := MergeState{
		SchemaVersion:     SchemaVersion,
		DeliveryID:        deliveryID,
		Status:            StatusMerging,
		BaseCommit:        item.BaseCommit,
		IntegrationCommit: integration.Commit,
		StartedAt:         time.Now().UTC(),
	}
	if _, err := s.Store.Transition(deliveryID, StatusMerging, "DeliveryMergeStarted", map[string]any{
		"integration_commit": integration.Commit,
	}); err != nil {
		return state, err
	}
	failBeforeCommit := func(failure error) (MergeState, error) {
		rollbackErr := restoreMainWorkspace(context.Background(), item.ProjectRoot, item.BaseCommit)
		if rollbackErr != nil {
			failure = errors.Join(failure, fmt.Errorf("restore main workspace: %w", rollbackErr))
		}
		state.Status = StatusApproved
		state.Error = failure.Error()
		state.FinishedAt = time.Now().UTC()
		_ = s.Store.SaveMerge(deliveryID, state)
		_, _ = s.Store.Transition(deliveryID, StatusApproved, "DeliveryMergeAborted", map[string]any{"error": failure.Error()})
		return state, failure
	}
	if _, err := runDeliveryGitText(ctx, item.ProjectRoot, "merge", "--no-ff", "--no-commit", integration.Commit); err != nil {
		return failBeforeCommit(err)
	}
	treeHash, err := runDeliveryGitText(ctx, item.ProjectRoot, "write-tree")
	if err != nil {
		return failBeforeCommit(err)
	}
	state.TreeHash = treeHash
	snapshot, err := captureMergeSnapshot(ctx, item.ProjectRoot)
	if err != nil {
		return failBeforeCommit(err)
	}
	for _, gate := range contract.RequiredGates {
		if gate.Kind != "command" {
			continue
		}
		result, gateErr := s.runMergeGate(ctx, item, contract, gate, integration.Commit, treeHash, snapshot)
		state.PreMergeGates = append(state.PreMergeGates, result)
		_ = s.Store.SaveMerge(deliveryID, state)
		if gateErr != nil && gate.Mandatory {
			return failBeforeCommit(gateErr)
		}
	}
	commitMessage := "cohort delivery " + deliveryID
	if _, err := runDeliveryGitText(ctx, item.ProjectRoot,
		"-c", "user.name=Cohort Delivery",
		"-c", "user.email=cohort-delivery@localhost",
		"commit", "-m", commitMessage,
	); err != nil {
		return failBeforeCommit(err)
	}
	mergeCommit, err := runDeliveryGitText(ctx, item.ProjectRoot, "rev-parse", "HEAD")
	if err != nil {
		return failBeforeCommit(err)
	}
	state.MergeCommit = mergeCommit
	state.Status = StatusMergedUnverified
	if err := s.Store.SaveMerge(deliveryID, state); err != nil {
		return state, err
	}
	if _, err := s.Store.Transition(deliveryID, StatusMergedUnverified, "DeliveryMerged", map[string]any{
		"merge_commit": mergeCommit,
		"tree_hash":    treeHash,
	}); err != nil {
		return state, err
	}
	return s.finishPostMerge(ctx, item, contract, state)
}

func (s MergeService) Recover(ctx context.Context, deliveryID string) (MergeState, error) {
	item, contract, integration, _, err := s.reviewInputs(deliveryID)
	if err != nil {
		return MergeState{}, err
	}
	if item.Status != StatusMerging && item.Status != StatusMergedUnverified {
		return MergeState{}, fmt.Errorf("delivery status %s has no recoverable merge", item.Status)
	}
	state, stateErr := s.Store.LoadMerge(deliveryID)
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return MergeState{}, stateErr
	}
	if state.DeliveryID == "" {
		state = MergeState{
			SchemaVersion: SchemaVersion, DeliveryID: deliveryID, Status: item.Status,
			BaseCommit: item.BaseCommit, IntegrationCommit: integration.Commit, StartedAt: time.Now().UTC(),
		}
	}
	if item.Status == StatusMerging {
		head, err := runDeliveryGitText(ctx, item.ProjectRoot, "rev-parse", "HEAD")
		if err != nil {
			return state, err
		}
		if head == item.BaseCommit {
			if err := restoreMainWorkspace(ctx, item.ProjectRoot, item.BaseCommit); err != nil {
				return state, err
			}
			state.Status = StatusApproved
			state.Error = "recovered interrupted pre-commit merge"
			state.FinishedAt = time.Now().UTC()
			if err := s.Store.SaveMerge(deliveryID, state); err != nil {
				return state, err
			}
			_, err = s.Store.Transition(deliveryID, StatusApproved, "DeliveryMergeRecoveredBeforeCommit", nil)
			return state, err
		}
		treeHash, err := runDeliveryGitText(ctx, item.ProjectRoot, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return state, err
		}
		parents, err := runDeliveryGitText(ctx, item.ProjectRoot, "rev-list", "--parents", "-n", "1", "HEAD")
		if err != nil {
			return state, err
		}
		if treeHash != integration.TreeHash || len(strings.Fields(parents)) < 3 {
			return state, errors.New("interrupted merge produced an unrecognized commit; human recovery required")
		}
		if _, err := runDeliveryGitText(ctx, item.ProjectRoot, "merge-base", "--is-ancestor", integration.Commit, head); err != nil {
			return state, errors.New("interrupted merge commit does not contain approved integration commit")
		}
		state.Status = StatusMergedUnverified
		state.MergeCommit = head
		state.TreeHash = treeHash
		state.Error = ""
		if err := s.Store.SaveMerge(deliveryID, state); err != nil {
			return state, err
		}
		if _, err := s.Store.Transition(deliveryID, StatusMergedUnverified, "DeliveryMergeCommitRecovered", map[string]any{
			"merge_commit": head, "tree_hash": treeHash,
		}); err != nil {
			return state, err
		}
	}
	return s.finishPostMerge(ctx, item, contract, state)
}

func (s MergeService) finishPostMerge(ctx context.Context, item Delivery, contract AcceptanceContract, state MergeState) (MergeState, error) {
	postErr := s.postMergeVerify(ctx, item, contract, &state)
	state.FinishedAt = time.Now().UTC()
	if postErr != nil {
		state.Status = StatusMergedUnverified
		state.Error = fmt.Sprintf("post-merge verification failed; merge commit %s remains unverified: %v", state.MergeCommit, postErr)
		_ = s.Store.SaveMerge(item.ID, state)
		return state, postErr
	}
	state.Status = StatusVerified
	state.Error = ""
	if err := s.Store.SaveMerge(item.ID, state); err != nil {
		return state, err
	}
	if _, err := s.Store.Transition(item.ID, StatusVerified, "DeliveryVerified", map[string]any{
		"merge_commit": state.MergeCommit,
		"tree_hash":    state.TreeHash,
	}); err != nil {
		return state, err
	}
	return state, nil
}

func restoreMainWorkspace(ctx context.Context, root string, baseCommit string) error {
	_, abortErr := runDeliveryGitText(ctx, root, "merge", "--abort")
	if _, err := runDeliveryGitText(ctx, root, "restore", "--source="+baseCommit, "--staged", "--worktree", "--", ":/"); err != nil {
		return errors.Join(abortErr, err)
	}
	untracked, err := runDeliveryGitText(ctx, root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return errors.Join(abortErr, err)
	}
	for _, relative := range strings.Split(untracked, "\n") {
		relative = strings.TrimSpace(relative)
		if relative == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		cleanRoot := filepath.Clean(root)
		cleanPath := filepath.Clean(path)
		if cleanPath == cleanRoot || !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
			return errors.Join(abortErr, fmt.Errorf("refuse to remove untracked path outside project: %s", relative))
		}
		if err := os.RemoveAll(cleanPath); err != nil {
			return errors.Join(abortErr, err)
		}
	}
	status, err := runDeliveryGitText(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return errors.Join(abortErr, err)
	}
	if status != "" {
		return errors.Join(abortErr, fmt.Errorf("workspace remains dirty after rollback: %s", status))
	}
	return nil
}

func (s MergeService) reviewInputs(deliveryID string) (Delivery, AcceptanceContract, IntegrationState, VerificationState, error) {
	item, err := s.Store.Load(deliveryID)
	if err != nil {
		return Delivery{}, AcceptanceContract{}, IntegrationState{}, VerificationState{}, err
	}
	contract, _, err := s.Store.LoadPlan(deliveryID)
	if err != nil {
		return Delivery{}, AcceptanceContract{}, IntegrationState{}, VerificationState{}, err
	}
	integration, err := s.Store.LoadIntegration(deliveryID)
	if err != nil {
		return Delivery{}, AcceptanceContract{}, IntegrationState{}, VerificationState{}, err
	}
	verification, err := s.Store.LoadVerification(deliveryID)
	return item, contract, integration, verification, err
}

func validateIntegrationEvidence(store Store, item Delivery, contract AcceptanceContract, integration IntegrationState) error {
	environmentHash, err := EnvironmentHash(integration.WorktreePath)
	if err != nil {
		return err
	}
	gates := make(map[string]GateSpec, len(contract.RequiredGates))
	for _, gate := range contract.RequiredGates {
		gates[gate.ID] = gate
	}
	var evidence []EvidenceEnvelope
	for _, evidenceID := range integration.EvidenceIDs {
		envelope, err := store.LoadEvidence(item.ID, evidenceID)
		if err != nil {
			return err
		}
		gate, exists := gates[envelope.GateID]
		if !exists {
			return fmt.Errorf("evidence %q references unknown gate", evidenceID)
		}
		if err := VerifyEvidenceFreshness(envelope, item, integration.TreeHash, gate, environmentHash); err != nil {
			return err
		}
		if _, _, err := store.ReadArtifact(item.ID, envelope.ArtifactHash); err != nil {
			return err
		}
		evidence = append(evidence, envelope)
	}
	return ValidateMandatoryEvidence(contract, evidence)
}

func (s MergeService) runMergeGate(ctx context.Context, item Delivery, contract AcceptanceContract, gate GateSpec, commit string, treeHash string, before mergeSnapshot) (GateResult, error) {
	execution, err := executeGateCommand(ctx, gate, item.ProjectRoot)
	if err != nil {
		return GateResult{}, err
	}
	after, err := captureMergeSnapshot(ctx, item.ProjectRoot)
	if err != nil {
		return GateResult{}, err
	}
	runErr := execution.Err
	if before != after {
		runErr = errors.Join(runErr, errors.New("gate mutated staged, unstaged, or untracked merge state"))
	}
	return s.persistMergeGate(item, contract, gate, commit, treeHash, "pre_merge_gate", execution, runErr)
}

func (s MergeService) persistMergeGate(item Delivery, contract AcceptanceContract, gate GateSpec, commit string, treeHash string, producer string, execution gateExecution, runErr error) (GateResult, error) {
	artifact, err := s.Store.PublishArtifact(item.ID, ArtifactMeta{
		Kind: "gate_output", Producer: producer + ":" + gate.ID,
		BaseCommit: item.BaseCommit, TreeHash: treeHash, MediaType: "text/plain",
	}, execution.Output)
	if err != nil {
		return GateResult{}, err
	}
	environmentHash, err := EnvironmentHash(item.ProjectRoot)
	if err != nil {
		return GateResult{}, err
	}
	commandHash, err := GateCommandHash(gate)
	if err != nil {
		return GateResult{}, err
	}
	status := EvidencePassed
	errorText := ""
	if runErr != nil {
		status = EvidenceFailed
		errorText = runErr.Error()
	}
	envelope := EvidenceEnvelope{
		SchemaVersion: SchemaVersion, ID: newEvidenceID(gate.ID, treeHash), DeliveryID: item.ID,
		CriterionIDs: CriteriaForGate(contract, gate.ID), GateID: gate.ID, Producer: producer,
		ContractHash: item.ContractHash, BaseCommit: item.BaseCommit, CandidateCommit: commit,
		TreeHash: treeHash, CommandHash: commandHash, EnvironmentHash: environmentHash,
		ExitCode: execution.ExitCode, StartedAt: execution.StartedAt, FinishedAt: execution.FinishedAt,
		ArtifactHash: artifact.ID, Status: status, Error: errorText,
	}
	if err := s.Store.SaveEvidence(item.ID, envelope); err != nil {
		return GateResult{}, err
	}
	result := GateResult{
		Gate: gate, Evidence: envelope,
		Output:     previewGateOutput(string(execution.Output), 4000),
		DurationMS: execution.FinishedAt.Sub(execution.StartedAt).Milliseconds(),
	}
	if runErr != nil {
		return result, fmt.Errorf("gate %s failed: %w", gate.ID, runErr)
	}
	return result, nil
}

func (s MergeService) postMergeVerify(ctx context.Context, item Delivery, contract AcceptanceContract, state *MergeState) error {
	manager, err := worktree.NewManager(item.ProjectRoot, item.ProjectRoot)
	if err != nil {
		return err
	}
	spec := worktree.Spec{ID: "post-merge", BaseCommit: item.BaseCommit, Branch: "post-merge", Path: item.ProjectRoot}
	runner := GateRunner{Store: s.Store}
	var evidence []EvidenceEnvelope
	for _, gate := range contract.RequiredGates {
		if gate.Kind != "command" {
			continue
		}
		result, gateErr := runner.Run(ctx, item, contract, gate, manager, spec, state.MergeCommit, state.TreeHash)
		state.PostMergeGates = append(state.PostMergeGates, result)
		_ = s.Store.SaveMerge(item.ID, *state)
		if result.Evidence.ID != "" {
			evidence = append(evidence, result.Evidence)
		}
		if gateErr != nil && gate.Mandatory {
			return gateErr
		}
	}
	return ValidateMandatoryEvidence(contract, evidence)
}

type mergeSnapshot struct {
	Cached    string
	Unstaged  string
	Untracked string
}

func captureMergeSnapshot(ctx context.Context, root string) (mergeSnapshot, error) {
	cached, err := runDeliveryGitBytes(ctx, root, "diff", "--cached", "--binary")
	if err != nil {
		return mergeSnapshot{}, err
	}
	unstaged, err := runDeliveryGitBytes(ctx, root, "diff", "--binary")
	if err != nil {
		return mergeSnapshot{}, err
	}
	untracked, err := runDeliveryGitBytes(ctx, root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return mergeSnapshot{}, err
	}
	return mergeSnapshot{
		Cached: HashString(string(cached)), Unstaged: HashString(string(unstaged)), Untracked: HashString(string(untracked)),
	}, nil
}

func runDeliveryGitText(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := runDeliveryGitBytes(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runDeliveryGitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (s Store) SaveApproval(deliveryID string, record ApprovalRecord) error {
	if record.SchemaVersion != SchemaVersion || record.DeliveryID != deliveryID {
		return errors.New("approval identity or schema mismatch")
	}
	return s.writeDeliveryState(deliveryID, approvalFile, record)
}

func (s Store) ApproveDelivery(record ApprovalRecord) (Delivery, error) {
	if record.SchemaVersion != SchemaVersion || strings.TrimSpace(record.DeliveryID) == "" {
		return Delivery{}, errors.New("approval identity or schema mismatch")
	}
	if strings.TrimSpace(record.ApprovedBy) == "" || record.ApprovedAt.IsZero() ||
		record.ContractHash == "" || record.IntegrationCommit == "" || record.IntegrationTree == "" {
		return Delivery{}, errors.New("approval record is incomplete")
	}
	release, err := s.AcquireDeliveryLock(record.DeliveryID)
	if err != nil {
		return Delivery{}, err
	}
	defer release()
	item, err := s.loadDeliveryUnlocked(record.DeliveryID)
	if err != nil {
		return Delivery{}, err
	}
	if item.Status != StatusReadyForReview {
		return Delivery{}, fmt.Errorf("delivery status is %s, want %s", item.Status, StatusReadyForReview)
	}
	if err := s.writeJSON(filepath.Join(s.deliveryDir(record.DeliveryID), approvalFile), record); err != nil {
		return Delivery{}, err
	}
	item.Status = StatusApproved
	item.ApprovedAt = record.ApprovedAt
	item.UpdatedAt = record.ApprovedAt
	if err := s.writeJSON(s.deliveryPath(item.ID), item); err != nil {
		return Delivery{}, err
	}
	if err := s.appendEventUnlocked(item.ID, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(record.ApprovedAt),
		DeliveryID:    item.ID,
		Type:          "DeliveryApproved",
		Time:          record.ApprovedAt,
		Data: map[string]any{
			"status":             StatusApproved,
			"approved_by":        record.ApprovedBy,
			"integration_commit": record.IntegrationCommit,
			"integration_tree":   record.IntegrationTree,
		},
	}); err != nil {
		return Delivery{}, err
	}
	return item, nil
}

func (s Store) LoadApproval(deliveryID string) (ApprovalRecord, error) {
	var record ApprovalRecord
	if err := readJSON(filepath.Join(s.deliveryDir(deliveryID), approvalFile), &record); err != nil {
		return ApprovalRecord{}, err
	}
	if record.SchemaVersion != SchemaVersion || record.DeliveryID != deliveryID {
		return ApprovalRecord{}, errors.New("approval identity or schema mismatch")
	}
	return record, nil
}

func (s Store) SaveMerge(deliveryID string, state MergeState) error {
	if state.SchemaVersion != SchemaVersion || state.DeliveryID != deliveryID {
		return errors.New("merge identity or schema mismatch")
	}
	return s.writeDeliveryState(deliveryID, mergeFile, state)
}

func (s Store) LoadMerge(deliveryID string) (MergeState, error) {
	var state MergeState
	if err := readJSON(filepath.Join(s.deliveryDir(deliveryID), mergeFile), &state); err != nil {
		return MergeState{}, err
	}
	if state.SchemaVersion != SchemaVersion || state.DeliveryID != deliveryID {
		return MergeState{}, errors.New("merge identity or schema mismatch")
	}
	return state, nil
}

func (s Store) writeDeliveryState(deliveryID string, name string, value any) error {
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	return s.writeJSON(filepath.Join(s.deliveryDir(deliveryID), name), value)
}

func DefaultApproverIdentity() string {
	for _, name := range []string{"COHORT_APPROVER", "USER", "USERNAME"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return "local-user"
}
