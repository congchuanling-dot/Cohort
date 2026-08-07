package hermes

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"cohort/internal/evaluation"
)

type Service struct {
	ProjectRoot string
	Store       Store
	EvalStore   evaluation.Store
	Config      Config
}

func NewService(projectRoot string) (Service, error) {
	store := NewStore(projectRoot)
	if err := store.Ensure(); err != nil {
		return Service{}, err
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return Service{}, err
	}
	return Service{
		ProjectRoot: projectRoot,
		Store:       store,
		EvalStore:   evaluation.NewStore(projectRoot),
		Config:      cfg,
	}, nil
}

func (s Service) RefreshStability() (evaluation.StabilityIndex, Queue, []Alert, error) {
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
	status.LastError = ""
	if err := s.Store.SaveStatus(status); err != nil {
		return evaluation.StabilityIndex{}, Queue{}, nil, err
	}
	return index, queue, alerts, nil
}

func (s Service) Serve(ctx context.Context) error {
	if err := s.Store.Ensure(); err != nil {
		return err
	}
	status := Status{
		Running:   true,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Config:    s.Config,
	}
	if err := os.WriteFile(s.Store.PIDPath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		return err
	}
	if err := s.Store.SaveStatus(status); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(s.Store.PIDPath())
		status.Running = false
		status.PID = 0
		_ = s.Store.SaveStatus(status)
	}()
	runOnce := func(task string, fn func() error) {
		started := time.Now()
		record := RunRecord{Task: task, Time: started.UTC(), Status: "success"}
		if err := fn(); err != nil {
			record.Status = "error"
			record.Error = err.Error()
			current, _ := s.Store.LoadStatus()
			current.LastError = err.Error()
			current.Config = s.Config
			_ = s.Store.SaveStatus(current)
		}
		record.DurationMS = time.Since(started).Milliseconds()
		_ = s.Store.AppendRun(record)
	}
	runOnce("eval_stability", func() error {
		_, _, _, err := s.RefreshStability()
		return err
	})
	interval := time.Duration(s.Config.EvalStabilityIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runOnce("eval_stability", func() error {
				_, _, _, err := s.RefreshStability()
				return err
			})
		}
	}
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
