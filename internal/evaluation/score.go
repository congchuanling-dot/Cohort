package evaluation

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	scoreStateAssertions(assertions, execution.Workspace, add)
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

func scoreStateAssertions(assertions Assertions, workspace string, add func(string, string, string, bool, float64, string)) {
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
	for path, values := range assertions.FileNotContains {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		for _, value := range values {
			passed := err == nil && !strings.Contains(string(data), value)
			add("file_not_contains", path+": "+value, stateFileActual(data, err), passed, 3, "")
		}
	}
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
