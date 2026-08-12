import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { apiGet, TraceGraph, TuningReport } from "./api";

export function TraceGraphPage() {
  const { sessionId = "", runId = "" } = useParams();
  const trace = useQuery({
    queryKey: ["quality", "trace", sessionId, runId],
    queryFn: () => apiGet<{ graph: TraceGraph }>(`/api/v1/quality/traces/${encodeURIComponent(sessionId)}/${encodeURIComponent(runId)}`),
    enabled: sessionId !== "" && runId !== "",
  });
  const [selectedID, setSelectedID] = useState("");
  const [criticalOnly, setCriticalOnly] = useState(false);
  const [zoom, setZoom] = useState(1);
  if (trace.isPending) return <div className="empty">正在重建因果图…</div>;
  if (trace.isError) return <div className="page-error"><strong>Trace 不可用</strong><p>{trace.error.message}</p></div>;
  const graph = trace.data.graph;
  const selected = graph.nodes.find((node) => node.id === selectedID);
  return <section className="page-stack">
    <div className="quality-breadcrumb"><Link to="/quality">质量中心</Link><span>/</span><strong>{graph.run_id}</strong></div>
    <header className="page-heading"><div><p className="eyebrow">AGENT FLIGHT RECORDER</p><h2>Causal Trace Graph</h2><p>Session {graph.session_id} · Run {graph.run_id} · {graph.status}</p></div><a className="button-link" href={`/api/v1/exports/traces/${encodeURIComponent(graph.session_id)}/${encodeURIComponent(graph.run_id)}.html`}>导出离线 HTML</a></header>
    <div className="quality-metrics">
      <TraceMetric label="Duration" value={formatDuration(graph.duration_ms)} /><TraceMetric label="Critical Path" value={formatDuration(graph.critical_path_ms)} />
      <TraceMetric label="Nodes / Edges" value={`${graph.summary.node_count} / ${graph.summary.edge_count}`} /><TraceMetric label="LLM / Tools" value={`${graph.summary.llm_nodes} / ${graph.summary.tool_nodes}`} />
      <TraceMetric label="Failed Tools" value={graph.summary.failed_tools} tone={graph.summary.failed_tools ? "bad" : "good"} /><TraceMetric label="File Changes" value={graph.summary.file_changes} />
    </div>
    <section className="panel trace-panel">
      <div className="trace-toolbar"><button type="button" className={criticalOnly ? "active" : ""} onClick={() => setCriticalOnly((value) => !value)}>关键路径</button><button type="button" onClick={() => setSelectedID("")}>重置选择</button><label>缩放 <input type="range" min=".6" max="1.8" step=".1" value={zoom} onChange={(event) => setZoom(Number(event.target.value))} /></label></div>
      <TraceCanvas graph={graph} selectedID={selectedID} criticalOnly={criticalOnly} zoom={zoom} onSelect={setSelectedID} />
    </section>
    <div className="quality-columns">
      <FindingPanel title="Latency Bottlenecks" items={graph.bottlenecks ?? []} />
      <FindingPanel title="Anomalies" items={graph.anomalies ?? []} />
    </div>
    {selected && <ExecutionInspector node={selected} onClose={() => setSelectedID("")} />}
  </section>;
}

