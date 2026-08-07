package evaluation

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

type Suite struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ToolGroups    []string `json:"tool_groups,omitempty"`
	DefaultRepeat int      `json:"default_repeat,omitempty"`
	Cases         []Case   `json:"cases"`
}

type Case struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Prompt      string                  `json:"prompt"`
	Tags        []string                `json:"tags,omitempty"`
	TimeoutSec  int                     `json:"timeout_seconds,omitempty"`
	Repeat      int                     `json:"repeat,omitempty"`
	Environment EnvironmentRequirements `json:"environment,omitempty"`
	Fixture     Fixture                 `json:"fixture,omitempty"`
	Assertions  Assertions              `json:"assertions"`
}

type EnvironmentRequirements struct {
	OperatingSystems   []string `json:"operating_systems,omitempty"`
	Commands           []string `json:"commands,omitempty"`
	Applications       []string `json:"applications,omitempty"`
	Environment        []string `json:"environment,omitempty"`
	BrowserBridge      bool     `json:"browser_bridge,omitempty"`
	DesktopPermissions bool     `json:"desktop_permissions,omitempty"`
	OnMissing          string   `json:"on_missing,omitempty"`
}

type Fixture struct {
	// Mode 支持 project 和 temp。temp 会为每个 attempt 创建全新工作区。
	Mode  string            `json:"mode,omitempty"`
	Files map[string]string `json:"files,omitempty"`
}

type Assertions struct {
	Status              string                     `json:"status,omitempty"`
	OutputContains      []string                   `json:"output_contains,omitempty"`
	OutputNotContains   []string                   `json:"output_not_contains,omitempty"`
	OutputRegex         []string                   `json:"output_regex,omitempty"`
	MinOutputChars      int                        `json:"min_output_chars,omitempty"`
	MaxOutputChars      int                        `json:"max_output_chars,omitempty"`
	RequiredTools       []string                   `json:"required_tools,omitempty"`
	ForbiddenTools      []string                   `json:"forbidden_tools,omitempty"`
	MaxTurns            int                        `json:"max_turns,omitempty"`
	MaxDurationMS       int64                      `json:"max_duration_ms,omitempty"`
	MaxToolFailures     int                        `json:"max_tool_failures,omitempty"`
	MaxToolCalls        int                        `json:"max_tool_calls,omitempty"`
	ToolSequence        []string                   `json:"tool_sequence,omitempty"`
	NoConsecutiveRepeat bool                       `json:"no_consecutive_tool_repeat,omitempty"`
	FilesExist          []string                   `json:"files_exist,omitempty"`
	FilesNotExist       []string                   `json:"files_not_exist,omitempty"`
	FileEquals          map[string]string          `json:"file_equals,omitempty"`
	FileContains        map[string][]string        `json:"file_contains,omitempty"`
	FileNotContains     map[string][]string        `json:"file_not_contains,omitempty"`
	FileJSONEquals      map[string]json.RawMessage `json:"file_json_equals,omitempty"`
	FileDiffContains    map[string][]string        `json:"file_diff_contains,omitempty"`
	CommandAssertions   []CommandAssertion         `json:"command_assertions,omitempty"`
	GitStatus           *GitStatusAssertion        `json:"git_status,omitempty"`
	Judge               *JudgeAssertion            `json:"judge,omitempty"`
}

type CommandAssertion struct {
	Name              string   `json:"name,omitempty"`
	Command           string   `json:"command"`
	ExitCode          int      `json:"exit_code,omitempty"`
	OutputContains    []string `json:"output_contains,omitempty"`
	OutputNotContains []string `json:"output_not_contains,omitempty"`
	OutputRegex       []string `json:"output_regex,omitempty"`
	TimeoutSec        int      `json:"timeout_seconds,omitempty"`
}

type GitStatusAssertion struct {
	Clean            bool     `json:"clean,omitempty"`
	AllowedChanged   []string `json:"allowed_changed,omitempty"`
	ForbiddenChanged []string `json:"forbidden_changed,omitempty"`
}

