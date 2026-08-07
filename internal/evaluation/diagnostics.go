package evaluation

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"cohort/internal/traceview"
)

const maxEmbeddedTraceEvents = 80

// EnrichDiagnostics turns raw eval evidence into report-ready diagnostics.
// It is intentionally deterministic: external LLM judges or SaaS sinks can
// consume the same ActionItems later without changing the local eval result.
func EnrichDiagnostics(result RunResult) RunResult {
	regressed := map[string]bool{}
	if result.Baseline != nil {
		for _, caseID := range result.Baseline.RegressedCases {
			regressed[caseID] = true
		}
	}
	for i := range result.Cases {
		enrichCaseDiagnostics(result.SuiteID, result.RunID, regressed[result.Cases[i].CaseID], &result.Cases[i])
	}
	return result
}

func enrichCaseDiagnostics(suiteID, runID string, regressed bool, c *CaseResult) {
	if trace := loadTraceSummary(c.SessionID, c.TraceRunID, c.TracePath); trace != nil {
		c.Trace = trace
	}
	for i := range c.AttemptResults {
		attempt := &c.AttemptResults[i]
		if trace := loadTraceSummary(attempt.SessionID, attempt.TraceRunID, attempt.TracePath); trace != nil {
			attempt.Trace = trace
		}
	}
	c.ActionItems = buildCaseActionItems(suiteID, runID, regressed, *c)
}

func loadTraceSummary(sessionID, runID, tracePath string) *TraceSummary {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(tracePath) == "" {
		return nil
	}
	sessionRoot := filepath.Dir(filepath.Dir(tracePath))
	view, err := traceview.LoadSessionRun(sessionRoot, sessionID, runID)
	if err != nil {
		return nil
	}
	summary := view.Summary()
	trace := TraceSummary{
		Status:         summary.Status,
		EventCount:     summary.EventCount,
		TurnCount:      summary.TurnCount,
		WarningCount:   summary.WarningCount,
		ErrorCount:     summary.ErrorCount,
		ContextBuilds:  summary.ContextBuilds,
		LLMCalls:       summary.LLMCalls,
		LLMDurationMS:  summary.LLMDurationMS,
		ToolCalls:      summary.ToolCalls,
		ToolFailures:   summary.ToolFailures,
		ToolDurationMS: summary.ToolDurationMS,
		DurationMS:     summary.DurationMS,
		TotalTokens:    summary.TotalTokens,
		InputTokens:    summary.InputTokens,
		OutputTokens:   summary.OutputTokens,
	}
	for _, item := range boundedTimeline(summary.Timeline, maxEmbeddedTraceEvents) {
		trace.Timeline = append(trace.Timeline, TraceTimelineItem{
			OffsetMS:      item.OffsetMS,
			Turn:          item.Turn,
			EventType:     item.EventType,
			Severity:      item.Severity,
			Summary:       item.Summary,
			SincePrevious: item.SincePrevious,
		})
	}
	for i, gap := range summary.Gaps {
		if i >= 3 {
			break
		}
		trace.SlowestGaps = append(trace.SlowestGaps, TraceGap{
			FromEvent: gap.FromEvent,
			ToEvent:   gap.ToEvent,
			GapMS:     gap.GapMS,
			Turn:      gap.Turn,
		})
	}
	return &trace
}

func boundedTimeline(items []traceview.TimelineItem, limit int) []traceview.TimelineItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	head := limit / 2
	tail := limit - head
	bounded := make([]traceview.TimelineItem, 0, limit)
	bounded = append(bounded, items[:head]...)
	bounded = append(bounded, items[len(items)-tail:]...)
	return bounded
}

