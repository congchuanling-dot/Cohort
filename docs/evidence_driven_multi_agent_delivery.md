# Cohort 证据驱动的多 Agent 交付引擎

> 文档状态：`[部分完成]`
>
> 调研与设计基线：2026-08-12
>
> 工作名称：Evidence-Driven Multi-Agent Delivery
>
> 推荐 CLI：`cohort deliver`
>
> 当前进展：Delivery 状态机、原子 Store、Acceptance Contract / Task DAG 本地校验、
> `deliver plan/list/status/show/cancel`，以及隔离子进程 Builder、Git worktree、DAG
> Scheduler、lease/heartbeat、内容寻址 Artifact Board、Evidence 新鲜度、Integration
> Worktree 和确定性 Gate 已落地；Verifier Council、返修和事务合并仍按本文阶段继续实现。

## 1. 执行摘要

Cohort 下一项值得投入的大功能，不应该只是增加一个 `spawn_agent` 工具，而应该实现一个
**可恢复、可审计、可验证、人工批准后才合并的多 Agent 软件交付控制平面**：

```text
用户需求
  -> 仓库调研
  -> 验收契约 Acceptance Contract
  -> 可执行任务 DAG
  -> 隔离 Worktree 并行实现
  -> 候选方案选择
  -> 独立 Verifier Council
  -> 失败定向返修
  -> Integration Worktree 事务集成
  -> Test / Eval Gate
  -> 人工 Review
  -> 主分支合并
  -> 合并后再次验证
  -> SessionEnd Reflection
```

它要解决的核心问题不是“怎么同时运行多个模型”，而是：

> 多个 Agent 产出的代码，如何证明它们共同满足需求，而且证据没有因后续修改失效。

该系统最重要的技术特征是 **Proof-Carrying State Transition**：

- `done` 不是 Agent 的自然语言声明，而是一个受状态机约束的结论。
- 每份测试、静态检查、评审结果都绑定到具体 `base_commit`、`candidate_commit`、Git tree
  和环境指纹。
- 候选代码发生变化后，旧证据自动失效，禁止“测试后继续改代码，仍拿旧绿灯宣布完成”。
- LLM Verifier 提供语义判断和修复建议，但不能单独覆盖失败的确定性 Gate。
- 主工作区在人工批准前不发生修改；批准后的合并仍要执行合并态复验。

这会把 Cohort 从“功能完整的单 Agent Runtime”提升为“本地优先的 Agent Delivery
Control Plane”。

## 2. 为什么是这个方向

### 2.1 市场已经进入多 Agent 交付阶段

主流 Coding Agent 的竞争重点已经从单轮代码生成转向并行执行、隔离环境、后台任务和验证闭环。

| 产品 | 代表能力 | 对 Cohort 的启示 |
| --- | --- | --- |
| Claude Code | 独立上下文 Subagent、角色化 prompt、工具权限、前后台执行 | Subagent 必须有上下文和权限隔离，不能共享主 Agent 的全部历史 |
| OpenAI Codex | 多 Agent command center、内置 worktree、云环境、后台任务 | 并行 Agent 必须默认隔离工作区，并有集中 Review 控制面 |
| Cursor | 并行 Subagent、Cloud Agent、Verifier/Orchestrator pattern、可恢复 Agent ID | 只读调查、实现、验证应是不同角色，结果需要结构化交接 |
| Devin | Managed Devins、独立 VM、Coordinator、预算限制、消息和终止控制 | 编排器必须掌握预算、超时、取消和子任务生命周期 |
| OpenHands | 持久事件、Workspace 抽象、Goal Judge、Critic、迭代修正 | “Agent 认为完成”不能终止任务，必须有独立目标判定和证据 |
| GitHub Copilot | 后台分支执行、计划、PR、Custom Agent、结果指标 | 交付物应进入标准分支/PR/Review 流，而不是只返回聊天文本 |

共同趋势是：

```text
Single Agent
  -> Specialized Agents
  -> Isolated Parallel Work
  -> Independent Review
  -> Human-Controlled Delivery
```

但市场上的很多实现仍停留在“并行会话 + 最后汇总文本”。Cohort 可以用已有的
Evidence、Eval、Hermes Gate 和 Trace 基础，把重点放在 **证据绑定和事务交付** 上。

### 2.2 研究结果指向独立验证

2026 年的 Agentic Rubrics 研究表明：由独立 Agent 先理解仓库，再生成项目上下文相关的
验收 rubric，可以在候选 Patch 选择中提供比普通 Judge 更有效、可解释的信号，并发现测试
没有覆盖的规格和边界问题。

SWE-Review 的实验进一步表明，`generate -> review -> revise` 闭环优于只生成一次或固定上下文
单轮 Review。关键不在于多调用一次模型，而在于 Reviewer 能主动探索仓库并输出可执行的
修改反馈。

因此 Cohort 的目标不应是“让 Builder 自己再看一遍”，而应该是：

