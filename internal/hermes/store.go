package hermes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cohort/internal/evaluation"
)

type Store struct {
	Root string
}

func NewStore(projectRoot string) Store {
	return Store{Root: filepath.Join(projectRoot, ".cohort", "hermes")}
}

func (s Store) ConfigPath() string         { return filepath.Join(s.Root, "config.json") }
func (s Store) StatusPath() string         { return filepath.Join(s.Root, "status.json") }
func (s Store) QueuePath() string          { return filepath.Join(s.Root, "action_queue.json") }
func (s Store) JobsPath() string           { return filepath.Join(s.Root, "jobs.json") }
func (s Store) RepairsPath() string        { return filepath.Join(s.Root, "repairs.json") }
func (s Store) WorktreesDir() string       { return filepath.Join(s.Root, "worktrees") }
func (s Store) RepairArtifactsDir() string { return filepath.Join(s.Root, "repair-artifacts") }
func (s Store) AlertsPath() string         { return filepath.Join(s.Root, "alerts.jsonl") }
func (s Store) EventsPath() string         { return filepath.Join(s.Root, "events.jsonl") }
func (s Store) RunsPath() string           { return filepath.Join(s.Root, "runs.jsonl") }
func (s Store) PIDPath() string            { return filepath.Join(s.Root, "hermes.pid") }
func (s Store) LockPath() string           { return filepath.Join(s.Root, "hermes.lock") }
func (s Store) LogPath() string            { return filepath.Join(s.Root, "hermes.log") }

func DefaultConfig() Config {
	return Config{
		EvalStabilityIntervalSeconds: 15 * 60,
		SchedulerPollSeconds:         5,
		API: APIConfig{
			Enabled:       true,
			ListenAddress: "127.0.0.1:18778",
		},
		AutoRepair: AutoRepairConfig{
			MinSeverity:     AlertSeverityHigh,
			MaxConcurrent:   1,
			MaxAttempts:     2,
			MaxChangedFiles: 12,
			MaxDiffBytes:    512 * 1024,
			TimeoutSeconds:  30 * 60,
			RequireApproval: true,
			TestCommands:    []string{"go test ./..."},
			ProtectedPaths:  []string{".git", ".cohort", ".env", ".env.*"},
		},
	}
}

func (s Store) Ensure() error {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return err
	}
	if _, err := os.Stat(s.ConfigPath()); errors.Is(err, os.ErrNotExist) {
		return s.SaveConfig(DefaultConfig())
	}
	return nil
}

func (s Store) LoadConfig() (Config, error) {
	if err := s.Ensure(); err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	needsMigration := false
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) == nil {
		if _, exists := fields["api"]; !exists {
			cfg.API = DefaultConfig().API
			needsMigration = true
		}
		if raw, exists := fields["auto_repair"]; !exists {
			cfg.AutoRepair = DefaultConfig().AutoRepair
			needsMigration = true
		} else {
			var repairFields map[string]json.RawMessage
			if json.Unmarshal(raw, &repairFields) == nil {
				if _, hasApproval := repairFields["require_approval"]; !hasApproval {
					cfg.AutoRepair.RequireApproval = DefaultConfig().AutoRepair.RequireApproval
					needsMigration = true
				}
			}
		}
	}
	if cfg.EvalStabilityIntervalSeconds <= 0 {
		cfg.EvalStabilityIntervalSeconds = DefaultConfig().EvalStabilityIntervalSeconds
	}
	normalizeConfig(&cfg)
	if needsMigration {
		if err := writeJSONFile(s.ConfigPath(), cfg); err != nil {
			return Config{}, fmt.Errorf("persist migrated hermes config: %w", err)
		}
	}
	return cfg, nil
}

func (s Store) SaveConfig(cfg Config) error {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return err
	}
	if cfg.EvalStabilityIntervalSeconds <= 0 {
		cfg.EvalStabilityIntervalSeconds = DefaultConfig().EvalStabilityIntervalSeconds
	}
	normalizeConfig(&cfg)
	return writeJSONFile(s.ConfigPath(), cfg)
}

