package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

const runtimeFile = "runtime.json"

func (s Store) SaveWorkerResult(deliveryID string, nodeID string, candidateID string, result WorkerResult) error {
	if err := validateStableID(nodeID); err != nil {
		return err
	}
	if err := validateStableID(candidateID); err != nil {
		return err
	}
	path := filepath.Join(s.deliveryDir(deliveryID), "nodes", nodeID, candidateID, "worker-result.json")
	return s.writeJSON(path, result)
}

func (s Store) LoadWorkerResult(deliveryID string, nodeID string, candidateID string) (WorkerResult, error) {
	if err := validateStableID(nodeID); err != nil {
		return WorkerResult{}, err
	}
	if err := validateStableID(candidateID); err != nil {
		return WorkerResult{}, err
	}
	var result WorkerResult
	path := filepath.Join(s.deliveryDir(deliveryID), "nodes", nodeID, candidateID, "worker-result.json")
	if err := readJSON(path, &result); err != nil {
		return WorkerResult{}, err
	}
	return result, nil
}

func (s Store) InitializeRuntime(deliveryID string) (RuntimeState, error) {
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	defer release()
	if runtime, err := s.loadRuntimeUnlocked(deliveryID); err == nil {
		return runtime, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return RuntimeState{}, err
	}
	_, graph, err := s.LoadPlan(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	now := s.now()
	runtime := RuntimeState{
		SchemaVersion: SchemaVersion,
		DeliveryID:    deliveryID,
		Nodes:         make(map[string]NodeRuntime, len(graph.Nodes)),
		UpdatedAt:     now,
	}
	for _, node := range graph.Nodes {
		status := NodePending
		if len(node.Dependencies) == 0 {
			status = NodeReady
		}
		runtime.Nodes[node.ID] = NodeRuntime{NodeID: node.ID, Status: status}
	}
	if err := s.writeJSON(s.runtimePath(deliveryID), runtime); err != nil {
		return RuntimeState{}, err
	}
	return runtime, nil
}

func (s Store) LoadRuntime(deliveryID string) (RuntimeState, error) {
	if err := validateDeliveryID(deliveryID); err != nil {
		return RuntimeState{}, err
	}
	return s.loadRuntimeUnlocked(deliveryID)
}

func (s Store) ClaimNode(deliveryID string, nodeID string, ownerID string, ownerPID int, candidates []Candidate, ttl time.Duration) (NodeRuntime, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return NodeRuntime{}, err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return NodeRuntime{}, err
	}
	node, exists := runtime.Nodes[nodeID]
	if !exists {
		return NodeRuntime{}, fmt.Errorf("delivery node %q not found", nodeID)
	}
	if node.Status != NodeReady && node.Status != NodePending {
		return NodeRuntime{}, fmt.Errorf("delivery node %q cannot be claimed from status %s", nodeID, node.Status)
	}
	now := s.now()
	for index := range candidates {
		candidates[index].NodeID = nodeID
		candidates[index].Status = CandidatePending
		candidates[index].CreatedAt = now
		candidates[index].UpdatedAt = now
	}
	node.Status = NodeRunning
	node.Attempt++
	node.StartedAt = now
	node.FinishedAt = time.Time{}
	node.LastError = ""
	node.Candidates = candidates
	node.SelectedID = ""
	node.Lease = Lease{
		OwnerID:   ownerID,
		OwnerPID:  ownerPID,
		Heartbeat: now,
		ExpiresAt: now.Add(ttl),
	}
	runtime.Nodes[nodeID] = node
	runtime.UpdatedAt = now
	if runtime.StartedAt.IsZero() {
		runtime.StartedAt = now
	}
	if err := s.writeJSON(s.runtimePath(deliveryID), runtime); err != nil {
		return NodeRuntime{}, err
	}
	if err := s.appendEventUnlocked(deliveryID, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(now),
		DeliveryID:    deliveryID,
		NodeID:        nodeID,
		Type:          "DeliveryNodeClaimed",
		Time:          now,
		Data: map[string]any{
			"owner_id":   ownerID,
			"owner_pid":  ownerPID,
			"attempt":    node.Attempt,
			"candidates": len(candidates),
		},
	}); err != nil {
		return NodeRuntime{}, err
	}
	return node, nil
}

