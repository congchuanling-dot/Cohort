package replay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type RunMetrics struct {
	Status          string `json:"status"`
	Turns           int    `json:"turns"`
	LLMCalls        int    `json:"llm_calls"`
	ToolCalls       int    `json:"tool_calls"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	CacheReadTokens int    `json:"cache_read_tokens"`
	DurationMS      int64  `json:"duration_ms"`
	FinalHash       string `json:"final_hash,omitempty"`
}

type TrialResult struct {
	Index               int        `json:"index"`
	SessionID           string     `json:"session_id,omitempty"`
	RunID               string     `json:"run_id,omitempty"`
	Status              string     `json:"status"`
	Error               string     `json:"error,omitempty"`
	Metrics             RunMetrics `json:"metrics"`
	FirstDivergenceTurn int        `json:"first_divergence_turn,omitempty"`
}

type ExperimentReport struct {
	SchemaVersion  int           `json:"schema_version"`
	ID             string        `json:"id"`
	CreatedAt      time.Time     `json:"created_at"`
	SourceSession  string        `json:"source_session"`
	SourceRun      string        `json:"source_run"`
	ForkTurn       int           `json:"fork_turn"`
	Model          string        `json:"model,omitempty"`
	SystemPrompt   string        `json:"system_prompt,omitempty"`
	Baseline       RunMetrics    `json:"baseline"`
	Trials         []TrialResult `json:"trials"`
	Successful     int           `json:"successful"`
	SuccessRate    float64       `json:"success_rate"`
	MeanTokens     float64       `json:"mean_tokens"`
	MeanDurationMS float64       `json:"mean_duration_ms"`
	ProofHash      string        `json:"proof_hash"`
}

func Metrics(manifest Manifest, frames []Frame) RunMetrics {
	metrics := RunMetrics{Status: manifest.FinalStatus}
	if !manifest.CompletedAt.IsZero() {
		metrics.DurationMS = manifest.CompletedAt.Sub(manifest.CreatedAt).Milliseconds()
	}
	for _, frame := range frames {
		if frame.Turn > metrics.Turns {
			metrics.Turns = frame.Turn
		}
		switch frame.Kind {
		case FrameResponse:
			metrics.LLMCalls++
			metrics.InputTokens += frame.Response.Usage.InputTokens
			metrics.OutputTokens += frame.Response.Usage.OutputTokens
			metrics.CacheReadTokens += frame.Response.Usage.CacheReadInputTokens
			if len(frame.Response.ToolCalls) == 0 {
				metrics.FinalHash = StableHash(frame.Response.Content)
			}
		case FrameTool:
			metrics.ToolCalls++
		}
	}
	return metrics
}

func FirstDivergenceTurn(baseline, candidate []Frame) int {
	baselineResponses := map[int]string{}
	for _, frame := range baseline {
		if frame.Response != nil {
			baselineResponses[frame.Turn] = frame.Response.Hash
		}
	}
	for _, frame := range candidate {
		if frame.Response == nil {
			continue
		}
		if expected, ok := baselineResponses[frame.Turn]; !ok || expected != frame.Response.Hash {
			return frame.Turn
		}
	}
	return 0
}

func FinalizeReport(report *ExperimentReport) {
	if report == nil {
		return
	}
	// FinalizeReport 会在执行完成和原子落盘前调用，必须保持幂等。
	report.Successful = 0
	report.SuccessRate = 0
	report.MeanTokens = 0
	report.MeanDurationMS = 0
	report.ProofHash = ""
	var tokens int64
	var duration int64
	for _, trial := range report.Trials {
		if trial.Error == "" && trial.Status == "done" {
			report.Successful++
		}
		tokens += int64(trial.Metrics.InputTokens + trial.Metrics.OutputTokens)
		duration += trial.Metrics.DurationMS
	}
	if len(report.Trials) > 0 {
		report.SuccessRate = float64(report.Successful) / float64(len(report.Trials))
		report.MeanTokens = float64(tokens) / float64(len(report.Trials))
		report.MeanDurationMS = float64(duration) / float64(len(report.Trials))
	}
	report.ProofHash = StableHash(report)
}

func WriteReport(path string, report ExperimentReport) error {
	if report.ID == "" {
		return errors.New("experiment report id is required")
	}
	FinalizeReport(&report)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ArchiveBundle(sessionRoot, sessionID, runID, destination string) error {
	source := filepath.Join(sessionRoot, sessionID, ReplayDirName, runID)
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for _, name := range []string{ManifestFileName, FramesFileName, RuntimeFileName} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}