func normalizeConfig(cfg *Config) {
	defaults := DefaultConfig()
	if cfg.SchedulerPollSeconds <= 0 {
		cfg.SchedulerPollSeconds = defaults.SchedulerPollSeconds
	}
	if strings.TrimSpace(cfg.API.ListenAddress) == "" {
		cfg.API.ListenAddress = defaults.API.ListenAddress
	}
	if strings.TrimSpace(cfg.AutoRepair.MinSeverity) == "" {
		cfg.AutoRepair.MinSeverity = defaults.AutoRepair.MinSeverity
	}
	if cfg.AutoRepair.MaxConcurrent <= 0 {
		cfg.AutoRepair.MaxConcurrent = defaults.AutoRepair.MaxConcurrent
	}
	if cfg.AutoRepair.MaxAttempts <= 0 {
		cfg.AutoRepair.MaxAttempts = defaults.AutoRepair.MaxAttempts
	}
	if cfg.AutoRepair.MaxChangedFiles <= 0 {
		cfg.AutoRepair.MaxChangedFiles = defaults.AutoRepair.MaxChangedFiles
	}
	if cfg.AutoRepair.MaxDiffBytes <= 0 {
		cfg.AutoRepair.MaxDiffBytes = defaults.AutoRepair.MaxDiffBytes
	}
	if cfg.AutoRepair.TimeoutSeconds <= 0 {
		cfg.AutoRepair.TimeoutSeconds = defaults.AutoRepair.TimeoutSeconds
	}
	if len(cfg.AutoRepair.TestCommands) == 0 {
		cfg.AutoRepair.TestCommands = append([]string(nil), defaults.AutoRepair.TestCommands...)
	}
	if len(cfg.AutoRepair.ProtectedPaths) == 0 {
		cfg.AutoRepair.ProtectedPaths = append([]string(nil), defaults.AutoRepair.ProtectedPaths...)
	}
}

func (s Store) LoadStatus() (Status, error) {
	data, err := os.ReadFile(s.StatusPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg, _ := s.LoadConfig()
			return Status{Running: false, UpdatedAt: time.Now().UTC(), Config: cfg}, nil
		}
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, err
	}
	if cfg, loadErr := s.LoadConfig(); loadErr == nil {
		status.Config = cfg
	}
	return status, nil
}

func (s Store) SaveStatus(status Status) error {
	status.UpdatedAt = time.Now().UTC()
	return writeJSONFile(s.StatusPath(), status)
}

func (s Store) LoadQueue() (Queue, error) {
	data, err := os.ReadFile(s.QueuePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Queue{UpdatedAt: time.Now().UTC()}, nil
		}
		return Queue{}, err
	}
	var queue Queue
	if err := json.Unmarshal(data, &queue); err != nil {
		return Queue{}, err
	}
	return queue, nil
}

func (s Store) SaveQueue(queue Queue) error {
	queue.UpdatedAt = time.Now().UTC()
	sort.SliceStable(queue.Actions, func(i, j int) bool {
		if severityRank(queue.Actions[i].Severity) != severityRank(queue.Actions[j].Severity) {
			return severityRank(queue.Actions[i].Severity) > severityRank(queue.Actions[j].Severity)
		}
		return queue.Actions[i].LastSeenAt.After(queue.Actions[j].LastSeenAt)
	})
	return writeJSONFile(s.QueuePath(), queue)
}

func (s Store) LoadJobs() (Jobs, error) {
	data, err := os.ReadFile(s.JobsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Jobs{UpdatedAt: time.Now().UTC()}, nil
		}
		return Jobs{}, err
	}
	var jobs Jobs
	if err := json.Unmarshal(data, &jobs); err != nil {
		return Jobs{}, err
	}
	for i := range jobs.Jobs {
		normalizeJob(&jobs.Jobs[i])
	}
	return jobs, nil
}

func (s Store) SaveJobs(jobs Jobs) error {
	jobs.UpdatedAt = time.Now().UTC()
	for i := range jobs.Jobs {
		normalizeJob(&jobs.Jobs[i])
	}
	sort.SliceStable(jobs.Jobs, func(i, j int) bool { return jobs.Jobs[i].ID < jobs.Jobs[j].ID })
	return writeJSONFile(s.JobsPath(), jobs)
}

func (s Store) LoadRepairs() (Repairs, error) {
	data, err := os.ReadFile(s.RepairsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Repairs{UpdatedAt: time.Now().UTC()}, nil
		}
		return Repairs{}, err
	}
	var repairs Repairs
	if err := json.Unmarshal(data, &repairs); err != nil {
		return Repairs{}, err
	}
	return repairs, nil
}

func (s Store) SaveRepairs(repairs Repairs) error {
	repairs.UpdatedAt = time.Now().UTC()
	sort.SliceStable(repairs.Repairs, func(i, j int) bool {
		return repairs.Repairs[i].CreatedAt.After(repairs.Repairs[j].CreatedAt)
	})
	return writeJSONFile(s.RepairsPath(), repairs)
}

