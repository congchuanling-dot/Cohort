# Cohort 自适应工具路由

> 文档状态：`[完成]`。状态基线：2026-08-12。

## 1. 面试一句话

Cohort 在保留完整 Capability Registry 的同时，根据当前任务意图为每轮 LLM 请求动态生成
最小工具 schema；当模型因能力不可见准备早停，或工具连续失败时，Router 自动升级到完整
工具面，因此同时兼顾首 token 性能、工具召回率和可恢复性。

```text
Full Registry
  -> Intent Router
  -> Progressive Schema Disclosure
  -> LLM Request
  -> Failure / Capability Stop Detector
  -> Full Surface Escalation
```

它不是简单配置一组固定工具，也不是让另一个 LLM 先做分类。

## 2. 解决的问题

优化前，普通代码任务也会携带 Browser、Desktop、Computer、MCP 和 Adapter 等全部 schema：

- 工具数量：81。
- schema payload：约 66KB。
- 大量与当前任务无关的工具会增加输入 token 和模型路由噪声。
- 固定轻量工具组虽然快，但遇到跨域任务需要用户手工修改配置并重启。

自适应路由的目标是：

```text
注册完整能力 != 每轮暴露完整能力
```

Registry 继续保存所有已启用工具；Router 只决定当前 LLM 请求看见哪些 schema。

## 3. 当前实测

使用当前项目真实 81 个工具运行：

```bash
cohort tools route "分析 internal/agent/runner.go 的工具循环并修复测试"
```

结果：

| 场景 | 完整工具 | 选中工具 | 完整 schema | 选中 schema | 减少 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 代码任务 | 81 | 15 | 66,354 B | 11,564 B | 82.6% |
| 浏览器任务 | 81 | 33 | 66,354 B | 24,248 B | 63.5% |

真实 LLM 请求链路验收：

```text
run duration: 2.472s
LLM duration: 2.447s
tool schemas: 15 / 81
schema bytes saved: 54,790
route escalations: 0
```

这组耗时只用于证明 Router 已进入真实 Runner 请求链路；延迟对比仍应在相同模型、网络和
prompt 下做多次 A/B，不能用单次请求宣称稳定提速比例。

代码任务保留：

- 文件读写和命令执行。
- Go/TS/Python LSP。
- checkpoint 和长期记忆。
- Skill 路由。
- `ask_user`。

浏览器任务在上述基线上增加完整 browser 工具组，不暴露 Desktop 和 Computer Use。

## 4. 路由层级

### 4.1 基础工具面

普通任务默认暴露：

```text
core + lsp + memory + skill + ask
```

这组能力足以完成代码阅读、修改、测试、项目检索、记忆沉淀和阻塞式询问。

### 4.2 意图工具面

Router 使用确定性本地规则识别：

| 意图 | 增量工具组 |
| --- | --- |
| URL、网页、浏览器、前端页面 | `browser` |
| 桌面、原生应用、Computer Use | `desktop + computer` |
| 飞书、数据库、邮件等外部领域 | 相关 MCP/Adapter Top-K |

外部工具使用工具名、描述和中英文领域 alias 做相关性排序，默认最多加入 8 个。

### 4.3 完整工具面

以下情况自动升级到完整 schema：

1. 模型无 tool call，并明确声称“缺少工具、无法访问或无法操作”。
2. 工具连续失败达到阈值，默认 2 次。

升级最多发生一次，后续轮次保持完整工具面，不会在两个路由状态间振荡。

## 5. Runner 数据流

```text
Runner.Run(input)
  -> registry.Schemas()                       # 完整工具面
  -> AdaptiveToolRouter.Route(input)
  -> emit ToolRouteSelected
  -> LLM.Chat(selectedSchemas)
  -> tool calls
       -> ObserveToolResult(success/failure)
       -> repeated failures => Escalate()
  -> no tool response
       -> capability limitation detector
       -> append escalation hint
       -> rebuild context
       -> next turn with full schemas
```

关键实现：

