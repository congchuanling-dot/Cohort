import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  ActionPreparation,
  apiGet,
  apiPost,
  EntityDescriptor,
  Operation,
  ReplayRuntimeView,
  TraceRuntimeView,
} from "./api";

export function TimeMachinePage() {
  const queryClient = useQueryClient();
  const [sessionID, setSessionID] = useState("");
  const [runID, setRunID] = useState("");
  const [forkTurn, setForkTurn] = useState(0);
  const [repeat, setRepeat] = useState(3);
  const [profileID, setProfileID] = useState("");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [keepWorktrees, setKeepWorktrees] = useState(false);

  const sessions = useQuery({
    queryKey: ["entities", "session", "time-machine"],
    queryFn: () => apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/session?limit=500&recent_first=true"),
  });
  const bundles = useQuery({
    queryKey: ["entities", "replay_bundle", sessionID],
    queryFn: () => apiGet<{ entities: EntityDescriptor[] }>(`/api/v1/entities/replay_bundle?limit=500&recent_first=true&session_id=${encodeURIComponent(sessionID)}`),
    enabled: sessionID !== "",
  });
  const profiles = useQuery({
    queryKey: ["entities", "model_profile", "time-machine"],
    queryFn: () => apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/model_profile?limit=100"),
  });
  const replay = useQuery({
    queryKey: ["replay", sessionID, runID],
    queryFn: () => apiGet<ReplayRuntimeView>(`/api/v1/replays/${encodeURIComponent(sessionID)}/${encodeURIComponent(runID)}`),
    enabled: sessionID !== "" && runID !== "",
    refetchInterval: 10_000,
  });
  const trace = useQuery({
    queryKey: ["quality", "trace", sessionID, runID, "time-machine"],
    queryFn: () => apiGet<TraceRuntimeView>(`/api/v1/quality/traces/${encodeURIComponent(sessionID)}/${encodeURIComponent(runID)}`),
    enabled: sessionID !== "" && runID !== "",
  });
  const turns = useMemo(() => {
    const grouped = new Map<number, { llm: number; tools: number; problems: number }>();
    for (const node of trace.data?.graph.nodes ?? []) {
      if (!node.turn || node.turn <= 0) continue;
      const value = grouped.get(node.turn) ?? { llm: 0, tools: 0, problems: 0 };
      if (node.kind === "llm") value.llm++;
      if (node.kind === "tool") value.tools++;
      if (node.status === "error" || node.severity === "error") value.problems++;
      grouped.set(node.turn, value);
    }
    return [...grouped.entries()].sort(([left], [right]) => left - right);
  }, [trace.data]);
  const fork = useMutation({
    mutationFn: async () => {
      const input: Record<string, unknown> = {
        session_id: sessionID,
        run_id: runID,
        fork_turn: forkTurn,
        repeat,
        keep_worktrees: keepWorktrees,
      };
      if (profileID) input.profile_id = profileID;
      if (systemPrompt.trim()) input.system_prompt = systemPrompt;
      const prepared = await apiPost<ActionPreparation>("/api/v1/actions/replay.fork/prepare", { input });
      return apiPost<Operation>("/api/v1/actions/replay.fork/execute", {
        input,
        preparation_token: prepared.preparation_token,
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["operations"] });
      void queryClient.invalidateQueries({ queryKey: ["replay", sessionID, runID] });
    },
  });

  const chooseSession = (entity: EntityDescriptor) => {
    setSessionID(entity.id);
    setRunID("");
    setForkTurn(0);
  };
  const chooseRun = (entity: EntityDescriptor) => {
    setRunID(entity.id);
    setForkTurn(0);
  };
  const manifest = replay.data?.manifest;
  const proof = replay.data?.exact_proof;
  const canFork = manifest?.replayability === "forkable" && proof?.verified && forkTurn > 0;

  return <section className="page-stack time-machine-page">
    <header className="page-heading">
      <div><p className="eyebrow">PROOF-CARRYING COUNTERFACTUAL REPLAY</p><h2>Cohort Time Machine</h2><p>选择一次历史运行，在因果 Turn 上分叉，并用重复 Trial 验证模型或 Prompt 变更。</p></div>
      {sessionID && runID && <Link className="button-link" to={`/quality/traces/${encodeURIComponent(sessionID)}/${encodeURIComponent(runID)}`}>查看完整因果图</Link>}
    </header>

    <div className="time-machine-selector">
      <EntityColumn title="1. 选择 Session" entities={sessions.data?.entities ?? []} selectedID={sessionID} loading={sessions.isPending} onSelect={chooseSession} />
      <EntityColumn title="2. 选择 Replay Run" entities={bundles.data?.entities ?? []} selectedID={runID} loading={bundles.isPending} disabled={!sessionID} onSelect={chooseRun} />
      <section className="panel replay-proof-card">
        <div className="panel-heading"><h3>3. Exact Proof</h3>{proof && <span className={`risk ${proof.verified ? "ok" : "warn"}`}>{proof.verified ? "VERIFIED" : "DIVERGED"}</span>}</div>
        {!runID && <div className="empty">选择一个 Replay Run 后自动校验证据。</div>}
        {replay.isPending && runID && <div className="empty">正在离线校验 Bundle…</div>}
        {replay.isError && <div className="source-error"><strong>Replay 不可用</strong><span>{replay.error.message}</span></div>}
        {proof && manifest && <>
          <dl className="replay-proof-grid">
            <div><dt>状态</dt><dd>{manifest.replayability}</dd></div>
            <div><dt>Frames</dt><dd>{proof.frame_count}</dd></div>
            <div><dt>Turns</dt><dd>{proof.turn_count}</dd></div>
            <div><dt>LLM / Tools</dt><dd>{proof.llm_calls} / {proof.tool_calls}</dd></div>
            <div><dt>模型</dt><dd>{manifest.model || "unknown"}</dd></div>
            <div><dt>Workspace</dt><dd>{manifest.git.dirty ? "snapshot" : "clean"}</dd></div>
          </dl>
          {manifest.replay_block_reason && <p className="drawer-error">{manifest.replay_block_reason}</p>}
          {proof.first_divergence && <p className="drawer-error">Turn {proof.first_divergence.turn ?? "-"}: {proof.first_divergence.reason}</p>}
          <code className="proof-hash">{proof.proof_hash}</code>
        </>}
      </section>
    </div>

    {runID && <section className="panel fork-studio">
      <div className="panel-heading"><div><p className="eyebrow">INTERVENTION POINT</p><h3>4. 点击一个 Turn 作为分叉点</h3></div><span className="safe-pill">{forkTurn ? `Fork at Turn ${forkTurn}` : "尚未选择"}</span></div>
      {trace.isPending && <div className="empty">正在读取因果轨迹…</div>}
      {trace.isError && <div className="source-error"><strong>轨迹不可用</strong><span>{trace.error.message}</span></div>}
      <div className="turn-picker">
        {turns.map(([turn, summary]) => <button key={turn} type="button" className={forkTurn === turn ? "selected" : ""} onClick={() => setForkTurn(turn)}>
          <strong>Turn {turn}</strong><span>{summary.llm} LLM · {summary.tools} tools</span>{summary.problems > 0 && <em>{summary.problems} problems</em>}
        </button>)}
      </div>
    </section>}

    {runID && <div className="time-machine-experiment-grid">
      <section className="panel intervention-form">
        <div className="panel-heading"><div><p className="eyebrow">COUNTERFACTUAL</p><h3>5. 设置干预变量</h3></div></div>
        <label><span>候选模型 Profile</span><select value={profileID} onChange={(event) => setProfileID(event.target.value)}><option value="">沿用当前模型</option>{(profiles.data?.entities ?? []).map((profile) => <option key={profile.id} value={profile.id}>{profile.title} · {profile.subtitle}</option>)}</select></label>
        <label><span>候选 System Prompt</span><textarea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} placeholder="留空：沿用录制 Prompt。填写后只改变分叉实验的 System Prompt。" /></label>
        <label><span>重复 Trial：{repeat}</span><input type="range" min="1" max="10" value={repeat} onChange={(event) => setRepeat(Number(event.target.value))} /></label>
        <label className="checkbox-field"><input type="checkbox" checked={keepWorktrees} onChange={(event) => setKeepWorktrees(event.target.checked)} /><span>保留隔离 Worktree 供人工检查</span></label>
        <button className="primary execute-button" type="button" disabled={!canFork || fork.isPending} onClick={() => fork.mutate()}>
          {fork.isPending ? "正在创建 Replay Operation…" : "启动分叉实验"}
        </button>
        {manifest?.replayability !== "forkable" && manifest && <p className="form-error">该 Bundle 只能 Exact Replay，不能执行 Fork。</p>}
        {fork.error && <p className="form-error">{fork.error.message}</p>}
        {fork.data && <p className="good-text">Operation 已启动，可在 Operations 页面查看实时状态。</p>}
      </section>
      <section className="panel experiment-history">
        <div className="panel-heading"><div><p className="eyebrow">PROOF REPORTS</p><h3>历史实验</h3></div><span className="risk read">{replay.data?.experiments.length ?? 0}</span></div>
        {(replay.data?.experiments ?? []).map((experiment) => <article key={experiment.id}>
          <div><strong>Fork at Turn {experiment.fork_turn}</strong><small>{experiment.trials} trials · {new Date(experiment.created_at).toLocaleString()}</small></div>
          <span className={`risk ${experiment.success_rate === 1 ? "ok" : experiment.success_rate > 0 ? "warn" : ""}`}>{(experiment.success_rate * 100).toFixed(0)}%</span>
          <code>{experiment.proof_hash.slice(0, 16)}</code>
        </article>)}
        {(replay.data?.experiments.length ?? 0) === 0 && <div className="empty">尚未执行分叉实验。</div>}
      </section>
    </div>}
  </section>;
}

