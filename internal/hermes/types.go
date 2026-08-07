package hermes

import "time"

const (
	QueueStatusOpen         = "open"
	QueueStatusAcknowledged = "acknowledged"
	QueueStatusInProgress   = "in_progress"
	QueueStatusResolved     = "resolved"
	QueueStatusDismissed    = "dismissed"

	RepairStatusQueued         = "queued"
	RepairStatusRunning        = "running"
	RepairStatusReadyForReview = "ready_for_review"
	RepairStatusFailed         = "failed"
	RepairStatusApproved       = "approved"
	RepairStatusRejected       = "rejected"
	RepairStatusMerging        = "merging"
	RepairStatusMerged         = "merged"
	RepairStatusCancelled      = "cancelled"

	AlertSeverityInfo     = "info"
	AlertSeverityHigh     = "high"
	AlertSeverityCritical = "critical"
)

type Config struct {
	EvalStabilityIntervalSeconds int                  `json:"eval_stability_interval_seconds"`
	EvalRunIntervalSeconds       int                  `json:"eval_run_interval_seconds,omitempty"`
	EvalRunSuites                []string             `json:"eval_run_suites,omitempty"`
	SchedulerPollSeconds         int                  `json:"scheduler_poll_seconds,omitempty"`
	API                          APIConfig            `json:"api"`
	Notifications                []NotificationConfig `json:"notifications,omitempty"`
	AutoRepair                   AutoRepairConfig     `json:"auto_repair"`
}