func (s Store) AcquireRepairLock(repairID string) (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Root, "locks"), 0755); err != nil {
		return nil, err
	}
	path := s.repairLockPath(repairID)
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		active, checkErr := processLockActive(path)
		if checkErr != nil {
			return nil, checkErr
		}
		if active {
			return nil, fmt.Errorf("repair %q is already running", repairID)
		}
	}
	return nil, fmt.Errorf("unable to acquire repair lock %q", repairID)
}

func (s Store) RepairLockActive(repairID string) (bool, error) {
	return processLockActive(s.repairLockPath(repairID))
}

func (s Store) repairLockPath(repairID string) string {
	return filepath.Join(s.Root, "locks", "repair-"+sanitizeFileID(repairID)+".lock")
}

func processLockActive(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr == nil && processRunning(pid) {
		return true, nil
	}
	info, statErr := os.Stat(path)
	if parseErr != nil && statErr == nil && time.Since(info.ModTime()) < 5*time.Second {
		// 创建者可能仍处于 O_EXCL 成功后写入 PID 的极短窗口，不能误删活锁。
		return true, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove stale process lock: %w", err)
	}
	return false, nil
}

func (s Store) AcquireGlobalLock() (func(), error) {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(s.LockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(s.LockPath()) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, readErr := os.ReadFile(s.LockPath())
		if readErr != nil {
			return nil, fmt.Errorf("read hermes lock: %w", readErr)
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && processRunning(pid) {
			return nil, fmt.Errorf("hermes store is locked by pid %d", pid)
		}
		if removeErr := os.Remove(s.LockPath()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale hermes lock: %w", removeErr)
		}
	}
	return nil, errors.New("unable to acquire hermes store lock")
}

func (s Store) AcquireJobLock(jobID string) (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Root, "locks"), 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Root, "locks", sanitizeFileID(jobID)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("job %q is already running", jobID)
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Close()
	return func() { _ = os.Remove(path) }, nil
}

func (s Store) AppendAlert(alert Alert) error {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return err
	}
	if alert.ID == "" {
		alert.ID = fmt.Sprintf("alert_%d", time.Now().UTC().UnixNano())
	}
	if alert.Time.IsZero() {
		alert.Time = time.Now().UTC()
	}
	line, err := json.Marshal(alert)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.AlertsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return s.AppendEvent(Event{Type: "alert", Severity: alert.Severity, SourceID: alert.ActionID, Data: alert})
}

func (s Store) AppendEvent(event Event) error {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("event_%d", time.Now().UTC().UnixNano())
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.EventsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(line, '\n'))
	return err
}

func (s Store) LoadEvents(limit int) ([]Event, error) {
	var events []Event
	if err := readJSONLines(s.EventsPath(), func(line []byte) {
		var event Event
		if json.Unmarshal(line, &event) == nil {
			events = append(events, event)
		}
	}); err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Time.After(events[j].Time) })
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (s Store) LoadAlerts(limit int) ([]Alert, error) {
	file, err := os.Open(s.AlertsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var alerts []Alert
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var alert Alert
		if err := json.Unmarshal(scanner.Bytes(), &alert); err == nil {
			alerts = append(alerts, alert)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Time.After(alerts[j].Time) })
	if limit > 0 && len(alerts) > limit {
		alerts = alerts[:limit]
	}
	return alerts, nil
}

func (s Store) AppendRun(record RunRecord) error {
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return err
	}
	if record.ID == "" {
		record.ID = fmt.Sprintf("hermes_run_%d", time.Now().UTC().UnixNano())
	}
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.RunsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return s.AppendEvent(Event{Type: "job_run", Severity: runSeverity(record), SourceID: record.JobID, Data: record})
}