func (s Store) HeartbeatNode(deliveryID string, nodeID string, ownerID string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Minute
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return err
	}
	node, exists := runtime.Nodes[nodeID]
	if !exists {
		return fmt.Errorf("delivery node %q not found", nodeID)
	}
	if node.Status != NodeRunning || node.Lease.OwnerID != ownerID {
		return fmt.Errorf("delivery node %q lease is not owned by %q", nodeID, ownerID)
	}
	now := s.now()
	node.Lease.Heartbeat = now
	node.Lease.ExpiresAt = now.Add(ttl)
	runtime.Nodes[nodeID] = node
	runtime.UpdatedAt = now
	return s.writeJSON(s.runtimePath(deliveryID), runtime)
}

func (s Store) SetCandidateRunning(deliveryID string, nodeID string, candidateID string) error {
	return s.updateCandidate(deliveryID, nodeID, candidateID, func(candidate *Candidate, now time.Time) {
		candidate.Status = CandidateRunning
		candidate.UpdatedAt = now
	})
}

func (s Store) CompleteCandidate(deliveryID string, nodeID string, candidate Candidate) error {
	return s.updateCandidate(deliveryID, nodeID, candidate.ID, func(stored *Candidate, now time.Time) {
		candidate.CreatedAt = stored.CreatedAt
		candidate.UpdatedAt = now
		*stored = candidate
	})
}

func (s Store) RejectCandidate(deliveryID string, nodeID string, candidateID string) error {
	return s.updateCandidate(deliveryID, nodeID, candidateID, func(candidate *Candidate, now time.Time) {
		if candidate.Status == CandidatePassed {
			candidate.Status = CandidateRejected
			candidate.UpdatedAt = now
		}
	})
}

func (s Store) CompleteNode(deliveryID string, nodeID string, selectedID string) (RuntimeState, error) {
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	_, graph, err := s.LoadPlan(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	node, exists := runtime.Nodes[nodeID]
	if !exists {
		return RuntimeState{}, fmt.Errorf("delivery node %q not found", nodeID)
	}
	found := false
	now := s.now()
	for index := range node.Candidates {
		if node.Candidates[index].ID != selectedID {
			continue
		}
		if node.Candidates[index].Status != CandidatePassed && node.Candidates[index].Status != CandidateSelected {
			return RuntimeState{}, fmt.Errorf("candidate %q is not passing", selectedID)
		}
		node.Candidates[index].Status = CandidateSelected
		node.Candidates[index].UpdatedAt = now
		found = true
	}
	if !found {
		return RuntimeState{}, fmt.Errorf("candidate %q not found for node %q", selectedID, nodeID)
	}
	node.Status = NodePassed
	node.SelectedID = selectedID
	node.Lease = Lease{}
	node.FinishedAt = now
	node.LastError = ""
	runtime.Nodes[nodeID] = node
	markReadyNodes(&runtime, graph)
	runtime.UpdatedAt = now
	if err := s.writeJSON(s.runtimePath(deliveryID), runtime); err != nil {
		return RuntimeState{}, err
	}
	if err := s.appendEventUnlocked(deliveryID, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(now),
		DeliveryID:    deliveryID,
		NodeID:        nodeID,
		Type:          "DeliveryNodeFinished",
		Time:          now,
		Data:          map[string]any{"status": NodePassed, "selected_candidate": selectedID},
	}); err != nil {
		return RuntimeState{}, err
	}
	return runtime, nil
}

