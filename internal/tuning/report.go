package tuning

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cohort/internal/session"
	"cohort/internal/traceview"
)

const DefaultReportPath = "memory/reflection/tuning_report.md"

type Options struct {
	SessionRoot string
	Limit       int
	OutputPath  string
}

type Report struct {
	OutputPath       string
	RunsScanned      int
	SessionsScanned  int
	TotalDurationMS  int64
	LLMDurationMS    int64
	ToolDurationMS   int64
	ToolFailures     int
	AskUserCalls     int
	PermissionEvents int
	SchemaBloatRuns  int
	RequestBloatRuns int
	ContextBloatRuns int
	SlowLLMs         []SlowLLM
	FailedTools      []FailedTool
	Recommendations  []string
}

type SlowLLM struct {
	SessionID       string
	RunID           string
	Turn            int
	DurationMS      int64
	ToolSchemaCount int64
	RequestChars    int64
	TotalTokens     int64
}

type FailedTool struct {
	Tool      string
	ErrorCode string
	Status    string
	Count     int
	Sessions  int
}

type failureGroup struct {
	Tool      string
	ErrorCode string
	Status    string
	Count     int
	Sessions  map[string]bool
}

func Generate(workspace string, opts Options) (Report, error) {
	if opts.SessionRoot == "" {
		opts.SessionRoot = session.DefaultRootDir
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.OutputPath == "" {
		opts.OutputPath = filepath.Join(workspace, filepath.FromSlash(DefaultReportPath))
	}
	views, err := traceview.LoadRecentRuns(opts.SessionRoot, opts.Limit)
	if err != nil {
		return Report{}, err
	}
	report := buildReport(views)
	report.OutputPath = opts.OutputPath
	content := renderReport(report, views)
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0755); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(opts.OutputPath, []byte(content), 0644); err != nil {
		return Report{}, err
	}
	return report, nil
}

func buildReport(views []traceview.RunView) Report {
	report := Report{RunsScanned: len(views)}
	sessions := map[string]bool{}
	failures := map[string]*failureGroup{}
	for _, view := range views {
		sessions[view.SessionID] = true
		summary := view.Summary()
		report.TotalDurationMS += summary.DurationMS
		report.LLMDurationMS += summary.LLMDurationMS
		report.ToolDurationMS += summary.ToolDurationMS
		report.ToolFailures += summary.ToolFailures
		if summary.LastToolSchemaCount >= 60 {
			report.SchemaBloatRuns++
		}
		if summary.LastRequestChars >= 50000 {
			report.RequestBloatRuns++
		}
		if summary.LastFinalTokens >= 20000 || summary.LastFinalChars >= 80000 {
			report.ContextBloatRuns++
		}
		for _, llm := range summary.LLMs {
			report.SlowLLMs = append(report.SlowLLMs, SlowLLM{
				SessionID:       view.SessionID,
				RunID:           view.RunID,
				Turn:            llm.Turn,
				DurationMS:      llm.DurationMS,
				ToolSchemaCount: summary.LastToolSchemaCount,
				RequestChars:    summary.LastRequestChars,
				TotalTokens:     llm.TotalTokens,
			})
		}
		for _, tool := range summary.Tools {
			if tool.Name == "ask_user" {
				report.AskUserCalls++
			}
			if tool.Status == "" || tool.Status == "success" {
				continue
			}
			key := strings.Join([]string{tool.Name, tool.ErrorCode, tool.Status}, "\x00")
			group := failures[key]
			if group == nil {
				group = &failureGroup{
					Tool:      tool.Name,
					ErrorCode: tool.ErrorCode,
					Status:    tool.Status,
					Sessions:  map[string]bool{},
				}
				failures[key] = group
			}
			group.Count++
			group.Sessions[view.SessionID] = true
		}
		for _, item := range summary.Timeline {
			if item.EventType == "PermissionDecision" {
				report.PermissionEvents++
			}
		}
	}
	report.SessionsScanned = len(sessions)
	report.FailedTools = failedToolList(failures)
	sort.Slice(report.SlowLLMs, func(i, j int) bool {
		return report.SlowLLMs[i].DurationMS > report.SlowLLMs[j].DurationMS
	})
	if len(report.SlowLLMs) > 12 {
		report.SlowLLMs = report.SlowLLMs[:12]
	}
	report.Recommendations = recommendations(report)
	return report
}

