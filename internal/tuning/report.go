package tuning

import (
	"bytes"
	"fmt"
	"html/template"
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
	OutputPath           string
	DashboardPath        string
	RunsScanned          int
	SessionsScanned      int
	TotalDurationMS      int64
	LLMDurationMS        int64
	ToolDurationMS       int64
	ToolFailures         int
	AskUserCalls         int
	PermissionEvents     int
	SchemaBloatRuns      int
	AdaptiveRoutedRuns   int
	ToolRouteEscalations int
	SchemaBytesSaved     int64
	RequestBloatRuns     int
	ContextBloatRuns     int
	SlowLLMs             []SlowLLM
	FailedTools          []FailedTool
	Recommendations      []string
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
	report.DashboardPath = strings.TrimSuffix(opts.OutputPath, filepath.Ext(opts.OutputPath)) + ".html"
	content := renderReport(report, views)
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0755); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(opts.OutputPath, []byte(content), 0644); err != nil {
		return Report{}, err
	}
	if err := writeDashboard(report.DashboardPath, report); err != nil {
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
		if summary.AdaptiveRouteTurns > 0 {
			report.AdaptiveRoutedRuns++
		}
		report.ToolRouteEscalations += summary.ToolRouteEscalations
		report.SchemaBytesSaved += summary.TotalSavedSchemaBytes
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
		recs = append(recs, "仍存在工具 schema 膨胀：检查 tools.adaptive_routing 是否启用，以及任务是否频繁触发完整工具面升级。")
	}
	if report.AdaptiveRoutedRuns > 0 && report.SchemaBytesSaved > 0 {
		recs = append(recs, fmt.Sprintf("自适应工具路由已在 %d 个 run 生效，累计减少约 %dKB schema payload；结合 escalation 次数检查召回与性能平衡。", report.AdaptiveRoutedRuns, report.SchemaBytesSaved/1024))
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
	fmt.Fprintf(&b, "- adaptive_routed_runs: %d\n", report.AdaptiveRoutedRuns)
	fmt.Fprintf(&b, "- tool_route_escalations: %d\n", report.ToolRouteEscalations)
	fmt.Fprintf(&b, "- schema_bytes_saved: %d\n", report.SchemaBytesSaved)
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

func writeDashboard(path string, report Report) error {
	tmpl, err := template.New("tuning").Funcs(template.FuncMap{
		"duration": formatMS,
	}).Parse(tuningDashboardHTML)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, report); err != nil {
		return err
	}
	return os.WriteFile(path, output.Bytes(), 0644)
}

const tuningDashboardHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cohort Runtime Tuning</title><style>
:root{--bg:#0b1020;--panel:#131c31;--line:#283755;--text:#eef3ff;--muted:#91a0ba;--blue:#70a9ff;--red:#ff7181;--green:#49d6a0}
*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 80% 0,#17264a,transparent 38%),var(--bg);color:var(--text);font:14px/1.5 system-ui,-apple-system,sans-serif}.wrap{max-width:1320px;margin:auto;padding:28px}h1{margin:4px 0 3px;font-size:30px}.sub{color:var(--muted)}.grid{display:grid;grid-template-columns:repeat(5,1fr);gap:12px;margin:22px 0}.card,.panel{background:linear-gradient(145deg,var(--panel),#10182a);border:1px solid var(--line);border-radius:14px;padding:17px}.label{font-size:11px;color:var(--muted);letter-spacing:.09em;text-transform:uppercase}.value{font-size:25px;font-weight:750;margin-top:5px}.layout{display:grid;grid-template-columns:1fr 1fr;gap:14px}.panel h2{font-size:15px;margin:0 0 12px}.rec{border-left:3px solid var(--blue);padding:8px 12px;margin:8px 0;background:#182239}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:9px;border-bottom:1px solid var(--line)}th{color:var(--muted);font-size:11px;text-transform:uppercase}.bad{color:var(--red)}.good{color:var(--green)}@media(max-width:850px){.grid{grid-template-columns:repeat(2,1fr)}.layout{grid-template-columns:1fr}}
</style></head><body><main class="wrap"><div class="sub">COHORT RUNTIME OBSERVABILITY</div><h1>日常 Agent 调优面板</h1><div class="sub">从最近 {{.RunsScanned}} 次 run.log.jsonl 异步生成，不调用额外模型</div>
<section class="grid"><div class="card"><div class="label">Runs</div><div class="value">{{.RunsScanned}}</div></div><div class="card"><div class="label">Total Time</div><div class="value">{{duration .TotalDurationMS}}</div></div><div class="card"><div class="label">LLM Time</div><div class="value">{{duration .LLMDurationMS}}</div></div><div class="card"><div class="label">Tool Time</div><div class="value">{{duration .ToolDurationMS}}</div></div><div class="card"><div class="label">Tool Failures</div><div class="value {{if gt .ToolFailures 0}}bad{{else}}good{{end}}">{{.ToolFailures}}</div></div></section>
<section class="layout"><div class="panel"><h2>调优建议</h2>{{range .Recommendations}}<div class="rec">{{.}}</div>{{end}}</div><div class="panel"><h2>工具路由与膨胀</h2><table><tr><td>Adaptive routed</td><td>{{.AdaptiveRoutedRuns}} runs</td></tr><tr><td>Schema saved</td><td>{{.SchemaBytesSaved}} bytes</td></tr><tr><td>Escalations</td><td>{{.ToolRouteEscalations}}</td></tr><tr><td>Schema bloat</td><td>{{.SchemaBloatRuns}} runs</td></tr><tr><td>Request bloat</td><td>{{.RequestBloatRuns}} runs</td></tr><tr><td>Context bloat</td><td>{{.ContextBloatRuns}} runs</td></tr><tr><td>ask_user</td><td>{{.AskUserCalls}} calls</td></tr><tr><td>Permissions</td><td>{{.PermissionEvents}} events</td></tr></table></div></section>
<section class="layout" style="margin-top:14px"><div class="panel"><h2>最慢 LLM 调用</h2><table><tr><th>耗时</th><th>Session</th><th>Turn</th><th>Tools</th></tr>{{range .SlowLLMs}}<tr><td>{{duration .DurationMS}}</td><td>{{.SessionID}}</td><td>{{.Turn}}</td><td>{{.ToolSchemaCount}}</td></tr>{{end}}</table></div><div class="panel"><h2>失败工具 Top</h2><table><tr><th>Tool</th><th>Error</th><th>Count</th></tr>{{range .FailedTools}}<tr><td>{{.Tool}}</td><td>{{.ErrorCode}}</td><td>{{.Count}}</td></tr>{{end}}</table></div></section>
</main></body></html>`
