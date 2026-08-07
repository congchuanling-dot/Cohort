package hermes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func (s Store) ConfigPath() string { return filepath.Join(s.Root, "config.json") }
func (s Store) StatusPath() string { return filepath.Join(s.Root, "status.json") }
func (s Store) QueuePath() string  { return filepath.Join(s.Root, "action_queue.json") }
func (s Store) AlertsPath() string { return filepath.Join(s.Root, "alerts.jsonl") }
func (s Store) RunsPath() string   { return filepath.Join(s.Root, "runs.jsonl") }
func (s Store) PIDPath() string    { return filepath.Join(s.Root, "hermes.pid") }
func (s Store) LockPath() string   { return filepath.Join(s.Root, "hermes.lock") }
func (s Store) LogPath() string    { return filepath.Join(s.Root, "hermes.log") }

func DefaultConfig() Config {
	return Config{
		EvalStabilityIntervalSeconds: 15 * 60,
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
	if cfg.EvalStabilityIntervalSeconds <= 0 {
		cfg.EvalStabilityIntervalSeconds = DefaultConfig().EvalStabilityIntervalSeconds
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
	return writeJSONFile(s.ConfigPath(), cfg)
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
	if status.Config.EvalStabilityIntervalSeconds <= 0 {
		status.Config, _ = s.LoadConfig()
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
	return nil
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
	return nil
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
			existing.Occurrences++
			existing.Severity = action.Severity
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
		queue.Actions = append(queue.Actions, action)
		byFingerprint[action.Fingerprint] = len(queue.Actions) - 1
		if action.Severity == "critical" || action.Severity == "high" {
			alerts = append(alerts, Alert{
				Severity:    action.Severity,
				Category:    action.Category,
				Title:       "new eval action: " + action.Title,
				Detail:      action.Evidence,
				ActionID:    action.ID,
				Fingerprint: action.Fingerprint,
			})
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
	if status != QueueStatusOpen && status != QueueStatusAcknowledged && status != QueueStatusInProgress && status != QueueStatusResolved && status != QueueStatusDismissed {
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
			if status == QueueStatusResolved {
				queue.Actions[i].ResolvedFromRun = queue.Actions[i].RunID
			}
			if err := store.SaveQueue(queue); err != nil {
				return QueueAction{}, err
			}
			return queue.Actions[i], nil
		}
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
	return os.WriteFile(path, append(data, '\n'), 0644)
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
