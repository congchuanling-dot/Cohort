package evaluation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type TagMetric struct {
	Tag      string
	Total    int
	Passed   int
	PassRate float64
}

type DashboardData struct {
	Result      RunResult
	History     []RunResult
	Tags        []TagMetric
	GeneratedAt string
	ResultJSON  template.JS
	HistoryJSON template.JS
}

func WriteReports(store Store, result RunResult) (markdownPath, htmlPath string, err error) {
	history, _ := store.ListResults()
	dir := store.RunDir(result.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", err
	}
	markdownPath = filepath.Join(dir, "report.md")
	htmlPath = filepath.Join(dir, "report.html")
	if err := os.WriteFile(markdownPath, []byte(renderMarkdown(result)), 0644); err != nil {
		return "", "", err
	}
	if err := renderHTML(htmlPath, result, history); err != nil {
		return "", "", err
	}
	return markdownPath, htmlPath, nil
}

func renderMarkdown(result RunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Cohort Eval: %s\n\n", result.SuiteName)
	fmt.Fprintf(&b, "- run: `%s`\n- model: `%s`\n- pass_rate: %.1f%%\n- score: %.1f\n- cases: %d passed / %d failed\n- duration: %s\n- tokens: %d\n\n",
		result.RunID, result.Model, result.PassRate, result.Score, result.PassedCases, result.FailedCases, formatDuration(result.DurationMS), result.TotalTokens)
	if result.Baseline != nil {
		fmt.Fprintf(&b, "## Baseline\n\n- run: `%s`\n- score_delta: %+.1f\n- pass_rate_delta: %+.1f%%\n- duration_delta: %s\n- token_delta: %d\n\n",
			result.Baseline.RunID, result.Baseline.ScoreDelta, result.Baseline.PassRateDelta, signedDuration(result.Baseline.DurationDeltaMS), result.Baseline.TokenDelta)
	}
	fmt.Fprintf(&b, "## Cases\n\n| case | result | score | duration | turns | tokens | tools |\n| --- | --- | ---: | ---: | ---: | ---: | --- |\n")
	for _, c := range result.Cases {
		status := "PASS"
		if !c.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %.1f | %s | %d | %d | %s |\n", c.CaseID, status, c.Score, formatDuration(c.DurationMS), c.Turns, c.TotalTokens, strings.Join(c.Tools, ", "))
	}
	fmt.Fprintf(&b, "\n## Failures\n\n")
	failed := 0
	for _, c := range result.Cases {
		if c.Passed {
			continue
		}
		failed++
		fmt.Fprintf(&b, "### %s\n\n", c.CaseID)
		if c.Error != "" {
			fmt.Fprintf(&b, "- execution_error: `%s`\n", c.Error)
		}
		for _, assertion := range c.AssertionResults {
			if !assertion.Passed {
				fmt.Fprintf(&b, "- `%s`: expected `%s`, actual `%s`\n", assertion.Kind, assertion.Expected, assertion.Actual)
			}
		}
		fmt.Fprintln(&b)
	}
	if failed == 0 {
		fmt.Fprintln(&b, "No failed cases.")
	}
	return b.String()
}

func renderHTML(path string, result RunResult, history []RunResult) error {
	resultJSON, _ := json.Marshal(result)
	historyJSON, _ := json.Marshal(history)
	data := DashboardData{
		Result:      result,
		History:     history,
		Tags:        aggregateTags(result.Cases),
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		ResultJSON:  template.JS(resultJSON),
		HistoryJSON: template.JS(historyJSON),
	}
	tmpl, err := template.New("dashboard").Funcs(template.FuncMap{
		"duration": formatDuration,
		"join":     strings.Join,
		"status": func(value bool) string {
			if value {
				return "PASS"
			}
			return "FAIL"
		},
		"pct":   func(value float64) string { return fmt.Sprintf("%.1f%%", value) },
		"delta": func(value float64) string { return fmt.Sprintf("%+.1f", value) },
	}).Parse(dashboardHTML)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return err
	}
	return os.WriteFile(path, output.Bytes(), 0644)
}

func aggregateTags(cases []CaseResult) []TagMetric {
	metrics := map[string]*TagMetric{}
	for _, c := range cases {
		tags := c.Tags
		if len(tags) == 0 {
			tags = []string{"untagged"}
		}
		for _, tag := range tags {
			metric := metrics[tag]
			if metric == nil {
				metric = &TagMetric{Tag: tag}
				metrics[tag] = metric
			}
			metric.Total++
			if c.Passed {
				metric.Passed++
			}
		}
	}
	result := make([]TagMetric, 0, len(metrics))
	for _, metric := range metrics {
		metric.PassRate = float64(metric.Passed) / float64(metric.Total) * 100
		result = append(result, *metric)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Tag < result[j].Tag })
	return result
}