- Verifier 使用独立 Session，不继承 Builder 的推理过程。
- Verifier 只接收需求契约、最终 Diff、必要仓库上下文和新鲜证据。
- 不同 Verifier 关注不同缺陷类型，避免所有 Reviewer 复制同一种确认偏误。
- Review Finding 必须能追踪到契约条目、文件位置和修复轮次。

## 3. Cohort 当前能力与真实缺口

### 3.1 已经具备的底座

Cohort 不是从零开始。当前已有：

| 能力 | 当前实现 | 可复用部分 |
| --- | --- | --- |
| 单 Agent Runtime | `internal/agent` | Builder、Planner、Verifier 的统一执行内核 |
| 只读并行调查 | `internal/explorer` | 只读 Tool Runner、隔离子进程、batch lane |
| 隔离修复 | `internal/hermes` | Git worktree、Diff 限制、Gate、人工审批、事务合并 |
| 评测 | `internal/evaluation` | Test/Eval Gate、历史基线、稳定性、Judge |
| 计划 | `internal/plan` | 可恢复步骤状态和 evidence 约束 |
| 观测 | `internal/observability`、`internal/traceview` | 生命周期事件、因果图、性能和异常定位 |
| 自适应工具路由 | `internal/agent/tool_router.go` | 按角色控制工具可见面 |
| 反思 | `internal/evolution` | SessionEnd 队列、跨会话总结、SOP/Skill candidate |
| 常驻调度 | `internal/hermes/service.go` | daemon、持久任务、锁、恢复和通知 |

### 3.2 关键缺口

当前 Cohort 仍缺：

1. 通用的可写 Subagent Runner。
2. 基于依赖和文件所有权的任务 DAG。
3. 每个 Builder 独立 worktree 和生命周期管理。
4. Typed Artifact Blackboard，而不是只汇总自然语言回答。
5. 多候选生成、评分和选择。
6. 独立 Verifier Council。
7. 从 Review Finding 自动生成定向返修任务。
8. 多分支在 Integration Worktree 中的有序合并。
9. 绑定 Git tree 的证据新鲜度校验。
10. 整个交付任务的预算、取消、租约和崩溃恢复。

Hermes 已经实现“一个 Action 对应一个 Repair”的安全闭环，但还不能表达：

```text
一个需求
  -> 多个有依赖的实现节点
  -> 多个隔离分支
  -> 多个独立验证角色
  -> 统一集成和返修
```

### 3.3 不选择其他方向的原因

| 方向 | 不作为下一项大功能的原因 |
| --- | --- |
| Web UI / 全屏 TUI | 展示价值高，但不会显著提高 Agent 的任务完成能力 |
| Docker/Kubernetes Sandbox | 工程量大，Cohort 当前 worktree 隔离足以支撑本地交付第一版 |
| 代码知识图谱 / RAG | 能改善检索，但不能形成需求到验证的完整交付闭环 |
| 单纯增加 Subagent Tool | 很快能演示，但没有冲突治理、验证、恢复和合并语义 |
| 自动 PR Review | 范围偏窄，而且 Hermes/Eval 已有部分验证基础 |
| 无限 Best-of-N | 成本不可控，且没有可靠 Verifier 时只是扩大候选数量 |

## 4. 产品定义

### 4.1 一句话

`cohort deliver` 把一个软件需求编译成验收契约和任务 DAG，在隔离 worktree 中并行运行
专业 Agent，通过确定性 Gate 与独立 Verifier 反复修正，最后经人工批准事务性合并。

### 4.2 用户入口

主命令保持精简：

```bash
cohort deliver plan "为服务增加 API 限流、指标和降级策略"
cohort deliver run <delivery_id>
cohort deliver review <delivery_id> --open
cohort deliver accept <delivery_id>
```

高级命令：

```bash
cohort deliver status [delivery_id] [--watch]
cohort deliver show <delivery_id> [--json]
cohort deliver retry <delivery_id> [--node <node_id>]
cohort deliver cancel <delivery_id>
cohort deliver inspect <delivery_id> --open
```

命令语义：

- `plan`：只读调研并生成契约/DAG，不修改代码。
- `run`：视为用户批准该计划，只在隔离 worktree 中执行。
- `review`：展示契约覆盖、Diff、测试、Verifier Finding、成本和风险。
- `accept`：人工批准后才允许进入主工作区合并，并执行合并后 Gate。

### 4.3 成功定义

一个 Delivery 只有同时满足以下条件才能标记为 `verified`：

1. 所有 Mandatory Acceptance Criteria 均有新鲜证据。
2. 所有确定性 Gate 通过。
3. 不存在未解决的 Critical/High Finding。
4. 所有 Builder Diff 已在 Integration Worktree 成功合并。
5. Integration tree 通过完整验证。
6. 用户显式批准。
7. 主工作区合并成功。
8. Merge commit 上再次执行最小必要验证并通过。

## 5. 总体架构

