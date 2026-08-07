package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func ScoreCase(c Case, execution Execution) CaseResult {
	result := CaseResult{
		CaseID:       c.ID,
		Name:         c.Name,
		Tags:         append([]string(nil), c.Tags...),
		Status:       execution.Status,
		Error:        execution.Error,
		Output:       execution.Output,
		SessionID:    execution.SessionID,
		TraceRunID:   execution.TraceRunID,
		TracePath:    execution.TracePath,
		Workspace:    execution.Workspace,
		DurationMS:   execution.DurationMS,
		Turns:        execution.Turns,
		Tools:        append([]string(nil), execution.Tools...),
		ToolFailures: execution.ToolFailures,
		TotalTokens:  execution.TotalTokens,
		InputTokens:  execution.InputTokens,
		OutputTokens: execution.OutputTokens,
		Attempts:     1,
	}
	add := func(kind, expected, actual string, passed bool, weight float64, message string) {
		result.AssertionResults = append(result.AssertionResults, AssertionResult{
			Kind: kind, Expected: expected, Actual: actual, Passed: passed, Weight: weight, Message: message,
		})
	}
	assertions := c.Assertions
	if assertions.Status != "" {
		add("status", assertions.Status, execution.Status, execution.Status == assertions.Status, 2, "")
	}
	lowerOutput := strings.ToLower(execution.Output)
	for _, expected := range assertions.OutputContains {
		passed := strings.Contains(lowerOutput, strings.ToLower(expected))
		add("output_contains", expected, "", passed, 1, "")
	}
	for _, forbidden := range assertions.OutputNotContains {
		passed := !strings.Contains(lowerOutput, strings.ToLower(forbidden))
		add("output_not_contains", forbidden, "", passed, 1, "")
	}
	for _, pattern := range assertions.OutputRegex {
		matched, _ := regexp.MatchString(pattern, execution.Output)
		add("output_regex", pattern, "", matched, 1, "")
	}
	outputChars := len([]rune(execution.Output))
	if assertions.MinOutputChars > 0 {
		add("min_output_chars", strconv.Itoa(assertions.MinOutputChars), strconv.Itoa(outputChars), outputChars >= assertions.MinOutputChars, 0.5, "")
	}
	if assertions.MaxOutputChars > 0 {
		add("max_output_chars", strconv.Itoa(assertions.MaxOutputChars), strconv.Itoa(outputChars), outputChars <= assertions.MaxOutputChars, 0.5, "")
	}
	for _, tool := range assertions.RequiredTools {
		add("required_tool", tool, strings.Join(execution.Tools, ","), slices.Contains(execution.Tools, tool), 2, "")
	}
	for _, tool := range assertions.ForbiddenTools {
		add("forbidden_tool", tool, strings.Join(execution.Tools, ","), !slices.Contains(execution.Tools, tool), 2, "")
	}
	if assertions.MaxTurns > 0 {
		add("max_turns", strconv.Itoa(assertions.MaxTurns), strconv.Itoa(execution.Turns), execution.Turns <= assertions.MaxTurns, 1, "")
	}
	if assertions.MaxDurationMS > 0 {
		add("max_duration_ms", strconv.FormatInt(assertions.MaxDurationMS, 10), strconv.FormatInt(execution.DurationMS, 10), execution.DurationMS <= assertions.MaxDurationMS, 1, "")
	}
	if assertions.MaxToolFailures >= 0 && hasToolFailureAssertion(c) {
		add("max_tool_failures", strconv.Itoa(assertions.MaxToolFailures), strconv.Itoa(execution.ToolFailures), execution.ToolFailures <= assertions.MaxToolFailures, 2, "")
	}
	if assertions.MaxToolCalls > 0 {
		add("max_tool_calls", strconv.Itoa(assertions.MaxToolCalls), strconv.Itoa(len(execution.Tools)), len(execution.Tools) <= assertions.MaxToolCalls, 1, "")
	}
	if len(assertions.ToolSequence) > 0 {
		add("tool_sequence", strings.Join(assertions.ToolSequence, " -> "), strings.Join(execution.Tools, " -> "), orderedSubsequence(execution.Tools, assertions.ToolSequence), 2, "")
	}
	if assertions.NoConsecutiveRepeat {
		add("no_consecutive_tool_repeat", "no adjacent duplicate tools", strings.Join(execution.Tools, " -> "), !hasConsecutiveRepeat(execution.Tools), 1, "")
	}
	scoreStateAssertions(c, assertions, execution.Workspace, add)
	scoreCommandAssertions(assertions, execution.Workspace, add)
	scoreGitStatus(assertions, execution.Workspace, add)
	if assertions.Judge != nil && assertions.Judge.Enabled && judgeMode(assertions.Judge.Mode) != "llm" {
		judge := scoreHeuristicJudge(assertions, execution)
		result.Judge = &judge
		minScore := assertions.Judge.MinScore
		if minScore <= 0 {
			minScore = 80
		}
		add("judge_score", fmt.Sprintf(">= %.1f", minScore), fmt.Sprintf("%.1f", judge.Score), judge.Score >= minScore, 2, judge.Summary)
	}
	if execution.Error != "" {
		add("execution_error", "none", execution.Error, false, 3, "case execution failed")
	}
	if len(result.AssertionResults) == 0 {
		add("execution", "successful non-empty response", fmt.Sprintf("status=%s chars=%d", execution.Status, outputChars), execution.Error == "" && outputChars > 0, 1, "")
	}
	var earned, total float64
	result.Passed = true
	for _, assertion := range result.AssertionResults {
		total += assertion.Weight
		if assertion.Passed {
			earned += assertion.Weight
		} else {
			result.Passed = false
		}
	}
	if total > 0 {
		result.Score = earned / total * 100
	}
	if result.Passed {
		result.PassedAttempts = 1
		result.StabilityRate = 100
	}
	return result
}

func judgeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "heuristic"
	}
	return mode
}

func hasToolFailureAssertion(c Case) bool {
	// JSON 的零值无法区分“未设置”和显式 0。只要 case 涉及工具约束，
	// 默认要求工具零失败；其他 case 不凭空增加该断言。
	return len(c.Assertions.RequiredTools) > 0 || len(c.Assertions.ForbiddenTools) > 0 || c.Assertions.MaxToolFailures > 0
}

func orderedSubsequence(actual, expected []string) bool {
	index := 0
	for _, tool := range actual {
		if index < len(expected) && tool == expected[index] {
			index++
		}
	}
	return index == len(expected)
}

func hasConsecutiveRepeat(tools []string) bool {
	for i := 1; i < len(tools); i++ {
		if tools[i] == tools[i-1] {
			return true
		}
	}
	return false
}

func scoreStateAssertions(c Case, assertions Assertions, workspace string, add func(string, string, string, bool, float64, string)) {
	if strings.TrimSpace(workspace) == "" {
		if hasStateAssertions(assertions) {
			add("workspace", "non-empty workspace", "", false, 3, "state assertions require a workspace")
		}
		return
	}
	for _, path := range assertions.FilesExist {
		_, err := os.Stat(filepath.Join(workspace, filepath.Clean(path)))
		add("file_exists", path, stateActual(err), err == nil, 3, "")
	}
	for _, path := range assertions.FilesNotExist {
		_, err := os.Stat(filepath.Join(workspace, filepath.Clean(path)))
		add("file_not_exists", path, stateActual(err), os.IsNotExist(err), 3, "")
	}
	for path, values := range assertions.FileContains {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		for _, value := range values {
			passed := err == nil && strings.Contains(string(data), value)
			add("file_contains", path+": "+value, stateFileActual(data, err), passed, 3, "")
		}
	}
	for path, expected := range assertions.FileEquals {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		actual := stateFileActual(data, err)
		if err == nil {
			actual = string(data)
		}
		add("file_equals", path, actual, err == nil && string(data) == expected, 3, "")
	}
	for path, values := range assertions.FileNotContains {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		for _, value := range values {
			passed := err == nil && !strings.Contains(string(data), value)
			add("file_not_contains", path+": "+value, stateFileActual(data, err), passed, 3, "")
		}
	}
	for path, expected := range assertions.FileJSONEquals {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		passed, actual := jsonEqual(data, expected, err)
		add("file_json_equals", path, actual, passed, 4, "")
	}
	for path, values := range assertions.FileDiffContains {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		diff := unifiedFixtureDiff(c, assertions, path, string(data))
		if err != nil {
			diff = err.Error()
		}
		for _, value := range values {
			add("file_diff_contains", path+": "+value, truncateActual(diff), err == nil && strings.Contains(diff, value), 3, "")
		}
	}
}

