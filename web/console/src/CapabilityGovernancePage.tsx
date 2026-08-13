import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { apiGet, CapabilityResource, EntityDescriptor, EntityKind } from "./api";
import { entityInput } from "./EntityPages";

type SelectedEntity = { kind: EntityKind; id: string } | null;

export function CapabilityGovernancePage({ onAction }: { onAction: (actionID: string, input?: Record<string, unknown>) => void }) {
  const [selected, setSelected] = useState<SelectedEntity>(null);
  const resource = useQuery({
    queryKey: ["resource", "capabilities", "governance"],
    queryFn: () => apiGet<CapabilityResource>("/api/v1/resources/capabilities"),
  });
  const entities = useQuery({
    queryKey: ["entities", "capability-governance"],
    queryFn: async () => {
      const [gaps, proposals, capabilities, plans] = await Promise.all([
        apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/capability_gap?limit=500&recent_first=true"),
        apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/capability_proposal?limit=500&recent_first=true"),
        apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/capability?limit=500&recent_first=true"),
        apiGet<{ entities: EntityDescriptor[] }>("/api/v1/entities/dependency_plan?limit=500&recent_first=true"),
      ]);
      return { gaps: gaps.entities, proposals: proposals.entities, capabilities: capabilities.entities, plans: plans.entities };
    },
  });
  const detail = useMemo(() => findCapabilityDetail(resource.data, selected), [resource.data, selected]);
  const totals = {
    gaps: entities.data?.gaps.length ?? 0,
    proposals: entities.data?.proposals.length ?? 0,
    capabilities: entities.data?.capabilities.length ?? 0,
    plans: entities.data?.plans.length ?? 0,
  };

  return <section className="page-stack capability-governance-page">
    <header className="page-heading capability-governance-heading">
      <div><p className="eyebrow">CAPABILITY EVOLUTION CONTROL PLANE</p><h2>能力治理工作台</h2><p>从缺口发现、方案审批、能力验证到依赖安装，每次状态迁移都携带证据并经过风险门禁。</p></div>
      <div className="hero-actions"><button className="primary" type="button" onClick={() => onAction("capability.propose")}>记录新能力缺口</button><button type="button" onClick={() => onAction("reflection.drain")}>运行反思队列</button></div>
    </header>

    <div className="capability-flow-summary">
      <FlowMetric label="Open Gaps" value={totals.gaps} tone="warn" />
      <span>→</span><FlowMetric label="Proposals" value={totals.proposals} />
      <span>→</span><FlowMetric label="Capabilities" value={totals.capabilities} tone="good" />
      <span>→</span><FlowMetric label="Dependency Plans" value={totals.plans} />
    </div>

    {entities.isError && <div className="source-error"><strong>能力实体索引不可用</strong><span>{entities.error.message}</span></div>}
    <div className="capability-board">
      <CapabilityLane title="1. Gaps" subtitle="运行时发现的能力缺口" entities={entities.data?.gaps ?? []} selected={selected} onSelect={setSelected} onAction={onAction} />
      <CapabilityLane title="2. Proposals" subtitle="等待构建或依赖规划" entities={entities.data?.proposals ?? []} selected={selected} onSelect={setSelected} onAction={onAction} />
      <CapabilityLane title="3. Capabilities" subtitle="候选、可用和已禁用能力" entities={entities.data?.capabilities ?? []} selected={selected} onSelect={setSelected} onAction={onAction} />
      <CapabilityLane title="4. Dependency Plans" subtitle="受审批约束的固定安装计划" entities={entities.data?.plans ?? []} selected={selected} onSelect={setSelected} onAction={onAction} />
    </div>

    <div className="capability-lower-grid">
      <section className="panel capability-detail-panel">
        <div className="panel-heading"><div><p className="eyebrow">EVIDENCE INSPECTOR</p><h3>{detail ? detail.title : "选择一项查看证据"}</h3></div>{detail && <span className={`entity-status ${detail.status}`}>{detail.status}</span>}</div>
        {!detail && <div className="empty">点击上方任意 Gap、Proposal、Capability 或 Dependency Plan。</div>}
        {detail && <CapabilityDetail detail={detail} />}
      </section>
      <section className="panel">
        <div className="panel-heading"><div><p className="eyebrow">REPEATED GAPS</p><h3>自动建议</h3></div><span className="risk read">{resource.data?.suggestions.length ?? 0}</span></div>
        <div className="suggestion-list">
          {(resource.data?.suggestions ?? []).map((suggestion) => <article key={suggestion.missing_capability}>
            <span>{suggestion.count}×</span><div><strong>{humanize(suggestion.missing_capability)}</strong><small>{suggestion.reason}</small></div>
          </article>)}
          {(resource.data?.suggestions.length ?? 0) === 0 && <div className="empty">暂无重复缺口需要升级。</div>}
        </div>
      </section>
    </div>
  </section>;
}