```text
                    +----------------------+
User Requirement ->| Contract Compiler    |
                    | repo-grounded rubric |
                    +----------+-----------+
                               |
                               v
                    +----------------------+
                    | DAG Planner          |
                    | deps / write sets    |
                    | risk / budgets       |
                    +----------+-----------+
                               |
                +--------------+--------------+
                |                             |
                v                             v
       +-----------------+           +-----------------+
       | Builder Lane A  |    ...    | Builder Lane N  |
       | isolated WT     |           | isolated WT     |
       +--------+--------+           +--------+--------+
                |                             |
                +--------------+--------------+
                               v
                    +----------------------+
                    | Candidate Selector   |
                    | proof + rubric score |
                    +----------+-----------+
                               |
                               v
                    +----------------------+
                    | Integration Worktree |
                    | topo merge / conflict|
                    +----------+-----------+
                               |
                               v
              +--------------------------------------+
              | Verifier Council                     |
              | tests + spec + correctness + security|
              +------------------+-------------------+
                                 |
                    fail --------+-------- pass
                      |                     |
                      v                     v
              Targeted Revision      Human Review
                      |                     |
                      +--> bounded loop     v
                                      Transactional Merge
                                             |
                                             v
                                      Post-Merge Verify
                                             |
                                             v
                                         Reflection
```

### 5.1 核心组件

| 组件 | 职责 |
| --- | --- |
| `DeliveryStore` | 持久化 Delivery、Node、Lease、Artifact、Event |
| `ContractCompiler` | 将自然语言需求转换为仓库相关的验收契约 |
| `GraphPlanner` | 生成可执行 DAG、依赖、write set、风险和预算 |
| `DeliveryScheduler` | claim ready node、控制并发、预算、取消和重试 |
| `AgentWorker` | 在独立进程和 worktree 中运行角色化 Runner |
| `ArtifactBoard` | 发布和读取结构化、不可变、带 hash 的交接产物 |
| `CandidateSelector` | 在高风险节点的多个候选中选择可验证结果 |
| `IntegrationManager` | 在独立 integration worktree 中拓扑合并 |
| `VerifierCouncil` | 执行确定性 Gate 和独立语义验证 |
| `RevisionPlanner` | 把 Finding 转成最小返修节点 |
| `EvidenceVerifier` | 校验证据来源、新鲜度、hash 和适用 tree |
| `DeliveryReporter` | CLI、JSON、HTML 和因果 DAG |
| `RecoveryManager` | lease 回收、进程崩溃恢复和幂等续跑 |

## 6. 持久化模型

### 6.1 目录

```text
.cohort/deliveries/<delivery_id>/
  delivery.json
  contract.json
  graph.json
  events.jsonl
  budget.json
  approval.json
  nodes/
    <node_id>/
      task.json
      result.json
      evidence.json
      findings.json
      attempts/
  artifacts/
    <sha256>/
      meta.json
      payload
  reports/
    summary.md
    delivery.html
    causal-graph.json

.cohort/delivery-worktrees/<delivery_id>/
  builders/<node_id>/<candidate_id>/
  integration/
  verifier/
```

所有状态文件使用：

- 临时文件写入。
- `fsync`。
- 原子 `rename`。
- schema version。
- append-only `events.jsonl`。

### 6.2 Delivery

```go
type Delivery struct {
    ID              string
    Status          DeliveryStatus
    Requirement     string
    ProjectRoot     string
    BaseCommit      string
    ContractHash    string
    GraphHash       string
    IntegrationTree string
    MergeCommit     string
    Budget          Budget
    CreatedAt       time.Time
    UpdatedAt       time.Time
    ApprovedAt      time.Time
    VerifiedAt      time.Time
}
```

状态：

```text
draft
planned
running
integrating
verifying
needs_revision
needs_human_decision
ready_for_review
approved
merging
merged_unverified
verified
budget_exhausted
failed
cancelled
```

只允许通过显式状态迁移表变更状态，禁止任意字符串覆盖。

### 6.3 Acceptance Contract

```go
type AcceptanceContract struct {
    RequirementHash string
    BaseCommit      string
    Criteria        []Criterion
    Invariants      []Invariant
    AllowedScope    []string
    ForbiddenScope  []string
    RiskProfile     RiskProfile
    RequiredGates   []GateSpec
}

type Criterion struct {
    ID             string
    Statement      string
    Mandatory      bool
    Verification   VerificationKind
    TargetPaths    []string
    EvidencePolicy EvidencePolicy
}
```

`VerificationKind`：

- `command`
- `file_assertion`
- `api_contract`
- `behavioral_eval`
- `rubric`
- `human`

契约必须包含：

- 用户可见行为。
- 兼容性和不变量。
- 允许/禁止修改范围。
- 测试或可验证命令。
- 无法自动验证、必须人工判断的条目。

Planner 不能用模糊的“代码质量良好”作为 Mandatory Criterion。

### 6.4 Task Node

```go
type TaskNode struct {
    ID             string
    Role           AgentRole
    Status         NodeStatus
    Dependencies   []string
    ReadSet        []string
    DeclaredWrites []string
    ActualWrites   []string
    Criteria       []string
    Risk           RiskLevel
    CandidateCount int
    Budget         NodeBudget
    Lease          Lease
}
```

角色：