function ExecutionInspector({ node, onClose }: { node: TraceGraph["nodes"][number]; onClose: () => void }) {
  const execution = node.execution ?? {};
  const attributes = Object.entries(execution.attributes ?? {});
  return <aside className="operation-drawer execution-inspector">
    <div className="drawer-heading"><div><p className="eyebrow">EXECUTION EVIDENCE</p><h3>{node.label}</h3></div><button type="button" onClick={onClose}>关闭</button></div>
    <dl className="detail-list"><div><dt>Kind</dt><dd>{node.kind}</dd></div><div><dt>Status</dt><dd>{node.status || "-"}</dd></div><div><dt>Turn</dt><dd>{node.turn ?? 0}</dd></div><div><dt>Duration</dt><dd>{formatDuration(node.duration_ms ?? 0)}</dd></div></dl>
    <ExecutionSection title="这一步做了什么" value={execution.what || node.detail} />
    <ExecutionSection title="怎么执行" value={execution.how} />
    <ExecutionSection title="输入摘要" value={execution.input_summary} />
    <ExecutionSection title="参数摘要（已脱敏）" value={execution.parameters_summary} mono />
    {execution.parameters_hash && <ExecutionSection title="参数证据 Hash" value={execution.parameters_hash} mono />}
    <ExecutionSection title="输出摘要" value={execution.output_summary} />
    {execution.token_usage && <section className="execution-section"><h4>Token / 成本依据</h4><dl className="execution-grid">
      <div><dt>来源</dt><dd>{execution.token_usage.source}</dd></div>
      <div><dt>Input</dt><dd>{execution.token_usage.input ?? "-"}</dd></div>
      <div><dt>Output</dt><dd>{execution.token_usage.output ?? "-"}</dd></div>
      <div><dt>Total</dt><dd>{execution.token_usage.total ?? "-"}</dd></div>
      <div><dt>Cache Read</dt><dd>{execution.token_usage.cache_read ?? "-"}</dd></div>
      <div><dt>本地估算</dt><dd>{execution.token_usage.estimated_input ?? "-"}</dd></div>
    </dl></section>}
    {execution.permission && <section className="execution-section"><h4>权限决策</h4><dl className="execution-grid">
      <div><dt>Decision</dt><dd>{execution.permission.decision}</dd></div><div><dt>Risk</dt><dd>{execution.permission.risk || "-"}</dd></div>
      <div><dt>External</dt><dd>{execution.permission.external ? "yes" : "no"}</dd></div><div><dt>Server</dt><dd>{execution.permission.server || "-"}</dd></div>
    </dl></section>}
    {attributes.length > 0 && <section className="execution-section"><h4>执行属性</h4><dl className="execution-attributes">{attributes.map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl></section>}
    <section className="execution-section"><h4>Evidence</h4><div className="evidence-list">{(execution.evidence ?? []).map((item, index) => <article key={`${item.ref}:${index}`}><strong>{item.label}</strong><small>{item.type}</small>{item.ref && <code>{item.ref}</code>}</article>)}{!execution.evidence?.length && <div className="empty">当前节点没有关联证据</div>}</div></section>
    <code className="node-id">{node.id}</code>
  </aside>;
}

function ExecutionSection({ title, value, mono = false }: { title: string; value?: string; mono?: boolean }) {
  if (!value) return null;
  return <section className="execution-section"><h4>{title}</h4>{mono ? <pre>{value}</pre> : <p>{value}</p>}</section>;
}

function TraceCanvas({ graph, selectedID, criticalOnly, zoom, onSelect }: { graph: TraceGraph; selectedID: string; criticalOnly: boolean; zoom: number; onSelect: (id: string) => void }) {
  const layout = useMemo(() => layoutGraph(graph), [graph]);
  const related = new Set<string>([selectedID]);
  if (selectedID) for (const edge of graph.edges) if (edge.from === selectedID || edge.to === selectedID) { related.add(edge.from); related.add(edge.to); }
  return <div className="trace-canvas-shell"><svg viewBox={`0 0 ${layout.width} ${layout.height}`} style={{ width: layout.width * zoom, height: layout.height * zoom }} role="img" aria-label="Causal trace graph">
    <defs><marker id="trace-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10z" /></marker></defs>
    {layout.edges.map((edge) => {
      const dim = criticalOnly && !(layout.byID.get(edge.from)?.critical && layout.byID.get(edge.to)?.critical) || selectedID !== "" && !(edge.from === selectedID || edge.to === selectedID);
      return <path key={`${edge.from}:${edge.to}:${edge.relation}`} className={`trace-edge ${dim ? "dim" : ""}`} d={edge.path}><title>{edge.relation}</title></path>;
    })}
    {layout.nodes.map((node) => {
      const dim = criticalOnly && !node.critical || selectedID !== "" && !related.has(node.id);
      return <g key={node.id} className={`trace-node kind-${node.kind} ${node.critical ? "critical" : ""} ${node.severity === "error" || node.status === "error" ? "problem" : ""} ${selectedID === node.id ? "selected" : ""} ${dim ? "dim" : ""}`} transform={`translate(${node.x},${node.y})`} onClick={() => onSelect(node.id)} role="button" tabIndex={0} onKeyDown={(event) => { if (event.key === "Enter") onSelect(node.id); }}>
        <rect width="180" height="62" rx="9" /><text x="10" y="19" className="node-label">{truncate(node.label, 25)}</text><text x="10" y="37" className="node-detail">{truncate(node.detail ?? "", 34)}</text><text x="10" y="53" className="node-meta">turn {node.turn ?? 0} · {node.kind} · {formatDuration(node.duration_ms ?? 0)}</text>
      </g>;
    })}
  </svg></div>;
}

function layoutGraph(graph: TraceGraph) {
  const column = (kind: string) => ({ run: 20, prompt: 20, turn: 20, context: 230, compact: 230, route: 440, llm: 650, permission: 860, decision: 860, tool: 860, artifact: 1070 }[kind] ?? 440);
  const nodes = graph.nodes.map((node) => ({ ...node, x: column(node.kind), y: 25 + node.order * 82 }));
  const byID = new Map(nodes.map((node) => [node.id, node]));
  const edges = graph.edges.flatMap((edge) => {
    const from = byID.get(edge.from), to = byID.get(edge.to);
    if (!from || !to) return [];
    const x1 = from.x + 180, y1 = from.y + 31, x2 = to.x, y2 = to.y + 31;
    return [{ ...edge, path: `M ${x1} ${y1} C ${x1 + 60} ${y1}, ${x2 - 60} ${y2}, ${x2} ${y2}` }];
  });
  return { nodes, edges, byID, width: 1280, height: Math.max(480, 120 + nodes.length * 82) };
}

export function TuningPage() {
  const tuning = useQuery({ queryKey: ["quality", "tuning"], queryFn: () => apiGet<TuningReport>("/api/v1/quality/tuning?limit=100") });
  if (tuning.isPending) return <div className="empty">正在分析最近 Agent Runs…</div>;
  if (tuning.isError) return <div className="page-error"><strong>Tuning 数据不可用</strong><p>{tuning.error.message}</p></div>;
  const report = tuning.data;
  const total = Math.max(1, report.total_duration_ms);
  return <section className="page-stack">
    <div className="quality-breadcrumb"><Link to="/quality">质量中心</Link><span>/</span><strong>Tuning</strong></div>
    <header className="page-heading"><div><p className="eyebrow">RUNTIME OBSERVABILITY</p><h2>Agent 调优</h2><p>{report.runs_scanned} Runs · {report.sessions_scanned} Sessions</p></div><a className="button-link" href="/api/v1/exports/tuning.html?limit=100">导出离线 HTML</a></header>
    <div className="quality-metrics"><TraceMetric label="Total" value={formatDuration(report.total_duration_ms)} /><TraceMetric label="LLM" value={formatDuration(report.llm_duration_ms)} /><TraceMetric label="Tools" value={formatDuration(report.tool_duration_ms)} /><TraceMetric label="Failures" value={report.tool_failures} tone={report.tool_failures ? "bad" : "good"} /><TraceMetric label="Schema Bloat" value={report.schema_bloat_runs} /><TraceMetric label="Context Bloat" value={report.context_bloat_runs} /></div>
    <section className="panel"><div className="panel-heading"><h3>耗时构成</h3></div><div className="duration-bars"><div><span>LLM</span><i style={{ width: `${report.llm_duration_ms / total * 100}%` }} /><b>{formatDuration(report.llm_duration_ms)}</b></div><div><span>Tools</span><i style={{ width: `${report.tool_duration_ms / total * 100}%` }} /><b>{formatDuration(report.tool_duration_ms)}</b></div></div></section>
    <div className="quality-columns">
      <section className="panel"><div className="panel-heading"><h3>调优建议</h3></div>{report.recommendations.map((item) => <p className="tuning-recommendation" key={item}>{item}</p>)}</section>
      <section className="panel"><div className="panel-heading"><h3>膨胀与路由</h3></div><dl className="detail-list"><div><dt>Request Bloat</dt><dd>{report.request_bloat_runs}</dd></div><div><dt>Schema Saved</dt><dd>{report.schema_bytes_saved} B</dd></div><div><dt>Route Escalations</dt><dd>{report.tool_route_escalations}</dd></div><div><dt>Permissions</dt><dd>{report.permission_events}</dd></div></dl></section>
    </div>
    <div className="quality-columns">
      <section className="panel"><div className="panel-heading"><h3>最慢 LLM</h3></div>{report.slow_llms.slice(0, 15).map((item) => <Link className="tuning-row" key={`${item.session_id}:${item.run_id}:${item.turn}`} to={`/quality/traces/${encodeURIComponent(item.session_id)}/${encodeURIComponent(item.run_id)}`}><strong>{formatDuration(item.duration_ms)}</strong><span>{item.session_id}</span><small>turn {item.turn} · {item.tool_schema_count} tools · {item.total_tokens} tokens</small></Link>)}</section>
      <section className="panel"><div className="panel-heading"><h3>失败工具</h3></div>{report.failed_tools.map((item) => <article className="tuning-row" key={`${item.tool}:${item.error_code}:${item.status}`}><strong>{item.tool}</strong><span>{item.count}x</span><small>{item.error_code || item.status} · {item.sessions} sessions</small></article>)}</section>
    </div>
  </section>;
}

function FindingPanel({ title, items }: { title: string; items: Array<{ node_id: string; label: string; reason: string; duration_ms?: number }> }) {
  return <section className="panel"><div className="panel-heading"><h3>{title}</h3><span className="risk read">{items.length}</span></div>{items.map((item) => <article className="finding-row" key={`${item.node_id}:${item.reason}`}><div><strong>{item.label}</strong><small>{item.reason}</small></div><span>{formatDuration(item.duration_ms ?? 0)}</span></article>)}{items.length === 0 && <div className="empty">没有发现问题</div>}</section>;
}
function TraceMetric({ label, value, tone = "" }: { label: string; value: string | number; tone?: string }) { return <article><span>{label}</span><strong className={tone}>{value}</strong></article>; }
function formatDuration(ms: number) { return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(2)}s`; }
function truncate(value: string, limit: number) { return value.length <= limit ? value : `${value.slice(0, limit)}…`; }
