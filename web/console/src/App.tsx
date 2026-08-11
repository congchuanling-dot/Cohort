import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ActionSpec, apiGet, apiPost, CapabilityResource, DashboardSnapshot, initializeSession,
  DataSourceHealth, DeliveryItem, EvalRun, HermesResource, InputField, LSPResource, MCPServerSummary,
  Operation, operationEvents, ProjectRecord, SessionInfo, SessionSummary,
  SettingsResource, SkillSummary,
} from "./api";

function StatusDot({ online }: { online: boolean }) {
  return <span className={online ? "status-dot online" : "status-dot"} aria-hidden="true" />;
}

export default function App() {
  const queryClient = useQueryClient();
  const [commandOpen, setCommandOpen] = useState(false);
  const [commandIntent, setCommandIntent] = useState("");
  const session = useQuery({ queryKey: ["session"], queryFn: initializeSession, retry: false });
  const catalog = useQuery({
    queryKey: ["catalog"],
    queryFn: () => apiGet<{ actions: ActionSpec[] }>("/api/v1/catalog"),
    enabled: session.isSuccess,
  });
  const operations = useQuery({
    queryKey: ["operations"],
    queryFn: () => apiGet<{ operations: Operation[] }>("/api/v1/operations"),
    enabled: session.isSuccess,
  });
  const snapshot = useQuery({
    queryKey: ["snapshot"],
    queryFn: () => apiGet<DashboardSnapshot>("/api/v1/snapshot"),
    enabled: session.isSuccess,
    refetchInterval: 10_000,
  });
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => apiGet<{ projects: ProjectRecord[] }>("/api/v1/projects"),
    enabled: session.isSuccess,
  });
  const deliveries = useQuery({
    queryKey: ["resource", "deliveries"],
    queryFn: () => apiGet<{ deliveries: DeliveryItem[] }>("/api/v1/resources/deliveries"),
    enabled: session.isSuccess,
    refetchInterval: 10_000,
  });
  const hermes = useQuery({
    queryKey: ["resource", "hermes"],
    queryFn: () => apiGet<HermesResource>("/api/v1/resources/hermes"),
    enabled: session.isSuccess,
    refetchInterval: 10_000,
  });
  const evaluations = useQuery({
    queryKey: ["resource", "evaluations"],
    queryFn: () => apiGet<{ runs: EvalRun[] }>("/api/v1/resources/evaluations"),
    enabled: session.isSuccess,
  });
  const traces = useQuery({
    queryKey: ["resource", "traces"],
    queryFn: () => apiGet<{ sessions: SessionSummary[] }>("/api/v1/resources/traces"),
    enabled: session.isSuccess,
  });
  const capabilities = useQuery({
    queryKey: ["resource", "capabilities"],
    queryFn: () => apiGet<CapabilityResource>("/api/v1/resources/capabilities"),
    enabled: session.isSuccess,
  });
  const skills = useQuery({
    queryKey: ["resource", "skills"],
    queryFn: () => apiGet<{ skills: SkillSummary[] }>("/api/v1/resources/skills"),
    enabled: session.isSuccess,
  });
  const mcp = useQuery({
    queryKey: ["resource", "mcp"],
    queryFn: () => apiGet<{ servers: MCPServerSummary[] }>("/api/v1/resources/mcp"),
    enabled: session.isSuccess,
  });
  const lsp = useQuery({
    queryKey: ["resource", "lsp"],
    queryFn: () => apiGet<LSPResource>("/api/v1/resources/lsp"),
    enabled: session.isSuccess,
  });
  const settings = useQuery({
    queryKey: ["resource", "settings"],
    queryFn: () => apiGet<SettingsResource>("/api/v1/resources/settings"),
    enabled: session.isSuccess,
  });

  useEffect(() => {
    if (!session.isSuccess) return;
    return operationEvents(() => {
      void queryClient.invalidateQueries({ queryKey: ["operations"] });
      void queryClient.invalidateQueries({ queryKey: ["snapshot"] });
      void queryClient.invalidateQueries({ queryKey: ["resource"] });
    });
  }, [queryClient, session.isSuccess]);

  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandIntent("");
        setCommandOpen((open) => !open);
      }
      if (event.key === "Escape") setCommandOpen(false);
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, []);

  if (session.isPending) return <CenteredState title="正在建立安全会话" detail="连接本地 Cohort Control Plane…" />;
  if (session.isError) return <CenteredState title="无法进入控制台" detail={session.error.message} failed />;

  const sessionInfo = session.data as SessionInfo;
  const running = operations.data?.operations.filter((item) => item.status === "pending" || item.status === "running").length ?? 0;
  const data = snapshot.data;

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand"><div className="brand-mark">C</div><div><strong>Cohort</strong><span>Control Center</span></div></div>
        <nav aria-label="主导航">
          <a className="active" href="#overview">概览</a><a href="#sessions">Agent Sessions</a>
          <a href="#deliveries">Deliveries</a><a href="#operations">Operations</a>
          <a href="#quality">质量与追踪</a><a href="#capabilities">能力中心</a><a href="#settings">设置</a>
        </nav>
        <div className="project-switcher">
          <span>当前项目</span>
          <strong>{data?.project.name ?? "Cohort"}</strong>
          <small>{projects.data?.projects.length ?? 1} 个已登记项目</small>
        </div>
        <div className="sidebar-foot"><StatusDot online /><span>本地控制面已连接</span></div>
      </aside>
      <main>
        <header>
          <div><p className="eyebrow">LOCAL-FIRST AGENT RUNTIME</p><h1>控制中心</h1></div>
          <button className="command-button" type="button" onClick={() => { setCommandIntent(""); setCommandOpen(true); }}><kbd>⌘</kbd><kbd>K</kbd> 搜索动作</button>
        </header>

        <section className="hero">
          <div>
            <span className="safe-pill"><StatusDot online /> {data?.project.branch ?? "本地项目"} · {data?.project.head ?? "loading"}</span>
            <h2>从一个工作面管理 Agent 的执行、证据与审批。</h2>
            <p>{sessionInfo.project_root}</p>
          </div>
          <div className="hero-actions">
            <button className="primary" type="button" onClick={() => { setCommandIntent(""); setCommandOpen(true); }}>发起动作</button>
            <a className="button-link" href="#operations">查看运行记录</a>
          </div>
        </section>

        <section className="metrics" aria-label="运行指标">
          <Metric label="可用动作" value={catalog.data?.actions.length ?? 0} hint="统一 Action Catalog" />
          <Metric label="运行中" value={running} hint="实时 Operation" accent={running > 0} />
          <Metric label="Deliveries" value={data?.counts.deliveries ?? 0} hint={`${data?.delivery.verified ?? 0} verified`} />
          <Metric label="Eval 通过率" value={`${(data?.evaluation.pass_rate ?? 0).toFixed(1)}%`} hint={`${data?.evaluation.regressions ?? 0} regressions`} />
        </section>

        <DataSourcesPanel />

        <div className="overview-grid">
          <section className="panel">
            <div className="panel-heading"><div><p className="eyebrow">PROJECT HEALTH</p><h3>项目状态</h3></div><span className={data?.project.dirty ? "risk warn" : "risk ok"}>{data?.project.dirty ? "DIRTY" : "CLEAN"}</span></div>
            <dl className="detail-list">
              <Detail label="模型" value={data?.model.model || "未配置"} />
              <Detail label="Provider" value={data?.model.provider || "未配置"} />
              <Detail label="Sessions" value={String(data?.counts.sessions ?? 0)} />
              <Detail label="Explorers" value={String(data?.counts.explorers ?? 0)} />
              <Detail label="Reflection Queue" value={String(data?.reflection?.pending ?? 0)} />
            </dl>
          </section>
          <section className="panel">
            <div className="panel-heading"><div><p className="eyebrow">AUTONOMOUS OPS</p><h3>Hermes</h3></div><span className={data?.hermes.running ? "risk ok" : "risk"}>{data?.hermes.running ? "RUNNING" : "STOPPED"}</span></div>
            <dl className="detail-list">
              <Detail label="Open Actions" value={String(data?.hermes.open_actions ?? 0)} />
              <Detail label="Critical" value={String(data?.hermes.critical_actions ?? 0)} danger={(data?.hermes.critical_actions ?? 0) > 0} />
              <Detail label="Jobs / Repairs" value={`${data?.hermes.running_jobs ?? 0} / ${data?.hermes.running_repairs ?? 0}`} />
              <Detail label="Eval Runs" value={String(data?.counts.eval_runs ?? 0)} />
            </dl>
          </section>
        </div>

        <section className="panel operation-panel" id="operations">
          <div className="panel-heading"><div><p className="eyebrow">AUDIT TRAIL</p><h3>最近操作</h3></div><span className="safe-pill"><StatusDot online /> SSE 实时同步</span></div>
          <OperationList operations={operations.data?.operations ?? []} />
        </section>
        <DomainPanels
          deliveries={deliveries.data?.deliveries ?? []}
          hermes={hermes.data}
          evaluations={evaluations.data?.runs ?? []}
          sessions={traces.data?.sessions ?? []}
          runAction={(intent) => { setCommandIntent(intent); setCommandOpen(true); }}
        />
        <CapabilityCenter
          capabilities={capabilities.data}
          skills={skills.data?.skills ?? []}
          servers={mcp.data?.servers ?? []}
          lsp={lsp.data}
          settings={settings.data}
          runAction={(intent) => { setCommandIntent(intent); setCommandOpen(true); }}
        />
      </main>
      {commandOpen && <CommandCenter actions={catalog.data?.actions ?? []} initialQuery={commandIntent} onClose={() => setCommandOpen(false)} />}
    </div>
  );
}

