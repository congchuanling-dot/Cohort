package computeruse

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNoState 表示还没有可供 computer_find/check 使用的 computer_see 快照。
var ErrNoState = errors.New("computer state cache is empty")

// ErrTargetNotFound 表示 target_id 不存在或已经被清理。
var ErrTargetNotFound = errors.New("computer target not found")

// ErrTargetExpired 表示 target_id 来自旧界面快照，必须重新 see/find。
var ErrTargetExpired = errors.New("computer target expired")

// Store 保存最近的 computer_see 状态和可操作 target_id 映射。
type Store struct {
	mu            sync.Mutex
	states        map[string]ComputerState
	targets       map[string]ComputerTarget
	latestStateID string
	nextState     int64
	nextTarget    int64
	ttl           time.Duration
	now           func() time.Time
}

// NewStore 创建进程内 target cache。
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTargetTTL
	}
	return &Store{
		states:  make(map[string]ComputerState),
		targets: make(map[string]ComputerTarget),
		ttl:     ttl,
		now:     time.Now,
	}
}

// SaveState 写入状态快照，并为没有 ID 的候选目标分配短期 target_id。
func (s *Store) SaveState(state ComputerState) ComputerState {
	if s == nil {
		return state
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.pruneLocked(now)
	s.nextState++
	state.ID = fmt.Sprintf("cs_%d", s.nextState)
	state.CreatedAt = now
	state.ExpiresAt = now.Add(s.ttl)
	for index := range state.Candidates {
		s.nextTarget++
		target := state.Candidates[index]
		if target.ID == "" {
			target.ID = fmt.Sprintf("ct_%d", s.nextTarget)
		}
		target.CreatedAt = now
		target.ExpiresAt = state.ExpiresAt
		state.Candidates[index] = target
		s.targets[target.ID] = target
	}
	s.states[state.ID] = state
	s.latestStateID = state.ID
	return state
}

// LatestState 返回最近一次未过期的 computer_see 状态。
func (s *Store) LatestState() (ComputerState, error) {
	if s == nil {
		return ComputerState{}, ErrNoState
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.pruneLocked(now)
	state, ok := s.states[s.latestStateID]
	if !ok {
		return ComputerState{}, ErrNoState
	}
	if !state.ExpiresAt.After(now) {
		delete(s.states, state.ID)
		if s.latestStateID == state.ID {
			s.latestStateID = ""
		}
		return ComputerState{}, ErrNoState
	}
	return state, nil
}

// Target 返回未过期的缓存目标。
func (s *Store) Target(id string) (ComputerTarget, error) {
	if s == nil {
		return ComputerTarget{}, ErrTargetNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.pruneLocked(now)
	target, ok := s.targets[id]
	if !ok {
		return ComputerTarget{}, ErrTargetNotFound
	}
	if !target.ExpiresAt.After(now) {
		delete(s.targets, id)
		return ComputerTarget{}, ErrTargetExpired
	}
	return target, nil
}

func (s *Store) pruneLocked(now time.Time) {
	for id, state := range s.states {
		if !state.ExpiresAt.After(now) {
			delete(s.states, id)
			if s.latestStateID == id {
				s.latestStateID = ""
			}
		}
	}
	for id, target := range s.targets {
		if !target.ExpiresAt.After(now) {
			delete(s.targets, id)
		}
	}
}