func buildCaseActionItems(suiteID, runID string, regressed bool, c CaseResult) []ActionItem {
	var items []ActionItem
	seen := map[string]bool{}
	add := func(severity, category, title, detail, evidence string) {
		key := severity + "\x00" + category + "\x00" + title
		if seen[key] {
			return
		}
		seen[key] = true
		items = append(items, ActionItem{
			ID:         actionID(runID, c.CaseID, len(items)+1),
			Scope:      "case",
			Severity:   severity,
			Category:   category,
			Title:      title,
			Detail:     detail,
			Evidence:   evidence,
			SuiteID:    suiteID,
			CaseID:     c.CaseID,
			RunID:      runID,
			TracePath:  c.TracePath,
			TraceRunID: c.TraceRunID,
		})
	}
	if regressed {
		add("critical", "regression", "优先修复回归 case", "该 case 从上一条可比基线的通过状态变为失败，先查看 trace 与失败断言，再补回归测试或调整执行策略。", "baseline regression")
	}
	if c.Attempts > 1 && c.PassedAttempts > 0 && c.PassedAttempts < c.Attempts {
		add("high", "flaky", "治理不稳定 case", "同一 case 多次 attempt 同时出现通过和失败，优先收敛非确定性路径、工具顺序和等待条件。", fmt.Sprintf("passed_attempts=%d attempts=%d stability=%.1f%%", c.PassedAttempts, c.Attempts, c.StabilityRate))
	}
	if c.ToolFailures > 0 {
		add("high", "tool_failure", "消除工具失败", "工具失败会污染轨迹和质量评分；先从 trace 中失败工具的错误码、参数摘要和耗时定位根因。", fmt.Sprintf("tool_failures=%d", c.ToolFailures))
	}
	if c.Trace != nil {
		if c.Trace.ErrorCount > 0 {
			add("high", "trace_error", "排查 trace 错误事件", "run.log 中存在 error 级事件，需要按时间线定位第一个错误事件及其前置工具/LLM 请求。", fmt.Sprintf("errors=%d warnings=%d", c.Trace.ErrorCount, c.Trace.WarningCount))
		} else if c.Trace.WarningCount > 0 && !c.Passed {
			add("medium", "trace_warning", "清理失败路径中的 warning 事件", "失败 case 的 trace 存在 warning，建议先处理 warning 中暴露的上下文、权限或工具异常。", fmt.Sprintf("warnings=%d", c.Trace.WarningCount))
		}
		if len(c.Trace.SlowestGaps) > 0 && c.Trace.SlowestGaps[0].GapMS >= 3000 {
			add("medium", "latency", "压缩慢事件间隔", "trace 中存在明显长间隔，优先检查对应 LLM 或工具调用是否可缓存、裁剪或提前失败。", fmt.Sprintf("%s -> %s gap=%dms", c.Trace.SlowestGaps[0].FromEvent, c.Trace.SlowestGaps[0].ToEvent, c.Trace.SlowestGaps[0].GapMS))
		}
	}
	for _, assertion := range c.AssertionResults {
		if assertion.Passed {
			continue
		}
		severity, category, title, detail := actionForAssertion(assertion.Kind)
		evidence := fmt.Sprintf("%s expected=%q actual=%q", assertion.Kind, assertion.Expected, assertion.Actual)
		if assertion.Message != "" {
			evidence += " message=" + assertion.Message
		}
		add(severity, category, title, detail, evidence)
	}
	if !c.Passed && len(items) == 0 {
		add("medium", "unknown_failure", "补充失败诊断", "该 case 已失败但没有足够结构化断言，建议增加状态断言、工具轨迹断言或 Judge rubric。", "no failed assertion")
	}
	return items
}

func actionForAssertion(kind string) (severity, category, title, detail string) {
	switch kind {
	case "max_tool_calls", "max_turns", "no_consecutive_tool_repeat", "tool_sequence":
		return "medium", "trajectory", "收敛 Agent 执行轨迹", "该失败属于过程质量问题，优先减少重复工具调用、无效 turn 或错误工具顺序。"
	case "required_tool", "forbidden_tool":
		return "high", "tool_routing", "修正工具路由策略", "Agent 选择了不符合预期的工具路径，需要调整工具描述、系统提示或 suite 的工具约束。"
	case "max_duration_ms":
		return "medium", "latency", "降低 case 执行耗时", "该 case 超过耗时预算，需要结合 trace 的 LLM/tool 耗时定位瓶颈。"
	case "max_tool_failures":
		return "high", "tool_failure", "修复工具失败路径", "工具失败数超过预算，需要优先定位失败工具和错误码。"
	case "file_exists", "file_not_exists", "file_equals", "file_contains", "file_not_contains", "file_json_equals", "file_diff_contains", "command_assertion", "git_status":
		return "high", "state_assertion", "修复最终状态断言", "Agent 输出不足以代表任务完成，必须让工作区文件、命令或 Git 状态满足断言。"
	case "judge_score":
		return "medium", "judge_quality", "提升 Judge 质量评分", "模型结果未达到质量 rubric 或过程约束，优先检查输出冗余、遗漏和工具过度使用。"
	case "execution_error", "status":
		return "critical", "runtime", "修复运行时失败", "case 没有正常完成，先处理执行错误、超时或异常状态。"
	case "output_contains", "output_not_contains", "output_regex", "min_output_chars", "max_output_chars", "execution":
		return "medium", "answer_quality", "修正最终回答质量", "最终输出不满足断言，需调整完成条件或提示词中的回答格式要求。"
	default:
		return "medium", "assertion", "处理失败断言", "根据失败断言补齐实现、提示词或评测预期。"
	}
}

func actionID(runID, caseID string, index int) string {
	parts := []string{sanitizeID(runID), sanitizeID(caseID), fmt.Sprintf("%02d", index)}
	return strings.Join(parts, "-")
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" || slices.Contains([]string{".", ".."}, result) {
		return "item"
	}
	return result
}