- `scout`
- `planner`
- `builder`
- `test_builder`
- `integrator`
- `spec_verifier`
- `correctness_verifier`
- `security_verifier`
- `performance_verifier`
- `compatibility_verifier`
- `revision_builder`

第一版禁止 Worker 再创建 Worker，最大深度固定为 1。

## 7. Contract Compiler

`deliver plan` 先并行启动 2-4 个只读 Scout：

- 架构和依赖调查。
- 测试与构建命令调查。
- 变更影响面调查。
- 风险/安全调查。

Scout 复用 `internal/explorer` 的隔离子进程和 `ReadOnlyToolRunner`，但输出必须符合 JSON Schema，
不能只返回 Markdown。

Contract Compiler 输入：

- 用户原始需求。
- Scout 结构化结果。
- Project Mode 约束。
- 相关 SOP/Skill。
- 当前 `base_commit`。

输出必须经过确定性检查：

1. Criterion ID 唯一。
2. 每个 Mandatory Criterion 有 VerificationKind。
3. Command Gate 不允许包含 shell 拼接和未批准网络操作。
4. Scope 必须使用仓库相对路径。
5. 不能把“Agent 说已完成”作为 EvidencePolicy。
6. 契约 hash 与 base commit 一起持久化。

如果需求存在无法推断的产品语义，`plan` 必须返回阻塞项，不能让 Builder 自行拍板。

## 8. DAG Planner 与冲突预防

### 8.1 DAG 规则

- 每个节点必须关联至少一个 Criterion 或基础设施目标。
- Builder 节点必须声明 `DeclaredWrites`。
- write set 重叠的节点默认串行。
- 只有确定不共享可变状态的节点才能并行。
- 数据库 schema、公共接口、共享配置属于高冲突资源，必须建立显式依赖。
- 纯测试节点可以依赖实现节点，但不能反向成为实现的隐式前置。

### 8.2 Write Set 校验

运行前：

```text
DeclaredWrites(A) intersects DeclaredWrites(B)
  -> 有依赖：允许
  -> 无依赖：Planner 必须重写 DAG 或请求人工确认
```

运行后：

```text
ActualWrites not subset of DeclaredWrites
  -> node = needs_review
  -> 禁止自动进入 integration
```

这不是安全沙箱的替代品，而是降低并行 Agent 合并冲突和越界改动。

### 8.3 Best-of-K

默认 `CandidateCount=1`。

只有以下情况允许 `K=2`：

- 风险为 high。
- 接口设计存在两个合理方案。
- 历史同类节点失败率高。
- 用户显式要求多方案竞争。

禁止默认大规模 Best-of-N。候选增加必须受总预算约束。

## 9. Worker 隔离与调度

### 9.1 每个 Builder 的边界

每个 Builder：

- 独立 OS 子进程。
- 独立 Git worktree 和 branch。
- 独立 Session、Log、Trace、Token Budget。
- 只读取经过裁剪的 Task Package。
- 只暴露角色允许的工具。
- 不继承其他 Builder 的完整对话。
- 不直接写主工作区。

Task Package：

```json
{
  "delivery_id": "delivery_xxx",
  "node_id": "api_rate_limit",
  "base_commit": "abc123",
  "objective": "...",
  "criteria": ["AC-1", "AC-3"],
  "dependencies": ["config_contract"],
  "declared_writes": ["internal/ratelimit/**"],
  "invariants": ["public API remains backward compatible"],
  "artifact_refs": ["sha256:..."],
  "budget": {"turns": 80, "tokens": 120000, "seconds": 1800}
}
```

### 9.2 Scheduler

Scheduler 只 claim 满足以下条件的节点：

- 所有依赖已通过。
- 没有被取消。
- 预算仍充足。
- 没有冲突 write set 正在运行。
- Worker lease 可获取。

默认并发：

```text
max_builders = 3
max_verifiers = 3
max_total_agents = 5
```

限制原因不是机器性能，而是验证和集成能力必须跟上生成速度。

### 9.3 Lease 与恢复

```go
type Lease struct {
    OwnerPID  int
    OwnerID   string
    ExpiresAt time.Time
    Heartbeat time.Time
    Attempt   int
}
```

- Worker 周期性 heartbeat。
- 进程不存在且 lease 过期时回收。
- 中断的 Builder worktree 保留用于审计。
- `retry` 从固定 base commit 重建 worktree，不在未知中间状态上继续。
- 所有状态迁移和 artifact publish 必须幂等。

## 10. Typed Artifact Blackboard

Worker 之间不开放自由聊天总线。默认协作方式是不可变 Artifact：

```go
type ArtifactEnvelope struct {
    ID           string
    Kind         string
    Producer     string
    DeliveryID   string
    NodeID       string
    BaseCommit   string
    TreeHash     string
    ContentHash  string
    Schema       string
    CreatedAt    time.Time
    Redaction    RedactionSummary
}
```

Artifact Kind：

- `research_report`
- `interface_contract`
- `patch`
- `test_report`
- `coverage_report`
- `benchmark_report`
- `review_findings`
- `revision_request`
- `decision_record`
- `integration_manifest`