type JudgeAssertion struct {
	Enabled              bool     `json:"enabled,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	MinScore             float64  `json:"min_score,omitempty"`
	Rubric               []string `json:"rubric,omitempty"`
	ExpectedBehavior     string   `json:"expected_behavior,omitempty"`
	FailureModes         []string `json:"failure_modes,omitempty"`
	MaxOutputChars       int      `json:"max_output_chars,omitempty"`
	MaxToolCalls         int      `json:"max_tool_calls,omitempty"`
	RequireNoToolOveruse bool     `json:"require_no_tool_overuse,omitempty"`
}

type JudgeResult struct {
	Enabled         bool     `json:"enabled,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	Score           float64  `json:"score,omitempty"`
	Passed          bool     `json:"passed,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	Strengths       []string `json:"strengths,omitempty"`
	Weaknesses      []string `json:"weaknesses,omitempty"`
	FailureCategory string   `json:"failure_category,omitempty"`
	RepairHint      string   `json:"repair_hint,omitempty"`
	RawPath         string   `json:"raw_path,omitempty"`
	Error           string   `json:"error,omitempty"`
}

type RunResult struct {
	SchemaVersion int          `json:"schema_version"`
	RunID         string       `json:"run_id"`
	SuiteID       string       `json:"suite_id"`
	SuiteName     string       `json:"suite_name"`
	Profile       string       `json:"profile,omitempty"`
	Model         string       `json:"model,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
	TotalCases    int          `json:"total_cases"`
	PassedCases   int          `json:"passed_cases"`
	FailedCases   int          `json:"failed_cases"`
	SkippedCases  int          `json:"skipped_cases,omitempty"`
	PassRate      float64      `json:"pass_rate"`
	Score         float64      `json:"score"`
	TotalTokens   int64        `json:"total_tokens,omitempty"`
	InputTokens   int64        `json:"input_tokens,omitempty"`
	OutputTokens  int64        `json:"output_tokens,omitempty"`
	Cases         []CaseResult `json:"cases"`
	Baseline      *Comparison  `json:"baseline,omitempty"`
	Gate          *GateResult  `json:"gate,omitempty"`
}

type CaseResult struct {
	CaseID           string            `json:"case_id"`
	Name             string            `json:"name"`
	Tags             []string          `json:"tags,omitempty"`
	Passed           bool              `json:"passed"`
	Skipped          bool              `json:"skipped,omitempty"`
	SkipReason       string            `json:"skip_reason,omitempty"`
	Score            float64           `json:"score"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
	Output           string            `json:"output,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	TraceRunID       string            `json:"trace_run_id,omitempty"`
	TracePath        string            `json:"trace_path,omitempty"`
	Workspace        string            `json:"workspace,omitempty"`
	DurationMS       int64             `json:"duration_ms"`
	Turns            int               `json:"turns"`
	Tools            []string          `json:"tools,omitempty"`
	ToolFailures     int               `json:"tool_failures"`
	TotalTokens      int64             `json:"total_tokens,omitempty"`
	InputTokens      int64             `json:"input_tokens,omitempty"`
	OutputTokens     int64             `json:"output_tokens,omitempty"`
	Attempts         int               `json:"attempts"`
	PassedAttempts   int               `json:"passed_attempts"`
	StabilityRate    float64           `json:"stability_rate"`
	Judge            *JudgeResult      `json:"judge,omitempty"`
	Trace            *TraceSummary     `json:"trace,omitempty"`
	ActionItems      []ActionItem      `json:"action_items,omitempty"`
	AttemptResults   []AttemptResult   `json:"attempt_results,omitempty"`
	AssertionResults []AssertionResult `json:"assertion_results"`
}

