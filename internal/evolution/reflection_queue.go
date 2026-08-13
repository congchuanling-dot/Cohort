package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	reflectionQueueSchemaVersion = 1
	reflectionDirName            = "reflection"
	reflectionQueueDirName       = "queue"
	reflectionPendingDirName     = "pending"
	reflectionRunningDirName     = "running"
	reflectionDoneDirName        = "done"
	reflectionDeadDirName        = "dead"
	reflectionStateFileName      = "state.json"
	reflectionRunsFileName       = "runs.jsonl"
	reflectionWorkerLockName     = "worker.lock"

	defaultReflectionMaxAttempts = 3
	defaultReflectionLease       = 10 * time.Minute
	defaultReflectionDoneLimit   = 200
	defaultReflectionStateLimit  = 5000
	maxReflectionErrorRunes      = 500
)

// ReflectionTrigger 是 SessionEnd Hook 写入持久队列的最小任务信封。
// 它只保存定位反思数据所需的路径和水位，不复制任何对话或工具正文。
type ReflectionTrigger struct {
	SchemaVersion   int       `json:"schema_version"`
	ID              string    `json:"id"`
	DedupeKey       string    `json:"dedupe_key"`
	Kind            string    `json:"kind,omitempty"`
	ProjectRoot     string    `json:"project_root"`
	MemoryWorkspace string    `json:"memory_workspace"`
	SessionRoot     string    `json:"session_root"`
	DeliveryPath    string    `json:"delivery_path,omitempty"`
	SessionID       string    `json:"session_id"`
	RunID           string    `json:"run_id,omitempty"`
	HistoryLen      int       `json:"history_len"`
	RunStatus       string    `json:"run_status,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	AvailableAt     time.Time `json:"available_at"`
	Attempt         int       `json:"attempt"`
	MaxAttempts     int       `json:"max_attempts"`
	ClaimedAt       time.Time `json:"claimed_at,omitempty"`
	LeaseUntil      time.Time `json:"lease_until,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

// ReflectionSessionState 保存已经成功处理的 session history 水位。
type ReflectionSessionState struct {
	HistoryLen  int       `json:"history_len"`
	ProcessedAt time.Time `json:"processed_at"`
}

// ReflectionTaskState 保存任务冷却时间和尚未触发该任务的新 run 数量。
type ReflectionTaskState struct {
	LastRunAt time.Time `json:"last_run_at,omitempty"`
	RunsSince int       `json:"runs_since,omitempty"`
}

// ReflectionState 是队列消费成功后原子更新的持久状态。
type ReflectionState struct {
	SchemaVersion int                               `json:"schema_version"`
	Sessions      map[string]ReflectionSessionState `json:"sessions"`
	Tasks         map[string]ReflectionTaskState    `json:"tasks"`
	UpdatedAt     time.Time                         `json:"updated_at,omitempty"`
}

// ReflectionQueueStatus 是 CLI 和 doctor 使用的轻量队列状态。
type ReflectionQueueStatus struct {
	Root          string    `json:"root"`
	Pending       int       `json:"pending"`
	Running       int       `json:"running"`
	Done          int       `json:"done"`
	Dead          int       `json:"dead"`
	SessionStates int       `json:"session_states"`
	LastUpdatedAt time.Time `json:"last_updated_at,omitempty"`
	OldestPending time.Time `json:"oldest_pending,omitempty"`
	NextAvailable time.Time `json:"next_available,omitempty"`
	LastDeadError string    `json:"last_dead_error,omitempty"`
	LastDeadJobID string    `json:"last_dead_job_id,omitempty"`
}

// ReflectionQueueItem 是控制面只读枚举使用的任务及其当前队列状态。
type ReflectionQueueItem struct {
	Trigger ReflectionTrigger `json:"trigger"`
	Status  string            `json:"status"`
}

// ReflectionRunRecord 记录一次批量反思的结果，不包含输入正文。
type ReflectionRunRecord struct {
	Time        time.Time `json:"time"`
	Status      string    `json:"status"`
	TriggerIDs  []string  `json:"trigger_ids,omitempty"`
	Tasks       []string  `json:"tasks,omitempty"`
	DurationMS  int64     `json:"duration_ms"`
	Error       string    `json:"error,omitempty"`
	SessionRoot string    `json:"session_root,omitempty"`
}

// ReflectionQueue 使用目录和原子 rename 实现跨进程可恢复的本地队列。
type ReflectionQueue struct {
	ProjectRoot string
	RootDir     string
	now         func() time.Time
}

func NewReflectionQueue(projectRoot string) ReflectionQueue {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	projectRoot = canonicalExistingPath(projectRoot)
	if gitRoot := findGitRoot(projectRoot); gitRoot != "" {
		projectRoot = canonicalExistingPath(gitRoot)
	}
	return ReflectionQueue{
		ProjectRoot: projectRoot,
		RootDir:     filepath.Join(projectRoot, ".cohort", reflectionDirName),
		now:         time.Now,
	}
}

func (q ReflectionQueue) pendingDir() string {
	return filepath.Join(q.RootDir, reflectionQueueDirName, reflectionPendingDirName)
}

func (q ReflectionQueue) runningDir() string {
	return filepath.Join(q.RootDir, reflectionQueueDirName, reflectionRunningDirName)
}

func (q ReflectionQueue) doneDir() string {
	return filepath.Join(q.RootDir, reflectionQueueDirName, reflectionDoneDirName)
}

func (q ReflectionQueue) deadDir() string {
	return filepath.Join(q.RootDir, reflectionQueueDirName, reflectionDeadDirName)
}

func (q ReflectionQueue) statePath() string {
	return filepath.Join(q.RootDir, reflectionStateFileName)
}

func (q ReflectionQueue) runsPath() string {
	return filepath.Join(q.RootDir, reflectionRunsFileName)
}

func (q ReflectionQueue) workerLockPath() string {
	return filepath.Join(q.RootDir, reflectionWorkerLockName)
}

func (q ReflectionQueue) nowUTC() time.Time {
	if q.now != nil {
		return q.now().UTC()
	}
	return time.Now().UTC()
}

func (q ReflectionQueue) Ensure() error {
	for _, dir := range []string{q.pendingDir(), q.runningDir(), q.doneDir(), q.deadDir()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// Enqueue 将 trigger 持久化到 pending。返回 false 表示相同水位已经入队或处理。
func (q ReflectionQueue) Enqueue(trigger ReflectionTrigger) (ReflectionTrigger, bool, error) {
	if err := q.Ensure(); err != nil {
		return ReflectionTrigger{}, false, err
	}
	if err := q.normalizeTrigger(&trigger); err != nil {
		return ReflectionTrigger{}, false, err
	}
	state, err := q.LoadState()
	if err != nil {
		return ReflectionTrigger{}, false, err
	}
	if watermark := state.Sessions[trigger.SessionID].HistoryLen; watermark >= trigger.HistoryLen {
		return trigger, false, nil
	}
	for _, dir := range []string{q.pendingDir(), q.runningDir(), q.doneDir(), q.deadDir()} {
		_, statErr := os.Stat(filepath.Join(dir, trigger.ID+".json"))
		if statErr == nil {
			return trigger, false, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return ReflectionTrigger{}, false, statErr
		}
	}
	path := filepath.Join(q.pendingDir(), trigger.ID+".json")
	created, err := writeJSONExclusive(path, trigger)
	if err != nil {
		return ReflectionTrigger{}, false, err
	}
	return trigger, created, nil
}

func (q ReflectionQueue) normalizeTrigger(trigger *ReflectionTrigger) error {
	if trigger == nil {
		return errors.New("reflection trigger is nil")
	}
	trigger.ProjectRoot = canonicalExistingPath(trigger.ProjectRoot)
	trigger.MemoryWorkspace = filepath.Clean(strings.TrimSpace(trigger.MemoryWorkspace))
	trigger.SessionRoot = filepath.Clean(strings.TrimSpace(trigger.SessionRoot))
	trigger.DeliveryPath = canonicalExistingPath(trigger.DeliveryPath)
	trigger.SessionID = strings.TrimSpace(trigger.SessionID)
	trigger.Kind = strings.TrimSpace(trigger.Kind)
	if trigger.Kind == "" {
		trigger.Kind = "session"
	}
	if trigger.Kind != "session" && trigger.Kind != "delivery" {
		return fmt.Errorf("unsupported reflection trigger kind %q", trigger.Kind)
	}
	if trigger.ProjectRoot == "." || !filepath.IsAbs(trigger.ProjectRoot) {
		return errors.New("reflection project_root must be absolute")
	}
	if trigger.MemoryWorkspace == "." || !filepath.IsAbs(trigger.MemoryWorkspace) {
		return errors.New("reflection memory_workspace must be absolute")
	}
	if trigger.Kind == "session" && (trigger.SessionRoot == "." || !filepath.IsAbs(trigger.SessionRoot)) {
		return errors.New("reflection session_root must be absolute")
	}
	if trigger.Kind == "delivery" && (trigger.DeliveryPath == "." || !filepath.IsAbs(trigger.DeliveryPath)) {
		return errors.New("reflection delivery_path must be absolute")
	}
	if trigger.ProjectRoot != q.ProjectRoot {
		return fmt.Errorf("reflection project_root %q does not match queue root %q", trigger.ProjectRoot, q.ProjectRoot)
	}
	if trigger.Kind == "delivery" {
		relative, err := filepath.Rel(trigger.ProjectRoot, trigger.DeliveryPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("reflection delivery_path must stay inside project root")
		}
	}
	if trigger.SessionID == "" {
		return errors.New("reflection session_id is required")
	}
	if strings.ContainsAny(trigger.SessionID, `/\`) || trigger.SessionID == "." || trigger.SessionID == ".." {
		return errors.New("reflection session_id is invalid")
	}
	if trigger.HistoryLen <= 0 {
		return errors.New("reflection history_len must be positive")
	}
	now := q.nowUTC()
	trigger.SchemaVersion = reflectionQueueSchemaVersion
	if trigger.MaxAttempts <= 0 {
		trigger.MaxAttempts = defaultReflectionMaxAttempts
	}
	if trigger.CreatedAt.IsZero() {
		trigger.CreatedAt = now
	}
	if trigger.AvailableAt.IsZero() {
		trigger.AvailableAt = now
	}
	trigger.DedupeKey = reflectionDedupeKey(trigger.ProjectRoot, trigger.Kind+":"+trigger.SessionID, trigger.HistoryLen)
	trigger.ID = "reflect_" + trigger.DedupeKey[:20]
	trigger.LastError = truncateReflectionError(trigger.LastError)
	return nil
}

func reflectionDedupeKey(projectRoot string, sessionID string, historyLen int) string {
	sum := sha256.Sum256([]byte(projectRoot + "\x00" + sessionID + "\x00" + strconv.Itoa(historyLen)))
	return hex.EncodeToString(sum[:])
}

func canonicalExistingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "."
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func (q ReflectionQueue) LoadState() (ReflectionState, error) {
	state := ReflectionState{
		SchemaVersion: reflectionQueueSchemaVersion,
		Sessions:      map[string]ReflectionSessionState{},
		Tasks:         map[string]ReflectionTaskState{},
	}
	data, err := os.ReadFile(q.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return ReflectionState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return ReflectionState{}, fmt.Errorf("decode reflection state: %w", err)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]ReflectionSessionState{}
	}
	if state.Tasks == nil {
		state.Tasks = map[string]ReflectionTaskState{}
	}
	state.SchemaVersion = reflectionQueueSchemaVersion
	return state, nil
}

func (q ReflectionQueue) saveState(state ReflectionState) error {
	state.SchemaVersion = reflectionQueueSchemaVersion
	state.UpdatedAt = q.nowUTC()
	if state.Sessions == nil {
		state.Sessions = map[string]ReflectionSessionState{}
	}
	if state.Tasks == nil {
		state.Tasks = map[string]ReflectionTaskState{}
	}
	return writeJSONAtomic(q.statePath(), state)
}

// ClaimDueBatch 原子 claim 当前到期任务，并写入 lease。
func (q ReflectionQueue) ClaimDueBatch(limit int, lease time.Duration) ([]ReflectionTrigger, error) {
	if err := q.Ensure(); err != nil {
		return nil, err
	}
	if lease <= 0 {
		lease = defaultReflectionLease
	}
	if limit <= 0 {
		limit = 100
	}
	if _, err := q.RecoverExpiredClaims(); err != nil {
		return nil, err
	}
	state, err := q.LoadState()
	if err != nil {
		return nil, err
	}
	items, err := q.loadDir(q.pendingDir())
	if err != nil {
		return nil, err
	}
	now := q.nowUTC()
	var claimed []ReflectionTrigger
	for _, item := range items {
		if len(claimed) >= limit || item.AvailableAt.After(now) {
			continue
		}
		source := filepath.Join(q.pendingDir(), item.ID+".json")
		if state.Sessions[item.SessionID].HistoryLen >= item.HistoryLen {
			_ = os.Rename(source, filepath.Join(q.doneDir(), item.ID+".json"))
			continue
		}
		target := filepath.Join(q.runningDir(), item.ID+".json")
		if err := os.Rename(source, target); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return claimed, err
		}
		item.Attempt++
		item.ClaimedAt = now
		item.LeaseUntil = now.Add(lease)
		if err := writeJSONAtomic(target, item); err != nil {
			_ = os.Rename(target, source)
			return claimed, err
		}
		claimed = append(claimed, item)
	}
	return claimed, nil
}

// RecoverExpiredClaims 将无 lease 或 lease 已过期的 running 任务放回 pending。
func (q ReflectionQueue) RecoverExpiredClaims() (int, error) {
	if err := q.Ensure(); err != nil {
		return 0, err
	}
	items, err := q.loadDir(q.runningDir())
	if err != nil {
		return 0, err
	}
	now := q.nowUTC()
	recovered := 0
	for _, item := range items {
		if !item.LeaseUntil.IsZero() && item.LeaseUntil.After(now) {
			continue
		}
		item.ClaimedAt = time.Time{}
		item.LeaseUntil = time.Time{}
		item.AvailableAt = now
		source := filepath.Join(q.runningDir(), item.ID+".json")
		target := filepath.Join(q.pendingDir(), item.ID+".json")
		if err := writeJSONAtomic(source, item); err != nil {
			return recovered, err
		}
		if err := os.Rename(source, target); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

// CompleteBatch 推进 session watermark、更新 Planner 状态并归档 trigger。
func (q ReflectionQueue) CompleteBatch(
	items []ReflectionTrigger,
	ranTasks []string,
	allPlannedTasks []string,
	startedAt time.Time,
) error {
	state, err := q.LoadState()
	if err != nil {
		return err
	}
	now := q.nowUTC()
	for _, item := range items {
		current := state.Sessions[item.SessionID]
		if item.HistoryLen > current.HistoryLen {
			state.Sessions[item.SessionID] = ReflectionSessionState{
				HistoryLen:  item.HistoryLen,
				ProcessedAt: now,
			}
		}
	}
	ran := make(map[string]bool, len(ranTasks))
	for _, task := range ranTasks {
		ran[task] = true
	}
	for _, task := range allPlannedTasks {
		taskState := state.Tasks[task]
		if ran[task] {
			taskState.LastRunAt = now
			taskState.RunsSince = 0
		} else {
			taskState.RunsSince += len(items)
		}
		state.Tasks[task] = taskState
	}
	pruneReflectionSessionState(&state, defaultReflectionStateLimit)
	if err := q.saveState(state); err != nil {
		return err
	}
	for _, item := range items {
		source := filepath.Join(q.runningDir(), item.ID+".json")
		target := filepath.Join(q.doneDir(), item.ID+".json")
		if err := os.Rename(source, target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := q.appendRun(ReflectionRunRecord{
		Time:        now,
		Status:      "success",
		TriggerIDs:  triggerIDs(items),
		Tasks:       append([]string(nil), ranTasks...),
		DurationMS:  now.Sub(startedAt).Milliseconds(),
		SessionRoot: commonSessionRoot(items),
	}); err != nil {
		return err
	}
	return q.pruneDone(defaultReflectionDoneLimit)
}

func pruneReflectionSessionState(state *ReflectionState, limit int) {
	if state == nil || limit <= 0 || len(state.Sessions) <= limit {
		return
	}
	type sessionWatermark struct {
		id          string
		processedAt time.Time
	}
	items := make([]sessionWatermark, 0, len(state.Sessions))
	for id, watermark := range state.Sessions {
		items = append(items, sessionWatermark{id: id, processedAt: watermark.ProcessedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].processedAt.Equal(items[j].processedAt) {
			return items[i].id < items[j].id
		}
		return items[i].processedAt.After(items[j].processedAt)
	})
	for _, item := range items[limit:] {
		delete(state.Sessions, item.id)
	}
}

// FailBatch 将失败任务按退避策略重试，耗尽后进入 dead。
func (q ReflectionQueue) FailBatch(items []ReflectionTrigger, runErr error, startedAt time.Time) error {
	now := q.nowUTC()
	summary := truncateReflectionError(errorText(runErr))
	for _, item := range items {
		item.LastError = summary
		item.ClaimedAt = time.Time{}
		item.LeaseUntil = time.Time{}
		source := filepath.Join(q.runningDir(), item.ID+".json")
		targetDir := q.pendingDir()
		if item.Attempt >= item.MaxAttempts {
			targetDir = q.deadDir()
		} else {
			item.AvailableAt = now.Add(reflectionRetryDelay(item.Attempt))
		}
		if err := writeJSONAtomic(source, item); err != nil {
			return err
		}
		if err := os.Rename(source, filepath.Join(targetDir, item.ID+".json")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return q.appendRun(ReflectionRunRecord{
		Time:        now,
		Status:      "error",
		TriggerIDs:  triggerIDs(items),
		DurationMS:  now.Sub(startedAt).Milliseconds(),
		Error:       summary,
		SessionRoot: commonSessionRoot(items),
	})
}

func reflectionRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// Retry 将 dead trigger 显式放回 pending，并重置尝试次数。
func (q ReflectionQueue) Retry(id string) (ReflectionTrigger, error) {
	id = strings.TrimSpace(strings.TrimSuffix(id, ".json"))
	if id == "" || strings.ContainsAny(id, `/\`) {
		return ReflectionTrigger{}, errors.New("reflection job id is invalid")
	}
	path := filepath.Join(q.deadDir(), id+".json")
	item, err := readReflectionTrigger(path)
	if err != nil {
		return ReflectionTrigger{}, err
	}
	item.Attempt = 0
	item.LastError = ""
	item.ClaimedAt = time.Time{}
	item.LeaseUntil = time.Time{}
	item.AvailableAt = q.nowUTC()
	if err := writeJSONAtomic(path, item); err != nil {
		return ReflectionTrigger{}, err
	}
	if err := os.Rename(path, filepath.Join(q.pendingDir(), id+".json")); err != nil {
		return ReflectionTrigger{}, err
	}
	return item, nil
}

func (q ReflectionQueue) Status() (ReflectionQueueStatus, error) {
	if err := q.Ensure(); err != nil {
		return ReflectionQueueStatus{}, err
	}
	status := ReflectionQueueStatus{Root: q.RootDir}
	state, err := q.LoadState()
	if err != nil {
		return status, err
	}
	status.SessionStates = len(state.Sessions)
	status.LastUpdatedAt = state.UpdatedAt
	for _, descriptor := range []struct {
		dir    string
		assign func(int)
	}{
		{q.pendingDir(), func(value int) { status.Pending = value }},
		{q.runningDir(), func(value int) { status.Running = value }},
		{q.doneDir(), func(value int) { status.Done = value }},
		{q.deadDir(), func(value int) { status.Dead = value }},
	} {
		items, err := q.loadDir(descriptor.dir)
		if err != nil {
			return status, err
		}
		descriptor.assign(len(items))
		if descriptor.dir == q.pendingDir() {
			for _, item := range items {
				if status.OldestPending.IsZero() || item.CreatedAt.Before(status.OldestPending) {
					status.OldestPending = item.CreatedAt
				}
				if status.NextAvailable.IsZero() || item.AvailableAt.Before(status.NextAvailable) {
					status.NextAvailable = item.AvailableAt
				}
			}
		}
		if descriptor.dir == q.deadDir() && len(items) > 0 {
			last := items[len(items)-1]
			status.LastDeadJobID = last.ID
			status.LastDeadError = last.LastError
		}
	}
	return status, nil
}

// ListJobs 枚举当前持久队列。它不 claim、不恢复 lease，也不修改任何任务。
func (q ReflectionQueue) ListJobs() ([]ReflectionQueueItem, error) {
	if err := q.Ensure(); err != nil {
		return nil, err
	}
	descriptors := []struct {
		status string
		dir    string
	}{
		{status: reflectionPendingDirName, dir: q.pendingDir()},
		{status: reflectionRunningDirName, dir: q.runningDir()},
		{status: reflectionDoneDirName, dir: q.doneDir()},
		{status: reflectionDeadDirName, dir: q.deadDir()},
	}
	var result []ReflectionQueueItem
	for _, descriptor := range descriptors {
		items, err := q.loadDir(descriptor.dir)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			result = append(result, ReflectionQueueItem{Trigger: item, Status: descriptor.status})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Trigger
		right := result[j].Trigger
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	return result, nil
}

func (q ReflectionQueue) loadDir(dir string) ([]ReflectionTrigger, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]ReflectionTrigger, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		item, err := readReflectionTrigger(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if err := q.validateStoredTrigger(item, strings.TrimSuffix(entry.Name(), ".json")); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AvailableAt.Equal(items[j].AvailableAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].AvailableAt.Before(items[j].AvailableAt)
	})
	return items, nil
}

func (q ReflectionQueue) validateStoredTrigger(item ReflectionTrigger, fileID string) error {
	if item.SchemaVersion != reflectionQueueSchemaVersion {
		return fmt.Errorf("reflection trigger %q has unsupported schema version %d", fileID, item.SchemaVersion)
	}
	if item.ID == "" || item.ID != fileID {
		return fmt.Errorf("reflection trigger file %q does not match id %q", fileID, item.ID)
	}
	if canonicalExistingPath(item.ProjectRoot) != q.ProjectRoot {
		return fmt.Errorf("reflection trigger %q has unexpected project root", item.ID)
	}
	if !filepath.IsAbs(item.MemoryWorkspace) ||
		(item.Kind != "delivery" && !filepath.IsAbs(item.SessionRoot)) ||
		(item.Kind == "delivery" && !filepath.IsAbs(item.DeliveryPath)) {
		return fmt.Errorf("reflection trigger %q contains a non-absolute data path", item.ID)
	}
	if item.SessionID == "" || strings.ContainsAny(item.SessionID, `/\`) || item.HistoryLen <= 0 {
		return fmt.Errorf("reflection trigger %q contains invalid session metadata", item.ID)
	}
	sessionKey := item.SessionID
	if item.Kind != "" {
		sessionKey = item.Kind + ":" + item.SessionID
	}
	dedupeKey := reflectionDedupeKey(item.ProjectRoot, sessionKey, item.HistoryLen)
	if item.DedupeKey != dedupeKey || item.ID != "reflect_"+dedupeKey[:20] {
		return fmt.Errorf("reflection trigger %q failed integrity validation", item.ID)
	}
	if item.MaxAttempts <= 0 {
		return fmt.Errorf("reflection trigger %q has invalid max_attempts", item.ID)
	}
	return nil
}

func readReflectionTrigger(path string) (ReflectionTrigger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ReflectionTrigger{}, err
	}
	var item ReflectionTrigger
	if err := json.Unmarshal(data, &item); err != nil {
		return ReflectionTrigger{}, fmt.Errorf("decode reflection trigger %s: %w", path, err)
	}
	return item, nil
}

// AcquireWorkerLock 保证一个项目同一时间只有一个反思 Worker 写产物。
func (q ReflectionQueue) AcquireWorkerLock() (func(), error) {
	if err := q.Ensure(); err != nil {
		return nil, err
	}
	path := q.workerLockPath()
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && reflectionProcessRunning(pid) {
			return nil, fmt.Errorf("reflection worker is already running with pid %d", pid)
		}
		if info, statErr := os.Stat(path); parseErr != nil && statErr == nil && q.nowUTC().Sub(info.ModTime()) < 5*time.Second {
			return nil, errors.New("reflection worker lock is being initialized")
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, removeErr
		}
	}
	return nil, errors.New("unable to acquire reflection worker lock")
}

func reflectionProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func (q ReflectionQueue) appendRun(record ReflectionRunRecord) error {
	if err := q.Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(q.runsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (q ReflectionQueue) pruneDone(limit int) error {
	if limit <= 0 {
		return nil
	}
	items, err := q.loadDir(q.doneDir())
	if err != nil {
		return err
	}
	if len(items) <= limit {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	for _, item := range items[:len(items)-limit] {
		if err := os.Remove(filepath.Join(q.doneDir(), item.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeJSONExclusive(path string, value any) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reflection-trigger-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func triggerIDs(items []ReflectionTrigger) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	return ids
}

func commonSessionRoot(items []ReflectionTrigger) string {
	if len(items) == 0 {
		return ""
	}
	root := items[0].SessionRoot
	for _, item := range items[1:] {
		if item.SessionRoot != root {
			return ""
		}
	}
	return root
}

func truncateReflectionError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxReflectionErrorRunes {
		return value
	}
	return string(runes[:maxReflectionErrorRunes])
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
