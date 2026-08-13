import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { apiGet, EntityDescriptor, EntityKind } from "./api";

export interface EntityPageProps {
  kind: EntityKind;
  title: string;
  description: string;
  basePath: string;
  onAction: (actionID: string, input: Record<string, unknown>) => void;
}

export function entityInput(kind: EntityKind, id: string): Record<string, unknown> {
  const fields: Partial<Record<EntityKind, string>> = {
    delivery: "delivery_id",
    session: "session_id",
    run: "run_id",
    replay_bundle: "run_id",
    eval_run: "run_id",
    hermes_action: "action_id",
    skill: "skill_id",
    capability: "capability_id",
    capability_gap: "gap_id",
    capability_proposal: "proposal_id",
    dependency_plan: "plan_id",
    reflection_job: "job_id",
    mcp_server: "name",
    mcp_tool: "tool",
    model_profile: "profile_id",
  };
  const field = fields[kind];
  return field ? { [field]: id } : {};
}

export function EntityListPage({ kind, title, description, basePath, onAction }: EntityPageProps) {
  const entities = useQuery({
    queryKey: ["entities", kind, "page"],
    queryFn: () => apiGet<{ entities: EntityDescriptor[] }>(`/api/v1/entities/${kind}?limit=500`),
  });
  return <section className="page-stack">
    <header className="page-heading"><div><p className="eyebrow">PROJECT ENTITIES</p><h2>{title}</h2><p>{description}</p></div><span className="risk read">{entities.data?.entities.length ?? 0}</span></header>
    {entities.isError && <PageError title="本地数据读取失败" detail={entities.error.message} />}
    <div className="entity-list-page">
      {(entities.data?.entities ?? []).map((entity) => <article key={`${entity.kind}:${entity.id}`}>
        <Link to={`${basePath}/${encodeURIComponent(entity.id)}`}>
          <div><strong>{entity.title}</strong><small>{entity.subtitle || entity.id}</small></div>
          <span className={`entity-status ${entity.status}`}>{entity.status || entity.kind}</span>
          <time>{entity.updated_at ? new Date(entity.updated_at).toLocaleString() : "本地配置"}</time>
        </Link>
        <div className="entity-actions">
          {(entity.actions ?? []).filter((action) => action.enabled).slice(0, 3).map((action) => <button key={action.action_id} type="button" onClick={() => onAction(action.action_id, entityInput(entity.kind, entity.id))}>{action.label}</button>)}
        </div>
      </article>)}
      {entities.isPending && <div className="empty">正在加载本地对象…</div>}
      {entities.isSuccess && entities.data.entities.length === 0 && <div className="empty">该项目尚无相关数据。</div>}
    </div>
  </section>;
}

export function EntityDetailPage({ kind, title, basePath, onAction }: Omit<EntityPageProps, "description">) {
  const { id = "" } = useParams();
  const entity = useQuery({
    queryKey: ["entity", kind, id],
    queryFn: () => apiGet<{ entity: EntityDescriptor }>(`/api/v1/entities/${kind}/${encodeURIComponent(id)}`),
    enabled: id !== "",
  });
  const detailURL = entityResourceURL(kind, id);
  const detail = useQuery({
    queryKey: ["entity-detail", kind, id],
    queryFn: () => apiGet<Record<string, unknown>>(detailURL),
    enabled: detailURL !== "",
  });
  if (entity.isPending) return <div className="empty">正在读取 {title}…</div>;
  if (entity.isError) return <PageError title={`${title} 不可用`} detail={entity.error.message} />;
  const item = entity.data.entity;
  return <section className="page-stack">
    <Link className="back-link" to={basePath}>← 返回{title}列表</Link>
    <article className="entity-detail-card">
      <header><div><p className="eyebrow">{item.kind}</p><h2>{item.title}</h2><p>{item.subtitle}</p></div><span className={`entity-status ${item.status}`}>{item.status}</span></header>
      <dl className="detail-list">
        <div><dt>最近更新</dt><dd>{item.updated_at ? new Date(item.updated_at).toLocaleString() : "本地配置"}</dd></div>
        <div><dt>版本</dt><dd><code>{item.version}</code></dd></div>
        <div><dt>内部 ID</dt><dd><code>{item.id}</code></dd></div>
      </dl>
      <section className="context-actions">
        <h3>可执行动作</h3>
        {(item.actions ?? []).map((action) => <button key={action.action_id} type="button" disabled={!action.enabled} title={action.disabled_reason} onClick={() => onAction(action.action_id, entityInput(item.kind, item.id))}>
          <span>{action.label}</span><small>{action.enabled ? action.risk : action.disabled_reason}</small>
        </button>)}
      </section>
      {detail.data && <details className="technical-detail"><summary>领域详情与证据</summary><pre>{JSON.stringify(detail.data, null, 2)}</pre></details>}
    </article>
  </section>;
}

function entityResourceURL(kind: EntityKind, id: string): string {
  const resources: Partial<Record<EntityKind, string>> = {
    delivery: "deliveries",
    session: "sessions",
    skill: "skills",
  };
  const resource = resources[kind];
  return resource ? `/api/v1/resources/${resource}?id=${encodeURIComponent(id)}` : "";
}

function PageError({ title, detail }: { title: string; detail: string }) {
  return <div className="page-error"><strong>{title}</strong><p>{detail}</p><button type="button" onClick={() => window.location.reload()}>重新加载</button></div>;
}