优点：

- 防止所有 Agent 共享长上下文。
- 可校验来源和完整性。
- 可重放、可比较、可缓存。
- 可作为 Reflection 和 Eval 的事实来源。

Coordinator 可以给运行中的 Worker 发送控制消息，但消息必须持久化为：

- `clarification`
- `cancel`
- `budget_update`
- `revision_request`

禁止未记录的进程内私有指令。

## 11. 证据系统

### 11.1 Evidence Envelope

```go
type EvidenceEnvelope struct {
    ID              string
    CriterionID     string
    Producer        string
    ContractHash    string
    BaseCommit      string
    CandidateCommit string
    TreeHash        string
    CommandHash     string
    EnvironmentHash string
    ExitCode        int
    StartedAt       time.Time
    FinishedAt      time.Time
    ArtifactHash    string
    Status          EvidenceStatus
}
```

### 11.2 新鲜度规则

Evidence 可用必须满足：

```text
evidence.TreeHash == currentCandidate.TreeHash
AND evidence.CandidateCommit == currentCandidate.Commit
AND evidence.ContractHash == delivery.ContractHash
AND evidence.EnvironmentHash compatible with required gate
AND evidence.ArtifactHash verified
```

以下事件会使旧证据失效：

- Candidate tree 发生任何修改。
- Contract Mandatory Criterion 变化。
- Gate command 变化。
- 影响构建结果的环境依赖变化。
- 多分支集成生成新的 tree。

### 11.3 证据优先级

```text
确定性执行证据
  > 结构化静态分析
  > 仓库相关 Rubric Verifier
  > 通用 LLM Judge
  > Agent 自我声明
```

低优先级证据不能覆盖高优先级失败。

## 12. Verifier Council

### 12.1 动态角色选择

不是每个任务都启动所有 Verifier。

```text
所有任务：
  Deterministic Gate
  Spec Verifier
  Correctness Verifier

涉及鉴权、输入、网络、secret：
  + Security Verifier

涉及公共 API、schema、序列化：
  + Compatibility Verifier

涉及热路径、批处理、并发：
  + Performance Verifier
```

### 12.2 独立上下文

Verifier 只收到：

- 原始需求。
- Acceptance Contract。
- 最终候选 Diff。
- 仓库当前代码。
- 确定性 Gate 摘要和 Artifact Ref。

Verifier 不收到：

- Builder 的完整思考过程。
- Builder 对自己实现的辩护。
- 已有 Reviewer 的结论，避免锚定。

### 12.3 Finding

```go
type Finding struct {
    ID          string
    Verifier    string
    CriterionID string
    Severity    string
    Confidence  float64
    File        string
    Line        int
    Claim       string
    Evidence    []string
    FixHint     string
    Status      FindingStatus
}
```

Finding 必须满足：

- 有具体 Criterion 或明确的跨领域风险。
- 有文件/行为/命令证据。
- 不允许仅凭风格偏好阻塞交付。
- Critical/High 默认阻塞。
- 不同 Verifier 的重复 Finding 按 fingerprint 合并。
- 冲突 Finding 进入 `needs_human_decision`，不能用简单多数票掩盖。

### 12.4 判定规则

Delivery 进入 `ready_for_review` 的必要条件：

```text
all mandatory deterministic gates passed
AND no unresolved critical/high findings
AND all mandatory criteria have fresh evidence
AND integration tree is stable
```

Rubric 分数用于：

- 候选排序。
- 发现规格遗漏。
- 决定是否触发额外 Verifier。

Rubric 分数不能单独把失败测试判为通过。

## 13. 自动返修闭环

Verifier 失败后，`RevisionPlanner` 不把整份 Review 原样塞回 Builder，而是生成定向任务：

```json
{
  "parent_node": "api_rate_limit",
  "attempt": 2,
  "failed_criteria": ["AC-3"],
  "findings": ["finding_17"],
  "allowed_writes": ["internal/ratelimit/**", "internal/http/middleware.go"],
  "must_preserve": ["AC-1", "AC-2"],
  "required_gates": ["go test ./internal/ratelimit ./internal/http"]
}
```

返修规则：

- 默认最多 2 轮。
- 只允许修改 Finding 相关 write set。
- 已通过 Criterion 必须进入 `must_preserve`。
- 每轮重新生成全部受影响证据。
- 相同 fingerprint 连续出现两次时停止自动返修并转人工。
- Diff 持续增大但 rubric/gate 不改善时触发熔断。

## 14. 集成与事务合并

### 14.1 Integration Worktree

所有候选先合并到独立 Integration Worktree：

1. 从 Delivery `base_commit` 创建。
2. 按 DAG 拓扑顺序 cherry-pick/merge。
3. 每次合并校验实际 write set。
4. 冲突时创建 `integration_conflict` 节点。
5. 全部合并后计算新的 Integration tree hash。
6. 对 Integration tree 执行完整 Gate 和 Verifier。

Builder worktree 中的通过证据不能直接证明 Integration tree 通过，必须重跑受影响 Gate。

