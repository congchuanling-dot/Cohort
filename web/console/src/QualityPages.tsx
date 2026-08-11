import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { apiGet, EvalCaseResult, EvalDashboard, QualitySummary, StabilityIndex, StabilityRun } from "./api";

export function QualityOverviewPage() {
  const quality = useQuery({ queryKey: ["quality", "summary"], queryFn: () => apiGet<QualitySummary>("/api/v1/quality/summary") });
  if (quality.isPending) return <QualityLoading />;
  if (quality.isError) return <QualityError detail={quality.error.message} />;
  const data = quality.data;
  return <section className="page-stack">
    <header className="page-heading"><div><p className="eyebrow">AGENT QUALITY</p><h2>质量与稳定性</h2><p>评测结果、回归趋势和失败证据统一视图。</p></div><div className="hero-actions"><Link className="button-link" to="/quality/stability">稳定性分析</Link><Link className="button-link" to="/quality/tuning">运行调优</Link></div></header>
    <div className="quality-metrics">
      <QualityMetric label="Runs" value={data.summary.runs} />
      <QualityMetric label="Pass Rate" value={`${data.summary.average_pass_rate.toFixed(1)}%`} tone={data.summary.average_pass_rate >= 90 ? "good" : "warn"} />
      <QualityMetric label="Score" value={data.summary.average_score.toFixed(1)} />
      <QualityMetric label="Stability" value={`${data.summary.average_stability.toFixed(1)}%`} />
      <QualityMetric label="Flaky" value={data.summary.flaky_cases} tone={data.summary.flaky_cases ? "warn" : "good"} />
      <QualityMetric label="Regressions" value={data.summary.regressions} tone={data.summary.regressions ? "bad" : "good"} />
    </div>
    <section className="panel"><div className="panel-heading"><h3>通过率趋势</h3><span className="risk read">LAST {data.runs.length}</span></div><TrendChart runs={data.runs} /></section>
    <section className="panel">
      <div className="panel-heading"><h3>最近 Eval Runs</h3><a className="button-link" href="/api/v1/exports/stability.html">导出 Stability HTML</a></div>
      <div className="quality-run-list">{[...data.runs].reverse().map((run) => <Link key={run.run_id} to={`/quality/evals/${encodeURIComponent(run.run_id)}`}>
        <span className={`score-ring ${run.pass_rate >= 90 ? "good" : run.pass_rate >= 70 ? "warn" : "bad"}`}>{run.pass_rate.toFixed(0)}</span>
        <div><strong>{run.suite_name || run.suite_id}</strong><small>{run.model || run.profile || "default"} · score {run.score.toFixed(1)}</small></div>
        <time>{new Date(run.started_at).toLocaleString()}</time>
      </Link>)}</div>
    </section>
  </section>;
}

