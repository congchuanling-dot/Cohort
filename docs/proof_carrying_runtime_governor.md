# Proof-Carrying Agent Runtime Governor

状态：已实现

## 目标

Control Center 不再只是运行记录查看器，而是基于 Provider 回执、上下文容量、因果证据和治理策略的 Agent 运行控制平面。

核心原则：

1. `run.log.jsonl` 是不可变证据源，不复制第二套运行日志。
2. Provider 实际值、本地估算值和不可用状态必须严格区分。
3. Web 只展示脱敏摘要、hash、计数和 Evidence Ref，不返回完整 Prompt、工具结果或密钥。
4. 优化建议必须绑定当前 Run 与成功基线，并进入现有 Capability 审批、验证和晋级闭环。

## Provider Receipt Ledger

入口：

```text
GET /api/v1/quality/receipts/:session_id/:run_id
```

每轮回执包含：

- `usage_source`: `provider_reported` 或 `unavailable`
- `input_tokens`
- `output_tokens`
- `total_tokens`
- `cache_read_tokens`
- `cache_write_tokens`
- 本地 `estimated_input_tokens`
- 请求消息数、字符数、工具 Schema 数和耗时

成本只有在显式配置 `COHORT_COST_*_USD_PER_1M` 后才计算。未配置时返回 `not_configured`，不会猜测供应商价格。

## Context Capacity Governor

入口：

```text
GET /api/v1/quality/capacity/:session_id/:run_id
```

模型能力使用带版本和来源的注册表：

- `deepseek-v4-pro` / `dsv4*`: 项目内置 1M override
- `deepseek-chat` / `deepseek-reasoner`: 128K registry
- 未知模型：128K 保守兜底，`confidence=unknown`

每次 Context Build 记录：

- Context Window、来源、版本和置信度
- 输出预留、安全余量、可用输入预算
- 本地估算 token 与 Provider input token
- Provider 反校准比例
- History、Memory、Compact Summary、Micro Compact 的 Context Waterfall
- `healthy / warning / critical / blocked`

普通请求的 Context Build 只作为 LLM 节点属性。只有发生压缩、裁剪、Memory 注入或预算异常时，DAG 才创建独立 Context Governance 节点。

## Causal DAG Execution Evidence

DAG 默认主链：

```text
Task -> LLM -> Tool -> Artifact / Decision
```

点击节点后展示结构化执行合同：

- 这一步做了什么
- 怎么执行
- 输入摘要
- 脱敏参数摘要和参数 hash
- 输出摘要
- 状态、耗时、Provider token 与本地估算
- Permission Decision、Risk、External Server
- Runtime Event、Redaction 和 Artifact Evidence

旧日志仍可读取。缺失字段明确显示为 unavailable，不推断不存在的事实。

## Executable Policy Engine

入口：

```text
GET /api/v1/quality/governance/:session_id/:run_id
```

当前策略：

| Policy | 阈值 | 动作 |
| --- | --- | --- |
| `context.capacity` | 70% / 90% / 100% | Micro Compact / Full Compact / Block |
| `tool.repeated_identical_failure` | 相同工具和参数失败 2 次 | 第 3 次调用熔断 |
| `tool.route_escalation` | 能力不足或连续失败 | 升级工具面 |
| `permission.gate` | 外部或高风险动作 | allow / ask / deny |

工具熔断在 Runner 内真实执行，并写入 `GovernanceIntervention` 事件。控制面同时展示已执行干预和待处理建议。

## Run Compare 与 Proposal

入口：

```text
GET /api/v1/quality/compare/:session_id/:run_id
```

系统自动从本地 Run 中选择最相似的成功基线，比较：

- Duration
- Provider Input / Output / Cache Tokens
- LLM Calls
- Tool Failures
- Context Peak
- Tool Schema Count

生成 Proposal：

```text
POST /api/v1/actions/runtime.optimization.propose/prepare
POST /api/v1/actions/runtime.optimization.propose/execute
```

写操作必须经过 Session Entity 校验、Preparation Token、CSRF 和 Operation 审计。执行后生成 Capability Gap + Proposal，继续使用既有 Build、Verify、Promote 流程。

## 验收记录

真实 DeepSeek Eval Run：

```text
input_tokens:      382152
output_tokens:       2513
cache_read_tokens: 325248
total_tokens:      384665
provider_turns:        12
context_peak:        3.7%
```

浏览器验收覆盖：

- Eval Case 深链到 Trace
- Provider Receipt Ledger
- Context Capacity 与 Waterfall
- Governance Policy
- 自动成功基线
- LLM 节点执行详情
- Tool 节点脱敏参数、hash、结果和 Evidence
- Console 无错误