func hasStateAssertions(assertions Assertions) bool {
	return len(assertions.FilesExist) > 0 ||
		len(assertions.FilesNotExist) > 0 ||
		len(assertions.FileEquals) > 0 ||
		len(assertions.FileContains) > 0 ||
		len(assertions.FileNotContains) > 0 ||
		len(assertions.FileJSONEquals) > 0 ||
		len(assertions.FileDiffContains) > 0
}

func jsonEqual(actual []byte, expected []byte, readErr error) (bool, string) {
	if readErr != nil {
		return false, readErr.Error()
	}
	var actualValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		return false, "invalid actual json: " + err.Error()
	}
	var expectedValue any
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		return false, "invalid expected json: " + err.Error()
	}
	if reflect.DeepEqual(actualValue, expectedValue) {
		return true, "json equal"
	}
	return false, truncateActual(string(actual))
}

func unifiedFixtureDiff(c Case, assertions Assertions, path string, actual string) string {
	before := c.Fixture.Files[path]
	if before == "" {
		before = assertions.FileEquals[path]
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(actual, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s.expected\n+++ %s.actual\n", path, path)
	max := len(beforeLines)
	if len(afterLines) > max {
		max = len(afterLines)
	}
	for i := 0; i < max; i++ {
		var oldLine, newLine string
		if i < len(beforeLines) {
			oldLine = beforeLines[i]
		}
		if i < len(afterLines) {
			newLine = afterLines[i]
		}
		if oldLine == newLine {
			continue
		}
		if i < len(beforeLines) {
			fmt.Fprintf(&b, "-%s\n", oldLine)
		}
		if i < len(afterLines) {
			fmt.Fprintf(&b, "+%s\n", newLine)
		}
	}
	return b.String()
}

func scoreCommandAssertions(assertions Assertions, workspace string, add func(string, string, string, bool, float64, string)) {
	for _, assertion := range assertions.CommandAssertions {
		name := assertion.Name
		if strings.TrimSpace(name) == "" {
			name = assertion.Command
		}
		output, exitCode, err := runAssertionCommand(workspace, assertion)
		actual := fmt.Sprintf("exit=%d output=%s", exitCode, truncateActual(output))
		if err != nil && exitCode == 0 {
			actual = err.Error()
		}
		add("command_exit_code", name+": "+strconv.Itoa(assertion.ExitCode), actual, err == nil && exitCode == assertion.ExitCode, 3, "")
		for _, expected := range assertion.OutputContains {
			add("command_output_contains", name+": "+expected, actual, err == nil && strings.Contains(output, expected), 2, "")
		}
		for _, forbidden := range assertion.OutputNotContains {
			add("command_output_not_contains", name+": "+forbidden, actual, err == nil && !strings.Contains(output, forbidden), 2, "")
		}
		for _, pattern := range assertion.OutputRegex {
			matched, _ := regexp.MatchString(pattern, output)
			add("command_output_regex", name+": "+pattern, actual, err == nil && matched, 2, "")
		}
	}
}

func runAssertionCommand(workspace string, assertion CommandAssertion) (string, int, error) {
	timeout := time.Duration(assertion.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", assertion.Command)
	if strings.TrimSpace(workspace) != "" {
		cmd.Dir = workspace
	}
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	if ctx.Err() != nil {
		return string(output), exitCode, ctx.Err()
	}
	if err != nil && exitCode != assertion.ExitCode {
		return string(output), exitCode, err
	}
	return string(output), exitCode, nil
}

func scoreGitStatus(assertions Assertions, workspace string, add func(string, string, string, bool, float64, string)) {
	if assertions.GitStatus == nil {
		return
	}
	output, err := gitStatus(workspace)
	actual := strings.TrimSpace(output)
	if err != nil {
		add("git_status", "git status available", err.Error(), false, 3, "")
		return
	}
	if assertions.GitStatus.Clean {
		add("git_clean", "clean working tree", actual, actual == "", 3, "")
	}
	changed := parseGitChangedPaths(output)
	if len(assertions.GitStatus.AllowedChanged) > 0 {
		allowed := stringSet(assertions.GitStatus.AllowedChanged)
		var unexpected []string
		for _, path := range changed {
			if !allowed[path] {
				unexpected = append(unexpected, path)
			}
		}
		add("git_allowed_changed", strings.Join(assertions.GitStatus.AllowedChanged, ","), strings.Join(unexpected, ","), len(unexpected) == 0, 3, "")
	}
	forbidden := stringSet(assertions.GitStatus.ForbiddenChanged)
	if len(forbidden) > 0 {
		var found []string
		for _, path := range changed {
			if forbidden[path] {
				found = append(found, path)
			}
		}
		add("git_forbidden_changed", strings.Join(assertions.GitStatus.ForbiddenChanged, ","), strings.Join(found, ","), len(found) == 0, 3, "")
	}
}

func gitStatus(workspace string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	if strings.TrimSpace(workspace) != "" {
		cmd.Dir = workspace
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func parseGitChangedPaths(output string) []string {
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = strings.TrimSpace(parts[len(parts)-1])
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func scoreHeuristicJudge(assertions Assertions, execution Execution) JudgeResult {
	judge := assertions.Judge
	result := JudgeResult{Enabled: true, Mode: strings.TrimSpace(judge.Mode), Score: 100}
	if result.Mode == "" {
		result.Mode = "heuristic"
	}
	if strings.TrimSpace(execution.Output) == "" {
		result.Score -= 45
		result.Reasons = append(result.Reasons, "empty output")
	}
	maxOutputChars := judge.MaxOutputChars
	if maxOutputChars <= 0 {
		maxOutputChars = assertions.MaxOutputChars
	}
	if maxOutputChars > 0 {
		outputChars := len([]rune(execution.Output))
		if outputChars > maxOutputChars {
			result.Score -= minFloat(30, float64(outputChars-maxOutputChars)/float64(maxOutputChars)*30)
			result.Reasons = append(result.Reasons, fmt.Sprintf("verbose output: %d chars > %d", outputChars, maxOutputChars))
		}
	}
	maxToolCalls := judge.MaxToolCalls
	if maxToolCalls <= 0 && judge.RequireNoToolOveruse {
		maxToolCalls = assertions.MaxToolCalls
	}
	if maxToolCalls > 0 && len(execution.Tools) > maxToolCalls {
		result.Score -= minFloat(35, float64(len(execution.Tools)-maxToolCalls)*10)
		result.Reasons = append(result.Reasons, fmt.Sprintf("tool overuse: %d calls > %d", len(execution.Tools), maxToolCalls))
	}
	if execution.ToolFailures > 0 {
		result.Score -= minFloat(30, float64(execution.ToolFailures)*12)
		result.Reasons = append(result.Reasons, fmt.Sprintf("tool failures: %d", execution.ToolFailures))
	}
	if execution.Error != "" {
		result.Score -= 30
		result.Reasons = append(result.Reasons, "execution error")
	}
	if execution.Status != "" && execution.Status != "done" {
		result.Score -= 10
		result.Reasons = append(result.Reasons, "non-done status: "+execution.Status)
	}
	if result.Score < 0 {
		result.Score = 0
	}
	minScore := judge.MinScore
	if minScore <= 0 {
		minScore = 80
	}
	result.Passed = result.Score >= minScore
	if len(result.Reasons) == 0 {
		result.Summary = "quality checks passed"
	} else {
		result.Summary = strings.Join(result.Reasons, "; ")
	}
	return result
}

func stateActual(err error) string {
	if err == nil {
		return "exists"
	}
	if os.IsNotExist(err) {
		return "missing"
	}
	return err.Error()
}

func stateFileActual(data []byte, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%d bytes", len(data))
}

func truncateActual(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 400 {
		return value
	}
	runes := []rune(value)
	return string(runes[:400]) + "...<truncated>"
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