function DataSourcesPanel() {
  const queryClient = useQueryClient();
  const sources = useQuery({
    queryKey: ["data-sources"],
    queryFn: () => apiGet<{ project_root: string; sources: DataSourceHealth[] }>("/api/v1/data-sources"),
    refetchInterval: 15_000,
  });
  const refresh = useMutation({
    mutationFn: () => apiPost<{ sources: DataSourceHealth[] }>("/api/v1/data-sources/refresh", {}),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["data-sources"] });
      void queryClient.invalidateQueries({ queryKey: ["resource"] });
      void queryClient.invalidateQueries({ queryKey: ["snapshot"] });
    },
  });
  return <section className="panel source-panel" id="data-sources">
    <div className="panel-heading">
      <div><p className="eyebrow">LOCAL DATA HUB</p><h3>本地数据来源</h3></div>
      <button type="button" disabled={refresh.isPending} onClick={() => refresh.mutate()}>
        {refresh.isPending ? "扫描中…" : "重新扫描"}
      </button>
    </div>
    {sources.isError && <div className="source-error"><strong>数据索引加载失败</strong><span>{sources.error.message}</span></div>}
    <div className="source-grid">
      {(sources.data?.sources ?? []).map((source) => <article key={source.kind} className={`source-card ${source.state}`}>
        <div><strong>{source.label}</strong><span className={`source-state ${source.state}`}>{source.state}</span></div>
        <b>{source.count}</b>
        <code>{source.relative_path}</code>
        <small>{source.error || `扫描于 ${new Date(source.scanned_at).toLocaleTimeString()}`}</small>
      </article>)}
      {sources.isPending && <div className="empty">正在扫描本地 Store…</div>}
    </div>
  </section>;
}