export function EvalRunPage() {
  const { runId = "" } = useParams();
  const dashboard = useQuery({
    queryKey: ["quality", "eval", runId],
    queryFn: () => apiGet<EvalDashboard>(`/api/v1/quality/evals/${encodeURIComponent(runId)}`),
    enabled: runId !== "",
  });
  const [selected, setSelected] = useState<EvalCaseResult | null>(null);
  if (dashboard.isPending) return <QualityLoading />;
  if (dashboard.isError) return <QualityError detail={dashboard.error.message} />;
  const { result } = dashboard.data;
  return <section className="page-stack">
    <div className="quality-breadcrumb"><Link to="/quality">质量中心</Link><span>/</span><strong>{result.run_id}</strong></div>
    <header className="page-heading"><div><p className="eyebrow">EVAL RUN</p><h2>{result.suite_name || result.suite_id}</h2><p>{result.model || result.profile} · {(result.duration_ms / 1000).toFixed(1)}s · {result.total_tokens ?? 0} tokens</p></div><a className="button-link" href={`/api/v1/exports/evals/${encodeURIComponent(result.run_id)}.html`}>导出离线 HTML</a></header>
    <div className="quality-metrics">
      <QualityMetric label="Pass Rate" value={`${result.pass_rate.toFixed(1)}%`} tone={result.pass_rate >= 90 ? "good" : "bad"} />
      <QualityMetric label="Score" value={result.score.toFixed(1)} />
      <QualityMetric label="Passed" value={result.passed_cases} tone="good" />
      <QualityMetric label="Failed" value={result.failed_cases} tone={result.failed_cases ? "bad" : "good"} />
      <QualityMetric label="Stability" value={`${dashboard.data.average_stability.toFixed(1)}%`} />
      <QualityMetric label="Gate" value={result.gate?.passed === false ? "FAIL" : "PASS"} tone={result.gate?.passed === false ? "bad" : "good"} />
    </div>
    {result.gate?.violations && result.gate.violations.length > 0 && <section className="quality-alert"><strong>Gate Violations</strong>{result.gate.violations.map((item) => <span key={item}>{item}</span>)}</section>}
    <section className="panel"><div className="panel-heading"><h3>Cases</h3><span className="risk read">{result.cases.length}</span></div>
      <div className="case-table">{result.cases.map((item) => <button type="button" key={item.case_id} onClick={() => setSelected(item)}>
        <span className={`case-status ${item.passed ? "pass" : item.skipped ? "skip" : "fail"}`}>{item.passed ? "PASS" : item.skipped ? "SKIP" : "FAIL"}</span>
        <div><strong>{item.name || item.case_id}</strong><small>{item.case_id} · {(item.tools ?? []).join(", ") || "no tools"}</small></div>
        <span>{item.score.toFixed(1)}</span><span>{item.stability_rate.toFixed(1)}%</span><span>{item.total_tokens ?? 0} tok</span>
      </button>)}</div>
    </section>
    {selected && <CaseDrawer item={selected} onClose={() => setSelected(null)} />}
  </section>;
}

export function StabilityPage() {
  const stability = useQuery({ queryKey: ["quality", "stability"], queryFn: () => apiGet<StabilityIndex>("/api/v1/quality/stability?window=50") });
  const [filter, setFilter] = useState("");
  if (stability.isPending) return <QualityLoading />;
  if (stability.isError) return <QualityError detail={stability.error.message} />;
  const data = stability.data;
  const cases = data.cases.filter((item) => `${item.suite_id} ${item.case_id} ${item.name} ${item.model}`.toLowerCase().includes(filter.toLowerCase()));
  return <section className="page-stack">
    <div className="quality-breadcrumb"><Link to="/quality">质量中心</Link><span>/</span><strong>Stability</strong></div>
    <header className="page-heading"><div><p className="eyebrow">HISTORICAL QUALITY</p><h2>稳定性分析</h2><p>Flaky Case、回归和重复失败签名。</p></div><a className="button-link" href="/api/v1/exports/stability.html?window=50">导出离线 HTML</a></header>
    <div className="quality-metrics">
      <QualityMetric label="Runs" value={data.summary.runs} /><QualityMetric label="Pass Rate" value={`${data.summary.average_pass_rate.toFixed(1)}%`} />
      <QualityMetric label="Stability" value={`${data.summary.average_stability.toFixed(1)}%`} /><QualityMetric label="Flaky" value={data.summary.flaky_cases} tone={data.summary.flaky_cases ? "warn" : "good"} />
      <QualityMetric label="Regressions" value={data.summary.regressions} tone={data.summary.regressions ? "bad" : "good"} /><QualityMetric label="Signatures" value={data.summary.failure_signatures} />
    </div>
    <section className="panel"><div className="panel-heading"><h3>历史趋势</h3><span className="risk read">{data.window} WINDOW</span></div><TrendChart runs={data.runs} /></section>
    <div className="quality-columns">
      <section className="panel"><div className="panel-heading"><h3>Failure Signatures</h3></div>{data.failure_signatures.slice(0, 12).map((item) => <article className="signature-row" key={item.signature}><strong>{item.signature}</strong><span>{item.count}x</span><small>{item.example}</small></article>)}</section>
      <section className="panel"><div className="panel-heading"><h3>Regressions</h3></div>{data.regressions.slice(0, 12).map((item) => <Link className="regression-row" key={`${item.case_id}:${item.to_run_id}`} to={`/quality/evals/${encodeURIComponent(item.to_run_id)}`}><strong>{item.case_id}</strong><small>{item.from_run_id} → {item.to_run_id}</small></Link>)}</section>
    </div>
    <section className="panel"><div className="panel-heading"><h3>Case Heat</h3><input className="table-search" value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="搜索 Case / Suite / Model" /></div>
      <div className="stability-table">{cases.map((item) => <article key={`${item.suite_id}:${item.case_id}:${item.model}`}><div><strong>{item.case_id}</strong><small>{item.suite_id} · {item.name}</small></div><span>{item.pass_rate.toFixed(1)}%</span><span>{item.average_stability.toFixed(1)}%</span><span className={item.flaky ? "warn-text" : "good-text"}>{item.flaky ? "FLAKY" : "STABLE"}</span><Link to={`/quality/evals/${encodeURIComponent(item.latest_run_id)}`}>latest</Link></article>)}</div>
    </section>
  </section>;
}