### 14.2 人工批准

`cohort deliver review` 必须展示：

- Requirement 和 Acceptance Contract。
- Criterion 覆盖矩阵。
- 节点 DAG 和状态。
- 每个 Builder 的 Diff。
- Integration Diff。
- Gate 与 Verifier 结果。
- 未解决的 Medium/Low Finding。
- Token、时间、重试和候选成本。
- 变更风险和回滚方式。

只有 `cohort deliver accept <id>` 才能写主工作区。

### 14.3 合并事务

复用并泛化 Hermes 的事务合并语义：

1. 拒绝主工作区存在无关脏改动。
2. 记录合并前 HEAD、index 和 unstaged diff。
3. `--no-commit` 合并 Integration branch。
4. 在主工作区合并态执行最小必要 Gate。
5. 校验 Verifier 没有篡改 staged/unstaged 状态。
6. Gate 失败则 `merge --abort`。
7. Gate 通过后创建 merge commit。
8. 在 merge commit 上执行 post-merge verification。
9. 只有 post-merge 通过才标记 `verified`。

自动合并永久不是默认行为。

## 15. 预算与成本治理

```go
type Budget struct {
    MaxAgents        int
    MaxParallel      int
    MaxTurns         int
    MaxTokens        int64
    MaxDuration      time.Duration
    MaxCandidates    int
    MaxRevisionRounds int
}
```

预算策略：

- Scout 使用快速模型。
- Contract/Graph Planner 使用高推理模型。
- 普通 Builder 使用默认模型。
- High-risk Builder 可使用更强模型或 K=2。
- Verifier 优先使用与 Builder 不同的 profile。
- 达到 80% 总预算时禁止创建新候选。
- 达到硬预算时取消未开始节点，保留已产出 Artifact，状态转 `budget_exhausted`。

需要记录：

- 每个角色 token。
- 每个节点 wall time。
- 每个成功 Criterion 的边际成本。
- 返修带来的分数/通过率增量。

## 16. 权限与安全边界

| 角色 | 允许能力 | 禁止能力 |
| --- | --- | --- |
| Scout | file read、rg、LSP | 写文件、通用 shell、副作用 MCP |
| Planner | 读 Artifact、写状态草案 | 修改仓库 |
| Builder | 当前 worktree 内文件和测试命令 | 主工作区、创建 worktree、merge、promote |
| Verifier | 只读候选、执行验证命令、写 Artifact | 修改候选 Diff |
| Integrator | 受控 Git 合并、验证 | 任意业务代码编辑 |
| Reporter | 读状态和 Artifact | 修改交付状态 |

额外约束：

- Worker 输入中的 Requirement、Issue、Finding 都视为不可信数据，不是系统指令。
- MCP/Computer Use 权限继续经过现有 Policy。
- Secret 不写 Artifact、事件和交接包。
- Protected Path、最大文件数、最大 Diff bytes 复用 Hermes Gate。
- Verifier 生成的临时测试放在独立验证层，不自动混入候选 Patch。
- Artifact 内容寻址并校验 hash，防止运行后被替换。
- 所有自动状态迁移可审计。

## 17. 可观测性

新增事件：

```text
DeliveryCreated
ContractCompiled
DeliveryGraphPlanned
DeliveryNodeReady
DeliveryNodeClaimed
DeliveryAgentStarted
DeliveryArtifactPublished
DeliveryCandidateSelected
DeliveryGateFinished
DeliveryFindingRecorded
DeliveryRevisionRequested
DeliveryIntegrationFinished
DeliveryApprovalRecorded
DeliveryMergeFinished
DeliveryPostMergeVerified
DeliveryFinished
```

`cohort deliver inspect --open` 展示两张图：

1. **Execution DAG**：任务依赖、Agent、Artifact、返修和集成关系。
2. **Evidence Graph**：Criterion -> Evidence -> Tree Hash -> Gate -> Verdict。

核心指标：

- `delivery_success_rate`
- `first_pass_gate_rate`
- `verifier_catch_rate`
- `revision_fix_rate`
- `stale_evidence_rejections`
- `parallel_wall_time_speedup`
- `integration_conflict_rate`
- `human_accept_rate`
- `post_merge_failure_rate`
- `tokens_per_verified_criterion`

## 18. 与现有代码的集成

### 18.1 新增模块

```text
internal/delivery/
  types.go
  store.go
  state_machine.go
  contract.go
  graph.go
  planner.go
  scheduler.go
  worker.go
  artifacts.go
  evidence.go
  selector.go
  verifier.go
  revision.go
  integration.go
  recovery.go
  report.go

internal/cli/
  delivery.go
```

### 18.2 应抽取的公共能力

不要让 `delivery` 直接依赖整个 `hermes` 包。应先抽取：

```text
internal/worktree/
  create.go
  inspect.go
  commit.go
  integrate.go
  transaction.go
```

Hermes Repair 和 Delivery 共同使用。

同样，应把 Explorer 的子进程执行和 read-only policy 抽成可复用 Agent Role Runtime：

