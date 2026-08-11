package evolution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultReflectionBatchLimit = 100
)

var reflectionPlannerTasks = []string{
	ReflectTaskSessionArchive,
	ReflectTaskToolFailureReport,
	ReflectTaskMemoryQualityReport,
	ReflectTaskMineSOPCandidates,
	ReflectTaskMineSkillCandidates,
}

type ReflectionWorkerConfig struct {
	BatchLimit    int
	LeaseDuration time.Duration
}

// ReflectionDrainResult 是一次前台 drain 或 daemon dispatch 的安全摘要。
type ReflectionDrainResult struct {
	Claimed   int                `json:"claimed"`
	Completed int                `json:"completed"`
	Failed    int                `json:"failed"`
	Tasks     []string           `json:"tasks,omitempty"`
	Results   []ReflectionResult `json:"results,omitempty"`
}

type reflectionExecuteFunc func(context.Context, string, string, string) (ReflectionResult, error)

// ReflectionWorker 消费持久 trigger，并复用现有确定性 ReflectOnce 实现。
type ReflectionWorker struct {
	Queue   ReflectionQueue
	Config  ReflectionWorkerConfig
	Execute reflectionExecuteFunc
	now     func() time.Time
}

func NewReflectionWorker(queue ReflectionQueue, cfg ReflectionWorkerConfig) *ReflectionWorker {
	worker := &ReflectionWorker{
		Queue:  queue,
		Config: cfg,
		now:    time.Now,
	}
	worker.Execute = func(ctx context.Context, task string, memoryWorkspace string, sessionRoot string) (ReflectionResult, error) {
		select {
		case <-ctx.Done():
			return ReflectionResult{}, ctx.Err()
		default:
		}
		return NewManager(memoryWorkspace).ReflectOnce(task, sessionRoot)
	}
	return worker
}

func (w *ReflectionWorker) nowUTC() time.Time {
	if w != nil && w.now != nil {
		return w.now().UTC()
	}
	return time.Now().UTC()
}

// Drain claim 当前 due trigger 并同步处理一批。没有任务时成功返回零值。
func (w *ReflectionWorker) Drain(ctx context.Context) (ReflectionDrainResult, error) {
	if w == nil {
		return ReflectionDrainResult{}, errors.New("reflection worker is nil")
	}
	release, err := w.Queue.AcquireWorkerLock()
	if err != nil {
		return ReflectionDrainResult{}, err
	}
	defer release()

	limit := w.Config.BatchLimit
	if limit <= 0 {
		limit = defaultReflectionBatchLimit
	}
	lease := w.Config.LeaseDuration
	if lease <= 0 {
		lease = defaultReflectionLease
	}
	items, err := w.Queue.ClaimDueBatch(limit, lease)
	if err != nil {
		return ReflectionDrainResult{}, err
	}
	result := ReflectionDrainResult{Claimed: len(items)}
	if len(items) == 0 {
		return result, nil
	}

	groups := groupReflectionTriggers(items)
	for _, group := range groups {
		groupResult, groupErr := w.processGroup(ctx, group)
		result.Completed += groupResult.Completed
		result.Failed += groupResult.Failed
		result.Tasks = append(result.Tasks, groupResult.Tasks...)
		result.Results = append(result.Results, groupResult.Results...)
		if groupErr != nil {
			err = errors.Join(err, groupErr)
		}
	}
	result.Tasks = uniqueSortedStrings(result.Tasks)
	return result, err
}

func (w *ReflectionWorker) processGroup(ctx context.Context, items []ReflectionTrigger) (ReflectionDrainResult, error) {
	result := ReflectionDrainResult{Claimed: len(items)}
	if len(items) == 0 {
		return result, nil
	}
	startedAt := w.nowUTC()
	state, err := w.Queue.LoadState()
	if err != nil {
		_ = w.Queue.FailBatch(items, err, startedAt)
		result.Failed = len(items)
		return result, err
	}
	tasks := PlanReflectionTasks(state, items, startedAt)
	result.Tasks = append(result.Tasks, tasks...)
	execute := w.Execute
	if execute == nil {
		execute = NewReflectionWorker(w.Queue, w.Config).Execute
	}
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		default:
		}
		if err != nil {
			break
		}
		sourcePath := items[0].SessionRoot
		if items[0].Kind == "delivery" {
			sourcePath = items[0].DeliveryPath
		}
		reflectionResult, executeErr := execute(
			ctx,
			task,
			items[0].MemoryWorkspace,
			sourcePath,
		)
		if executeErr != nil {
			err = fmt.Errorf("reflect %s: %w", task, executeErr)
			break
		}
		result.Results = append(result.Results, reflectionResult)
	}
	if err != nil {
		if failErr := w.Queue.FailBatch(items, err, startedAt); failErr != nil {
			err = errors.Join(err, failErr)
		}
		result.Failed = len(items)
		return result, err
	}
	plannedTasks := reflectionPlannerTasks
	if items[0].Kind == "delivery" {
		plannedTasks = []string{ReflectTaskDeliveryOutcomeReport}
	}
	if err := w.Queue.CompleteBatch(items, tasks, plannedTasks, startedAt); err != nil {
		if failErr := w.Queue.FailBatch(items, err, startedAt); failErr != nil {
			err = errors.Join(err, failErr)
		}
		result.Failed = len(items)
		return result, err
	}
	result.Completed = len(items)
	return result, nil
}

// PlanReflectionTasks 根据新增 run 数量和任务冷却时间选择本批反思任务。
func PlanReflectionTasks(state ReflectionState, items []ReflectionTrigger, now time.Time) []string {
	if len(items) == 0 {
		return nil
	}
	if items[0].Kind == "delivery" {
		return []string{ReflectTaskDeliveryOutcomeReport}
	}
	tasks := []string{ReflectTaskSessionArchive}
	newRuns := len(items)
	hasError := false
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.RunStatus), "error") {
			hasError = true
			break
		}
	}
	due := func(task string, threshold int, cooldown time.Duration) bool {
		taskState := state.Tasks[task]
		if taskState.RunsSince+newRuns < threshold {
			return false
		}
		return taskState.LastRunAt.IsZero() || now.Sub(taskState.LastRunAt) >= cooldown
	}
	if hasError || due(ReflectTaskToolFailureReport, 5, 0) {
		tasks = append(tasks, ReflectTaskToolFailureReport)
	}
	if due(ReflectTaskMemoryQualityReport, 5, 30*time.Minute) {
		tasks = append(tasks, ReflectTaskMemoryQualityReport)
	}
	if due(ReflectTaskMineSOPCandidates, 5, time.Hour) {
		tasks = append(tasks, ReflectTaskMineSOPCandidates)
	}
	if due(ReflectTaskMineSkillCandidates, 10, 6*time.Hour) {
		tasks = append(tasks, ReflectTaskMineSkillCandidates)
	}
	return tasks
}

func groupReflectionTriggers(items []ReflectionTrigger) [][]ReflectionTrigger {
	groupsByKey := map[string][]ReflectionTrigger{}
	var keys []string
	for _, item := range items {
		sourcePath := item.SessionRoot
		if item.Kind == "delivery" {
			sourcePath = item.DeliveryPath
		}
		key := item.Kind + "\x00" + item.MemoryWorkspace + "\x00" + sourcePath
		if _, exists := groupsByKey[key]; !exists {
			keys = append(keys, key)
		}
		groupsByKey[key] = append(groupsByKey[key], item)
	}
	sort.Strings(keys)
	groups := make([][]ReflectionTrigger, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, groupsByKey[key])
	}
	return groups
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
