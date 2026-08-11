package delivery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/worktree"
)

const integrationFile = "integration.json"

type IntegrationStatus string

const (
	IntegrationRunning IntegrationStatus = "running"
	IntegrationPassed  IntegrationStatus = "passed"
	IntegrationFailed  IntegrationStatus = "failed"
)

type IntegrationState struct {
	SchemaVersion    int               `json:"schema_version"`
	DeliveryID       string            `json:"delivery_id"`
	Status           IntegrationStatus `json:"status"`
	BaseCommit       string            `json:"base_commit"`
	Branch           string            `json:"branch"`
	WorktreePath     string            `json:"worktree_path"`
	CandidateCommits []string          `json:"candidate_commits"`
	Commit           string            `json:"commit,omitempty"`
	TreeHash         string            `json:"tree_hash,omitempty"`
	DiffArtifact     string            `json:"diff_artifact,omitempty"`
	GateResults      []GateResult      `json:"gate_results,omitempty"`
	EvidenceIDs      []string          `json:"evidence_ids,omitempty"`
	Error            string            `json:"error,omitempty"`
	StartedAt        time.Time         `json:"started_at"`
	FinishedAt       time.Time         `json:"finished_at,omitempty"`
}

type Integrator struct {
	Store Store
}

func (i Integrator) Run(ctx context.Context, deliveryID string) (IntegrationState, error) {
	item, err := i.Store.Load(deliveryID)
	if err != nil {
		return IntegrationState{}, err
	}
	if item.Status != StatusIntegrating {
		return IntegrationState{}, fmt.Errorf("delivery %q cannot integrate from status %s", deliveryID, item.Status)
	}
	contract, graph, err := i.Store.LoadPlan(deliveryID)
	if err != nil {
		return IntegrationState{}, err
	}
	runtime, err := i.Store.LoadRuntime(deliveryID)
	if err != nil {
		return IntegrationState{}, err
	}
	if !RuntimeComplete(runtime) {
		return IntegrationState{}, errors.New("delivery builders are not complete")
	}
	order, err := TopologicalOrder(graph)
	if err != nil {
		return IntegrationState{}, err
	}
	commits, err := selectedCommitsInOrder(order, runtime)
	if err != nil {
		return IntegrationState{}, err
	}
	revisionCommits, err := i.Store.RevisionCommits(deliveryID)
	if err != nil {
		return IntegrationState{}, err
	}
	commits = appendUniqueCommits(commits, revisionCommits...)
	manager, err := worktree.NewManager(item.ProjectRoot, i.Store.WorktreeDir)
	if err != nil {
		return IntegrationState{}, err
	}
	spec := worktree.Spec{
		ID:         deliveryID + "-integration",
		BaseCommit: item.BaseCommit,
		Branch:     "cohort/delivery/" + deliveryID + "/integration",
		Path:       filepath.Join(i.Store.WorktreeDir, deliveryID, "integration"),
	}
	state := IntegrationState{
		SchemaVersion:    SchemaVersion,
		DeliveryID:       deliveryID,
		Status:           IntegrationRunning,
		BaseCommit:       item.BaseCommit,
		Branch:           spec.Branch,
		WorktreePath:     spec.Path,
		CandidateCommits: commits,
		StartedAt:        time.Now().UTC(),
	}
	if err := i.Store.SaveIntegration(deliveryID, state); err != nil {
		return state, err
	}
	fail := func(failure error) (IntegrationState, error) {
		state.Status = IntegrationFailed
		state.Error = failure.Error()
		state.FinishedAt = time.Now().UTC()
		_ = i.Store.SaveIntegration(deliveryID, state)
		_, _ = i.Store.Transition(deliveryID, StatusNeedsRevision, "DeliveryIntegrationFailed", map[string]any{"error": failure.Error()})
		return state, failure
	}
	if err := manager.Prepare(ctx, spec); err != nil {
		return fail(err)
	}
	if err := manager.MergeCommits(ctx, spec, commits); err != nil {
		return fail(err)
	}
	state.Commit, err = manager.Head(ctx, spec)
	if err != nil {
		return fail(err)
	}
	state.TreeHash, err = manager.TreeHash(ctx, spec)
	if err != nil {
		return fail(err)
	}
	diff, err := manager.DiffBetween(ctx, spec, item.BaseCommit, state.Commit)
	if err != nil {
		return fail(err)
	}
	if len(diff) > 4<<20 {
		return fail(fmt.Errorf("integration diff exceeds 4 MiB: %d bytes", len(diff)))
	}
	diffArtifact, err := i.Store.PublishArtifact(deliveryID, ArtifactMeta{
		Kind:       "integration_patch",
		Producer:   "integrator",
		BaseCommit: item.BaseCommit,
		TreeHash:   state.TreeHash,
		MediaType:  "text/x-diff",
	}, diff)
	if err != nil {
		return fail(err)
	}
	state.DiffArtifact = diffArtifact.ID
	if err := i.Store.SaveIntegration(deliveryID, state); err != nil {
		return fail(err)
	}

	gateRunner := GateRunner{Store: i.Store}
	for _, gate := range contract.RequiredGates {
		if gate.Kind != "command" {
			continue
		}
		result, gateErr := gateRunner.Run(ctx, item, contract, gate, manager, spec, state.Commit, state.TreeHash)
		state.GateResults = append(state.GateResults, result)
		if result.Evidence.ID != "" {
			state.EvidenceIDs = append(state.EvidenceIDs, result.Evidence.ID)
		}
		if saveErr := i.Store.SaveIntegration(deliveryID, state); saveErr != nil {
			return fail(saveErr)
		}
		if gateErr != nil && gate.Mandatory {
			return fail(gateErr)
		}
	}
	evidence := make([]EvidenceEnvelope, 0, len(state.EvidenceIDs))
	environmentHash, err := EnvironmentHash(spec.Path)
	if err != nil {
		return fail(err)
	}
	gates := map[string]GateSpec{}
	for _, gate := range contract.RequiredGates {
		gates[gate.ID] = gate
	}
	for _, evidenceID := range state.EvidenceIDs {
		itemEvidence, err := i.Store.LoadEvidence(deliveryID, evidenceID)
		if err != nil {
			return fail(err)
		}
		gate, exists := gates[itemEvidence.GateID]
		if !exists {
			return fail(fmt.Errorf("evidence %q references unknown gate %q", evidenceID, itemEvidence.GateID))
		}
		if err := VerifyEvidenceFreshness(itemEvidence, item, state.TreeHash, gate, environmentHash); err != nil {
			return fail(fmt.Errorf("evidence %q is stale: %w", evidenceID, err))
		}
		if _, _, err := i.Store.ReadArtifact(deliveryID, itemEvidence.ArtifactHash); err != nil {
			return fail(fmt.Errorf("evidence %q output artifact is invalid: %w", evidenceID, err))
		}
		evidence = append(evidence, itemEvidence)
	}
	if err := ValidateMandatoryEvidence(contract, evidence); err != nil {
		return fail(err)
	}
	state.Status = IntegrationPassed
	state.FinishedAt = time.Now().UTC()
	state.Error = ""
	if err := i.Store.SaveIntegration(deliveryID, state); err != nil {
		return fail(err)
	}
	if _, err := i.Store.Transition(deliveryID, StatusVerifying, "DeliveryIntegrationFinished", map[string]any{
		"commit":       state.Commit,
		"tree_hash":    state.TreeHash,
		"gate_count":   len(state.GateResults),
		"evidence_ids": append([]string(nil), state.EvidenceIDs...),
	}); err != nil {
		return state, err
	}
	return state, nil
}

