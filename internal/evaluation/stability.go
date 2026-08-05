package evaluation

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type StabilityOptions struct {
	Window    int
	SuiteID   string
	Profile   string
	Model     string
	OnlyFlaky bool
}

type StabilityIndex struct {
	GeneratedAt       time.Time             `json:"generated_at"`
	Window            int                   `json:"window"`
	Filters           StabilityFilters      `json:"filters"`
	Runs              []StabilityRun        `json:"runs"`
	Suites            []StabilitySuite      `json:"suites"`
	Cases             []StabilityCase       `json:"cases"`
	FailureSignatures []FailureSignature    `json:"failure_signatures"`
	Regressions       []StabilityRegression `json:"regressions"`
	Summary           StabilitySummary      `json:"summary"`
}

type StabilityFilters struct {
	SuiteID string `json:"suite_id,omitempty"`
	Profile string `json:"profile,omitempty"`
	Model   string `json:"model,omitempty"`
}

type StabilitySummary struct {
	Runs              int     `json:"runs"`
	Suites            int     `json:"suites"`
	Cases             int     `json:"cases"`
	AveragePassRate   float64 `json:"average_pass_rate"`
	AverageScore      float64 `json:"average_score"`
	AverageStability  float64 `json:"average_stability"`
	FlakyCases        int     `json:"flaky_cases"`
	Regressions       int     `json:"regressions"`
	FailureSignatures int     `json:"failure_signatures"`
}