function EntityColumn({ title, entities, selectedID, loading, disabled, onSelect }: {
  title: string;
  entities: EntityDescriptor[];
  selectedID: string;
  loading: boolean;
  disabled?: boolean;
  onSelect: (entity: EntityDescriptor) => void;
}) {
  const [search, setSearch] = useState("");
  const keyword = search.trim().toLowerCase();
  const filtered = entities.filter((entity) => `${entity.title} ${entity.subtitle ?? ""} ${entity.status ?? ""}`.toLowerCase().includes(keyword));
  return <section className={`panel time-entity-column ${disabled ? "disabled" : ""}`}>
    <div className="panel-heading"><h3>{title}</h3><span className="risk read">{entities.length}</span></div>
    <input value={search} disabled={disabled} onChange={(event) => setSearch(event.target.value)} placeholder="按标题、模型或状态搜索" />
    <div>
      {filtered.map((entity) => <button key={`${entity.kind}:${entity.id}`} type="button" className={selectedID === entity.id ? "selected" : ""} onClick={() => onSelect(entity)}>
        <span><strong>{entity.title}</strong><small>{entity.subtitle}</small></span><em>{entity.status}</em>
      </button>)}
      {loading && <p className="empty">正在读取本地数据…</p>}
      {!loading && !disabled && filtered.length === 0 && <p className="empty">没有可选对象</p>}
      {disabled && <p className="empty">先选择 Session</p>}
    </div>
  </section>;
}
