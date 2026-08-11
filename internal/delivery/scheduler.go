package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Scheduler struct {
	Store       Store
	LeaseTTL    time.Duration
	MaxParallel int
	OwnerID     string
}

type nodeRunOutcome struct {
	NodeID string
	Err    error
}

func (s Scheduler) Run(ctx context.Context, deliveryID string, worker NodeWorker) (RuntimeState, error) {
	if worker == nil {
		return RuntimeState{}, errors.New("delivery node worker is required")
	}
	item, err := s.Store.Load(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	if item.Status != StatusPlanned && item.Status != StatusRunning {
		return RuntimeState{}, fmt.Errorf("delivery %q cannot run from status %s", deliveryID, item.Status)
	}
	head, dirty, err := RepositoryState(ctx, item.ProjectRoot)
	if err != nil {
		return RuntimeState{}, err
	}
	if head != item.BaseCommit {
		return RuntimeState{}, fmt.Errorf("delivery base commit changed: planned %s, current %s", item.BaseCommit, head)
	}
	if dirty {
		return RuntimeState{}, errors.New("main worktree has uncommitted changes; commit or stash them before delivery run")
	}
	contract, graph, err := s.Store.LoadPlan(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	runtime, err := s.Store.InitializeRuntime(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	if _, err := s.Store.RecoverExpiredLeases(deliveryID); err != nil {
		return RuntimeState{}, err
	}
	if item.Status == StatusPlanned {
		if _, err := s.Store.Transition(deliveryID, StatusRunning, "DeliveryStarted", nil); err != nil {
			return RuntimeState{}, err
		}
	}
	parallel := s.MaxParallel
	if parallel <= 0 || parallel > item.Budget.MaxParallel {
		parallel = item.Budget.MaxParallel
	}
	if parallel <= 0 {
		parallel = 1
	}
	nodeByID := map[string]TaskNode{}
	for _, node := range graph.Nodes {
		nodeByID[node.ID] = node
	}

	for {
		if err := ctx.Err(); err != nil {
			return runtime, err
		}
		runtime, err = s.Store.LoadRuntime(deliveryID)
		if err != nil {
			return RuntimeState{}, err
		}
		if RuntimeComplete(runtime) {
			if _, err := s.Store.Transition(deliveryID, StatusIntegrating, "DeliveryBuildersFinished", nil); err != nil {
				return runtime, err
			}
			return runtime, nil
		}
		if failedID, failure := failedRuntimeNode(runtime); failure != nil {
			_, _ = s.Store.Fail(deliveryID, fmt.Errorf("node %s: %w", failedID, failure))
			return runtime, fmt.Errorf("delivery node %s failed: %w", failedID, failure)
		}
		ready := ReadyNodeIDs(runtime)
		if len(ready) == 0 {
			if runningRuntimeNodes(runtime) > 0 {
				return runtime, errors.New("delivery has running nodes owned by another scheduler")
			}
			return runtime, errors.New("delivery graph has no runnable nodes")
		}
		if len(ready) > parallel {
			ready = ready[:parallel]
		}
		batchCtx, cancel := context.WithCancel(ctx)
		outcomes := make(chan nodeRunOutcome, len(ready))
		var workers sync.WaitGroup
		for _, nodeID := range ready {
			node := nodeByID[nodeID]
			workers.Add(1)
			go func() {
				defer workers.Done()
				err := s.runNode(batchCtx, item, contract, node, worker)
				outcomes <- nodeRunOutcome{NodeID: node.ID, Err: err}
				if err != nil {
					cancel()
				}
			}()
		}
		workers.Wait()
		cancel()
		close(outcomes)
		for outcome := range outcomes {
			if outcome.Err != nil {
				_, _ = s.Store.FailNode(deliveryID, outcome.NodeID, outcome.Err)
				_, _ = s.Store.Fail(deliveryID, fmt.Errorf("node %s: %w", outcome.NodeID, outcome.Err))
				return runtime, outcome.Err
			}
		}
	}
}

func (s Scheduler) runNode(ctx context.Context, item Delivery, contract AcceptanceContract, node TaskNode, worker NodeWorker) error {
	ownerID := strings.TrimSpace(s.OwnerID)
	if ownerID == "" {
		ownerID = fmt.Sprintf("scheduler-%d", os.Getpid())
	}
	ttl := s.LeaseTTL
	if ttl <= 0 {
		ttl = 45 * time.Second
	}
	runtime, err := s.Store.LoadRuntime(item.ID)
	if err != nil {
		return err
	}
	dependencyCommits, err := selectedDependencyCommits(node, runtime)
	if err != nil {
		return err
	}
	candidates := make([]Candidate, 0, node.CandidateCount)
	for index := 1; index <= node.CandidateCount; index++ {
		candidateID := fmt.Sprintf("%s-c%d", node.ID, index)
		candidates = append(candidates, Candidate{
			ID:                candidateID,
			NodeID:            node.ID,
			Status:            CandidatePending,
			BaseCommit:        item.BaseCommit,
			DependencyCommits: append([]string(nil), dependencyCommits...),
			Branch:            fmt.Sprintf("cohort/delivery/%s/%s", item.ID, candidateID),
			WorktreePath: filepath.Join(
				s.Store.WorktreeDir,
				item.ID,
				"builders",
				node.ID,
				candidateID,
			),
		})
	}
	claimed, err := s.Store.ClaimNode(item.ID, node.ID, ownerID, os.Getpid(), candidates, ttl)
	if err != nil {
		return err
	}
	nodeCtx, cancel := context.WithTimeout(ctx, time.Duration(node.Budget.MaxDurationSecond)*time.Second)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	go s.heartbeat(nodeCtx, item.ID, node.ID, ownerID, ttl, stopHeartbeat)
	defer close(stopHeartbeat)

	var selected string
	var failures []string
	for _, candidate := range claimed.Candidates {
		if err := s.Store.SetCandidateRunning(item.ID, node.ID, candidate.ID); err != nil {
			return err
		}
		startedAt := time.Now()
		result, runErr := worker(nodeCtx, item, contract, node, candidate)
		candidate.UpdatedAt = time.Now().UTC()
		candidate.DurationMS = time.Since(startedAt).Milliseconds()
		if result.DurationMS > 0 {
			candidate.DurationMS = result.DurationMS
		}
		candidate.Summary = strings.TrimSpace(result.Summary)
		candidate.Turns = result.Turns
		candidate.Tokens = result.Tokens
		candidate.Commit = result.Commit
		candidate.TreeHash = result.TreeHash
		candidate.ActualWrites = append([]string(nil), result.ActualWrites...)
		if runErr != nil {
			candidate.Status = CandidateFailed
			candidate.Error = runErr.Error()
			failures = append(failures, candidate.ID+": "+runErr.Error())
		} else {
			if err := validateActualWrites(node.DeclaredWrites, result.ActualWrites); err != nil {
				candidate.Status = CandidateFailed
				candidate.Error = err.Error()
				failures = append(failures, candidate.ID+": "+err.Error())
			} else if strings.TrimSpace(result.Commit) == "" || strings.TrimSpace(result.TreeHash) == "" {
				candidate.Status = CandidateFailed
				candidate.Error = "worker returned no commit or tree hash"
				failures = append(failures, candidate.ID+": "+candidate.Error)
			} else {
				candidate.Status = CandidatePassed
				diffMeta, err := s.Store.PublishArtifact(item.ID, ArtifactMeta{
					Kind:       "patch",
					NodeID:     node.ID,
					Producer:   candidate.ID,
					BaseCommit: item.BaseCommit,
					TreeHash:   result.TreeHash,
					MediaType:  "text/x-diff",
				}, result.Diff)
				if err != nil {
					return err
				}
				resultPayload := result.Result
				if len(resultPayload) == 0 {
					resultPayload, _ = json.MarshalIndent(result, "", "  ")
				}
				resultMeta, err := s.Store.PublishArtifact(item.ID, ArtifactMeta{
					Kind:       "builder_result",
					NodeID:     node.ID,
					Producer:   candidate.ID,
					BaseCommit: item.BaseCommit,
					TreeHash:   result.TreeHash,
					MediaType:  "application/json",
				}, resultPayload)
				if err != nil {
					return err
				}
				candidate.DiffArtifact = diffMeta.ID
				candidate.ResultArtifact = resultMeta.ID
				if selected == "" {
					selected = candidate.ID
				}
			}
		}
		if err := s.Store.CompleteCandidate(item.ID, node.ID, candidate); err != nil {
			return err
		}
		if selected != "" && node.CandidateCount == 1 {
			break
		}
	}
	if selected == "" {
		return fmt.Errorf("all candidates failed: %s", strings.Join(failures, "; "))
	}
	_, err = s.Store.CompleteNode(item.ID, node.ID, selected)
	return err
}

func (s Scheduler) heartbeat(ctx context.Context, deliveryID string, nodeID string, ownerID string, ttl time.Duration, stop <-chan struct{}) {
	interval := ttl / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			_ = s.Store.HeartbeatNode(deliveryID, nodeID, ownerID, ttl)
		}
	}
}

func selectedDependencyCommits(node TaskNode, runtime RuntimeState) ([]string, error) {
	commits := make([]string, 0, len(node.Dependencies))
	for _, dependencyID := range node.Dependencies {
		dependency, exists := runtime.Nodes[dependencyID]
		if !exists || dependency.Status != NodePassed || dependency.SelectedID == "" {
			return nil, fmt.Errorf("dependency %q is not complete", dependencyID)
		}
		found := false
		for _, candidate := range dependency.Candidates {
			if candidate.ID == dependency.SelectedID && candidate.Commit != "" {
				commits = append(commits, candidate.Commit)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("dependency %q has no selected commit", dependencyID)
		}
	}
	return commits, nil
}

func validateActualWrites(declared []string, actual []string) error {
	if len(actual) == 0 {
		return errors.New("builder produced no changed files")
	}
	for _, path := range actual {
		allowed := false
		for _, pattern := range declared {
			if repoPatternMatches(pattern, path) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("actual write %q is outside declared_writes", path)
		}
	}
	return nil
}

func repoPatternMatches(pattern string, path string) bool {
	pattern = filepath.ToSlash(filepath.Clean(strings.TrimSpace(pattern)))
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if pattern == path {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	matched, err := filepath.Match(pattern, path)
	return err == nil && matched
}

func failedRuntimeNode(runtime RuntimeState) (string, error) {
	for id, node := range runtime.Nodes {
		if node.Status == NodeFailed {
			return id, errors.New(node.LastError)
		}
	}
	return "", nil
}

func runningRuntimeNodes(runtime RuntimeState) int {
	count := 0
	for _, node := range runtime.Nodes {
		if node.Status == NodeRunning {
			count++
		}
	}
	return count
}
