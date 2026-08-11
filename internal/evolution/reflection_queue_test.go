package evolution

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"cohort/internal/hooks"
)

func TestReflectionQueueDeduplicatesAndAdvancesWatermark_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	memoryWorkspace := filepath.Join(projectRoot, "workspace")
	sessionRoot := filepath.Join(projectRoot, "sessions")
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	queue := NewReflectionQueue(projectRoot)
	queue.now = func() time.Time { return now }
	trigger := ReflectionTrigger{
		ProjectRoot:     projectRoot,
		MemoryWorkspace: memoryWorkspace,
		SessionRoot:     sessionRoot,
		SessionID:       "session-1",
		RunID:           "run-1",
		HistoryLen:      4,
		RunStatus:       "done",
	}

	enqueued, created, err := queue.Enqueue(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if !created || enqueued.ID == "" {
		t.Fatalf("enqueued=%#v created=%t, want new trigger", enqueued, created)
	}
	if _, created, err := queue.Enqueue(trigger); err != nil || created {
		t.Fatalf("duplicate enqueue created=%t err=%v, want no-op", created, err)
	}

	claimed, err := queue.ClaimDueBatch(10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Attempt != 1 {
		t.Fatalf("claimed=%#v, want one first attempt", claimed)
	}
	if err := queue.CompleteBatch(claimed, []string{ReflectTaskSessionArchive}, reflectionPlannerTasks, now); err != nil {
		t.Fatal(err)
	}
	state, err := queue.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Sessions["session-1"].HistoryLen != 4 {
		t.Fatalf("watermark=%#v, want history_len 4", state.Sessions["session-1"])
	}
	if _, created, err := queue.Enqueue(trigger); err != nil || created {
		t.Fatalf("processed watermark enqueue created=%t err=%v, want no-op", created, err)
	}
	trigger.HistoryLen = 5
	if _, created, err := queue.Enqueue(trigger); err != nil || !created {
		t.Fatalf("new watermark enqueue created=%t err=%v, want new trigger", created, err)
	}
}

func TestReflectionWorkerPlansBatchAndCompletesAllTriggers_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	memoryWorkspace := filepath.Join(projectRoot, "workspace")
	sessionRoot := filepath.Join(projectRoot, "sessions")
	now := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	queue := NewReflectionQueue(projectRoot)
	queue.now = func() time.Time { return now }
	for index := 0; index < 5; index++ {
		status := "done"
		if index == 0 {
			status = "error"
		}
		if _, created, err := queue.Enqueue(ReflectionTrigger{
			ProjectRoot:     projectRoot,
			MemoryWorkspace: memoryWorkspace,
			SessionRoot:     sessionRoot,
			SessionID:       "session-" + string(rune('a'+index)),
			RunID:           "run",
			HistoryLen:      2,
			RunStatus:       status,
		}); err != nil || !created {
			t.Fatalf("enqueue %d created=%t err=%v", index, created, err)
		}
	}
	worker := NewReflectionWorker(queue, ReflectionWorkerConfig{})
	worker.now = func() time.Time { return now }
	var executed []string
	worker.Execute = func(_ context.Context, task string, _, _ string) (ReflectionResult, error) {
		executed = append(executed, task)
		return ReflectionResult{Task: task}, nil
	}

	result, err := worker.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantTasks := []string{
		ReflectTaskSessionArchive,
		ReflectTaskToolFailureReport,
		ReflectTaskMemoryQualityReport,
		ReflectTaskMineSOPCandidates,
	}
	if result.Claimed != 5 || result.Completed != 5 || result.Failed != 0 {
		t.Fatalf("drain result=%#v, want five completed", result)
	}
	if !slices.Equal(executed, wantTasks) {
		t.Fatalf("executed=%#v, want %#v", executed, wantTasks)
	}
	status, err := queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Running != 0 || status.Done != 5 || status.SessionStates != 5 {
		t.Fatalf("queue status=%#v, want five done session states", status)
	}
}