function TrendChart({ runs }: { runs: StabilityRun[] }) {
  const points = useMemo(() => {
    const ordered = [...runs].sort((a, b) => new Date(a.started_at).getTime() - new Date(b.started_at).getTime());
    return ordered.map((run, index) => ({
      run, x: ordered.length === 1 ? 50 : 4 + index * 92 / (ordered.length - 1), y: 96 - run.pass_rate * .88,
    }));
  }, [runs]);
  if (points.length === 0) return <div className="empty">暂无趋势数据</div>;
  return <svg className="trend-chart" viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label="Eval pass rate trend">
    {[25, 50, 75, 100].map((value) => <line key={value} x1="0" x2="100" y1={96 - value * .88} y2={96 - value * .88} className="trend-grid" />)}
    <polyline points={points.map((point) => `${point.x},${point.y}`).join(" ")} className="trend-line" />
    {points.map((point) => <circle key={point.run.run_id} cx={point.x} cy={point.y} r="1.3" className="trend-dot"><title>{point.run.run_id}: {point.run.pass_rate.toFixed(1)}%</title></circle>)}
  </svg>;
}

function CaseDrawer({ item, onClose }: { item: EvalCaseResult; onClose: () => void }) {
  return <aside className="operation-drawer">
    <div className="drawer-heading"><div><p className="eyebrow">CASE EVIDENCE</p><h3>{item.name || item.case_id}</h3></div><button type="button" onClick={onClose}>关闭</button></div>
    <dl className="detail-list"><div><dt>Status</dt><dd>{item.status}</dd></div><div><dt>Score</dt><dd>{item.score.toFixed(1)}</dd></div><div><dt>Turns</dt><dd>{item.turns}</dd></div><div><dt>Duration</dt><dd>{item.duration_ms} ms</dd></div></dl>
    {item.error && <p className="drawer-error">{item.error}</p>}
    {item.session_id && item.trace_run_id && <Link className="button-link" to={`/quality/traces/${encodeURIComponent(item.session_id)}/${encodeURIComponent(item.trace_run_id)}`} onClick={onClose}>查看因果 Trace</Link>}
    <h4>Assertions</h4>{item.assertion_results.map((assertion, index) => <article className={`assertion-row ${assertion.passed ? "pass" : "fail"}`} key={`${assertion.kind}:${index}`}><strong>{assertion.kind}</strong><span>{assertion.passed ? "PASS" : "FAIL"}</span><small>{assertion.detail || `${assertion.expected ?? ""} → ${assertion.actual ?? ""}`}</small></article>)}
    <h4>Action Items</h4>{(item.action_items ?? []).map((action) => <article className="action-item-row" key={action.id}><strong>{action.title}</strong><span>{action.severity}</span><small>{action.evidence || action.detail}</small></article>)}
  </aside>;
}

function QualityMetric({ label, value, tone = "" }: { label: string; value: string | number; tone?: string }) {
  return <article><span>{label}</span><strong className={tone}>{value}</strong></article>;
}
function QualityLoading() { return <div className="empty">正在构建质量视图…</div>; }
function QualityError({ detail }: { detail: string }) { return <div className="page-error"><strong>质量数据不可用</strong><p>{detail}</p></div>; }
