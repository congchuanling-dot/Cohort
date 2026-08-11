import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ActionSpec, apiGet, initializeSession, Operation, operationEvents, SessionInfo } from "./api";

function StatusDot({ online }: { online: boolean }) {
  return <span className={online ? "status-dot online" : "status-dot"} aria-hidden="true" />;
}

export default function App() {
  const queryClient = useQueryClient();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: initializeSession,
    retry: false,
  });
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

  useEffect(() => {
    if (!session.isSuccess) return;
    return operationEvents(() => {
      void queryClient.invalidateQueries({ queryKey: ["operations"] });
    });
  }, [queryClient, session.isSuccess]);

  if (session.isPending) {
    return <CenteredState title="正在建立安全会话" detail="连接本地 Cohort Control Plane…" />;
  }
  if (session.isError) {
    return <CenteredState title="无法进入控制台" detail={session.error.message} failed />;
  }

  const sessionInfo = session.data as SessionInfo;
  const running = operations.data?.operations.filter((operation) =>
    operation.status === "pending" || operation.status === "running",
  ).length ?? 0;

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">C</div>
          <div><strong>Cohort</strong><span>Control Center</span></div>
        </div>
        <nav aria-label="主导航">
          <a className="active" href="#overview">概览</a>
          <a href="#sessions">Agent Sessions</a>
          <a href="#deliveries">Deliveries</a>
          <a href="#operations">Operations</a>
          <a href="#quality">质量与追踪</a>
          <a href="#capabilities">能力中心</a>
          <a href="#settings">设置</a>
        </nav>
        <div className="sidebar-foot">
          <StatusDot online />
          <span>本地控制面已连接</span>
        </div>
      </aside>
      <main>
        <header>
          <div>
            <p className="eyebrow">LOCAL-FIRST AGENT RUNTIME</p>
            <h1>控制中心</h1>
          </div>
          <button className="command-button" type="button"><kbd>⌘</kbd><kbd>K</kbd> 搜索动作</button>
        </header>

        <section className="hero">
          <div>
            <span className="safe-pill"><StatusDot online /> 仅本机可访问</span>
            <h2>从一个工作面管理 Agent 的执行、证据与审批。</h2>
            <p>{sessionInfo.project_root}</p>
          </div>
          <div className="hero-actions">
            <button className="primary" type="button">发起 Agent 任务</button>
            <button type="button">查看运行记录</button>
          </div>
        </section>

        <section className="metrics" aria-label="运行指标">
          <Metric label="可用动作" value={catalog.data?.actions.length ?? 0} hint="统一 Action Catalog" />
          <Metric label="运行中" value={running} hint="实时 Operation" accent={running > 0} />
          <Metric label="最近操作" value={operations.data?.operations.length ?? 0} hint="持久审计记录" />
          <Metric label="安全边界" value="Loopback" hint="Session + CSRF" />
        </section>

        <section className="panel">
          <div className="panel-heading">
            <div><p className="eyebrow">FOUNDATION READY</p><h3>控制面连接正常</h3></div>
            <span className="safe-pill"><StatusDot online /> SSE 已就绪</span>
          </div>
          <div className="foundation-grid">
            <Foundation title="Action Catalog" detail="强类型参数、风险分级、搜索元数据" />
            <Foundation title="Operation Manager" detail="持久化、取消、失败和重启恢复" />
            <Foundation title="Security Boundary" detail="一次性启动令牌、HttpOnly Cookie、CSRF" />
          </div>
        </section>
      </main>
    </div>
  );
}

function Metric({ label, value, hint, accent = false }: { label: string; value: string | number; hint: string; accent?: boolean }) {
  return <article className="metric"><span>{label}</span><strong className={accent ? "accent" : ""}>{value}</strong><small>{hint}</small></article>;
}

function Foundation({ title, detail }: { title: string; detail: string }) {
  return <article><span className="check">✓</span><div><strong>{title}</strong><p>{detail}</p></div></article>;
}

function CenteredState({ title, detail, failed = false }: { title: string; detail: string; failed?: boolean }) {
  return <div className="centered-state"><div className={failed ? "loader failed" : "loader"} /><h1>{title}</h1><p>{detail}</p></div>;
}