function DomainPanels({
  deliveries, hermes, evaluations, sessions, runAction,
}: {
  deliveries: DeliveryItem[];
  hermes?: HermesResource;
  evaluations: EvalRun[];
  sessions: SessionSummary[];
  runAction: (intent: string) => void;
}) {
  return <div className="domain-stack">
    <section className="panel" id="deliveries">
      <div className="panel-heading"><div><p className="eyebrow">EVIDENCE-DRIVEN DELIVERY</p><h3>Deliveries</h3></div><button type="button" onClick={() => runAction("delivery")}>Delivery 动作</button></div>
      <div className="resource-table">
        {deliveries.slice(0, 8).map((item) => <article key={item.id}><div><strong>{item.requirement}</strong><small>{item.id} · base {item.base_commit.slice(0, 10)}</small></div><span className={`delivery-state ${item.status}`}>{item.status}</span><time>{new Date(item.updated_at).toLocaleString()}</time></article>)}
        {deliveries.length === 0 && <div className="empty">暂无 Delivery。使用命令面板创建计划。</div>}
      </div>
    </section>
    <div className="overview-grid">
      <section className="panel" id="hermes">
        <div className="panel-heading"><div><p className="eyebrow">ACTION QUEUE</p><h3>Hermes</h3></div><button type="button" onClick={() => runAction("hermes")}>管理动作</button></div>
        <div className="compact-list">
          {(hermes?.actions ?? []).slice(0, 6).map((item) => <article key={item.id}><span className={`severity ${item.severity}`} /><div><strong>{item.title}</strong><small>{item.category} · {item.status}</small></div></article>)}
          {(hermes?.actions.length ?? 0) === 0 && <div className="empty">Action Queue 为空</div>}
        </div>
      </section>
      <section className="panel" id="quality">
        <div className="panel-heading"><div><p className="eyebrow">QUALITY GATES</p><h3>Eval Runs</h3></div><span className="risk read">{evaluations.length} RUNS</span></div>
        <div className="compact-list">
          {evaluations.slice(0, 6).map((run) => <article key={run.run_id}><span className={`score-ring ${run.pass_rate >= 90 ? "good" : run.pass_rate >= 70 ? "warn" : "bad"}`}>{run.pass_rate.toFixed(0)}</span><div><strong>{run.suite_id}</strong><small>{run.model || "default"} · {run.total_tokens ?? 0} tokens</small></div></article>)}
          {evaluations.length === 0 && <div className="empty">暂无 Eval 结果</div>}
        </div>
      </section>
    </div>
    <section className="panel" id="traces">
      <div className="panel-heading"><div><p className="eyebrow">SESSIONS & TRACE</p><h3>最近 Agent Sessions</h3></div><span className="safe-pill">{sessions.length} sessions</span></div>
      <div className="session-grid">
        {sessions.slice(0, 8).map((item) => <article key={item.id}><strong>{item.title}</strong><small>{item.model || "unknown model"}</small><time>{new Date(item.updated_at).toLocaleString()}</time><code>{item.id}</code></article>)}
        {sessions.length === 0 && <div className="empty">暂无可追踪 Session</div>}
      </div>
    </section>
  </div>;
}

