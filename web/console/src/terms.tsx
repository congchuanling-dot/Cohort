// terms.tsx 提供轻量的术语解释能力：一个纯 CSS tooltip 组件 + 集中维护的术语表。
// 目标是消除控制台里“一堆缩写没人解释”的问题，不引入任何第三方 tooltip 依赖。

// GLOSSARY 集中维护术语的中文解释。键为稳定标识，供 <Term id> 引用。
export const GLOSSARY: Record<string, string> = {
  exact_proof: "精确回放证明：离线重放这次运行的每一步请求/响应/工具，逐帧比对哈希，验证记录未被篡改、可如实复现。",
  replayability: "可回放级别：exact_only 只能原样精确回放；forkable 还能在某个 Turn 分叉、改变量重跑。",
  forkable: "可分叉：记录完整且工作区可复原，允许从某个 Turn 改模型或 Prompt 重新执行。",
  exact_only: "仅精确回放：只能原样重放校验，不能分叉重跑（通常因工作区快照缺失或不可复原）。",
  fork: "分叉：从历史运行的某个 Turn 出发，改变模型或 Prompt 等变量后重新执行，用于反事实对比。",
  trial: "试验次数：同一分叉设置重复运行的次数，用多次结果评估改动是否稳定，而非只看一次。",
  divergence: "偏离点：回放结果与原始记录首次不一致的位置，用于定位是哪一步开始变了。",
  proof_hash: "证明哈希：整次运行记录的聚合指纹，两次一致即可判定内容逐字节相同、未被改动。",
  frames: "帧：运行被拆成的最小事件单元，每帧是一次请求、一次响应或一次工具调用。",
  turn: "轮次：Agent 的一次“想一步、做一步”循环，通常含一次模型调用与其触发的工具执行。",
  capability_gap: "能力缺口：运行中发现模型缺少某种工具或能力而无法完成任务，被记录下来待补齐。",
  governance: "治理：对能力演化、动作执行施加的证据约束与风险门禁，确保变更经过验证与审批。",
  receipt_ledger: "回执账本：逐次记录模型调用的 Token 用量与来源，用于成本核算与用量审计。",
  causal_graph: "因果图：把一次运行的请求、响应、工具调用按因果关系连成的可视化轨迹。",
  context_capacity: "上下文容量：本次运行占用的上下文预算与峰值，反映是否逼近模型窗口上限。",
  hermes: "Hermes：Cohort 的自主运维子系统，负责后台动作队列与自动修复任务。",
  worktree: "隔离工作树：为分叉实验单独 checkout 的 Git 工作目录，避免污染当前工作区。",
  schema_bloat: "Schema 膨胀：单轮塞给模型的工具定义过多、载荷过大，会拖慢响应并浪费 Token。",
};

// Term 在一段文字旁渲染一个信息角标，悬浮显示解释。
// 传 id 用术语表里的解释，或直接传 tip 自定义。
export function Term({ children, id, tip }: { children: React.ReactNode; id?: string; tip?: string }) {
  const text = tip ?? (id ? GLOSSARY[id] : "");
  if (!text) return <>{children}</>;
  return <span className="term">
    {children}
    <span className="term-info" tabIndex={0} role="note" aria-label={text}>
      ?
      <span className="term-tip" role="tooltip">{text}</span>
    </span>
  </span>;
}