func failedToolList(groups map[string]*failureGroup) []FailedTool {
	result := make([]FailedTool, 0, len(groups))
	for _, group := range groups {
		result = append(result, FailedTool{
			Tool:      group.Tool,
			ErrorCode: group.ErrorCode,
			Status:    group.Status,
			Count:     group.Count,
			Sessions:  len(group.Sessions),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Tool < result[j].Tool
	})
	if len(result) > 12 {
		result = result[:12]
	}
	return result
}

func recommendations(report Report) []string {
	var recs []string
	if report.RunsScanned == 0 {
		return []string{"没有可分析的 run.log.jsonl。先运行一次 cohort ask 或 REPL 任务。"}
	}
	if report.LLMDurationMS > report.ToolDurationMS*2 {
		recs = append(recs, "主要瓶颈在 LLM 请求：优先检查模型延迟、工具 schema 数量、请求体大小和 provider cache 命中。")
	}
	if report.SchemaBloatRuns > 0 {
		recs = append(recs, "存在工具 schema 膨胀：对明确任务可考虑启用 tools.enabled_groups 轻量模式，或做基于 SOP/意图的动态工具路由。")
	}
	if report.RequestBloatRuns > 0 || report.ContextBloatRuns > 0 {
		recs = append(recs, "存在上下文/请求体膨胀：检查长历史、工具结果裁剪、session memory 和 compact 触发阈值。")
	}
	if len(report.FailedTools) > 0 {
		recs = append(recs, "存在重复工具失败：优先为高频失败工具补 doctor、前置状态检查或 SOP recovery。")
	}
	if report.AskUserCalls > 0 {
		recs = append(recs, "ask_user 调用较多：检查是否有工具被隐藏、权限状态不可观测，或 SOP 缺少前置检查。")
	}
	if report.PermissionEvents > 0 {
		recs = append(recs, "存在权限确认事件：确认 R2/R3 风险分级是否合理，并把可自动检查的权限前置到 doctor。")
	}
	if len(recs) == 0 {
		recs = append(recs, "未发现明显调优热点；继续积累更多 session 后再看趋势。")
	}
	return recs
}

func renderReport(report Report, views []traceview.RunView) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Cohort Agent 调优报告\n\n")
	fmt.Fprintf(&b, "> generated_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "> raw_content_policy: omitted; report uses event counts, durations, tool names, status and size metrics only\n\n")
	fmt.Fprintf(&b, "## 总览\n\n")
	fmt.Fprintf(&b, "- runs_scanned: %d\n", report.RunsScanned)
	fmt.Fprintf(&b, "- sessions_scanned: %d\n", report.SessionsScanned)
	fmt.Fprintf(&b, "- total_duration: %s\n", formatMS(report.TotalDurationMS))
	fmt.Fprintf(&b, "- llm_time: %s\n", formatMS(report.LLMDurationMS))
	fmt.Fprintf(&b, "- tool_time: %s\n", formatMS(report.ToolDurationMS))
	fmt.Fprintf(&b, "- tool_failures: %d\n", report.ToolFailures)
	fmt.Fprintf(&b, "- ask_user_calls: %d\n", report.AskUserCalls)
	fmt.Fprintf(&b, "- permission_events: %d\n", report.PermissionEvents)
	fmt.Fprintf(&b, "- schema_bloat_runs: %d\n", report.SchemaBloatRuns)
	fmt.Fprintf(&b, "- request_bloat_runs: %d\n", report.RequestBloatRuns)
	fmt.Fprintf(&b, "- context_bloat_runs: %d\n\n", report.ContextBloatRuns)
	fmt.Fprintf(&b, "## 建议\n\n")
	for _, rec := range report.Recommendations {
		fmt.Fprintf(&b, "- %s\n", rec)
	}
	fmt.Fprintf(&b, "\n## 最慢 LLM 调用\n\n")
	if len(report.SlowLLMs) == 0 {
		fmt.Fprintf(&b, "No LLM calls found.\n\n")
	} else {
		fmt.Fprintf(&b, "| duration | session | run | turn | tools | request_chars | tokens |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- |\n")
		for _, item := range report.SlowLLMs {
			fmt.Fprintf(&b, "| %s | `%s` | `%s` | %d | %d | %d | %d |\n",
				formatMS(item.DurationMS), item.SessionID, item.RunID, item.Turn, item.ToolSchemaCount, item.RequestChars, item.TotalTokens)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## 失败工具 Top\n\n")
	if len(report.FailedTools) == 0 {
		fmt.Fprintf(&b, "No failed tools found.\n\n")
	} else {
		fmt.Fprintf(&b, "| tool | status | error_code | count | sessions |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
		for _, item := range report.FailedTools {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d | %d |\n", item.Tool, item.Status, item.ErrorCode, item.Count, item.Sessions)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Run 明细\n\n")
	fmt.Fprintf(&b, "| session | run | status | duration | llm | tool | failures | tools | request_chars |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, view := range views {
		summary := view.Summary()
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s | %s | %d | %d | %d |\n",
			view.SessionID,
			view.RunID,
			summary.Status,
			formatMS(summary.DurationMS),
			formatMS(summary.LLMDurationMS),
			formatMS(summary.ToolDurationMS),
			summary.ToolFailures,
			summary.LastToolSchemaCount,
			summary.LastRequestChars,
		)
	}
	fmt.Fprintf(&b, "\n")
	return b.String()
}

func formatMS(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}
