package hermes

import "time"

const (
	QueueStatusOpen         = "open"
	QueueStatusAcknowledged = "acknowledged"
	QueueStatusInProgress   = "in_progress"
	QueueStatusResolved     = "resolved"
	QueueStatusDismissed    = "dismissed"

	AlertSeverityInfo     = "info"
	AlertSeverityHigh     = "high"
	AlertSeverityCritical = "critical"
)

type Config struct {
	EvalStabilityIntervalSeconds int      `json:"eval_stability_interval_seconds"`
	EvalRunIntervalSeconds       int      `json:"eval_run_interval_seconds,omitempty"`
	EvalRunSuites                []string `json:"eval_run_suites,omitempty"`
}

type Status struct {
	Running              bool             `json:"running"`
	PID                  int              `json:"pid,omitempty"`
	StartedAt            time.Time        `json:"started_at,omitempty"`
	UpdatedAt            time.Time        `json:"updated_at"`
	LastEvalAt           time.Time        `json:"last_eval_at,omitempty"`
	LastStabilityAt      time.Time        `json:"last_stability_at,omitempty"`
	LastStabilitySummary StabilitySummary `json:"last_stability_summary,omitempty"`
	OpenActions          int              `json:"open_actions"`
	CriticalActions      int              `json:"critical_actions"`
	HighActions          int              `json:"high_actions"`
	LastAlerts           []Alert          `json:"last_alerts,omitempty"`
	LastError            string           `json:"last_error,omitempty"`
	Config               Config           `json:"config"`
}

type StabilitySummary struct {
	Runs             int     `json:"runs"`
	AveragePassRate  float64 `json:"average_pass_rate"`
	AverageScore     float64 `json:"average_score"`
	AverageStability float64 `json:"average_stability"`
	FlakyCases       int     `json:"flaky_cases"`
	Regressions      int     `json:"regressions"`
	ActionItems      int     `json:"action_items"`
}

type Queue struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Actions   []QueueAction `json:"actions"`
}

type QueueAction struct {
	ID              string    `json:"id"`
	Fingerprint     string    `json:"fingerprint"`
	Status          string    `json:"status"`
	Severity        string    `json:"severity"`
	Category        string    `json:"category"`
	Title           string    `json:"title"`
	Detail          string    `json:"detail,omitempty"`
	Evidence        string    `json:"evidence,omitempty"`
	SuiteID         string    `json:"suite_id,omitempty"`
	CaseID          string    `json:"case_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	TracePath       string    `json:"trace_path,omitempty"`
	TraceRunID      string    `json:"trace_run_id,omitempty"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	LastStatusAt    time.Time `json:"last_status_at"`
	Occurrences     int       `json:"occurrences"`
	ResolvedFromRun string    `json:"resolved_from_run,omitempty"`
}

type Alert struct {
	ID          string    `json:"id"`
	Time        time.Time `json:"time"`
	Severity    string    `json:"severity"`
	Category    string    `json:"category"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail,omitempty"`
	ActionID    string    `json:"action_id,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type RunRecord struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Task       string    `json:"task"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}