func TestReflectionWorkerFailureDeadLetterAndRetry_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	queue := NewReflectionQueue(projectRoot)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	item, created, err := queue.Enqueue(ReflectionTrigger{
		ProjectRoot:     projectRoot,
		MemoryWorkspace: filepath.Join(projectRoot, "workspace"),
		SessionRoot:     filepath.Join(projectRoot, "sessions"),
		SessionID:       "session-fail",
		HistoryLen:      3,
		RunStatus:       "error",
		MaxAttempts:     1,
	})
	if err != nil || !created {
		t.Fatalf("enqueue created=%t err=%v", created, err)
	}
	worker := NewReflectionWorker(queue, ReflectionWorkerConfig{})
	worker.now = func() time.Time { return now }
	worker.Execute = func(context.Context, string, string, string) (ReflectionResult, error) {
		return ReflectionResult{}, errors.New("synthetic failure with secret body omitted")
	}
	result, err := worker.Drain(context.Background())
	if err == nil || result.Failed != 1 {
		t.Fatalf("drain result=%#v err=%v, want failed trigger", result, err)
	}
	status, err := queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Dead != 1 || status.Pending != 0 || status.LastDeadJobID != item.ID {
		t.Fatalf("status=%#v, want one dead trigger", status)
	}
	retried, err := queue.Retry(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Attempt != 0 || retried.LastError != "" {
		t.Fatalf("retried=%#v, want reset attempt and error", retried)
	}
	status, err = queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.Dead != 0 {
		t.Fatalf("status after retry=%#v, want pending trigger", status)
	}
}

func TestReflectionQueueRecoversExpiredClaim_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	now := time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)
	queue := NewReflectionQueue(projectRoot)
	queue.now = func() time.Time { return now }
	if _, created, err := queue.Enqueue(ReflectionTrigger{
		ProjectRoot:     projectRoot,
		MemoryWorkspace: filepath.Join(projectRoot, "workspace"),
		SessionRoot:     filepath.Join(projectRoot, "sessions"),
		SessionID:       "session-recover",
		HistoryLen:      3,
	}); err != nil || !created {
		t.Fatalf("enqueue created=%t err=%v", created, err)
	}
	claimed, err := queue.ClaimDueBatch(1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	now = now.Add(2 * time.Minute)
	recovered, err := queue.RecoverExpiredClaims()
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want one", recovered)
	}
	status, err := queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.Running != 0 {
		t.Fatalf("status=%#v, want recovered pending trigger", status)
	}
}

func TestPruneReflectionSessionStateKeepsNewestWatermarks_BitsUT(t *testing.T) {
	base := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	state := ReflectionState{Sessions: map[string]ReflectionSessionState{
		"old": {HistoryLen: 1, ProcessedAt: base},
		"mid": {HistoryLen: 2, ProcessedAt: base.Add(time.Minute)},
		"new": {HistoryLen: 3, ProcessedAt: base.Add(2 * time.Minute)},
	}}
	pruneReflectionSessionState(&state, 2)
	if len(state.Sessions) != 2 {
		t.Fatalf("sessions=%#v, want two newest watermarks", state.Sessions)
	}
	if _, exists := state.Sessions["old"]; exists {
		t.Fatalf("old watermark was not pruned: %#v", state.Sessions)
	}
}

func TestSessionEndReflectionHandlerOnlyQueuesInteractiveRuns_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	queue := NewReflectionQueue(projectRoot)
	queue.now = func() time.Time {
		return time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	}
	handler := NewSessionEndReflectionHandler(queue, SessionEndReflectionConfig{
		Enabled:         true,
		ProjectRoot:     projectRoot,
		MemoryWorkspace: filepath.Join(projectRoot, "workspace"),
		SessionRoot:     filepath.Join(projectRoot, "sessions"),
		Debounce:        30 * time.Second,
		MaxAttempts:     3,
	})
	base := hooks.Event{
		Type:      hooks.EventSessionEnd,
		RunID:     "run-1",
		SessionID: "session-1",
		Data: map[string]any{
			"history_len": 2,
			"status":      "done",
			"run_mode":    "eval",
		},
	}
	if err := handler.HandleHook(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	status, err := queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 {
		t.Fatalf("eval hook queued %d triggers, want zero", status.Pending)
	}
	base.Data["run_mode"] = ReflectionRunModeInteractive
	if err := handler.HandleHook(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	status, err = queue.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.NextAvailable.Sub(queue.nowUTC()) != 30*time.Second {
		t.Fatalf("interactive hook status=%#v, want one debounced trigger", status)
	}
}
