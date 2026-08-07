package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/llm"
)

type LLMJudgeOptions struct {
	Enabled     bool
	Mode        string
	Profile     string
	Model       string
	ArtifactDir string
	Timeout     time.Duration
}

type llmJudgePayload struct {
	Score           float64  `json:"score"`
	Passed          bool     `json:"passed"`
	Summary         string   `json:"summary"`
	Strengths       []string `json:"strengths"`
	Weaknesses      []string `json:"weaknesses"`
	FailureCategory string   `json:"failure_category"`
	RepairHint      string   `json:"repair_hint"`
}

type judgeArtifact struct {
	CaseID   string          `json:"case_id"`
	Attempt  int             `json:"attempt,omitempty"`
	Prompt   string          `json:"prompt"`
	Request  llm.ChatRequest `json:"request"`
	Raw      string          `json:"raw"`
	Result   *JudgeResult    `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
}

func ApplyLLMJudges(ctx context.Context, result RunResult, suite Suite, client llm.Client, opts LLMJudgeOptions) RunResult {
	if !opts.Enabled || strings.TrimSpace(opts.Mode) != "llm" || client == nil {
		return result
	}
	cases := map[string]Case{}
	for _, c := range suite.Cases {
		cases[c.ID] = c
	}
	for i := range result.Cases {
		c, ok := cases[result.Cases[i].CaseID]
		if !ok {
			continue
		}
		if c.Assertions.Judge == nil {
			c.Assertions.Judge = &JudgeAssertion{Enabled: true, Mode: "llm", MinScore: 80}
		}
		c.Assertions.Judge.Enabled = true
		c.Assertions.Judge.Mode = "llm"
		for attemptIndex := range result.Cases[i].AttemptResults {
			attempt := &result.Cases[i].AttemptResults[attemptIndex]
			judge := runLLMJudge(ctx, client, opts, c, attemptToCaseResult(result.Cases[i], *attempt), attempt.Attempt)
			attempt.Judge = &judge
		}
		judge := runLLMJudge(ctx, client, opts, c, result.Cases[i], 0)
		result.Cases[i].Judge = &judge
		minScore := c.Assertions.Judge.MinScore
		if minScore <= 0 {
			minScore = 80
		}
		result.Cases[i].AssertionResults = upsertJudgeAssertion(result.Cases[i].AssertionResults, minScore, judge)
		recalculateCaseScore(&result.Cases[i])
	}
	recalculateRunScore(&result)
	return result
}

func runLLMJudge(ctx context.Context, client llm.Client, opts LLMJudgeOptions, c Case, result CaseResult, attempt int) JudgeResult {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	judgeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := buildJudgeRequest(c, result)
	raw, err := collectLLMText(judgeCtx, client, req)
	if err != nil {
		judge := JudgeResult{Enabled: true, Mode: "llm", Score: 0, Passed: false, Error: err.Error(), Summary: "judge request failed"}
		judge.RawPath = writeJudgeArtifact(opts.ArtifactDir, c.ID, attempt, judgeArtifact{CaseID: c.ID, Attempt: attempt, Prompt: c.Prompt, Request: req, Error: err.Error(), Provider: opts.Profile, Model: opts.Model})
		return judge
	}
	payload, parseErr := parseJudgePayload(raw)
	if parseErr != nil {
		repairReq := buildJudgeRepairRequest(raw, parseErr)
		repaired, repairErr := collectLLMText(judgeCtx, client, repairReq)
		if repairErr == nil {
			if repairedPayload, err := parseJudgePayload(repaired); err == nil {
				raw = repaired
				payload = repairedPayload
				parseErr = nil
			} else {
				parseErr = err
			}
		} else {
			parseErr = repairErr
		}
	}
	if parseErr != nil {
		judge := JudgeResult{Enabled: true, Mode: "llm", Score: 0, Passed: false, Error: parseErr.Error(), Summary: "judge returned invalid json"}
		judge.RawPath = writeJudgeArtifact(opts.ArtifactDir, c.ID, attempt, judgeArtifact{CaseID: c.ID, Attempt: attempt, Prompt: c.Prompt, Request: req, Raw: raw, Result: &judge, Error: parseErr.Error(), Provider: opts.Profile, Model: opts.Model})
		return judge
	}
	judge := JudgeResult{
		Enabled:         true,
		Mode:            "llm",
		Score:           clampScore(payload.Score),
		Passed:          payload.Passed,
		Summary:         strings.TrimSpace(payload.Summary),
		Strengths:       cleanStringList(payload.Strengths),
		Weaknesses:      cleanStringList(payload.Weaknesses),
		FailureCategory: strings.TrimSpace(payload.FailureCategory),
		RepairHint:      strings.TrimSpace(payload.RepairHint),
	}
	minScore := c.Assertions.Judge.MinScore
	if minScore <= 0 {
		minScore = 80
	}
	if judge.Score < minScore {
		judge.Passed = false
	}
	if judge.Summary == "" {
		judge.Summary = "llm judge completed"
	}
	judge.RawPath = writeJudgeArtifact(opts.ArtifactDir, c.ID, attempt, judgeArtifact{CaseID: c.ID, Attempt: attempt, Prompt: c.Prompt, Request: req, Raw: raw, Result: &judge, Provider: opts.Profile, Model: opts.Model})
	return judge
}

func buildJudgeRequest(c Case, result CaseResult) llm.ChatRequest {
	judge := c.Assertions.Judge
	var b strings.Builder
	fmt.Fprintf(&b, "你是 Cohort Agent Eval 的严格评审器。只输出 JSON，不要输出 Markdown。\n\n")
	fmt.Fprintf(&b, "Case ID: %s\nName: %s\nUser prompt:\n%s\n\n", c.ID, c.Name, c.Prompt)
	if judge != nil {
		if judge.ExpectedBehavior != "" {
			fmt.Fprintf(&b, "Expected behavior:\n%s\n\n", judge.ExpectedBehavior)
		}
		if len(judge.Rubric) > 0 {
			fmt.Fprintf(&b, "Rubric:\n- %s\n\n", strings.Join(judge.Rubric, "\n- "))
		}
		if len(judge.FailureModes) > 0 {
			fmt.Fprintf(&b, "Known failure modes:\n- %s\n\n", strings.Join(judge.FailureModes, "\n- "))
		}
	}
	fmt.Fprintf(&b, "Agent output:\n%s\n\n", truncateJudgeText(result.Output, 8000))
	fmt.Fprintf(&b, "Execution evidence:\nstatus=%s error=%s turns=%d tools=%s tool_failures=%d score=%.1f passed=%t\n\n",
		result.Status, result.Error, result.Turns, strings.Join(result.Tools, " -> "), result.ToolFailures, result.Score, result.Passed)
	fmt.Fprintf(&b, "Assertion results:\n")
	for _, assertion := range result.AssertionResults {
		fmt.Fprintf(&b, "- kind=%s passed=%t expected=%q actual=%q message=%q\n", assertion.Kind, assertion.Passed, assertion.Expected, assertion.Actual, assertion.Message)
	}
	fmt.Fprintf(&b, "\nReturn exactly this JSON schema:\n{\"score\":0-100,\"passed\":true|false,\"summary\":\"short Chinese summary\",\"strengths\":[\"...\"],\"weaknesses\":[\"...\"],\"failure_category\":\"none|answer_quality|state_assertion|tool_routing|trajectory|runtime|flaky|other\",\"repair_hint\":\"specific next fix\"}\n")
	return llm.ChatRequest{
		System: "You are a deterministic eval judge. Return strict JSON only. Do not call tools.",
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: b.String(),
		}},
	}
}

func buildJudgeRepairRequest(raw string, parseErr error) llm.ChatRequest {
	return llm.ChatRequest{
		System: "Return strict JSON only.",
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: fmt.Sprintf("下面的 Judge 输出不是合法 JSON，错误是 %s。请只返回修复后的 JSON，不要解释。\n\n%s",
				parseErr.Error(), truncateJudgeText(raw, 12000)),
		}},
	}
}

func collectLLMText(ctx context.Context, client llm.Client, req llm.ChatRequest) (string, error) {
	stream, err := client.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var final *llm.Response
	for event := range stream {
		switch event.Type {
		case llm.EventText:
			b.WriteString(event.Text)
		case llm.EventDone:
			final = event.Response
		case llm.EventError:
			if event.Err != nil {
				return "", event.Err
			}
			return "", errors.New("llm judge failed")
		}
	}
	if final != nil && strings.TrimSpace(final.Content) != "" {
		return final.Content, nil
	}
	return b.String(), nil
}

func parseJudgePayload(raw string) (llmJudgePayload, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			raw = raw[start : end+1]
		}
	}
	var payload llmJudgePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return payload, err
	}
	if payload.Score < 0 || payload.Score > 100 {
		return payload, fmt.Errorf("judge score %.1f out of range", payload.Score)
	}
	return payload, nil
}

func upsertJudgeAssertion(assertions []AssertionResult, minScore float64, judge JudgeResult) []AssertionResult {
	passed := judge.Error == "" && judge.Score >= minScore && judge.Passed
	message := judge.Summary
	if judge.RepairHint != "" {
		message = strings.TrimSpace(message + " repair_hint=" + judge.RepairHint)
	}
	if judge.Error != "" {
		message = judge.Error
	}
	assertion := AssertionResult{
		Kind:     "judge_score",
		Expected: fmt.Sprintf(">= %.1f", minScore),
		Actual:   fmt.Sprintf("%.1f", judge.Score),
		Passed:   passed,
		Weight:   2,
		Message:  message,
	}
	for i := range assertions {
		if assertions[i].Kind == "judge_score" {
			assertions[i] = assertion
			return assertions
		}
	}
	return append(assertions, assertion)
}

func recalculateCaseScore(c *CaseResult) {
	var total, earned float64
	c.Passed = true
	for _, assertion := range c.AssertionResults {
		total += assertion.Weight
		if assertion.Passed {
			earned += assertion.Weight
		} else {
			c.Passed = false
		}
	}
	if total > 0 {
		c.Score = earned / total * 100
	}
}

func recalculateRunScore(result *RunResult) {
	result.PassedCases = 0
	result.FailedCases = 0
	result.Score = 0
	for _, c := range result.Cases {
		if c.Passed {
			result.PassedCases++
		} else {
			result.FailedCases++
		}
		result.Score += c.Score
	}
	result.TotalCases = len(result.Cases)
	if result.TotalCases > 0 {
		result.Score /= float64(result.TotalCases)
		result.PassRate = float64(result.PassedCases) / float64(result.TotalCases) * 100
	}
}

func attemptToCaseResult(parent CaseResult, attempt AttemptResult) CaseResult {
	return CaseResult{
		CaseID:           parent.CaseID,
		Name:             parent.Name,
		Tags:             append([]string(nil), parent.Tags...),
		Passed:           attempt.Passed,
		Score:            attempt.Score,
		Status:           attempt.Status,
		Error:            attempt.Error,
		Output:           attempt.Output,
		SessionID:        attempt.SessionID,
		TraceRunID:       attempt.TraceRunID,
		TracePath:        attempt.TracePath,
		Workspace:        attempt.Workspace,
		DurationMS:       attempt.DurationMS,
		Turns:            attempt.Turns,
		Tools:            append([]string(nil), attempt.Tools...),
		ToolFailures:     attempt.ToolFailures,
		TotalTokens:      attempt.TotalTokens,
		InputTokens:      attempt.InputTokens,
		OutputTokens:     attempt.OutputTokens,
		Attempts:         1,
		PassedAttempts:   boolInt(attempt.Passed),
		StabilityRate:    parent.StabilityRate,
		AssertionResults: append([]AssertionResult(nil), attempt.AssertionResults...),
	}
}

func writeJudgeArtifact(root string, caseID string, attempt int, artifact judgeArtifact) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	name := "case"
	if attempt > 0 {
		name = fmt.Sprintf("attempt-%02d", attempt)
	}
	dir := filepath.Join(root, sanitizeID(caseID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	path := filepath.Join(dir, name+".json")
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return ""
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return ""
	}
	return path
}

func truncateJudgeText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "...[truncated]"
}

func clampScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func cleanStringList(values []string) []string {
	var result []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