func (s Store) LoadRuns(limit int) ([]RunRecord, error) {
	var runs []RunRecord
	if err := readJSONLines(s.RunsPath(), func(line []byte) {
		var run RunRecord
		if json.Unmarshal(line, &run) == nil {
			runs = append(runs, run)
		}
	}); err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Time.After(runs[j].Time) })
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func SyncActions(store Store, index evaluation.StabilityIndex) (Queue, []Alert, error) {
	queue, err := store.LoadQueue()
	if err != nil {
		return Queue{}, nil, err
	}
	now := time.Now().UTC()
	byFingerprint := map[string]int{}
	for i, action := range queue.Actions {
		if action.Fingerprint == "" {
			action.Fingerprint = ActionFingerprint(action)
			queue.Actions[i] = action
		}
		byFingerprint[action.Fingerprint] = i
	}
	latestCases := map[string]evaluation.StabilityCase{}
	for _, metric := range index.Cases {
		key := metric.SuiteID + "\x00" + metric.CaseID
		current, exists := latestCases[key]
		if !exists || metric.LatestAt.After(current.LatestAt) {
			latestCases[key] = metric
		}
	}
	for i := range queue.Actions {
		action := &queue.Actions[i]
		metric, exists := latestCases[action.SuiteID+"\x00"+action.CaseID]
		if exists && metric.LatestPassed && metric.LatestRunID != action.RunID {
			action.FailureStreak = 0
			action.RegressionStreak = 0
		}
	}
	var alerts []Alert
	for _, item := range index.ActionItems {
		action := QueueAction{
			ID:          item.ID,
			Fingerprint: itemFingerprint(item),
			Status:      QueueStatusOpen,
			Severity:    item.Severity,
			Category:    item.Category,
			Title:       item.Title,
			Detail:      item.Detail,
			Evidence:    item.Evidence,
			SuiteID:     item.SuiteID,
			CaseID:      item.CaseID,
			RunID:       item.RunID,
			TracePath:   item.TracePath,
			TraceRunID:  item.TraceRunID,
		}
		if existingIndex, ok := byFingerprint[action.Fingerprint]; ok {
			existing := queue.Actions[existingIndex]
			existing.LastSeenAt = now
			isNewRun := action.RunID != "" && !containsString(existing.SeenRunIDs, action.RunID)
			if isNewRun {
				existing.Occurrences++
				existing.SeenRunIDs = appendBounded(existing.SeenRunIDs, action.RunID, 50)
				existing.FailureStreak++
				if action.Category == "regression" {
					existing.RegressionStreak++
				} else {
					existing.RegressionStreak = 0
				}
				previousSeverity := existing.Severity
				if existing.RegressionStreak >= 3 {
					existing.Severity = "critical"
				} else if existing.FailureStreak >= 2 && severityRank(existing.Severity) < severityRank("high") {
					existing.Severity = "high"
				}
				if existing.Status == QueueStatusResolved {
					existing.Status = QueueStatusOpen
					existing.LastStatusAt = now
					existing.ReopenCount++
					existing.VerificationRunID = ""
					existing.VerifiedAt = time.Time{}
					alerts = append(alerts, actionAlert(existing, "reopened eval action"))
				} else if severityRank(existing.Severity) > severityRank(previousSeverity) {
					alerts = append(alerts, actionAlert(existing, "escalated eval action"))
				}
			}
			if severityRank(action.Severity) > severityRank(existing.Severity) {
				existing.Severity = action.Severity
			}
			existing.Category = action.Category
			existing.Title = action.Title
			existing.Detail = action.Detail
			existing.Evidence = action.Evidence
			existing.SuiteID = action.SuiteID
			existing.CaseID = action.CaseID
			existing.RunID = action.RunID
			existing.TracePath = action.TracePath
			existing.TraceRunID = action.TraceRunID
			queue.Actions[existingIndex] = existing
			continue
		}
		action.FirstSeenAt = now
		action.LastSeenAt = now
		action.LastStatusAt = now
		action.Occurrences = 1
		action.FailureStreak = 1
		if action.Category == "regression" {
			action.RegressionStreak = 1
		}
		if action.RunID != "" {
			action.SeenRunIDs = []string{action.RunID}
		}
		queue.Actions = append(queue.Actions, action)
		byFingerprint[action.Fingerprint] = len(queue.Actions) - 1
		if action.Severity == "critical" || action.Severity == "high" {
			alerts = append(alerts, actionAlert(action, "new eval action"))
		}
	}
	if err := store.SaveQueue(queue); err != nil {
		return Queue{}, nil, err
	}
	for _, alert := range alerts {
		if err := store.AppendAlert(alert); err != nil {
			return Queue{}, nil, err
		}
	}
	return queue, alerts, nil
}

func UpdateActionStatus(store Store, id string, status string) (QueueAction, error) {
	status = strings.TrimSpace(status)
	if status != QueueStatusOpen && status != QueueStatusAcknowledged && status != QueueStatusInProgress && status != QueueStatusDismissed {
		return QueueAction{}, fmt.Errorf("invalid action status %q", status)
	}
	queue, err := store.LoadQueue()
	if err != nil {
		return QueueAction{}, err
	}
	for i := range queue.Actions {
		if queue.Actions[i].ID == id || queue.Actions[i].Fingerprint == id {
			queue.Actions[i].Status = status
			queue.Actions[i].LastStatusAt = time.Now().UTC()
			if err := store.SaveQueue(queue); err != nil {
				return QueueAction{}, err
			}
			return queue.Actions[i], nil
		}
	}
	return QueueAction{}, fmt.Errorf("action %q not found", id)
}