```text
internal/agentrole/
  profile.go
  child_process.go
  policy.go
  task_package.go
```

### 18.3 Runner 模式

新增显式 RunMode：

```go
RunModeDeliveryScout
RunModeDeliveryPlanner
RunModeDeliveryBuilder
RunModeDeliveryVerifier
```

每种 RunMode：

- 固定工具策略。
- 固定记忆写入边界。
- 固定 capability evolution 边界。
- 独立 observability source。
- 禁止自适应路由改变安全 allowlist；Router 只能在 allowlist 内裁剪。

### 18.4 可复用组件

| 现有组件 | Delivery 用法 |
| --- | --- |
| `explorer.NewReadOnlyToolRunner` | Scout 和 Verifier 仓库调查 |
| `agent.Runner` | 所有 LLM 角色执行 |
| `hermes` worktree/merge 逻辑 | 抽取为通用隔离和事务合并 |
| `evaluation.Engine` | Integration 和 post-merge Gate |
| `traceview` | Delivery DAG HTML 基础 |
| `evolution.ReflectionQueue` | 交付完成后的经验挖掘 |
| `plan` | 状态和 evidence 约束参考 |
| `ToolRouteSelected` | 角色内工具 schema 裁剪 |

## 19. 实施阶段

### Phase 0：状态、契约和只读计划

交付：

- `internal/delivery` 类型、状态机、Store、event log。
- `deliver plan/show/status/cancel`。
- Scout batch。
- Acceptance Contract 和 DAG schema。
- 确定性 schema/scope/write-set 校验。

验收：

- 不调用 Builder、不修改代码即可生成完整计划。
- 崩溃后状态可恢复。
- 同一 base commit 和需求可稳定重放。

### Phase 1：隔离并行 Builder

交付：

- Agent Role Runtime。
- Worktree 公共包。
- Scheduler、Lease、Heartbeat、Budget。
- Builder task package。
- Typed Artifact Board。
- 并行节点和 declared/actual write set 校验。

验收：

- 至少 3 个无冲突 Builder 可并行。
- 任一 Worker 崩溃不污染其他 worktree。
- 主工作区零修改。

### Phase 2：Evidence 与 Integration

交付：

- Evidence Envelope。
- Tree/commit/environment freshness 校验。
- Integration Worktree。
- 拓扑合并、冲突节点、受影响 Gate 重跑。
- `deliver review`。

验收：

- 修改候选后旧证据自动失效。
- Builder 单独通过但集成失败时不能进入 Review Ready。
- 冲突不会直接落到主工作区。

### Phase 3：Verifier Council 与返修

交付：

- Spec/Correctness/Security/Compatibility/Performance Verifier。
- Finding schema、dedupe 和 severity gate。
- Revision Planner。
- 最多两轮定向返修。
- 可选 K=2 Candidate Selector。

验收：

- Verifier 不读取 Builder 推理历史。
- Critical/High Finding 阻塞。
- 修复后必须刷新受影响 Evidence。
- 重复失败触发熔断而不是无限循环。

### Phase 4：事务合并、可视化和反思

交付：

- `deliver accept`。
- 主工作区事务合并和 post-merge verify。
- Delivery/Evidence Graph HTML。
- Hermes daemon 恢复和通知。
- SessionEnd Reflection。
- Delivery Eval suite。

验收：

- 未批准永不修改主工作区。
- 合并 Gate 失败自动 abort。
- post-merge 失败进入明确状态，不能伪装 verified。
- 可从 event log 重建完整交付过程。

## 20. 测试与评测

### 20.1 确定性测试

- 状态机非法迁移。
- Store 原子写和并发 claim。
- lease 过期恢复。
- DAG 环检测。
- write set 冲突。
- Artifact hash 篡改。
- stale evidence。
- Worker timeout/cancel。
- Integration conflict。
- merge abort。
- Verifier 试图修改候选。
- 主工作区脏状态。

### 20.2 故障注入

- Worker 写到一半进程退出。
- Scheduler 重启。
- worktree 被手工删除。
- Evidence 文件被篡改。
- Builder 产出超大 Diff。
- Verifier 返回无法解析的 JSON。
- Integration Gate 运行时超时。
- merge commit 后 post-merge Gate 失败。

### 20.3 对比评测

建立 `multi-agent-delivery` Eval Suite，至少包含：

1. 两个完全独立模块的并行功能。
2. 共享接口先行、两个实现节点依赖它。
3. 两个 Builder 声明 write set 不重叠，但实际越界。
4. 单节点测试通过、集成后失败。
5. 测试通过但违反 API 兼容性的 Patch。
6. 带安全缺陷的输入处理。
7. 需要返修一轮才能通过的任务。
8. 无法自动验证、必须人工判断的 Criterion。

对比：

```text
Baseline: 单 Agent Runner
Variant A: 多 Builder，无独立验证
Variant B: 完整 Delivery Engine
```

衡量：

- 完成率。
- 隐藏 Gate 通过率。
- Wall time。
- Token。
- Diff 大小。
- 回归数量。
- 人工接受率。

