package evaluation

import "time"

const SchemaVersion = 1

type Suite struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ToolGroups    []string `json:"tool_groups,omitempty"`
	Cases         []Case   `json:"cases"`
}

type Case struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prompt     string     `json:"prompt"`
	Tags       []string   `json:"tags,omitempty"`
	TimeoutSec int        `json:"timeout_seconds,omitempty"`
	Assertions Assertions `json:"assertions"`
}

type Assertions struct {
	Status            string   `json:"status,omitempty"`
	OutputContains    []string `json:"output_contains,omitempty"`
	OutputNotContains []string `json:"output_not_contains,omitempty"`
	OutputRegex       []string `json:"output_regex,omitempty"`
	MinOutputChars    int      `json:"min_output_chars,omitempty"`
	MaxOutputChars    int      `json:"max_output_chars,omitempty"`
	RequiredTools     []string `json:"required_tools,omitempty"`
	ForbiddenTools    []string `json:"forbidden_tools,omitempty"`
	MaxTurns          int      `json:"max_turns,omitempty"`
	MaxDurationMS     int64    `json:"max_duration_ms,omitempty"`
	MaxToolFailures   int      `json:"max_tool_failures,omitempty"`
}

type RunResult struct {
	SchemaVersion int          `json:"schema_version"`
	RunID         string       `json:"run_id"`
	SuiteID       string       `json:"suite_id"`
	SuiteName     string       `json:"suite_name"`
	Model         string       `json:"model,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
	TotalCases    int          `json:"total_cases"`
	PassedCases   int          `json:"passed_cases"`
	FailedCases   int          `json:"failed_cases"`
	PassRate      float64      `json:"pass_rate"`
	Score         float64      `json:"score"`
	TotalTokens   int64        `json:"total_tokens,omitempty"`
	InputTokens   int64        `json:"input_tokens,omitempty"`
	OutputTokens  int64        `json:"output_tokens,omitempty"`
	Cases         []CaseResult `json:"cases"`
	Baseline      *Comparison  `json:"baseline,omitempty"`
}

type CaseResult struct {
	CaseID           string            `json:"case_id"`
	Name             string            `json:"name"`
	Tags             []string          `json:"tags,omitempty"`
	Passed           bool              `json:"passed"`
	Score            float64           `json:"score"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
	Output           string            `json:"output,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	DurationMS       int64             `json:"duration_ms"`
	Turns            int               `json:"turns"`
	Tools            []string          `json:"tools,omitempty"`
	ToolFailures     int               `json:"tool_failures"`
	TotalTokens      int64             `json:"total_tokens,omitempty"`
	InputTokens      int64             `json:"input_tokens,omitempty"`
	OutputTokens     int64             `json:"output_tokens,omitempty"`
	AssertionResults []AssertionResult `json:"assertion_results"`
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
	DurationMS   int64
	Turns        int
	Tools        []string
	ToolFailures int
	TotalTokens  int64
	InputTokens  int64
	OutputTokens int64
}