func (s Store) SaveIntegration(deliveryID string, state IntegrationState) error {
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return errors.New("integration state identity or schema mismatch")
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	return s.writeJSON(filepath.Join(s.deliveryDir(deliveryID), integrationFile), state)
}

func (s Store) LoadIntegration(deliveryID string) (IntegrationState, error) {
	var state IntegrationState
	if err := readJSON(filepath.Join(s.deliveryDir(deliveryID), integrationFile), &state); err != nil {
		return IntegrationState{}, err
	}
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return IntegrationState{}, errors.New("integration state identity or schema mismatch")
	}
	return state, nil
}

func selectedCommitsInOrder(order []string, runtime RuntimeState) ([]string, error) {
	commits := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, nodeID := range order {
		node, exists := runtime.Nodes[nodeID]
		if !exists || node.Status != NodePassed || strings.TrimSpace(node.SelectedID) == "" {
			return nil, fmt.Errorf("node %q has no selected candidate", nodeID)
		}
		found := false
		for _, candidate := range node.Candidates {
			if candidate.ID != node.SelectedID {
				continue
			}
			if strings.TrimSpace(candidate.Commit) == "" {
				return nil, fmt.Errorf("selected candidate %q has no commit", candidate.ID)
			}
			if !seen[candidate.Commit] {
				commits = append(commits, candidate.Commit)
				seen[candidate.Commit] = true
			}
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("selected candidate %q not found for node %q", node.SelectedID, nodeID)
		}
	}
	return commits, nil
}

func appendUniqueCommits(commits []string, additional ...string) []string {
	seen := map[string]bool{}
	for _, commit := range commits {
		seen[commit] = true
	}
	for _, commit := range additional {
		commit = strings.TrimSpace(commit)
		if commit != "" && !seen[commit] {
			commits = append(commits, commit)
			seen[commit] = true
		}
	}
	return commits
}