type AutoRepairConfig struct {
	Enabled         bool     `json:"enabled"`
	MinSeverity     string   `json:"min_severity,omitempty"`
	MaxConcurrent   int      `json:"max_concurrent,omitempty"`
	MaxAttempts     int      `json:"max_attempts,omitempty"`
	MaxChangedFiles int      `json:"max_changed_files,omitempty"`
	MaxDiffBytes    int      `json:"max_diff_bytes,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	RequireApproval bool     `json:"require_approval"`
	TestCommands    []string `json:"test_commands,omitempty"`
	ProtectedPaths  []string `json:"protected_paths,omitempty"`
}

type APIConfig struct {
	Enabled       bool   `json:"enabled"`
	ListenAddress string `json:"listen_address"`
}

type NotificationConfig struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Target         string `json:"target,omitempty"`
	MinSeverity    string `json:"min_severity,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Enabled        bool   `json:"enabled"`
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
	APIPort              int              `json:"api_port,omitempty"`
	APIAddress           string           `json:"api_address,omitempty"`
	RunningJobs          []string         `json:"running_jobs,omitempty"`
	LastJobAt            time.Time        `json:"last_job_at,omitempty"`
	LastRepairAt         time.Time        `json:"last_repair_at,omitempty"`
	RunningRepairs       []string         `json:"running_repairs,omitempty"`
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
	ID                string    `json:"id"`
	Fingerprint       string    `json:"fingerprint"`
	Status            string    `json:"status"`
	Severity          string    `json:"severity"`
	Category          string    `json:"category"`
	Title             string    `json:"title"`
	Detail            string    `json:"detail,omitempty"`
	Evidence          string    `json:"evidence,omitempty"`
	SuiteID           string    `json:"suite_id,omitempty"`
	CaseID            string    `json:"case_id,omitempty"`
	RunID             string    `json:"run_id,omitempty"`
	TracePath         string    `json:"trace_path,omitempty"`
	TraceRunID        string    `json:"trace_run_id,omitempty"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	LastStatusAt      time.Time `json:"last_status_at"`
	Occurrences       int       `json:"occurrences"`
	ResolvedFromRun   string    `json:"resolved_from_run,omitempty"`
	VerificationRunID string    `json:"verification_run_id,omitempty"`
	VerifiedAt        time.Time `json:"verified_at,omitempty"`
	FailureStreak     int       `json:"failure_streak,omitempty"`
	RegressionStreak  int       `json:"regression_streak,omitempty"`
	ReopenCount       int       `json:"reopen_count,omitempty"`
	SeenRunIDs        []string  `json:"seen_run_ids,omitempty"`
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
	JobID      string    `json:"job_id,omitempty"`
	Attempt    int       `json:"attempt,omitempty"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	EvalRunIDs []string  `json:"eval_run_ids,omitempty"`
	GatePassed bool      `json:"gate_passed,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Jobs struct {
	UpdatedAt time.Time `json:"updated_at"`
	Jobs      []Job     `json:"jobs"`
}

type Job struct {
	ID                  string    `json:"id"`
	Enabled             bool      `json:"enabled"`
	Suite               string    `json:"suite"`
	Profile             string    `json:"profile,omitempty"`
	Judge               string    `json:"judge,omitempty"`
	JudgeProfile        string    `json:"judge_profile,omitempty"`
	Repeat              int       `json:"repeat,omitempty"`
	Workers             int       `json:"workers,omitempty"`
	Gate                Gate      `json:"gate"`
	Schedule            Schedule  `json:"schedule"`
	Retry               Retry     `json:"retry"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	NextRunAt           time.Time `json:"next_run_at,omitempty"`
	LastRunAt           time.Time `json:"last_run_at,omitempty"`
	LastRunIDs          []string  `json:"last_run_ids,omitempty"`
	LastStatus          string    `json:"last_status,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
}

type Gate struct {
	MinScore       float64 `json:"min_score,omitempty"`
	MinPassRate    float64 `json:"min_pass_rate,omitempty"`
	MinStability   float64 `json:"min_stability,omitempty"`
	MaxRegressions int     `json:"max_regressions"`
}

type Schedule struct {
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Cron            string `json:"cron,omitempty"`
}

type Retry struct {
	MaxAttempts    int `json:"max_attempts,omitempty"`
	BackoffSeconds int `json:"backoff_seconds,omitempty"`
}

type EvalRunOutcome struct {
	RunIDs     []string `json:"run_ids,omitempty"`
	GatePassed bool     `json:"gate_passed"`
}

type Event struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Type     string    `json:"type"`
	Severity string    `json:"severity,omitempty"`
	SourceID string    `json:"source_id,omitempty"`
	Data     any       `json:"data,omitempty"`
}

type Repairs struct {
	UpdatedAt time.Time    `json:"updated_at"`
	Repairs   []RepairTask `json:"repairs"`
}

type RepairTask struct {
	ID                string           `json:"id"`
	ActionID          string           `json:"action_id"`
	ActionFingerprint string           `json:"action_fingerprint"`
	Status            string           `json:"status"`
	Severity          string           `json:"severity"`
	SuiteID           string           `json:"suite_id,omitempty"`
	CaseID            string           `json:"case_id,omitempty"`
	SourceRunID       string           `json:"source_run_id,omitempty"`
	BaseCommit        string           `json:"base_commit"`
	Branch            string           `json:"branch"`
	WorktreePath      string           `json:"worktree_path"`
	ArtifactDir       string           `json:"artifact_dir"`
	Attempt           int              `json:"attempt"`
	MaxAttempts       int              `json:"max_attempts"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	StartedAt         time.Time        `json:"started_at,omitempty"`
	FinishedAt        time.Time        `json:"finished_at,omitempty"`
	ApprovedAt        time.Time        `json:"approved_at,omitempty"`
	RejectedAt        time.Time        `json:"rejected_at,omitempty"`
	MergedAt          time.Time        `json:"merged_at,omitempty"`
	RepairCommit      string           `json:"repair_commit,omitempty"`
	MergeCommit       string           `json:"merge_commit,omitempty"`
	Summary           string           `json:"summary,omitempty"`
	Error             string           `json:"error,omitempty"`
	ChangedFiles      []string         `json:"changed_files,omitempty"`
	DiffPath          string           `json:"diff_path,omitempty"`
	DiffBytes         int              `json:"diff_bytes,omitempty"`
	Execution         RepairExecution  `json:"execution,omitempty"`
	Validation        RepairValidation `json:"validation,omitempty"`
	MergeValidation   RepairValidation `json:"merge_validation,omitempty"`
	VerificationRunID string           `json:"verification_run_id,omitempty"`
}

type RepairExecution struct {
	Summary    string `json:"summary,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
}

type RepairValidation struct {
	Passed     bool              `json:"passed"`
	StartedAt  time.Time         `json:"started_at,omitempty"`
	FinishedAt time.Time         `json:"finished_at,omitempty"`
	Checks     []ValidationCheck `json:"checks,omitempty"`
	EvalRunIDs []string          `json:"eval_run_ids,omitempty"`
	GatePassed bool              `json:"gate_passed,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type ValidationCheck struct {
	Name       string `json:"name"`
	Command    string `json:"command,omitempty"`
	Passed     bool   `json:"passed"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	Error      string `json:"error,omitempty"`
}
