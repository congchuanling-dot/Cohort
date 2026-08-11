package delivery

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

type RevisionStatus string

const (
	RevisionRunning RevisionStatus = "running"
	RevisionPassed  RevisionStatus = "passed"
	RevisionFailed  RevisionStatus = "failed"
)

type RevisionRecord struct {
	SchemaVersion int            `json:"schema_version"`
	DeliveryID    string         `json:"delivery_id"`
	Round         int            `json:"round"`
	Status        RevisionStatus `json:"status"`
	FindingIDs    []string       `json:"finding_ids"`
	Node          TaskNode       `json:"node"`
	Candidate     Candidate      `json:"candidate"`
	Error         string         `json:"error,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at,omitempty"`
}

type RevisionState struct {
	SchemaVersion int              `json:"schema_version"`
	DeliveryID    string           `json:"delivery_id"`
	Records       []RevisionRecord `json:"records"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type RevisionService struct {
	Store Store
}

func (s RevisionService) Run(ctx context.Context, deliveryID string, worker NodeWorker) (RevisionRecord, error) {
	if worker == nil {
		return RevisionRecord{}, errors.New("revision worker is required")
	}
	item, err := s.Store.Load(deliveryID)
	if err != nil {
		return RevisionRecord{}, err
	}
	if item.Status != StatusNeedsRevision {
		return RevisionRecord{}, fmt.Errorf("delivery %q cannot revise from status %s", deliveryID, item.Status)
	}
	contract, _, err := s.Store.LoadPlan(deliveryID)
	if err != nil {
		return RevisionRecord{}, err
	}
	integration, err := s.Store.LoadIntegration(deliveryID)
	if err != nil {
		return RevisionRecord{}, err
	}
	verification, err := s.Store.LoadVerification(deliveryID)
	if err != nil {
		return RevisionRecord{}, err
	}
	revisions, _ := s.Store.LoadRevisions(deliveryID)
	round := len(revisions.Records) + 1
	if round > item.Budget.MaxRevisionRounds {
		_, _ = s.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryRevisionExhausted", map[string]any{"rounds": len(revisions.Records)})
		return RevisionRecord{}, fmt.Errorf("delivery exhausted %d revision rounds", item.Budget.MaxRevisionRounds)
	}
	findings := openBlockingFindings(verification.Findings)
	if len(findings) == 0 {
		return RevisionRecord{}, errors.New("revision requested without open high-severity findings")
	}
	node, findingIDs, err := buildRevisionNode(round, contract, findings)
	if err != nil {
		_, _ = s.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryRevisionNeedsHuman", map[string]any{"error": err.Error()})
		return RevisionRecord{}, err
	}
	candidateID := fmt.Sprintf("revision-%d-c1", round)
	candidate := Candidate{
		ID:           candidateID,
		NodeID:       node.ID,
		Status:       CandidateRunning,
		BaseCommit:   integration.Commit,
		Branch:       fmt.Sprintf("cohort/delivery/%s/%s", deliveryID, candidateID),
		WorktreePath: filepath.Join(s.Store.WorktreeDir, deliveryID, "revisions", fmt.Sprintf("round-%d", round)),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	record := RevisionRecord{
		SchemaVersion: SchemaVersion,
		DeliveryID:    deliveryID,
		Round:         round,
		Status:        RevisionRunning,
		FindingIDs:    findingIDs,
		Node:          node,
		Candidate:     candidate,
		StartedAt:     time.Now().UTC(),
	}
	revisions.SchemaVersion = SchemaVersion
	revisions.DeliveryID = deliveryID
	revisions.Records = append(revisions.Records, record)
	revisions.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveRevisions(deliveryID, revisions); err != nil {
		return record, err
	}
	if _, err := s.Store.Transition(deliveryID, StatusRunning, "DeliveryRevisionStarted", map[string]any{"round": round, "findings": findingIDs}); err != nil {
		return record, err
	}
	result, runErr := worker(ctx, item, contract, node, candidate)
	record.FinishedAt = time.Now().UTC()
	record.Candidate.UpdatedAt = record.FinishedAt
	if runErr != nil {
		record.Status = RevisionFailed
		record.Error = runErr.Error()
		record.Candidate.Status = CandidateFailed
		record.Candidate.Error = runErr.Error()
		_ = s.Store.replaceRevision(deliveryID, record)
		_, _ = s.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryRevisionFailed", map[string]any{"round": round, "error": runErr.Error()})
		return record, runErr
	}
	if err := validateActualWrites(node.DeclaredWrites, result.ActualWrites); err != nil {
		record.Status = RevisionFailed
		record.Error = err.Error()
		record.Candidate.Status = CandidateFailed
		record.Candidate.Error = err.Error()
		_ = s.Store.replaceRevision(deliveryID, record)
		_, _ = s.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryRevisionFailed", map[string]any{"round": round, "error": err.Error()})
		return record, err
	}
	diffArtifact, err := s.Store.PublishArtifact(deliveryID, ArtifactMeta{
		Kind:       "revision_patch",
		NodeID:     node.ID,
		Producer:   candidate.ID,
		BaseCommit: candidate.BaseCommit,
		TreeHash:   result.TreeHash,
		MediaType:  "text/x-diff",
	}, result.Diff)
	if err != nil {
		return record, err
	}
	resultArtifact, err := s.Store.PublishArtifact(deliveryID, ArtifactMeta{
		Kind:       "revision_result",
		NodeID:     node.ID,
		Producer:   candidate.ID,
		BaseCommit: candidate.BaseCommit,
		TreeHash:   result.TreeHash,
		MediaType:  "application/json",
	}, result.Result)
	if err != nil {
		return record, err
	}
	record.Status = RevisionPassed
	record.Error = ""
	record.Candidate.Status = CandidateSelected
	record.Candidate.Commit = result.Commit
	record.Candidate.TreeHash = result.TreeHash
	record.Candidate.ActualWrites = result.ActualWrites
	record.Candidate.DiffBytes = len(result.Diff)
	record.Candidate.DiffArtifact = diffArtifact.ID
	record.Candidate.ResultArtifact = resultArtifact.ID
	record.Candidate.Summary = result.Summary
	record.Candidate.Turns = result.Turns
	record.Candidate.Tokens = result.Tokens
	record.Candidate.DurationMS = result.DurationMS
	if err := s.Store.replaceRevision(deliveryID, record); err != nil {
		return record, err
	}
	if _, err := s.Store.Transition(deliveryID, StatusIntegrating, "DeliveryRevisionFinished", map[string]any{
		"round":  round,
		"commit": result.Commit,
	}); err != nil {
		return record, err
	}
	return record, nil
}

func (s Store) SaveRevisions(deliveryID string, state RevisionState) error {
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return errors.New("revision state identity or schema mismatch")
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	return s.writeJSON(filepath.Join(s.deliveryDir(deliveryID), "revisions.json"), state)
}

func (s Store) LoadRevisions(deliveryID string) (RevisionState, error) {
	var state RevisionState
	if err := readJSON(filepath.Join(s.deliveryDir(deliveryID), "revisions.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RevisionState{SchemaVersion: SchemaVersion, DeliveryID: deliveryID}, nil
		}
		return RevisionState{}, err
	}
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return RevisionState{}, errors.New("revision state identity or schema mismatch")
	}
	return state, nil
}

func (s Store) RevisionCommits(deliveryID string) ([]string, error) {
	state, err := s.LoadRevisions(deliveryID)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, record := range state.Records {
		if record.Status == RevisionPassed && record.Candidate.Commit != "" {
			commits = append(commits, record.Candidate.Commit)
		}
	}
	return commits, nil
}

func (s Store) replaceRevision(deliveryID string, record RevisionRecord) error {
	state, err := s.LoadRevisions(deliveryID)
	if err != nil {
		return err
	}
	found := false
	for index := range state.Records {
		if state.Records[index].Round == record.Round {
			state.Records[index] = record
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("revision round %d not found", record.Round)
	}
	state.UpdatedAt = time.Now().UTC()
	return s.SaveRevisions(deliveryID, state)
}

func buildRevisionNode(round int, contract AcceptanceContract, findings []Finding) (TaskNode, []string, error) {
	writeSet := map[string]bool{}
	criteria := map[string]bool{}
	var descriptions []string
	var findingIDs []string
	for _, finding := range findings {
		findingIDs = append(findingIDs, finding.ID)
		if finding.CriterionID != "" {
			criteria[finding.CriterionID] = true
		}
		if finding.File != "" {
			if !pathAllowedByContract(finding.File, contract) {
				return TaskNode{}, nil, fmt.Errorf("finding path %q is outside contract scope", finding.File)
			}
			writeSet[finding.File] = true
		}
		description := finding.Claim
		if finding.FixHint != "" {
			description += " Fix: " + finding.FixHint
		}
		descriptions = append(descriptions, description)
	}
	if len(writeSet) == 0 {
		return TaskNode{}, nil, errors.New("blocking findings do not identify a safe revision write set")
	}
	writes := mapKeys(writeSet)
	criterionIDs := mapKeys(criteria)
	sort.Strings(findingIDs)
	return TaskNode{
		ID:             fmt.Sprintf("revision-%d", round),
		Title:          fmt.Sprintf("Resolve verifier findings (round %d)", round),
		Objective:      strings.Join(descriptions, "\n"),
		Role:           RoleRevisionBuilder,
		Status:         NodeReady,
		ReadSet:        append([]string(nil), writes...),
		DeclaredWrites: writes,
		Criteria:       criterionIDs,
		Risk:           RiskHigh,
		CandidateCount: 1,
		Budget:         DefaultNodeBudget(),
	}, findingIDs, nil
}

func openBlockingFindings(findings []Finding) []Finding {
	var result []Finding
	for _, finding := range findings {
		if finding.Status == FindingOpen && (finding.Severity == SeverityHigh || finding.Severity == SeverityCritical) {
			result = append(result, finding)
		}
	}
	return result
}

func pathAllowedByContract(path string, contract AcceptanceContract) bool {
	for _, forbidden := range contract.ForbiddenScope {
		if repoPatternMatches(forbidden, path) {
			return false
		}
	}
	for _, allowed := range contract.AllowedScope {
		if repoPatternMatches(allowed, path) {
			return true
		}
	}
	return false
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