func formatDuration(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

func signedDuration(ms int64) string {
	if ms >= 0 {
		return "+" + formatDuration(ms)
	}
	return "-" + formatDuration(-ms)
}

const dashboardHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cohort Eval · {{.Result.SuiteName}}</title>
<style>
:root{--bg:#0b1020;--panel:#121a2d;--panel2:#182239;--text:#eef3ff;--muted:#8fa0bd;--line:#263451;--blue:#6ea8fe;--green:#43d39e;--red:#ff6b7a;--amber:#f4bd61}
*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 80% 0,#17264a 0,transparent 36%),var(--bg);color:var(--text);font:14px/1.5 Inter,ui-sans-serif,system-ui,-apple-system,sans-serif}
.wrap{max-width:1440px;margin:auto;padding:28px}.top{display:flex;justify-content:space-between;gap:24px;align-items:end;margin-bottom:22px}.eyebrow{color:var(--blue);font-weight:700;letter-spacing:.12em;text-transform:uppercase}.top h1{font-size:30px;margin:4px 0}.meta{color:var(--muted);font-family:ui-monospace,monospace}
.grid{display:grid;grid-template-columns:repeat(5,1fr);gap:12px}.card,.panel{background:linear-gradient(145deg,var(--panel),#10182a);border:1px solid var(--line);border-radius:14px;box-shadow:0 14px 35px #0003}.card{padding:17px}.label{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.08em}.value{font-size:28px;font-weight:750;margin-top:6px}.good{color:var(--green)}.bad{color:var(--red)}.warn{color:var(--amber)}
.layout{display:grid;grid-template-columns:1.35fr .65fr;gap:14px;margin-top:14px}.panel{padding:18px}.panel h2{font-size:15px;margin:0 0 15px}.chart{height:180px;position:relative}.chart svg{width:100%;height:100%;overflow:visible}.axis{stroke:var(--line)}.line{fill:none;stroke:var(--blue);stroke-width:3}.dot{fill:var(--blue)}.barrow{display:grid;grid-template-columns:110px 1fr 50px;align-items:center;gap:10px;margin:9px 0}.bar{height:9px;background:#263451;border-radius:9px;overflow:hidden}.bar i{display:block;height:100%;background:linear-gradient(90deg,var(--blue),var(--green))}
.toolbar{display:flex;gap:10px;margin:18px 0 10px}.toolbar input,.toolbar select{background:var(--panel);border:1px solid var(--line);color:var(--text);padding:10px 12px;border-radius:9px;outline:none}.toolbar input{flex:1}.cases{display:grid;gap:9px}.case{background:var(--panel);border:1px solid var(--line);border-left:4px solid var(--green);border-radius:10px;padding:14px}.case.fail{border-left-color:var(--red)}.casehead{display:flex;justify-content:space-between;gap:14px}.case h3{margin:0;font-size:15px}.chips{display:flex;gap:6px;flex-wrap:wrap;margin-top:8px}.chip{font-size:11px;background:var(--panel2);color:var(--muted);padding:3px 8px;border-radius:99px}.metrics{display:flex;gap:18px;color:var(--muted);font-size:12px;margin-top:9px}.assertions{margin-top:10px;border-top:1px solid var(--line);padding-top:8px}.assert{display:grid;grid-template-columns:18px 150px 1fr;gap:8px;padding:4px 0;color:var(--muted)}.assert strong{color:var(--text)}details summary{cursor:pointer;color:var(--blue)}.output{white-space:pre-wrap;max-height:220px;overflow:auto;background:#090e1b;padding:12px;border-radius:8px;color:#cbd7eb}.empty{color:var(--muted);padding:24px;text-align:center}
@media(max-width:900px){.grid{grid-template-columns:repeat(2,1fr)}.layout{grid-template-columns:1fr}.top{display:block}.wrap{padding:16px}}
</style></head>
<body><main class="wrap">
<header class="top"><div><div class="eyebrow">Cohort Evaluation</div><h1>{{.Result.SuiteName}}</h1><div class="meta">{{.Result.RunID}} · {{.Result.Model}}</div></div><div class="meta">生成于 {{.GeneratedAt}}</div></header>
<section class="grid">
<div class="card"><div class="label">Pass Rate</div><div class="value {{if eq .Result.FailedCases 0}}good{{else}}bad{{end}}">{{pct .Result.PassRate}}</div></div>
<div class="card"><div class="label">Quality Score</div><div class="value">{{printf "%.1f" .Result.Score}}</div></div>
<div class="card"><div class="label">Cases</div><div class="value"><span class="good">{{.Result.PassedCases}}</span><span class="meta"> / {{.Result.TotalCases}}</span></div></div>
<div class="card"><div class="label">Duration</div><div class="value">{{duration .Result.DurationMS}}</div></div>
<div class="card"><div class="label">Tokens</div><div class="value">{{.Result.TotalTokens}}</div></div>
</section>
{{if .Result.Baseline}}<section class="panel" style="margin-top:14px"><h2>与基线 {{.Result.Baseline.RunID}} 对比</h2><div class="metrics"><span>分数 <b>{{delta .Result.Baseline.ScoreDelta}}</b></span><span>通过率 <b>{{delta .Result.Baseline.PassRateDelta}}%</b></span><span>耗时 <b>{{.Result.Baseline.DurationDeltaMS}}ms</b></span><span>Token <b>{{.Result.Baseline.TokenDelta}}</b></span><span class="bad">回归 {{len .Result.Baseline.RegressedCases}}</span><span class="good">改善 {{len .Result.Baseline.ImprovedCases}}</span></div></section>{{end}}
<section class="layout"><div class="panel"><h2>历史趋势</h2><div id="trend" class="chart"></div></div><div class="panel"><h2>标签通过率</h2>{{range .Tags}}<div class="barrow"><span>{{.Tag}}</span><div class="bar"><i style="width:{{printf "%.1f" .PassRate}}%"></i></div><b>{{printf "%.0f" .PassRate}}%</b></div>{{end}}</div></section>
<div class="toolbar"><input id="search" placeholder="搜索 case、标签、工具、失败断言"><select id="filter"><option value="all">全部结果</option><option value="pass">仅通过</option><option value="fail">仅失败</option></select></div>
<section id="cases" class="cases">
{{range .Result.Cases}}<article class="case {{if not .Passed}}fail{{end}}" data-pass="{{.Passed}}" data-search="{{.CaseID}} {{.Name}} {{join .Tags " "}} {{join .Tools " "}}">
<div class="casehead"><div><h3>{{status .Passed}} · {{.CaseID}} · {{.Name}}</h3><div class="chips">{{range .Tags}}<span class="chip">{{.}}</span>{{end}}{{range .Tools}}<span class="chip">tool:{{.}}</span>{{end}}</div></div><strong class="{{if .Passed}}good{{else}}bad{{end}}">{{printf "%.1f" .Score}}</strong></div>
<div class="metrics"><span>{{duration .DurationMS}}</span><span>{{.Turns}} turns</span><span>{{.TotalTokens}} tokens</span><span>{{.ToolFailures}} tool failures</span><span>session {{.SessionID}}</span></div>
<div class="assertions">{{range .AssertionResults}}<div class="assert"><span>{{if .Passed}}✓{{else}}✕{{end}}</span><strong>{{.Kind}}</strong><span>期望 {{.Expected}}{{if .Actual}} · 实际 {{.Actual}}{{end}}</span></div>{{end}}</div>
{{if or .Error .Output}}<details><summary>查看执行信息</summary>{{if .Error}}<p class="bad">{{.Error}}</p>{{end}}{{if .Output}}<pre class="output">{{.Output}}</pre>{{end}}</details>{{end}}
</article>{{end}}</section>
</main><script>
const history={{.HistoryJSON}}, result={{.ResultJSON}};
function trend(){const el=document.getElementById('trend'),rows=[...history].reverse().slice(-20);if(!rows.length){el.innerHTML='<div class="empty">暂无历史数据</div>';return}const w=700,h=160,p=18,max=100;let pts=rows.map((r,i)=>[(p+i*(w-p*2)/Math.max(1,rows.length-1)),h-p-r.pass_rate*(h-p*2)/max]);let line=pts.map(p=>p.join(',')).join(' '),dots=pts.map((q,i)=>'<circle class="dot" cx="'+q[0]+'" cy="'+q[1]+'" r="4"><title>'+rows[i].run_id+': '+rows[i].pass_rate.toFixed(1)+'%</title></circle>').join('');el.innerHTML='<svg viewBox="0 0 '+w+' '+h+'"><line class="axis" x1="'+p+'" y1="'+(h-p)+'" x2="'+(w-p)+'" y2="'+(h-p)+'"/><line class="axis" x1="'+p+'" y1="'+p+'" x2="'+p+'" y2="'+(h-p)+'"/><polyline class="line" points="'+line+'"/>'+dots+'</svg>'}
trend();const search=document.getElementById('search'),filter=document.getElementById('filter');function apply(){let q=search.value.toLowerCase();document.querySelectorAll('.case').forEach(x=>{let pass=x.dataset.pass==='true',ok=(!q||x.dataset.search.toLowerCase().includes(q))&&(filter.value==='all'||(filter.value==='pass'&&pass)||(filter.value==='fail'&&!pass));x.style.display=ok?'':'none'})}search.oninput=apply;filter.onchange=apply;
</script></body></html>`