func (s Store) FailNode(deliveryID string, nodeID string, failure error) (RuntimeState, error) {
	if failure == nil {
		failure = errors.New("delivery node failed")
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return RuntimeState{}, err
	}
	node, exists := runtime.Nodes[nodeID]
	if !exists {
		return RuntimeState{}, fmt.Errorf("delivery node %q not found", nodeID)
	}
	now := s.now()
	node.Status = NodeFailed
	node.LastError = failure.Error()
	node.Lease = Lease{}
	node.FinishedAt = now
	runtime.Nodes[nodeID] = node
	for id, candidate := range runtime.Nodes {
		if candidate.Status == NodePending || candidate.Status == NodeReady {
			candidate.Status = NodeBlocked
			candidate.LastError = "dependency delivery node failed"
			runtime.Nodes[id] = candidate
		}
	}
	runtime.UpdatedAt = now
	if err := s.writeJSON(s.runtimePath(deliveryID), runtime); err != nil {
		return RuntimeState{}, err
	}
	if err := s.appendEventUnlocked(deliveryID, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(now),
		DeliveryID:    deliveryID,
		NodeID:        nodeID,
		Type:          "DeliveryNodeFinished",
		Time:          now,
		Data:          map[string]any{"status": NodeFailed, "error": failure.Error()},
	}); err != nil {
		return RuntimeState{}, err
	}
	return runtime, nil
}

func (s Store) RecoverExpiredLeases(deliveryID string) (int, error) {
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return 0, err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return 0, err
	}
	now := s.now()
	recovered := 0
	for id, node := range runtime.Nodes {
		if node.Status != NodeRunning || node.Lease.ExpiresAt.After(now) {
			continue
		}
		if processAlive(node.Lease.OwnerPID) {
			continue
		}
		node.Status = NodeReady
		node.LastError = "recovered expired worker lease"
		node.Lease = Lease{}
		node.Candidates = nil
		node.SelectedID = ""
		runtime.Nodes[id] = node
		recovered++
	}
	if recovered == 0 {
		return 0, nil
	}
	runtime.UpdatedAt = now
	if err := s.writeJSON(s.runtimePath(deliveryID), runtime); err != nil {
		return 0, err
	}
	return recovered, nil
}

func RuntimeComplete(runtime RuntimeState) bool {
	if len(runtime.Nodes) == 0 {
		return false
	}
	for _, node := range runtime.Nodes {
		if node.Status != NodePassed {
			return false
		}
	}
	return true
}

func ReadyNodeIDs(runtime RuntimeState) []string {
	var ids []string
	for id, node := range runtime.Nodes {
		if node.Status == NodeReady {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s Store) updateCandidate(deliveryID string, nodeID string, candidateID string, update func(*Candidate, time.Time)) error {
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	runtime, err := s.loadRuntimeUnlocked(deliveryID)
	if err != nil {
		return err
	}
	node, exists := runtime.Nodes[nodeID]
	if !exists {
		return fmt.Errorf("delivery node %q not found", nodeID)
	}
	for index := range node.Candidates {
		if node.Candidates[index].ID == candidateID {
			now := s.now()
			update(&node.Candidates[index], now)
			runtime.Nodes[nodeID] = node
			runtime.UpdatedAt = now
			return s.writeJSON(s.runtimePath(deliveryID), runtime)
		}
	}
	return fmt.Errorf("candidate %q not found for node %q", candidateID, nodeID)
}

func (s Store) runtimePath(deliveryID string) string {
	return filepath.Join(s.deliveryDir(deliveryID), runtimeFile)
}

func (s Store) loadRuntimeUnlocked(deliveryID string) (RuntimeState, error) {
	var runtime RuntimeState
	if err := readJSON(s.runtimePath(deliveryID), &runtime); err != nil {
		return RuntimeState{}, err
	}
	if runtime.SchemaVersion != SchemaVersion || runtime.DeliveryID != deliveryID {
		return RuntimeState{}, errors.New("delivery runtime identity or schema mismatch")
	}
	if runtime.Nodes == nil {
		runtime.Nodes = map[string]NodeRuntime{}
	}
	return runtime, nil
}

func markReadyNodes(runtime *RuntimeState, graph TaskGraph) {
	for _, planned := range graph.Nodes {
		current := runtime.Nodes[planned.ID]
		if current.Status != NodePending && current.Status != NodeBlocked {
			continue
		}
		ready := true
		for _, dependency := range planned.Dependencies {
			if runtime.Nodes[dependency].Status != NodePassed {
				ready = false
				break
			}
		}
		if ready {
			current.Status = NodeReady
			current.LastError = ""
			runtime.Nodes[planned.ID] = current
		}
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
