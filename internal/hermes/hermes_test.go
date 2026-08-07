package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cohort/internal/evaluation"
)

func TestJobPersistenceCronAndSchedulerRetry_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, err := UpsertJob(store, Job{
		ID: "core-nightly", Enabled: true, Suite: "core", Repeat: 2, Workers: 1,
		Schedule: Schedule{Cron: "*/15 * * * *"},
		Retry:    Retry{MaxAttempts: 2, BackoffSeconds: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := NextJobRun(job, time.Date(2026, 8, 7, 10, 7, 1, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 8, 7, 10, 15, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
	standard, err := parseCron("0 9 15 * 1")
	if err != nil {
		t.Fatal(err)
	}
	if !standard.matches(time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("cron day-of-month/day-of-week must use standard OR semantics")
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	service.RetryWait = func(context.Context, time.Duration) error { return nil }
	service.EvalRunner = func(context.Context, Job) (EvalRunOutcome, error) {
		if calls.Add(1) == 1 {
			return EvalRunOutcome{}, context.DeadlineExceeded
		}
		return EvalRunOutcome{RunIDs: []string{"eval-ok"}, GatePassed: true}, nil
	}
	job, err = service.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || job.LastStatus != "success" || job.ConsecutiveFailures != 0 {
		t.Fatalf("calls=%d job=%#v", calls.Load(), job)
	}
	runs, err := store.LoadRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Status != "success" || runs[1].Status != "error" {
		t.Fatalf("runs=%#v", runs)
	}
}

func TestActionEscalatesReopensAndRequiresVerifiedRun_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	item := evaluation.ActionItem{
		ID: "a1", Scope: "case", Severity: "medium", Category: "failure",
		Title: "fix case", SuiteID: "core", CaseID: "c1", RunID: "r1",
	}
	index := evaluation.StabilityIndex{ActionItems: []evaluation.ActionItem{item}}
	queue, _, err := SyncActions(store, index)
	if err != nil {
		t.Fatal(err)
	}
	item.RunID = "r2"
	index.ActionItems[0] = item
	queue, alerts, err := SyncActions(store, index)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Actions[0].Severity != "high" || queue.Actions[0].FailureStreak != 2 || len(alerts) != 1 {
		t.Fatalf("queue=%#v alerts=%#v", queue.Actions, alerts)
	}
	evalStore := evaluation.NewStore(root)
	if _, err := evalStore.SaveResult(evaluation.RunResult{
		RunID: "verify", SuiteID: "core", StartedAt: time.Now().UTC().Add(time.Second), Gate: &evaluation.GateResult{Passed: true},
		Cases: []evaluation.CaseResult{{CaseID: "c1", Passed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := VerifyActionWithRun(store, evalStore, "a1", "verify", true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != QueueStatusResolved {
		t.Fatalf("resolved=%#v", resolved)
	}
	item.RunID = "r3"
	index.ActionItems[0] = item
	queue, alerts, err = SyncActions(store, index)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Actions[0].Status != QueueStatusOpen || queue.Actions[0].ReopenCount != 1 || len(alerts) != 1 {
		t.Fatalf("reopened=%#v alerts=%#v", queue.Actions[0], alerts)
	}
}

func TestWebhookNotificationAndAPIRoutes_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.SaveQueue(Queue{Actions: []QueueAction{{ID: "a1", Status: QueueStatusOpen}}}); err != nil {
		t.Fatal(err)
	}
	var received atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "eval_action_alert" {
			t.Fatalf("event=%#v", event)
		}
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	if err := DeliverAlerts(context.Background(), store, []NotificationConfig{{
		ID: "hook", Type: "webhook", Target: webhook.URL, Enabled: true, MinSeverity: "high",
	}}, []Alert{{ID: "alert", Severity: "high", ActionID: "a1"}}, nil); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("received=%d", received.Load())
	}

	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.apiHandler())
	defer server.Close()
	for _, path := range []string{"/status", "/actions", "/jobs", "/repairs", "/eval/runs", "/events"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
}

func TestRepairAPICreateApproveAndReject_BitsUT(t *testing.T) {
	root := initRepairGitRepo(t)
	store := NewStore(root)
	action := seedRepairAction(t, store)
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.apiHandler())
	defer server.Close()

	body := strings.NewReader(`{"action_id":"` + action.ID + `"}`)
	response, err := http.Post(server.URL+"/repairs", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	var task RepairTask
	if err := json.NewDecoder(response.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Status != RepairStatusQueued {
		t.Fatalf("task=%#v", task)
	}
	task.Status = RepairStatusReadyForReview
	if err := UpdateRepair(store, task); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/repairs/"+task.ID, strings.NewReader(`{"operation":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approve status=%d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Status != RepairStatusApproved {
		t.Fatalf("task=%#v", task)
	}
}

func TestAcquireJobLockRejectsConcurrentProcess_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	release, err := store.AcquireJobLock("job")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.AcquireJobLock("job"); err == nil {
		t.Fatal("second lock unexpectedly succeeded")
	}
	lock := filepath.Join(store.Root, "locks", "job.lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatal(err)
	}
}

func TestRunDueJobsAutomaticallyExecutesExpiredJob_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, err := UpsertJob(store, Job{
		ID: "due", Enabled: true, Suite: "core",
		Schedule: Schedule{IntervalSeconds: 60},
		Retry:    Retry{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := store.LoadJobs()
	if err != nil {
		t.Fatal(err)
	}
	jobs.Jobs[0].NextRunAt = time.Now().UTC().Add(-time.Second)
	if err := store.SaveJobs(jobs); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	service.EvalRunner = func(context.Context, Job) (EvalRunOutcome, error) {
		done <- struct{}{}
		return EvalRunOutcome{RunIDs: []string{"auto"}, GatePassed: true}, nil
	}
	service.RunDueJobs(context.Background(), time.Now().UTC())
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("due job was not executed")
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		updated, findErr := FindJob(store, job.ID)
		if findErr == nil && updated.LastStatus == "success" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("due job result was not persisted")
}