function CapabilityLane({ title, subtitle, entities, selected, onSelect, onAction }: {
  title: string;
  subtitle: string;
  entities: EntityDescriptor[];
  selected: SelectedEntity;
  onSelect: (selected: SelectedEntity) => void;
  onAction: (actionID: string, input?: Record<string, unknown>) => void;
}) {
  return <section className="capability-lane">
    <header><div><h3>{title}</h3><p>{subtitle}</p></div><span>{entities.length}</span></header>
    <div>
      {entities.map((entity) => <article
        key={`${entity.kind}:${entity.id}`}
        role="button"
        tabIndex={0}
        className={selected?.kind === entity.kind && selected.id === entity.id ? "selected" : ""}
        onClick={() => onSelect({ kind: entity.kind, id: entity.id })}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            onSelect({ kind: entity.kind, id: entity.id });
          }
        }}
      >
        <div className="capability-card-heading"><strong>{humanize(entity.title)}</strong><span className={`entity-status ${entity.status}`}>{entity.status}</span></div>
        <p>{entity.subtitle}</p>
        {(entity.actions ?? []).some((action) => action.enabled) && <div className="capability-card-actions">
          {(entity.actions ?? []).filter((action) => action.enabled).slice(0, 3).map((action) => <button key={action.action_id} type="button" onClick={(event) => {
            event.stopPropagation();
            onAction(action.action_id, entityInput(entity.kind, entity.id));
          }}>{action.label}</button>)}
        </div>}
      </article>)}
      {entities.length === 0 && <div className="empty">当前 Lane 为空</div>}
    </div>
  </section>;
}

interface CapabilityDetailView {
  title: string;
  status: string;
  rows: Array<{ label: string; value: string }>;
  lists: Array<{ label: string; values: string[] }>;
}

function findCapabilityDetail(resource: CapabilityResource | undefined, selected: SelectedEntity): CapabilityDetailView | null {
  if (!resource || !selected) return null;
  if (selected.kind === "capability_gap") {
    const item = resource.registry.gaps.find((candidate) => candidate.id === selected.id);
    if (!item) return null;
    return {
      title: humanize(item.missing_capability), status: item.status,
      rows: [{ label: "任务", value: item.task }, { label: "来源", value: item.source ?? "-" }],
      lists: [{ label: "证据", values: item.evidence ?? [] }, { label: "建议动作", values: item.suggested_actions ?? [] }],
    };
  }
  if (selected.kind === "capability_proposal") {
    const item = resource.registry.proposals.find((candidate) => candidate.id === selected.id);
    if (!item) return null;
    const dependencies = [
      ...(item.dependencies?.python ?? []).map((value) => `python: ${value}`),
      ...(item.dependencies?.npm ?? []).map((value) => `npm: ${value}`),
      ...(item.dependencies?.brew ?? []).map((value) => `brew: ${value}`),
    ];
    return {
      title: item.summary, status: item.status,
      rows: [{ label: "风险", value: item.risk }, { label: "安装范围", value: item.install_scope ?? "project" }, { label: "验证任务", value: item.verification?.sample_task ?? "-" }],
      lists: [{ label: "候选产物", values: item.artifacts ?? [] }, { label: "依赖", values: dependencies }],
    };
  }
  if (selected.kind === "capability") {
    const item = resource.registry.capabilities.find((candidate) => candidate.id === selected.id);
    if (!item) return null;
    const requirements = [
      ...(item.requires?.tools ?? []).map((value) => `tool: ${value}`),
      ...(item.requires?.commands ?? []).map((value) => `command: ${value}`),
      ...(item.requires?.python ?? []).map((value) => `python: ${value}`),
      ...(item.requires?.npm ?? []).map((value) => `npm: ${value}`),
      ...(item.requires?.brew ?? []).map((value) => `brew: ${value}`),
    ];
    return {
      title: humanize(item.id), status: item.status,
      rows: [{ label: "类型", value: item.type }, { label: "风险", value: item.risk ?? "-" }, { label: "入口", value: item.entry ?? "-" }, { label: "验证命令", value: item.verification?.command ?? "-" }],
      lists: [{ label: "触发条件", values: item.triggers ?? [] }, { label: "运行依赖", values: requirements }],
    };
  }
  if (selected.kind === "dependency_plan") {
    const item = resource.dependencies?.plans.find((candidate) => candidate.id === selected.id);
    if (!item) return null;
    return {
      title: `${humanize(item.capability_id)} dependencies`, status: item.status,
      rows: [{ label: "范围", value: item.scope }, { label: "风险", value: item.risk }],
      lists: [{ label: "固定安装动作", values: item.actions.map((action) => `${action.manager}: ${action.command.join(" ")}`) }],
    };
  }
  return null;
}

function CapabilityDetail({ detail }: { detail: CapabilityDetailView }) {
  return <div className="capability-detail-content">
    <dl>{detail.rows.map((row) => <div key={row.label}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>
    {detail.lists.map((list) => <section key={list.label}><strong>{list.label}</strong>{list.values.length > 0 ? list.values.map((value) => <code key={value}>{value}</code>) : <small>无</small>}</section>)}
  </div>;
}

function FlowMetric({ label, value, tone = "" }: { label: string; value: number; tone?: string }) {
  return <article className={tone}><strong>{value}</strong><span>{label}</span></article>;
}

function humanize(value: string): string {
  return value.replaceAll("_", " ").replaceAll("-", " ");
}