没有 A/B 结果前，不宣称“多 Agent 一定更快或更准”。

## 21. 风险与应对

| 风险 | 应对 |
| --- | --- |
| 多 Agent Token 成本爆炸 | 默认 3 Builder、K=1、按风险启用额外 Verifier |
| Planner 错误拆分导致冲突 | declared write set、DAG 校验、integration worktree |
| Reviewer 产生大量误报 | Criterion 绑定、证据要求、severity/confidence、去重 |
| Agent 自我确认偏误 | 独立 Session、不同 profile、不共享 Builder 推理 |
| 测试证据过期 | Evidence 强绑定 candidate tree |
| Worker 崩溃 | 持久状态、lease、heartbeat、幂等 retry |
| 自动修复无限循环 | 最大两轮、重复 fingerprint 熔断、预算硬限制 |
| 主分支被污染 | 人工批准、dirty check、no-commit merge、失败 abort |
| 过度设计 | 第一版只支持单仓库、深度 1、本地 worktree |

## 22. 明确不做

第一版不做：

- Agent 递归创建 Agent。
- Worker 自由聊天网络。
- 自动 merge 到主分支。
- 跨仓库分布式事务。
- Kubernetes/远程 VM 调度。
- 仅凭 LLM 分数放行失败测试。
- 默认 Best-of-N。
- 自动把 Delivery 经验 promote 为正式 Skill。

## 23. 面试表达

### 23.1 30 秒版本

> 我在一个 Go Agent Runtime 上实现了证据驱动的多 Agent 交付引擎。系统先把自然语言需求
> 编译成仓库相关的验收契约和任务 DAG，再让多个 Builder 在独立 Git worktree 中并行实现。
> 独立 Verifier Council 基于确定性测试和语义 rubric 检查结果，失败会生成定向返修任务。
> 每份验证证据都绑定具体 Git tree hash，代码变化后旧证据自动失效。最终只在人工批准后
> 事务合并，并在 merge commit 上复验。

### 23.2 深挖点

面试官可以继续追问：

- 如何防止并行 Agent 修改同一个文件？
- 如何保证测试结果没有过期？
- 为什么不用 Builder 自己 Review？
- Verifier 意见冲突怎么处理？
- Agent 崩溃后怎么恢复？
- Integration 通过后为什么还要 post-merge verify？
- 如何控制多 Agent Token 成本？
- 为什么不直接上 Docker/Kubernetes？

这些问题都有明确的数据模型、状态机和安全边界，不是“调用多个 LLM”式 Demo。

### 23.3 可量化结果

实现完成后必须拿真实数据说话：

```text
single-agent success rate       -> delivery success rate
single-agent wall time          -> parallel critical-path wall time
unverified completion claims    -> stale evidence rejection count
post-review defects             -> verifier catch / revision fix rate
manual integration conflicts    -> preflight / integration conflict rate
tokens per task                 -> tokens per verified criterion
```

## 24. 推荐决策

建议把该项目作为 Cohort 下一条主线，原因：

1. 它是当前能力图中最大的结构性缺口。
2. Explorer、Hermes、Eval、Trace、Reflection 都能被复用，不是另起炉灶。
3. 功能本身形成完整闭环，可做真实任务和 A/B，而不是界面 Demo。
4. “证据绑定 Git tree + 事务合并”具有明确工程差异化。
5. 面试可以同时覆盖 Agent、并发调度、状态机、Git、验证、安全、可观测性和故障恢复。

推荐实现顺序：

```text
Phase 0 Contract/DAG
  -> Phase 1 Isolated Builders
  -> Phase 2 Evidence/Integration
  -> Phase 3 Verifier/Revision
  -> Phase 4 Merge/Trace/Reflection
```

任何阶段都不能跳过验证，尤其不能先做一个共享工作区的并行 Agent Demo。

## 25. 调研来源

产品官方资料：

- [Claude Code Subagents](https://code.claude.com/docs/en/sub-agents)
- [OpenAI Codex](https://openai.com/codex/)
- [Cursor Subagents](https://cursor.com/docs/subagents)
- [Cursor Bugbot](https://cursor.com/docs/bugbot.md)
- [Devin Advanced Capabilities](https://docs.devinenterprise.com/work-with-devin/advanced-capabilities)
- [OpenHands SDK Architecture](https://docs.openhands.dev/sdk/arch/sdk)
- [OpenHands Goal Completion Loop](https://docs.openhands.dev/sdk/guides/convo-goal)
- [GitHub Copilot CLI Customization Features](https://docs.github.com/en/copilot/concepts/agents/copilot-cli/comparing-cli-features)
- [GitHub Copilot Cloud Agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent)

研究资料：

- [Agentic Rubrics as Contextual Verifiers for SWE Agents](https://arxiv.org/html/2601.04171)
- [SWE-Review: Closing the Loop on Issue Resolution with Agentic Code Review](https://arxiv.org/html/2607.06065v1)

调研信息只用于确定设计方向。产品行为可能继续变化，开发时应重新核对官方文档。