type StabilityRun struct {
	RunID          string    `json:"run_id"`
	SuiteID        string    `json:"suite_id"`
	SuiteName      string    `json:"suite_name"`
	Profile        string    `json:"profile,omitempty"`
	Model          string    `json:"model,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	PassRate       float64   `json:"pass_rate"`
	Score          float64   `json:"score"`
	StabilityRate  float64   `json:"stability_rate"`
	FailedCases    int       `json:"failed_cases"`
	TotalCases     int       `json:"total_cases"`
	RegressedCases []string  `json:"regressed_cases,omitempty"`
	DurationMS     int64     `json:"duration_ms"`
	TotalTokens    int64     `json:"total_tokens,omitempty"`
}

type StabilitySuite struct {
	SuiteID          string  `json:"suite_id"`
	SuiteName        string  `json:"suite_name"`
	Runs             int     `json:"runs"`
	AveragePassRate  float64 `json:"average_pass_rate"`
	AverageScore     float64 `json:"average_score"`
	AverageStability float64 `json:"average_stability"`
	Regressions      int     `json:"regressions"`
	FlakyCases       int     `json:"flaky_cases"`
}

type StabilityCase struct {
	SuiteID          string    `json:"suite_id"`
	SuiteName        string    `json:"suite_name"`
	CaseID           string    `json:"case_id"`
	Name             string    `json:"name"`
	Tags             []string  `json:"tags,omitempty"`
	Profile          string    `json:"profile,omitempty"`
	Model            string    `json:"model,omitempty"`
	Observations     int       `json:"observations"`
	Passes           int       `json:"passes"`
	Failures         int       `json:"failures"`
	PassRate         float64   `json:"pass_rate"`
	AverageScore     float64   `json:"average_score"`
	AverageStability float64   `json:"average_stability"`
	Flaky            bool      `json:"flaky"`
	Regressions      int       `json:"regressions"`
	LatestRunID      string    `json:"latest_run_id"`
	LatestAt         time.Time `json:"latest_at"`
	LatestPassed     bool      `json:"latest_passed"`
	LatestScore      float64   `json:"latest_score"`
	LatestTracePath  string    `json:"latest_trace_path,omitempty"`
	LatestTraceRunID string    `json:"latest_trace_run_id,omitempty"`
}

type FailureSignature struct {
	Signature string   `json:"signature"`
	Kind      string   `json:"kind"`
	Count     int      `json:"count"`
	SuiteIDs  []string `json:"suite_ids,omitempty"`
	CaseIDs   []string `json:"case_ids,omitempty"`
	RunIDs    []string `json:"run_ids,omitempty"`
	Example   string   `json:"example,omitempty"`
}

type StabilityRegression struct {
	SuiteID     string    `json:"suite_id"`
	CaseID      string    `json:"case_id"`
	FromRunID   string    `json:"from_run_id"`
	ToRunID     string    `json:"to_run_id"`
	ToStartedAt time.Time `json:"to_started_at"`
}

func BuildStabilityIndex(results []RunResult, opts StabilityOptions) StabilityIndex {
	filtered := filterStabilityResults(results, opts)
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].StartedAt.After(filtered[j].StartedAt) })
	if opts.Window > 0 && len(filtered) > opts.Window {
		filtered = filtered[:opts.Window]
	}
	chronological := append([]RunResult(nil), filtered...)
	sort.Slice(chronological, func(i, j int) bool { return chronological[i].StartedAt.Before(chronological[j].StartedAt) })

	index := StabilityIndex{
		GeneratedAt: time.Now().UTC(),
		Window:      opts.Window,
		Filters: StabilityFilters{
			SuiteID: opts.SuiteID,
			Profile: opts.Profile,
			Model:   opts.Model,
		},
	}
	suiteAgg := map[string]*suiteAccumulator{}
	caseAgg := map[string]*caseAccumulator{}
	signatureAgg := map[string]*signatureAccumulator{}
	lastCasePassed := map[string]caseObservation{}
	for _, result := range chronological {
		run := StabilityRun{
			RunID:         result.RunID,
			SuiteID:       result.SuiteID,
			SuiteName:     result.SuiteName,
			Profile:       result.Profile,
			Model:         result.Model,
			StartedAt:     result.StartedAt,
			PassRate:      result.PassRate,
			Score:         result.Score,
			StabilityRate: averageCaseStability(result.Cases),
			FailedCases:   result.FailedCases,
			TotalCases:    result.TotalCases,
			DurationMS:    result.DurationMS,
			TotalTokens:   result.TotalTokens,
		}
		if result.Baseline != nil {
			run.RegressedCases = append([]string(nil), result.Baseline.RegressedCases...)
		}
		index.Runs = append(index.Runs, run)
		suite := suiteAgg[result.SuiteID]
		if suite == nil {
			suite = &suiteAccumulator{suiteID: result.SuiteID, suiteName: result.SuiteName}
			suiteAgg[result.SuiteID] = suite
		}
		suite.add(run)
		for _, c := range result.Cases {
			key := stabilityCaseKey(result, c)
			acc := caseAgg[key]
			if acc == nil {
				acc = &caseAccumulator{suiteID: result.SuiteID, suiteName: result.SuiteName, caseID: c.CaseID, name: c.Name, profile: result.Profile, model: result.Model, tags: append([]string(nil), c.Tags...)}
				caseAgg[key] = acc
			}
			acc.add(result, c)
			previous, ok := lastCasePassed[key]
			if ok && previous.passed && !c.Passed {
				acc.regressions++
				index.Regressions = append(index.Regressions, StabilityRegression{
					SuiteID: result.SuiteID, CaseID: c.CaseID, FromRunID: previous.runID, ToRunID: result.RunID, ToStartedAt: result.StartedAt,
				})
			}
			lastCasePassed[key] = caseObservation{runID: result.RunID, passed: c.Passed}
			for _, assertion := range c.AssertionResults {
				if assertion.Passed {
					continue
				}
				signature := failureSignature(result, c, assertion)
				acc := signatureAgg[signature]
				if acc == nil {
					acc = &signatureAccumulator{signature: signature, kind: assertion.Kind, example: assertion.Message}
					if acc.example == "" {
						acc.example = assertion.Expected
					}
					signatureAgg[signature] = acc
				}
				acc.add(result.SuiteID, c.CaseID, result.RunID)
			}
		}
	}
	sort.Slice(index.Runs, func(i, j int) bool { return index.Runs[i].StartedAt.After(index.Runs[j].StartedAt) })
	for _, acc := range suiteAgg {
		index.Suites = append(index.Suites, acc.metric())
	}
	sort.Slice(index.Suites, func(i, j int) bool { return index.Suites[i].SuiteID < index.Suites[j].SuiteID })
	for _, acc := range caseAgg {
		metric := acc.metric()
		if opts.OnlyFlaky && !metric.Flaky {
			continue
		}
		index.Cases = append(index.Cases, metric)
	}
	sort.Slice(index.Cases, func(i, j int) bool {
		if index.Cases[i].Flaky != index.Cases[j].Flaky {
			return index.Cases[i].Flaky
		}
		if index.Cases[i].PassRate != index.Cases[j].PassRate {
			return index.Cases[i].PassRate < index.Cases[j].PassRate
		}
		return index.Cases[i].CaseID < index.Cases[j].CaseID
	})
	for _, acc := range signatureAgg {
		index.FailureSignatures = append(index.FailureSignatures, acc.metric())
	}
	sort.Slice(index.FailureSignatures, func(i, j int) bool {
		if index.FailureSignatures[i].Count != index.FailureSignatures[j].Count {
			return index.FailureSignatures[i].Count > index.FailureSignatures[j].Count
		}
		return index.FailureSignatures[i].Signature < index.FailureSignatures[j].Signature
	})
	index.Summary = summarizeStability(index)
	return index
}

func WriteStabilityReports(store Store, index StabilityIndex) (indexPath, markdownPath, htmlPath string, err error) {
	dir := store.StabilityDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", "", err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", "", "", err
	}
	indexPath = filepath.Join(dir, "index.json")
	if err := os.WriteFile(indexPath, append(data, '\n'), 0644); err != nil {
		return "", "", "", err
	}
	markdownPath = filepath.Join(dir, "report.md")
	if err := os.WriteFile(markdownPath, []byte(renderStabilityMarkdown(index)), 0644); err != nil {
		return "", "", "", err
	}
	htmlPath = filepath.Join(dir, "report.html")
	if err := renderStabilityHTML(htmlPath, index); err != nil {
		return "", "", "", err
	}
	return indexPath, markdownPath, htmlPath, nil
}

func filterStabilityResults(results []RunResult, opts StabilityOptions) []RunResult {
	var filtered []RunResult
	for _, result := range results {
		if opts.SuiteID != "" && result.SuiteID != opts.SuiteID {
			continue
		}
		if opts.Model != "" && result.Model != opts.Model {
			continue
		}
		if opts.Profile != "" && result.Profile != opts.Profile && result.Model != opts.Profile {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

type suiteAccumulator struct {
	suiteID     string
	suiteName   string
	runs        int
	passRate    float64
	score       float64
	stability   float64
	regressions int
	flakyCases  map[string]bool
}

func (a *suiteAccumulator) add(run StabilityRun) {
	a.runs++
	a.passRate += run.PassRate
	a.score += run.Score
	a.stability += run.StabilityRate
	a.regressions += len(run.RegressedCases)
}

func (a *suiteAccumulator) metric() StabilitySuite {
	return StabilitySuite{
		SuiteID: a.suiteID, SuiteName: a.suiteName, Runs: a.runs,
		AveragePassRate:  safeDiv(a.passRate, float64(a.runs)),
		AverageScore:     safeDiv(a.score, float64(a.runs)),
		AverageStability: safeDiv(a.stability, float64(a.runs)),
		Regressions:      a.regressions,
	}
}

type caseAccumulator struct {
	suiteID          string
	suiteName        string
	caseID           string
	name             string
	tags             []string
	profile          string
	model            string
	observations     int
	passes           int
	failures         int
	score            float64
	stability        float64
	regressions      int
	latestRunID      string
	latestAt         time.Time
	latestPassed     bool
	latestScore      float64
	latestTracePath  string
	latestTraceRunID string
	seenPass         bool
	seenFail         bool
}

func (a *caseAccumulator) add(result RunResult, c CaseResult) {
	a.observations++
	if c.Passed {
		a.passes++
		a.seenPass = true
	} else {
		a.failures++
		a.seenFail = true
	}
	a.score += c.Score
	a.stability += c.StabilityRate
	if result.StartedAt.After(a.latestAt) {
		a.latestAt = result.StartedAt
		a.latestRunID = result.RunID
		a.latestPassed = c.Passed
		a.latestScore = c.Score
		a.latestTracePath = c.TracePath
		a.latestTraceRunID = c.TraceRunID
	}
}

func (a *caseAccumulator) metric() StabilityCase {
	return StabilityCase{
		SuiteID: a.suiteID, SuiteName: a.suiteName, CaseID: a.caseID, Name: a.name, Tags: append([]string(nil), a.tags...),
		Profile: a.profile, Model: a.model, Observations: a.observations, Passes: a.passes, Failures: a.failures,
		PassRate:         safeDiv(float64(a.passes), float64(a.observations)) * 100,
		AverageScore:     safeDiv(a.score, float64(a.observations)),
		AverageStability: safeDiv(a.stability, float64(a.observations)),
		Flaky:            a.seenPass && a.seenFail,
		Regressions:      a.regressions,
		LatestRunID:      a.latestRunID, LatestAt: a.latestAt, LatestPassed: a.latestPassed, LatestScore: a.latestScore,
		LatestTracePath: a.latestTracePath, LatestTraceRunID: a.latestTraceRunID,
	}
}

type signatureAccumulator struct {
	signature string
	kind      string
	count     int
	suiteIDs  map[string]bool
	caseIDs   map[string]bool
	runIDs    map[string]bool
	example   string
}

func (a *signatureAccumulator) add(suiteID, caseID, runID string) {
	a.count++
	if a.suiteIDs == nil {
		a.suiteIDs = map[string]bool{}
		a.caseIDs = map[string]bool{}
		a.runIDs = map[string]bool{}
	}
	a.suiteIDs[suiteID] = true
	a.caseIDs[caseID] = true
	a.runIDs[runID] = true
}

func (a *signatureAccumulator) metric() FailureSignature {
	return FailureSignature{Signature: a.signature, Kind: a.kind, Count: a.count, SuiteIDs: sortedKeys(a.suiteIDs), CaseIDs: sortedKeys(a.caseIDs), RunIDs: sortedKeys(a.runIDs), Example: a.example}
}

type caseObservation struct {
	runID  string
	passed bool
}

func stabilityCaseKey(result RunResult, c CaseResult) string {
	return strings.Join([]string{result.SuiteID, result.Profile, result.Model, c.CaseID}, "\x00")
}

func failureSignature(result RunResult, c CaseResult, assertion AssertionResult) string {
	expected := assertion.Expected
	if len([]rune(expected)) > 80 {
		expected = string([]rune(expected)[:80])
	}
	return strings.Join([]string{result.SuiteID, c.CaseID, assertion.Kind, expected}, "::")
}

func summarizeStability(index StabilityIndex) StabilitySummary {
	var summary StabilitySummary
	summary.Runs = len(index.Runs)
	summary.Suites = len(index.Suites)
	summary.Cases = len(index.Cases)
	for _, run := range index.Runs {
		summary.AveragePassRate += run.PassRate
		summary.AverageScore += run.Score
		summary.AverageStability += run.StabilityRate
	}
	summary.AveragePassRate = safeDiv(summary.AveragePassRate, float64(len(index.Runs)))
	summary.AverageScore = safeDiv(summary.AverageScore, float64(len(index.Runs)))
	summary.AverageStability = safeDiv(summary.AverageStability, float64(len(index.Runs)))
	for _, c := range index.Cases {
		if c.Flaky {
			summary.FlakyCases++
		}
	}
	summary.Regressions = len(index.Regressions)
	summary.FailureSignatures = len(index.FailureSignatures)
	return summary
}

func renderStabilityMarkdown(index StabilityIndex) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Cohort Eval Stability\n\n")
	fmt.Fprintf(&b, "- generated_at: `%s`\n- runs: `%d`\n- suites: `%d`\n- cases: `%d`\n- average_pass_rate: `%.1f%%`\n- average_score: `%.1f`\n- average_stability: `%.1f%%`\n- flaky_cases: `%d`\n- regressions: `%d`\n\n",
		index.GeneratedAt.Format(time.RFC3339), index.Summary.Runs, index.Summary.Suites, index.Summary.Cases, index.Summary.AveragePassRate, index.Summary.AverageScore, index.Summary.AverageStability, index.Summary.FlakyCases, index.Summary.Regressions)
	fmt.Fprintf(&b, "## Suites\n\n| suite | runs | pass rate | score | stability | regressions |\n| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, s := range index.Suites {
		fmt.Fprintf(&b, "| `%s` | %d | %.1f%% | %.1f | %.1f%% | %d |\n", s.SuiteID, s.Runs, s.AveragePassRate, s.AverageScore, s.AverageStability, s.Regressions)
	}
	fmt.Fprintf(&b, "\n## Cases\n\n| case | suite | pass rate | stability | flaky | regressions | latest |\n| --- | --- | ---: | ---: | --- | ---: | --- |\n")
	for _, c := range index.Cases {
		fmt.Fprintf(&b, "| `%s` | `%s` | %.1f%% | %.1f%% | %t | %d | `%s` |\n", c.CaseID, c.SuiteID, c.PassRate, c.AverageStability, c.Flaky, c.Regressions, c.LatestRunID)
	}
	fmt.Fprintf(&b, "\n## Failure Signatures\n\n| signature | count | cases |\n| --- | ---: | --- |\n")
	for _, sig := range index.FailureSignatures {
		fmt.Fprintf(&b, "| `%s` | %d | %s |\n", sig.Signature, sig.Count, strings.Join(sig.CaseIDs, ", "))
	}
	return b.String()
}

func renderStabilityHTML(path string, index StabilityIndex) error {
	data, _ := json.Marshal(index)
	tmpl, err := template.New("stability").Funcs(template.FuncMap{
		"pct":  func(v float64) string { return fmt.Sprintf("%.1f%%", v) },
		"join": strings.Join,
		"status": func(v bool) string {
			if v {
				return "PASS"
			}
			return "FAIL"
		},
	}).Parse(stabilityHTML)
	if err != nil {
		return err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, struct {
		Index     StabilityIndex
		IndexJSON template.JS
	}{Index: index, IndexJSON: template.JS(data)}); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

const stabilityHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cohort Eval Stability</title>
<style>
:root{--bg:#08111f;--panel:#111b2e;--panel2:#17233a;--text:#edf4ff;--muted:#8ea2c1;--line:#283955;--green:#42d392;--red:#ff6476;--amber:#f6bd60;--blue:#6ea8fe}
*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 80% 0,#17325d 0,transparent 34%),var(--bg);color:var(--text);font:14px/1.5 Inter,ui-sans-serif,system-ui,-apple-system,sans-serif}.wrap{max-width:1500px;margin:auto;padding:28px}.top{display:flex;justify-content:space-between;gap:20px;align-items:end}.eyebrow{color:var(--blue);font-weight:800;letter-spacing:.12em;text-transform:uppercase}.top h1{margin:3px 0;font-size:30px}.meta{color:var(--muted);font-family:ui-monospace,monospace}.grid{display:grid;grid-template-columns:repeat(6,1fr);gap:12px;margin:20px 0}.card,.panel{background:linear-gradient(145deg,var(--panel),#0d1728);border:1px solid var(--line);border-radius:14px;box-shadow:0 14px 35px #0003}.card{padding:16px}.label{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.08em}.value{font-size:27px;font-weight:800;margin-top:5px}.good{color:var(--green)}.bad{color:var(--red)}.warn{color:var(--amber)}.layout{display:grid;grid-template-columns:1fr 1fr;gap:14px}.panel{padding:18px;margin-bottom:14px}.panel h2{font-size:15px;margin:0 0 12px}table{width:100%;border-collapse:collapse}th,td{text-align:left;border-bottom:1px solid var(--line);padding:8px;vertical-align:top}th{color:var(--muted);font-size:12px;text-transform:uppercase}.chip{display:inline-block;background:var(--panel2);color:var(--muted);border-radius:99px;padding:2px 8px;font-size:11px;margin:1px}.toolbar{display:flex;gap:10px;margin:4px 0 14px}.toolbar input{flex:1;background:var(--panel);border:1px solid var(--line);border-radius:9px;color:var(--text);padding:10px}.trace{font-family:ui-monospace,monospace;color:var(--blue);font-size:12px;word-break:break-all}.bar{height:8px;background:#263451;border-radius:8px;overflow:hidden}.bar i{display:block;height:100%;background:linear-gradient(90deg,var(--red),var(--amber),var(--green))}@media(max-width:1000px){.grid{grid-template-columns:repeat(2,1fr)}.layout{grid-template-columns:1fr}.wrap{padding:16px}}
</style></head><body><main class="wrap">
<header class="top"><div><div class="eyebrow">Cohort Eval</div><h1>Stability Platform</h1><div class="meta">生成于 {{.Index.GeneratedAt.Format "2006-01-02 15:04:05"}} · window={{.Index.Window}}</div></div></header>
<section class="grid">
<div class="card"><div class="label">Runs</div><div class="value">{{.Index.Summary.Runs}}</div></div>
<div class="card"><div class="label">Pass Rate</div><div class="value">{{pct .Index.Summary.AveragePassRate}}</div></div>
<div class="card"><div class="label">Score</div><div class="value">{{printf "%.1f" .Index.Summary.AverageScore}}</div></div>
<div class="card"><div class="label">Stability</div><div class="value">{{pct .Index.Summary.AverageStability}}</div></div>
<div class="card"><div class="label">Flaky Cases</div><div class="value {{if gt .Index.Summary.FlakyCases 0}}warn{{else}}good{{end}}">{{.Index.Summary.FlakyCases}}</div></div>
<div class="card"><div class="label">Regressions</div><div class="value {{if gt .Index.Summary.Regressions 0}}bad{{else}}good{{end}}">{{.Index.Summary.Regressions}}</div></div>
</section>
<section class="layout"><div class="panel"><h2>Suite Stability</h2><table><thead><tr><th>Suite</th><th>Runs</th><th>Pass</th><th>Score</th><th>Stability</th><th>Reg</th></tr></thead><tbody>{{range .Index.Suites}}<tr><td><b>{{.SuiteID}}</b><br><span class="meta">{{.SuiteName}}</span></td><td>{{.Runs}}</td><td>{{pct .AveragePassRate}}</td><td>{{printf "%.1f" .AverageScore}}</td><td>{{pct .AverageStability}}</td><td>{{.Regressions}}</td></tr>{{end}}</tbody></table></div>
<div class="panel"><h2>Failure Signatures</h2><table><thead><tr><th>Signature</th><th>Count</th><th>Cases</th></tr></thead><tbody>{{range .Index.FailureSignatures}}<tr><td><code>{{.Signature}}</code><br><span class="meta">{{.Example}}</span></td><td>{{.Count}}</td><td>{{range .CaseIDs}}<span class="chip">{{.}}</span>{{end}}</td></tr>{{end}}</tbody></table></div></section>
<section class="panel"><h2>Case Heat</h2><div class="toolbar"><input id="q" placeholder="搜索 case / suite / model / trace"></div><table id="cases"><thead><tr><th>Case</th><th>Pass</th><th>Score</th><th>Stability</th><th>Flaky</th><th>Latest</th><th>Trace</th></tr></thead><tbody>{{range .Index.Cases}}<tr data-search="{{.SuiteID}} {{.CaseID}} {{.Name}} {{.Model}} {{.Profile}} {{.LatestTracePath}}"><td><b>{{.CaseID}}</b><br><span class="meta">{{.SuiteID}} · {{.Name}}</span></td><td><div>{{pct .PassRate}}</div><div class="bar"><i style="width:{{printf "%.1f" .PassRate}}%"></i></div></td><td>{{printf "%.1f" .AverageScore}}</td><td>{{pct .AverageStability}}</td><td class="{{if .Flaky}}warn{{else}}good{{end}}">{{.Flaky}}</td><td>{{status .LatestPassed}}<br><span class="meta">{{.LatestRunID}}</span></td><td class="trace">{{.LatestTracePath}}{{if .LatestTraceRunID}}<br>{{.LatestTraceRunID}}{{end}}</td></tr>{{end}}</tbody></table></section>
</main><script>const q=document.getElementById('q');q.oninput=()=>{let v=q.value.toLowerCase();document.querySelectorAll('#cases tbody tr').forEach(r=>r.style.display=r.dataset.search.toLowerCase().includes(v)?'':'none')};window.__STABILITY__={{.IndexJSON}};</script></body></html>`

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
