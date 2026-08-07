package hermes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"cohort/internal/evaluation"
)

type EvalRunner func(context.Context, Job) (EvalRunOutcome, error)

type Service struct {
	ProjectRoot string
	Store       Store
	EvalStore   evaluation.Store
	Config      Config
	EvalRunner  EvalRunner
	Output      io.Writer
	RetryWait   func(context.Context, time.Duration) error

	mu      sync.Mutex
	running map[string]bool
}

func NewService(projectRoot string) (*Service, error) {
	store := NewStore(projectRoot)
	if err := store.Ensure(); err != nil {
		return nil, err
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return nil, err
	}
	return &Service{
		ProjectRoot: projectRoot,
		Store:       store,
		EvalStore:   evaluation.NewStore(projectRoot),
		Config:      cfg,
		Output:      io.Discard,
		RetryWait:   waitWithContext,
		running:     map[string]bool{},
	}, nil
}

func (s *Service) RefreshStability() (evaluation.StabilityIndex, Queue, []Alert, error) {
	results, err := s.EvalStore.ListResults()
	if err != nil {
		return evaluation.StabilityIndex{}, Queue{}, nil, err
	}
	index := evaluation.BuildStabilityIndex(results, evaluation.StabilityOptions{Window: 20})
	if len(results) > 0 {
		if _, _, _, err := evaluation.WriteStabilityReports(s.EvalStore, index); err != nil {
			return evaluation.StabilityIndex{}, Queue{}, nil, err
		}
	}
	queue, alerts, err := SyncActions(s.Store, index)
	if err != nil {
		return evaluation.StabilityIndex{}, Queue{}, nil, err
	}
	notificationErr := DeliverAlerts(context.Background(), s.Store, s.Config.Notifications, alerts, s.Output)
	s.mu.Lock()
	defer s.mu.Unlock()
	status, _ := s.Store.LoadStatus()
	open, critical, high := CountOpen(queue)
	recentAlerts, _ := s.Store.LoadAlerts(5)
	status.Running = processRunning(status.PID)
	status.LastStabilityAt = time.Now().UTC()
	status.LastStabilitySummary = StatusSummaryFromIndex(index)
	status.OpenActions = open
	status.CriticalActions = critical
	status.HighActions = high
	status.LastAlerts = recentAlerts
	status.Config = s.Config
	status.RunningJobs = s.runningJobIDsLocked()
	status.LastError = ""
	if notificationErr != nil {
		status.LastError = "notification: " + notificationErr.Error()
	}
	if err := s.Store.SaveStatus(status); err != nil {
		return evaluation.StabilityIndex{}, Queue{}, nil, err
	}
	return index, queue, alerts, notificationErr
}

func (s *Service) RunJob(ctx context.Context, jobID string) (Job, error) {
	job, err := FindJob(s.Store, jobID)
	if err != nil {
		return Job{}, err
	}
	if s.EvalRunner == nil {
		return Job{}, errors.New("hermes eval runner is not configured")
	}
	release, err := s.Store.AcquireJobLock(job.ID)
	if err != nil {
		return Job{}, err
	}
	defer release()
	s.setJobRunning(job.ID, true)
	defer s.setJobRunning(job.ID, false)

	attempts := job.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var finalErr error
	var finalOutcome EvalRunOutcome
	for attempt := 1; attempt <= attempts; attempt++ {
		started := time.Now().UTC()
		outcome, runErr := s.EvalRunner(ctx, job)
		if runErr == nil && !outcome.GatePassed {
			runErr = errors.New("eval gate failed")
		}
		record := RunRecord{
			Task:       "eval_run",
			JobID:      job.ID,
			Attempt:    attempt,
			Time:       started,
			Status:     "success",
			DurationMS: time.Since(started).Milliseconds(),
			EvalRunIDs: outcome.RunIDs,
			GatePassed: outcome.GatePassed,
		}
		if runErr != nil {
			record.Status = "error"
			record.Error = runErr.Error()
		}
		_ = s.Store.AppendRun(record)
		finalOutcome = outcome
		finalErr = runErr
		if runErr == nil {
			break
		}
		if attempt < attempts {
			delay := time.Duration(job.Retry.BackoffSeconds) * time.Second
			if delay <= 0 {
				delay = 30 * time.Second
			}
			wait := s.RetryWait
			if wait == nil {
				wait = waitWithContext
			}
			if err := wait(ctx, delay); err != nil {
				finalErr = err
				break
			}
		}
	}

	now := time.Now().UTC()
	s.mu.Lock()
	jobs, loadErr := s.Store.LoadJobs()
	if loadErr == nil {
		for i := range jobs.Jobs {
			if jobs.Jobs[i].ID != job.ID {
				continue
			}
			stored := &jobs.Jobs[i]
			stored.LastRunAt = now
			stored.LastRunIDs = append([]string(nil), finalOutcome.RunIDs...)
			stored.UpdatedAt = now
			if finalErr == nil {
				stored.LastStatus = "success"
				stored.LastError = ""
				stored.ConsecutiveFailures = 0
			} else {
				stored.LastStatus = "error"
				stored.LastError = finalErr.Error()
				stored.ConsecutiveFailures++
			}
			stored.NextRunAt, _ = NextJobRun(*stored, now)
			job = *stored
			break
		}
		loadErr = s.Store.SaveJobs(jobs)
	}
	status, _ := s.Store.LoadStatus()
	status.LastEvalAt = now
	status.LastJobAt = now
	status.RunningJobs = s.runningJobIDsLocked()
	if finalErr != nil {
		status.LastError = finalErr.Error()
	}
	statusErr := s.Store.SaveStatus(status)
	s.mu.Unlock()
	if loadErr != nil {
		return job, loadErr
	}
	if statusErr != nil {
		return job, statusErr
	}
	_, _, _, refreshErr := s.RefreshStability()
	if finalErr != nil {
		return job, finalErr
	}
	return job, refreshErr
}

func waitWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) RunDueJobs(ctx context.Context, now time.Time) {
	jobs, err := s.Store.LoadJobs()
	if err != nil {
		s.recordError(err)
		return
	}
	for _, job := range jobs.Jobs {
		if !job.Enabled || job.NextRunAt.IsZero() || job.NextRunAt.After(now) || s.jobRunning(job.ID) {
			continue
		}
		jobID := job.ID
		go func() {
			if _, err := s.RunJob(ctx, jobID); err != nil {
				s.recordError(fmt.Errorf("job %s: %w", jobID, err))
			}
		}()
	}
}

func (s *Service) Serve(ctx context.Context) error {
	if err := s.Store.Ensure(); err != nil {
		return err
	}
	status, _ := s.Store.LoadStatus()
	status.Running = true
	status.PID = os.Getpid()
	status.StartedAt = time.Now().UTC()
	status.Config = s.Config
	status.LastError = ""
	if err := os.WriteFile(s.Store.PIDPath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		return err
	}
	if err := s.Store.SaveStatus(status); err != nil {
		return err
	}
	apiErr := make(chan error, 1)
	if s.Config.API.Enabled {
		go func() { apiErr <- s.ServeAPI(ctx, s.Config.API.ListenAddress) }()
	}
	defer func() {
		_ = os.Remove(s.Store.PIDPath())
		current, _ := s.Store.LoadStatus()
		current.Running = false
		current.PID = 0
		current.RunningJobs = nil
		_ = s.Store.SaveStatus(current)
	}()

	s.runMaintenance("eval_stability", func() error {
		_, _, _, err := s.RefreshStability()
		return err
	})
	stabilityInterval := time.Duration(s.Config.EvalStabilityIntervalSeconds) * time.Second
	if stabilityInterval <= 0 {
		stabilityInterval = 15 * time.Minute
	}
	pollInterval := time.Duration(s.Config.SchedulerPollSeconds) * time.Second
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	stabilityTicker := time.NewTicker(stabilityInterval)
	schedulerTicker := time.NewTicker(pollInterval)
	defer stabilityTicker.Stop()
	defer schedulerTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-apiErr:
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("hermes api: %w", err)
			}
		case <-schedulerTicker.C:
			s.RunDueJobs(ctx, time.Now().UTC())
		case <-stabilityTicker.C:
			s.runMaintenance("eval_stability", func() error {
				_, _, _, err := s.RefreshStability()
				return err
			})
		}
	}
}

func (s *Service) runMaintenance(task string, fn func() error) {
	started := time.Now()
	record := RunRecord{Task: task, Time: started.UTC(), Status: "success"}
	if err := fn(); err != nil {
		record.Status = "error"
		record.Error = err.Error()
		s.recordError(err)
	}
	record.DurationMS = time.Since(started).Milliseconds()
	_ = s.Store.AppendRun(record)
}

func (s *Service) setJobRunning(id string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		s.running[id] = true
	} else {
		delete(s.running, id)
	}
	status, _ := s.Store.LoadStatus()
	status.RunningJobs = s.runningJobIDsLocked()
	_ = s.Store.SaveStatus(status)
}

func (s *Service) jobRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *Service) runningJobIDsLocked() []string {
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, _ := s.Store.LoadStatus()
	status.LastError = err.Error()
	_ = s.Store.SaveStatus(status)
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