- `internal/agent/tool_router.go`
- `internal/agent/runner.go`
- `internal/observability/event.go`
- `internal/traceview/traceview.go`
- `internal/tuning/report.go`

## 6. 安全边界

Adaptive Tool Router 不是权限系统。

```text
Router：决定模型看见什么
Policy：决定工具能不能执行
```

即使 Router 升级到完整工具面：

- Skill `allow-tools/deny-tools` 仍由 `ToolPolicyRunner` 强制执行。
- MCP 风险和授权策略不变。
- Desktop/Computer R2/R3 confirmation 不变。
- Eval、Hermes Repair、Explorer 自动关闭自适应路由，保持工具面可复现。

因此路由优化不会绕过现有安全门禁。

## 7. 可观测性

每轮写入 `ToolRouteSelected`：

```json
{
  "mode": "adaptive",
  "reason": "intent_match",
  "full_schema_count": 81,
  "selected_count": 33,
  "full_schema_bytes": 66354,
  "selected_schema_bytes": 24248,
  "saved_schema_bytes": 42106,
  "escalated": false
}
```

查看单次运行：

```bash
cohort trace last
cohort perf last
```

`perf` 会展示：

- 最后一次路由模式。
- 完整/选中 schema 数量。
- 本轮和累计减少的 schema bytes。
- 自适应路由轮数。
- 自动升级次数。

跨会话查看：

```bash
cohort tuning report
```

调优报告会汇总：

- `adaptive_routed_runs`
- `tool_route_escalations`
- `schema_bytes_saved`
- 仍然发生 schema bloat 的 run

## 8. 配置

```yaml
tools:
  enabled_groups: [*]
  adaptive_routing: true
  adaptive_max_external_tools: 8
  adaptive_failure_threshold: 2
  adaptive_min_schema_count: 20
```

含义：

- `enabled_groups` 决定 Registry 中实际注册哪些工具。
- `adaptive_routing` 决定是否按任务动态裁剪。
- `adaptive_max_external_tools` 限制 MCP/Adapter 相关性召回数量。
- `adaptive_failure_threshold` 控制连续失败后的扩容阈值。
- `adaptive_min_schema_count` 避免对本来就很小的工具面继续路由。

## 9. 离线预览

不调用 LLM 即可解释路由决策：

```bash
cohort tools route "打开 https://example.com 检查登录按钮"
```

输出包含：

- 路由模式和原因。
- 完整/选中工具数量。
- schema bytes 和节省量。
- 选中的工具组。
- 相关外部工具。
- 最终工具名列表。

这使路由策略可测试、可调优，而不是隐藏在 prompt 中。

## 10. 为什么不用 LLM Router

第一版不额外调用模型做工具分类：

- 否则为了减少一次 LLM 请求 payload，先增加另一次 LLM 请求，收益不稳定。
- Router 模型本身会引入延迟、成本和不可复现输出。
- 确定性路由更容易写 Eval、做 A/B 和解释错误。

未来可以让 embedding/小模型只参与外部工具 Top-K，但最终仍应保留确定性 fallback。

## 11. 面试展开

可以按以下顺序讲：

1. **问题证据**：全量 81 个 schema 约 66KB，普通代码任务存在明显无关输入。
2. **架构取舍**：Registry 和 per-request visibility 分离，保留完整执行能力。
3. **性能设计**：本地确定性分类，不增加额外模型请求。
4. **召回设计**：意图命中、外部工具 Top-K、能力早停检测、连续失败扩容。
5. **安全设计**：Router 只管可见性，ToolPolicy/MCP/Computer confirmation 继续管权限。
6. **可观测设计**：每轮记录数量、字节、原因和 escalation，支持 trace/perf/tuning。
7. **量化结果**：代码任务 schema payload 减少 82.6%，浏览器任务减少 63.5%。

不要描述成“关键词过滤工具”。准确说法是：

> 一个带渐进扩容、执行门禁分层和运行时可观测性的 per-request capability routing system。