type AttemptResult struct {
	Attempt          int               `json:"attempt"`
	Passed           bool              `json:"passed"`
	Skipped          bool              `json:"skipped,omitempty"`
	SkipReason       string            `json:"skip_reason,omitempty"`
	Score            float64           `json:"score"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
	Output           string            `json:"output,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	TraceRunID       string            `json:"trace_run_id,omitempty"`
	TracePath        string            `json:"trace_path,omitempty"`
	Workspace        string            `json:"workspace,omitempty"`
	DurationMS       int64             `json:"duration_ms"`
	Turns            int               `json:"turns"`
	Tools            []string          `json:"tools,omitempty"`
	ToolFailures     int               `json:"tool_failures"`
	TotalTokens      int64             `json:"total_tokens,omitempty"`
	InputTokens      int64             `json:"input_tokens,omitempty"`
	OutputTokens     int64             `json:"output_tokens,omitempty"`
	Judge            *JudgeResult      `json:"judge,omitempty"`
	Trace            *TraceSummary     `json:"trace,omitempty"`
	AssertionResults []AssertionResult `json:"assertion_results"`
}

type TraceSummary struct {
	Status         string              `json:"status,omitempty"`
	EventCount     int                 `json:"event_count,omitempty"`
	TurnCount      int                 `json:"turn_count,omitempty"`
	WarningCount   int                 `json:"warning_count,omitempty"`
	ErrorCount     int                 `json:"error_count,omitempty"`
	ContextBuilds  int                 `json:"context_builds,omitempty"`
	LLMCalls       int                 `json:"llm_calls,omitempty"`
	LLMDurationMS  int64               `json:"llm_duration_ms,omitempty"`
	ToolCalls      int                 `json:"tool_calls,omitempty"`
	ToolFailures   int                 `json:"tool_failures,omitempty"`
	ToolDurationMS int64               `json:"tool_duration_ms,omitempty"`
	DurationMS     int64               `json:"duration_ms,omitempty"`
	TotalTokens    int64               `json:"total_tokens,omitempty"`
	InputTokens    int64               `json:"input_tokens,omitempty"`
	OutputTokens   int64               `json:"output_tokens,omitempty"`
	Timeline       []TraceTimelineItem `json:"timeline,omitempty"`
	SlowestGaps    []TraceGap          `json:"slowest_gaps,omitempty"`
}

type TraceTimelineItem struct {
	OffsetMS      int64  `json:"offset_ms"`
	Turn          int    `json:"turn,omitempty"`
	EventType     string `json:"event_type"`
	Severity      string `json:"severity,omitempty"`
	Summary       string `json:"summary,omitempty"`
	SincePrevious int64  `json:"since_previous_ms,omitempty"`
}

type TraceGap struct {
	FromEvent string `json:"from_event"`
	ToEvent   string `json:"to_event"`
	GapMS     int64  `json:"gap_ms"`
	Turn      int    `json:"turn,omitempty"`
}

type ActionItem struct {
	ID         string `json:"id"`
	Scope      string `json:"scope"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	SuiteID    string `json:"suite_id,omitempty"`
	CaseID     string `json:"case_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	TracePath  string `json:"trace_path,omitempty"`
	TraceRunID string `json:"trace_run_id,omitempty"`
}

type AssertionResult struct {
	Kind     string  `json:"kind"`
	Expected string  `json:"expected"`
	Actual   string  `json:"actual,omitempty"`
	Passed   bool    `json:"passed"`
	Weight   float64 `json:"weight"`
	Message  string  `json:"message,omitempty"`
}

type Comparison struct {
	RunID           string   `json:"run_id"`
	ScoreDelta      float64  `json:"score_delta"`
	PassRateDelta   float64  `json:"pass_rate_delta"`
	DurationDeltaMS int64    `json:"duration_delta_ms"`
	TokenDelta      int64    `json:"token_delta"`
	RegressedCases  []string `json:"regressed_cases,omitempty"`
	ImprovedCases   []string `json:"improved_cases,omitempty"`
}

type Execution struct {
	Status       string
	Output       string
	Error        string
	SessionID    string
	TraceRunID   string
	TracePath    string
	Workspace    string
	DurationMS   int64
	Turns        int
	Tools        []string
	ToolFailures int
	TotalTokens  int64
	InputTokens  int64
	OutputTokens int64
	Skipped      bool
	SkipReason   string
}

type GateConfig struct {
	MinScore       float64
	MinPassRate    float64
	MinStability   float64
	MaxRegressions int
	AllowFailures  bool
}

type GateResult struct {
	Passed     bool     `json:"passed"`
	Violations []string `json:"violations,omitempty"`
}