function CapabilityCenter({
  capabilities, skills, servers, lsp, settings, runAction,
}: {
  capabilities?: CapabilityResource;
  skills: SkillSummary[];
  servers: MCPServerSummary[];
  lsp?: LSPResource;
  settings?: SettingsResource;
  runAction: (intent: string) => void;
}) {
  const items = capabilities?.registry.capabilities ?? [];
  return <div className="capability-center" id="capabilities">
    <section className="panel capability-hero">
      <div><p className="eyebrow">CAPABILITY CONTROL PLANE</p><h3>能力中心</h3><p>统一管理可复用能力、Skills、MCP、LSP 和模型配置。所有写操作都经过 Action 风险门禁。</p></div>
      <div className="hero-actions"><button className="primary" type="button" onClick={() => runAction("agent.run")}>运行 Agent</button><button type="button" onClick={() => runAction("capability")}>管理能力</button></div>
    </section>
    <div className="capability-grid">
      <section className="panel">
        <div className="panel-heading"><h3>Capabilities</h3><span className="risk read">{items.length}</span></div>
        <div className="compact-list">
          {items.slice(0, 6).map((item) => <article key={item.id}><span className={`capability-icon ${item.status}`}>{item.type?.slice(0, 1).toUpperCase()}</span><div><strong>{item.id}</strong><small>{item.status} · {item.type}</small></div></article>)}
          {items.length === 0 && <div className="empty">尚未注册 Capability</div>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading"><h3>Skills</h3><button type="button" onClick={() => runAction("skill")}>管理</button></div>
        <div className="compact-list">
          {skills.slice(0, 6).map((item) => <article key={item.id}><span className="capability-icon skill">S</span><div><strong>{item.name}</strong><small>{item.scope} · {item.id}</small></div></article>)}
          {skills.length === 0 && <div className="empty">没有发现 Skill</div>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading"><h3>MCP Servers</h3><button type="button" onClick={() => runAction("mcp")}>管理</button></div>
        <div className="compact-list">
          {servers.slice(0, 6).map((item) => <article key={`${item.scope}/${item.name}`}><span className="capability-icon mcp">M</span><div><strong>{item.name}</strong><small>{item.scope} · {item.type} · {item.arg_count ?? 0} args</small></div></article>)}
          {servers.length === 0 && <div className="empty">没有配置 MCP Server</div>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading"><h3>LSP Backends</h3><button type="button" onClick={() => runAction("lsp")}>诊断</button></div>
        <div className="compact-list">
          {(lsp?.doctor ?? []).map((item) => <article key={item.language}><StatusDot online={item.ok} /><div><strong>{item.language}</strong><small>{item.ok ? item.version : item.error}</small></div></article>)}
        </div>
      </section>
    </div>
    <section className="panel settings-panel" id="settings">
      <div className="panel-heading"><div><p className="eyebrow">MODEL PROFILES</p><h3>运行设置</h3></div><button type="button" onClick={() => runAction("settings.model.activate")}>切换模型</button></div>
      <div className="profile-grid">
        {(settings?.profiles ?? []).map((profile) => <article key={profile.id} className={settings?.active_profile === profile.id ? "active" : ""}><div><strong>{profile.name || profile.id}</strong><span className={profile.api_key_present ? "risk ok" : "risk warn"}>{profile.api_key_present ? "KEY SET" : "NO KEY"}</span></div><p>{profile.provider} / {profile.model}</p><code>{profile.api_base}</code></article>)}
      </div>
    </section>
  </div>;
}

function CommandCenter({ actions, initialQuery, onClose }: { actions: ActionSpec[]; initialQuery: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [query, setQuery] = useState(initialQuery);
  const [selected, setSelected] = useState<ActionSpec | null>(null);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [confirmation, setConfirmation] = useState("");
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return actions;
    return actions.filter((action) => [action.id, action.label, action.description, ...(action.keywords ?? [])].join(" ").toLowerCase().includes(keyword));
  }, [actions, query]);
  const execute = useMutation({
    mutationFn: (action: ActionSpec) => apiPost<Operation>(`/api/v1/actions/${action.id}/execute`, { input: values, confirmation }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["operations"] });
      onClose();
    },
  });

  useEffect(() => {
    if (!selected && filtered.length === 1) {
      setSelected(filtered[0]);
      const defaults: Record<string, unknown> = {};
      for (const field of filtered[0].inputs ?? []) if (field.default !== undefined) defaults[field.name] = field.default;
      setValues(defaults);
    }
  }, [filtered, selected]);

  const choose = (action: ActionSpec) => {
    setSelected(action);
    const defaults: Record<string, unknown> = {};
    for (const field of action.inputs ?? []) if (field.default !== undefined) defaults[field.name] = field.default;
    setValues(defaults);
    setConfirmation("");
  };
  const submit = (event: { preventDefault: () => void }) => {
    event.preventDefault();
    if (selected) execute.mutate(selected);
  };

  return (
    <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="command-modal" role="dialog" aria-modal="true" aria-label="动作中心">
        <div className="command-search"><span>⌕</span><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索：运行任务、合并、重试、安装 MCP…" /><kbd>ESC</kbd></div>
        <div className="command-body">
          <div className="action-list">
            {filtered.map((action) => <button key={action.id} type="button" className={selected?.id === action.id ? "selected" : ""} onClick={() => choose(action)}><span><strong>{action.label}</strong><small>{action.category} · {action.id}</small></span><Risk risk={action.risk} /></button>)}
            {filtered.length === 0 && <p className="empty">没有匹配的动作</p>}
          </div>
          <form className="action-form" onSubmit={submit}>
            {selected ? <>
              <div><Risk risk={selected.risk} /><h3>{selected.label}</h3><p>{selected.description}</p></div>
              {(selected.inputs ?? []).map((field) => <DynamicField key={field.name} field={field} value={values[field.name]} onChange={(value) => setValues((current) => ({ ...current, [field.name]: value }))} />)}
              {selected.confirmation_text && <label><span>输入 <code>{selected.confirmation_text}</code> 确认</span><input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label>}
              {execute.error && <p className="form-error">{execute.error.message}</p>}
              <button className="primary execute-button" disabled={execute.isPending} type="submit">{execute.isPending ? "正在创建 Operation…" : "执行动作"}</button>
            </> : <div className="action-placeholder"><strong>选择一个动作</strong><p>参数表单会根据 Action Schema 自动生成。</p></div>}
          </form>
        </div>
      </section>
    </div>
  );
}

