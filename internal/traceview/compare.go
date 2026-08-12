package traceview

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const RunCompareSchemaVersion = 1

type RunComparison struct {
	SchemaVersion int                  `json:"schema_version"`
	State         string               `json:"state"`
	Current       RunCompareSnapshot   `json:"current"`
	Baseline      *RunCompareSnapshot  `json:"baseline,omitempty"`
	Deltas        []RunCompareDelta    `json:"deltas"`
	Findings      []RunCompareFinding  `json:"findings"`
	Proposal      OptimizationProposal `json:"proposal"`
}

type RunCompareSnapshot struct {
	SessionID       string  `json:"session_id"`
	RunID           string  `json:"run_id"`
	Status          string  `json:"status"`
	DurationMS      int64   `json:"duration_ms"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	ToolCalls       int     `json:"tool_calls"`
	ToolFailures    int     `json:"tool_failures"`
	LLMCalls        int     `json:"llm_calls"`
	ContextPeak     float64 `json:"context_peak"`
	SchemaCount     int64   `json:"schema_count"`
}

type RunCompareDelta struct {
	Metric    string  `json:"metric"`
	Current   float64 `json:"current"`
	Baseline  float64 `json:"baseline"`
	Delta     float64 `json:"delta"`
	DeltaRate float64 `json:"delta_rate,omitempty"`
	Unit      string  `json:"unit"`
}

type RunCompareFinding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
}

type OptimizationProposal struct {
	Summary             string   `json:"summary"`
	Risk                string   `json:"risk"`
	Recommendations     []string `json:"recommendations"`
	VerificationCommand string   `json:"verification_command"`
	Evidence            []string `json:"evidence"`
}

// CompareRuns 把当前 Run 与已选择的成功基线做同口径对比。
func CompareRuns(current, baseline RunView, model string) RunComparison {
	currentSnapshot := runCompareSnapshot(current, model)
	result := RunComparison{
		SchemaVersion: RunCompareSchemaVersion,
		State:         "ready",
		Current:       currentSnapshot,
		Deltas:        []RunCompareDelta{},
		Findings:      []RunCompareFinding{},
	}
	if baseline.RunID == "" {
		result.State = "baseline_unavailable"
		result.Proposal = OptimizationProposal{
			Summary:         "暂无可比较的成功 Run",
			Risk:            "R1: read-only analysis",
			Recommendations: []string{"先完成至少一个同项目成功 Run，再生成有证据的优化提案。"},
			Evidence:        []string{"trace://" + current.SessionID + "/" + current.RunID},
		}
		return result
	}
	baselineSnapshot := runCompareSnapshot(baseline, model)
	result.Baseline = &baselineSnapshot
	result.Deltas = compareSnapshotDeltas(currentSnapshot, baselineSnapshot)
	result.Findings = comparisonFindings(currentSnapshot, baselineSnapshot)
	result.Proposal = buildOptimizationProposal(currentSnapshot, baselineSnapshot, result.Findings)
	return result
}

// SelectSuccessfulBaseline 选择与当前 Run 最相似的成功运行，不依赖 Prompt 正文。
func SelectSuccessfulBaseline(current RunView, candidates []RunView) RunView {
	type scored struct {
		view  RunView
		score int
	}
	currentSummary := current.Summary()
	var matches []scored
	for _, candidate := range candidates {
		if candidate.RunID == current.RunID && candidate.SessionID == current.SessionID {
			continue
		}
		summary := candidate.Summary()
		if !successfulRunStatus(summary.Status) || summary.ToolFailures > 0 {
			continue
		}
		score := 0
		if candidate.SessionID == current.SessionID {
			score += 100
		}
		score += max(0, 30-absInt(summary.TurnCount-currentSummary.TurnCount)*3)
		score += int(toolSimilarity(currentSummary.Tools, summary.Tools) * 50)
		if summary.FinishedAt.Before(currentSummary.FinishedAt) {
			score += 10
		}
		matches = append(matches, scored{view: candidate, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].view.Summary().FinishedAt.After(matches[j].view.Summary().FinishedAt)
	})
	if len(matches) == 0 {
		return RunView{}
	}
	return matches[0].view
}

func runCompareSnapshot(view RunView, model string) RunCompareSnapshot {
	summary := view.Summary()
	receipts := view.ReceiptLedger()
	capacity := view.ContextCapacity(model)
	return RunCompareSnapshot{
		SessionID:       view.SessionID,
		RunID:           view.RunID,
		Status:          summary.Status,
		DurationMS:      summary.DurationMS,
		InputTokens:     receipts.InputTokens,
		OutputTokens:    receipts.OutputTokens,
		CacheReadTokens: receipts.CacheReadTokens,
		ToolCalls:       summary.ToolCalls,
		ToolFailures:    summary.ToolFailures,
		LLMCalls:        summary.LLMCalls,
		ContextPeak:     capacity.MaxOccupancyRatio,
		SchemaCount:     summary.LastToolSchemaCount,
	}
}

func compareSnapshotDeltas(current, baseline RunCompareSnapshot) []RunCompareDelta {
	return []RunCompareDelta{
		newRunDelta("duration", current.DurationMS, baseline.DurationMS, "ms"),
		newRunDelta("input_tokens", current.InputTokens, baseline.InputTokens, "tokens"),
		newRunDelta("output_tokens", current.OutputTokens, baseline.OutputTokens, "tokens"),
		newRunDelta("cache_read_tokens", current.CacheReadTokens, baseline.CacheReadTokens, "tokens"),
		newRunDelta("tool_failures", int64(current.ToolFailures), int64(baseline.ToolFailures), "count"),
		newRunDelta("llm_calls", int64(current.LLMCalls), int64(baseline.LLMCalls), "count"),
		newFloatDelta("context_peak", current.ContextPeak, baseline.ContextPeak, "ratio"),
		newRunDelta("tool_schema_count", current.SchemaCount, baseline.SchemaCount, "count"),
	}
}

func newRunDelta(metric string, current, baseline int64, unit string) RunCompareDelta {
	return newFloatDelta(metric, float64(current), float64(baseline), unit)
}

func newFloatDelta(metric string, current, baseline float64, unit string) RunCompareDelta {
	delta := current - baseline
	rate := float64(0)
	if baseline != 0 {
		rate = delta / baseline
	}
	return RunCompareDelta{Metric: metric, Current: current, Baseline: baseline, Delta: delta, DeltaRate: rate, Unit: unit}
}

func comparisonFindings(current, baseline RunCompareSnapshot) []RunCompareFinding {
	findings := []RunCompareFinding{}
	add := func(severity, category, title, detail, metric string) {
		findings = append(findings, RunCompareFinding{
			Severity: severity, Category: category, Title: title, Detail: detail,
			Evidence: fmt.Sprintf("compare://%s/%s#%s", current.RunID, baseline.RunID, metric),
		})
	}
	if current.ToolFailures > baseline.ToolFailures {
		add("high", "reliability", "工具失败高于成功基线",
			fmt.Sprintf("%d vs %d failures", current.ToolFailures, baseline.ToolFailures), "tool_failures")
	}
	if baseline.InputTokens > 0 && current.InputTokens > baseline.InputTokens*125/100 {
		add("medium", "token", "输入 Token 高于基线 25%",
			fmt.Sprintf("%d vs %d provider tokens", current.InputTokens, baseline.InputTokens), "input_tokens")
	}
	if baseline.DurationMS > 0 && current.DurationMS > baseline.DurationMS*125/100 {
		add("medium", "latency", "运行耗时高于基线 25%",
			fmt.Sprintf("%dms vs %dms", current.DurationMS, baseline.DurationMS), "duration")
	}
	if current.ContextPeak >= 0.7 && current.ContextPeak > baseline.ContextPeak {
		add("high", "context", "上下文容量压力高于基线",
			fmt.Sprintf("%.1f%% vs %.1f%%", current.ContextPeak*100, baseline.ContextPeak*100), "context_peak")
	}
	if baseline.CacheReadTokens > 0 && current.CacheReadTokens < baseline.CacheReadTokens*75/100 {
		add("low", "cache", "Prompt Cache 命中低于基线",
			fmt.Sprintf("%d vs %d cached tokens", current.CacheReadTokens, baseline.CacheReadTokens), "cache_read_tokens")
	}
	return findings
}

func buildOptimizationProposal(current, baseline RunCompareSnapshot, findings []RunCompareFinding) OptimizationProposal {
	recommendations := make([]string, 0, len(findings))
	for _, finding := range findings {
		switch finding.Category {
		case "reliability":
			recommendations = append(recommendations, "对重复失败工具启用参数级熔断，并在重试前强制诊断错误码。")
		case "token":
			recommendations = append(recommendations, "减少重复历史或工具 Schema，使用 Provider input_tokens 验证优化结果。")
		case "latency":
			recommendations = append(recommendations, "沿 DAG 关键路径优化最慢 LLM/Tool 节点，并复跑同一验收任务。")
		case "context":
			recommendations = append(recommendations, "在 70% 容量线执行 Micro Compact，90% 前执行 Full Compact。")
		case "cache":
			recommendations = append(recommendations, "稳定 System Prompt 与工具 Schema 顺序，提高 Provider Prompt Cache 复用。")
		}
	}
	recommendations = uniqueStrings(recommendations)
	if len(recommendations) == 0 {
		recommendations = []string{"当前 Run 未发现显著回归，保留为新的成功基线候选。"}
	}
	return OptimizationProposal{
		Summary:         fmt.Sprintf("Optimize run %s against successful baseline %s", current.RunID, baseline.RunID),
		Risk:            "R2: proposal only; implementation requires explicit approval and verification",
		Recommendations: recommendations,
		VerificationCommand: fmt.Sprintf(
			"cohort trace compare %s %s", current.RunID, baseline.RunID,
		),
		Evidence: []string{
			"trace://" + current.SessionID + "/" + current.RunID,
			"trace://" + baseline.SessionID + "/" + baseline.RunID,
		},
	}
}

func successfulRunStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done", "success", "succeeded", "passed":
		return true
	default:
		return false
	}
}

func toolSimilarity(left, right []ToolItem) float64 {
	leftSet, rightSet := map[string]bool{}, map[string]bool{}
	for _, item := range left {
		leftSet[item.Name] = true
	}
	for _, item := range right {
		rightSet[item.Name] = true
	}
	union, intersection := map[string]bool{}, 0
	for name := range leftSet {
		union[name] = true
		if rightSet[name] {
			intersection++
		}
	}
	for name := range rightSet {
		union[name] = true
	}
	if len(union) == 0 {
		return 1
	}
	return math.Min(1, float64(intersection)/float64(len(union)))
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