func VerifyActionWithRun(store Store, evalStore evaluation.Store, id string, runID string, resolve bool) (QueueAction, error) {
	result, err := evalStore.LoadResult(runID)
	if err != nil {
		return QueueAction{}, fmt.Errorf("load verification run %q: %w", runID, err)
	}
	if result.Gate == nil || !result.Gate.Passed {
		return QueueAction{}, fmt.Errorf("verification run %q did not pass its gate", result.RunID)
	}
	queue, err := store.LoadQueue()
	if err != nil {
		return QueueAction{}, err
	}
	for i := range queue.Actions {
		action := &queue.Actions[i]
		if action.ID != id && action.Fingerprint != id {
			continue
		}
		if result.StartedAt.IsZero() || !result.StartedAt.After(action.LastStatusAt) {
			return QueueAction{}, fmt.Errorf("verification run %q is not newer than the action status change", result.RunID)
		}
		if action.SuiteID != "" && action.SuiteID != result.SuiteID {
			return QueueAction{}, fmt.Errorf("verification run suite %q does not match action suite %q", result.SuiteID, action.SuiteID)
		}
		if action.CaseID != "" {
			matched := false
			for _, caseResult := range result.Cases {
				if caseResult.CaseID == action.CaseID {
					matched = true
					if !caseResult.Passed {
						return QueueAction{}, fmt.Errorf("verification case %q did not pass", action.CaseID)
					}
					break
				}
			}
			if !matched {
				return QueueAction{}, fmt.Errorf("verification run %q does not contain case %q", result.RunID, action.CaseID)
			}
		}
		action.VerificationRunID = result.RunID
		action.VerifiedAt = time.Now().UTC()
		if resolve {
			action.Status = QueueStatusResolved
			action.ResolvedFromRun = result.RunID
			action.FailureStreak = 0
			action.RegressionStreak = 0
		}
		action.LastStatusAt = time.Now().UTC()
		if err := store.SaveQueue(queue); err != nil {
			return QueueAction{}, err
		}
		eventType := "action_verified"
		if resolve {
			eventType = "action_resolved"
		}
		_ = store.AppendEvent(Event{Type: eventType, Severity: AlertSeverityInfo, SourceID: action.ID, Data: *action})
		return *action, nil
	}
	return QueueAction{}, fmt.Errorf("action %q not found", id)
}

func ActionFingerprint(action QueueAction) string {
	return strings.Join([]string{action.Category, action.SuiteID, action.CaseID, action.Title}, "\x00")
}

func itemFingerprint(item evaluation.ActionItem) string {
	return strings.Join([]string{item.Category, item.SuiteID, item.CaseID, item.Title}, "\x00")
}

func CountOpen(queue Queue) (open, critical, high int) {
	for _, action := range queue.Actions {
		if action.Status == QueueStatusResolved || action.Status == QueueStatusDismissed {
			continue
		}
		open++
		switch action.Severity {
		case "critical":
			critical++
		case "high":
			high++
		}
	}
	return open, critical, high
}

func StatusSummaryFromIndex(index evaluation.StabilityIndex) StabilitySummary {
	return StabilitySummary{
		Runs:             index.Summary.Runs,
		AveragePassRate:  index.Summary.AveragePassRate,
		AverageScore:     index.Summary.AverageScore,
		AverageStability: index.Summary.AverageStability,
		FlakyCases:       index.Summary.FlakyCases,
		Regressions:      index.Summary.Regressions,
		ActionItems:      index.Summary.ActionItems,
	}
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
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

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func actionAlert(action QueueAction, prefix string) Alert {
	return Alert{
		Severity:    action.Severity,
		Category:    action.Category,
		Title:       prefix + ": " + action.Title,
		Detail:      action.Evidence,
		ActionID:    action.ID,
		Fingerprint: action.Fingerprint,
	}
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func appendBounded(values []string, value string, limit int) []string {
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func sanitizeFileID(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "job"
	}
	return b.String()
}

func readJSONLines(path string, visit func([]byte)) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		visit(scanner.Bytes())
	}
	return scanner.Err()
}

func runSeverity(record RunRecord) string {
	if record.Status == "success" {
		return AlertSeverityInfo
	}
	return AlertSeverityHigh
}