function DynamicField({ field, value, onChange }: { field: InputField; value: unknown; onChange: (value: unknown) => void }) {
  if (field.type === "boolean") return <label className="checkbox-field"><input type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} /><span>{field.label}</span></label>;
  if (field.type === "select") return <label><span>{field.label}</span><select value={String(value ?? "")} required={field.required} onChange={(event) => onChange(event.target.value)}><option value="">请选择</option>{field.options?.map((option) => <option key={option}>{option}</option>)}</select><small>{field.description}</small></label>;
  if (field.type === "text") return <label><span>{field.label}</span><textarea value={String(value ?? "")} required={field.required} placeholder={field.placeholder} onChange={(event) => onChange(event.target.value)} /><small>{field.description}</small></label>;
  return <label><span>{field.label}</span><input type={field.type === "secret" || field.sensitive ? "password" : field.type === "integer" ? "number" : "text"} value={String(value ?? "")} required={field.required} placeholder={field.placeholder} onChange={(event) => onChange(field.type === "integer" ? Number(event.target.value) : event.target.value)} /><small>{field.description}</small></label>;
}

function OperationList({ operations }: { operations: Operation[] }) {
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Operation | null>(null);
  const cancel = useMutation({
    mutationFn: (operation: Operation) => apiPost<Operation>(`/api/v1/operations/${operation.id}/cancel`, {}),
    onSuccess: (operation) => {
      setSelected(operation);
      void queryClient.invalidateQueries({ queryKey: ["operations"] });
    },
  });
  useEffect(() => {
    if (!selected) return;
    const latest = operations.find((operation) => operation.id === selected.id);
    if (latest) setSelected(latest);
  }, [operations, selected?.id]);
  if (operations.length === 0) return <div className="empty">还没有操作记录。按 <kbd>⌘</kbd><kbd>K</kbd> 发起第一个动作。</div>;
  return <>
    <div className="operation-list">{operations.slice(0, 8).map((operation) => <button type="button" key={operation.id} onClick={() => setSelected(operation)}><span className={`operation-status ${operation.status}`} /><span><strong>{operation.action_id}</strong><small>{operation.summary || operation.error || operation.id}</small></span><time>{new Date(operation.updated_at).toLocaleTimeString()}</time><span className="status-text">{operation.status}</span></button>)}</div>
    {selected && <aside className="operation-drawer">
      <div className="drawer-heading"><div><p className="eyebrow">OPERATION DETAIL</p><h3>{selected.action_id}</h3></div><button type="button" onClick={() => setSelected(null)}>关闭</button></div>
      <dl className="detail-list"><Detail label="ID" value={selected.id} /><Detail label="Status" value={selected.status} /><Detail label="Actor" value={selected.actor} /><Detail label="Updated" value={new Date(selected.updated_at).toLocaleString()} /></dl>
      {selected.error && <p className="drawer-error">{selected.error}</p>}
      {selected.result !== undefined && <pre>{JSON.stringify(selected.result, null, 2)}</pre>}
      {(selected.status === "pending" || selected.status === "running") && <button className="danger-button" type="button" disabled={cancel.isPending} onClick={() => cancel.mutate(selected)}>取消 Operation</button>}
    </aside>}
  </>;
}

function Risk({ risk }: { risk: ActionSpec["risk"] }) { return <span className={`risk ${risk}`}>{risk.toUpperCase()}</span>; }
function Metric({ label, value, hint, accent = false }: { label: string; value: string | number; hint: string; accent?: boolean }) { return <article className="metric"><span>{label}</span><strong className={accent ? "accent" : ""}>{value}</strong><small>{hint}</small></article>; }
function Detail({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) { return <div><dt>{label}</dt><dd className={danger ? "danger-text" : ""}>{value}</dd></div>; }
function CenteredState({ title, detail, failed = false }: { title: string; detail: string; failed?: boolean }) { return <div className="centered-state"><div className={failed ? "loader failed" : "loader"} /><h1>{title}</h1><p>{detail}</p></div>; }
