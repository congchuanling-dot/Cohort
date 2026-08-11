# Cohort Agent 可观测性与调优技术方案

> 文档状态：`[规划]`。
> 目标：借鉴 GenericAgent 的 hook、tracing、working checkpoint、长期记忆和后台反射经验，为 Cohort 建立一套可审计、可检索、可调优的 Agent 运行观测系统。
> 原则：先做内部事件流和本地 JSONL，不急着做动态插件；先让运行过程可解释，再接 Langfuse / OpenTelemetry / TUI 面板。

## 1. 问题定义

Cohort 当前已经具备工具循环、session、context stats、raw model response、memory audit 和部分工具级 `run.log`。但这些信息仍然是分散的：

- Runner 做了什么，没有统一生命周期事件。
- LLM 请求、工具调用、compact、memory、SOP、permission、final review 不是同一条 trace。
- 工具失败后，很难快速回答“模型为什么这么做、下一步为什么失败”。
- token / cost / cache 命中没有完整统计。
- SOP 和 memory 是否真的降低失败率，目前没有度量。

可观测性第一阶段要解决的不是“漂亮看板”，而是让每次 Agent 运行可以被还原：

```text
user input
-> context build
-> llm request
-> llm response
-> tool calls
-> tool results
-> recovery / retry / confirmation
-> final answer
-> memory / SOP candidate / audit
```

## 2. GA 经验提炼

### 2.1 Hook Registry

GA 的 `plugins/hooks.py` 是一个极小 hook 系统：

```text
register(event)
trigger(event, ctx)
discover_and_load(plugin_dir)
```

核心事件：

```text
agent_before / agent_after
turn_before / turn_after
llm_before / llm_after
tool_before / tool_after
```

这个设计的价值不是复杂，而是把 Agent 主循环和外围策略解耦。Langfuse、Project Mode、调试日志都可以挂在事件上，而不是继续塞进主循环。

Cohort 应借鉴事件边界，但不要照搬动态 Python 插件加载。Go 第一版应只做内部 `EventSink`，避免跨平台插件、安全边界和测试复杂度。

### 2.2 Langfuse Tracing Sink

GA 的 `plugins/langfuse_tracing.py` 通过 hook 建三层 trace：

- `agent.task`：一次用户任务。
- `llm.chat`：每轮 LLM generation。
- tool span：每个工具调用。

同时它包装 SSE parser，提取 usage：

- input tokens
- output tokens
- cache creation tokens
- cache read tokens

这说明 tracing 不应该只是日志。它还要承载成本、延迟、错误和上下文大小。

Cohort 第一版可以先写本地 JSONL；第二版再把同一批事件投递到 Langfuse / OpenTelemetry。

### 2.3 Turn End Callback

GA 的 `turn_end_callback` 会在长任务、多轮失败、读 SOP 后提醒模型：

- 更新 working checkpoint。
- 停止无效重试。
- 重读相关 SOP。
- 把关键发现写入文件或记忆。

它不是强制模型无限循环，而是在合适的 turn 边界追加恢复提示。

Cohort 应把这类策略迁到事件消费者里：

```text
TurnFinished
-> CheckpointReminder
-> SOPRereadReminder
-> NoProgressDetector
-> MemoryUpdateReminder
```

### 2.4 Working Checkpoint

GA 的 `update_working_checkpoint` 把当前任务的短期状态压缩为：

- 关键约束。
- 已完成步骤。
- 当前失败点。
- 下一步。
- 相关 SOP。

后续 turn 通过 anchor prompt 注入。这是长任务稳定性的核心，不是普通聊天 summary。

Cohort 已有 `update_working_checkpoint`，但需要把它纳入事件流：什么时候更新、更新前后对任务效果有什么影响，都应该有观测数据。

### 2.5 L4 Raw Session Archive

GA 会把历史会话压缩归档到 L4，用于后台反射：

```text
raw sessions
-> compressed all_histories
-> mine repeated failures
-> SOP candidates
-> helper candidates
-> quality report
```

这类数据不应每轮注入上下文，而是作为离线挖掘素材。

Cohort 后续的调优不应依赖“模型最后自觉总结”，而应从 run log 和 session archive 中挖稳定模式。

## 3. Cohort 目标架构

### 3.1 总体链路

```text
Runner / Tools / Context Manager / Evolution
  -> EventBus
  -> EventSink[]
       -> JSONLRunLogSink
       -> ContextStatsSink
       -> UsageCostSink
       -> EvidenceSink
       -> LangfuseSink
       -> ReflectionQueueSink
```

第一版只实现：

```text
EventBus
JSONLRunLogSink
Usage summary
redaction
tests
```

第二版再接：

```text
LangfuseSink
OpenTelemetrySink
TUI trace panel
reflection mining
```

### 3.2 生命周期事件

建议事件集合：

| 事件 | 触发点 | 用途 |
| --- | --- | --- |
| `RunStarted` | 用户任务进入 Runner | 建立 run id、session id、workspace、model |
| `UserPromptSubmitted` | 用户输入入队后 | 记录输入摘要和来源，不保存敏感全文到公共 sink |
| `TurnStarted` | 每轮 LLM 前 | 记录 turn、history 长度、checkpoint、SOP hint |
| `ContextBuilt` | Context Manager 完成后 | 记录预算、裁剪、compact、memory 注入 |
| `LLMRequestStarted` | 调用模型前 | 记录 model、provider、message count、estimated tokens |
| `LLMResponseFinished` | 模型返回后 | 记录 usage、latency、tool call count、finish reason |
| `ToolStarted` | 工具执行前 | 记录 tool name、args 摘要、risk、target |
| `ToolFinished` | 工具执行后 | 记录 status、latency、result 摘要、artifact refs |
| `ToolBatchFinished` | 一轮所有工具完成后 | 记录 batch size、失败数、next prompt |
| `PermissionRequested` | R2/R3 或 ask_user | 记录 operation、binding、reason、风险级别 |
| `RecoveryAttempted` | retry/recover/handoff | 记录失败原因、恢复策略、是否成功 |
| `CompactStarted` | compact 前 | 记录触发原因和预算 |
| `CompactFinished` | compact 后 | 记录 compact 文件、摘要长度、节省 tokens |
| `MemoryProposed` | 长期记忆候选 | 记录 target、evidence、skip 原因 |
| `MemoryApplied` | 记忆写入 | 记录 audit id、target、entry hash |
| `RunFinishing` | final 前 | final review、候选经验提取 |
| `RunFinished` | 任务结束 | exit reason、turn count、tool count、cost、duration |

### 3.3 Event Envelope

统一事件格式：

```json
{
  "schema_version": 1,
  "event_id": "evt_...",
  "event_type": "ToolFinished",
  "time": "2026-07-28T12:00:00Z",
  "run_id": "run_...",
  "session_id": "20260728-...",
  "turn": 3,
  "workspace": "/path/to/repo",
  "source": "runner",
  "severity": "info",
  "data": {},
  "redaction": {
    "applied": true,
    "fields": ["args.text"]
  }
}
```

约束：

- `data` 必须是可 JSON 序列化对象。
- 大文本只记录长度、hash、前后少量摘要。
- 文件内容、剪贴板内容、secret、cookie、token 不进默认 sink。
- artifact 用路径或 ref，不直接写 base64。

### 3.4 Run Log 布局

建议落盘：

```text
temp/sessions/<session_id>/
  history.jsonl
  meta.json
  run.log.jsonl
  context.log.jsonl
  model_responses/
  artifacts/
  memory.md
  compact.md
```

`run.log.jsonl` 是统一事件流；`context.log.jsonl` 可保留为 Context Manager 的专项统计，后续也可以由 `ContextBuilt` 事件生成。

## 4. 调优闭环

### 4.1 单次任务调优

一次任务内可实时发现：

- 模型没有调用工具但用户要求改文件。
- 工具连续失败。
- 同一 target / bbox 重复点击。
- context 过大导致裁剪。
- SOP 命中但未读取。
- 读了 SOP 但未 checkpoint。
- R2 确认被拒绝后仍尝试继续。

对应策略：

```text
event stream
-> policy detector
-> next_prompt / warning / handoff
```

注意：这不是把模型“死缠烂打”回循环。只有明确异常才给一次恢复提示，正常 final 应允许结束。

### 4.2 跨任务调优

离线反射任务读取多次 run log：

```text
cohort reflect once --task session-archive
cohort reflect once --task mine-sop-candidates
cohort reflect once --task memory-quality-report
cohort reflect once --task tool-failure-report
```

输出只进入候选区：

```text
memory/reflection/sop_candidates.md
memory/reflection/failure_patterns.md
memory/reflection/helper_candidates/
memory/reflection/quality_reports/
```

进入正式 SOP / Skill / Tool Registry 前必须人工确认。

### 4.3 质量指标

建议第一版统计：

| 指标 | 说明 |
| --- | --- |
| `turn_count` | 完成任务轮数 |
| `tool_call_count` | 工具调用总数 |
| `tool_error_count` | 工具失败数 |
| `recovery_count` | 自动恢复次数 |
| `handoff_count` | 需要用户接管次数 |
| `confirmation_count` | R2 确认次数 |
| `context_tokens_estimated` | 请求上下文估算 |
| `prompt_tokens` / `completion_tokens` | provider usage |
| `cache_read_tokens` | prompt cache 命中 |
| `duration_ms` | 总耗时 |
| `sop_hit` / `sop_read` | SOP 是否命中并读取 |
| `checkpoint_updated` | 是否更新工作记忆 |
| `memory_candidate_count` | 经验候选数 |

调优判断：

- 相同场景平均 turn 数下降，说明 SOP/记忆有效。
- 工具失败率下降，说明工具 schema、错误提示或恢复策略有效。
- memory 命中后实际被使用，说明检索质量有效。
- recovery 次数下降，说明 target cache、OAV 或视觉候选更稳。

## 5. 模块设计

### 5.1 internal/observability

建议新增包：

```text
internal/observability/
  event.go
  bus.go
  sink.go
  jsonl_sink.go
  redactor.go
  usage.go
  noop.go
```

核心接口：

```go
type Event struct {
    SchemaVersion int
    EventID       string
    EventType     string
    Time          time.Time
    RunID         string
    SessionID     string
    Turn          int
    Workspace     string
    Source        string
    Severity      string
    Data          map[string]any
    Redaction     RedactionSummary
}

type Sink interface {
    Emit(ctx context.Context, event Event) error
    Close(ctx context.Context) error
}

type Bus interface {
    Emit(ctx context.Context, event Event)
}
```

实现要求：

- sink 失败不能中断 Agent 主流程。
- 默认 sink 是本地 JSONL。
- 测试中可使用 memory sink。
- 所有事件写入前先 redaction。

### 5.2 Runner 接入点

Runner 接入：

```text
Run start -> RunStarted
每轮开始 -> TurnStarted
ContextManager.Build -> ContextBuilt
LLM call before/after -> LLMRequestStarted / LLMResponseFinished
tool loop before/after -> ToolStarted / ToolFinished
final review -> RunFinishing
exit -> RunFinished
```

工具层不应直接依赖具体 sink，只需要 Runner 在调用工具前后发事件。少数底层模块如 Context Manager / Evolution 可以返回统计结构，由 Runner 统一发事件。

### 5.3 Redaction

默认脱敏规则：

- 字段名命中 `api_key`、`token`、`secret`、`password`、`cookie`：只保留 hash 和长度。
- `text`、`content`、`clipboard`：默认记录长度、行数、hash，不记录全文。
- 文件 patch：记录路径、changed lines、old/new bytes，不记录完整内容。
- browser / desktop screenshot：记录 artifact ref，不记录 base64。
- ask_user：记录 operation、risk、binding，不记录用户私密输入全文。

## 6. 开发阶段

### P0：本地事件流 `[完成]`

任务：

1. 新增 `internal/observability`。
2. 定义 Event / Sink / Bus / Redactor。
3. 在 session 目录写 `run.log.jsonl`。
4. Runner 接入核心事件。
5. ToolStarted / ToolFinished 记录工具名、耗时、状态、错误码。
6. ContextBuilt 记录 context stats。
7. RunFinished 输出汇总指标。

验收：

- `go test ./...` 通过。
- 一次 `cohort ask` 后能看到完整 `run.log.jsonl`。
- 日志里能定位每一轮模型调用和工具调用。
- 默认日志不泄露 secret、剪贴板正文或大文件内容。

### P0.5：Trace / Perf CLI `[完成]`

任务：

1. 实现 `cohort trace last`。
2. 实现 `cohort trace show <session_id> [--run <run_id>]`。
3. 实现 `cohort perf last`。
4. 实现 `cohort perf show <session_id> [--run <run_id>]`。
5. 从 `run.log.jsonl` 汇总 LLM、tool、context、usage 和最大事件间隔。
6. 实现 `cohort trace graph`，关联 LLM、tool、permission 和 file change，生成因果 DAG 与关键路径。

验收：

- 不启动 LLM、不依赖 API Key，也能读取本地 session 观测数据。
- `trace` 能按时间线展示每个关键生命周期事件。
- `perf` 能快速判断慢在 LLM、工具、上下文构建还是请求体膨胀。
- `trace graph` 能离线生成交互式 HTML，并用 JSON 暴露关键路径和异常节点。
- 日志行很大时仍能读取，不依赖 `bufio.Scanner` 的默认 token 上限。

### P1：Usage / Cost / Trace `[部分完成]`

任务：

1. 统一解析 OpenAI-compatible usage。
2. 增加 usage summary。
3. 支持 provider profile 价格表。
4. 增加 Langfuse sink。
5. 预留 OpenTelemetry sink。

验收：

- CLI 能显示本次 run 的 token 和成本估算。
- Langfuse 中能看到 agent -> llm -> tools 的层级 trace。
- tracing sink 失败不影响主任务。

当前状态：

- Runner 已在 `RunFinished` 汇总 usage/cost。
- Langfuse sink 已有基础 ingestion，并已通过异步 sink 避免阻塞主流程。
- trace/perf CLI 已能读本地 usage 和耗时。
- `cohort tuning report` 已能跨多次 run 汇总慢 LLM、失败工具、ask_user、权限事件、schema/request/context 膨胀。
- Langfuse 的完整层级 trace 与 OpenTelemetry sink 仍待补。

### P2：调优报告 `[部分完成]`

任务：

1. 实现 `cohort reflect once --task session-archive`。
2. 实现 `mine-sop-candidates`。
3. 实现 `memory-quality-report`。
4. 实现 `tool-failure-report`。
5. 实现 `cohort tuning report`。
6. 实现 eval suite、确定性断言、隔离 Fixture、最终状态验证、工具轨迹评分、repeat 稳定率、历史结果、基线比较和可视化 Dashboard。
7. 后续实现 judge 模型评分和 provider/prompt/tool A/B matrix。

验收：

- 能从多次 run log 中发现高频失败工具。
- 能输出 SOP candidate，但不自动晋级。
- 能报告 memory 命中后是否被使用。
- 能输出慢请求、工具 schema 膨胀、request/context 膨胀和下一步调优建议。
- 能用 `cohort eval run` 执行可复现回归，并用 `cohort eval report --open` 查看历史趋势、标签质量和失败断言。

### P3：前端可视化

任务：

1. `[完成]` REPL 增加 `/trace`、`/perf`、`/tuning` 和 `/eval`。
2. `[完成]` 每次普通 REPL 任务后可配置异步刷新 tuning Markdown/HTML Dashboard。
3. `[完成]` 支持按 session 查看 turn、tool、usage 和性能 gap；`FileChanged` 事件已按 `tool_call_id` 关联 artifact，并可生成因果 DAG。
4. `[规划]` 全屏 TUI trace panel。

验收：

- 用户不用翻 JSONL 也能看到“这轮为什么失败”。
- 能快速定位最后一次错误、最后一次文件修改和最后一次确认请求。

## 7. 不做什么

第一版不做：

- Go 动态插件系统。
- 任意第三方脚本 hook。
- 自动把 SOP candidate 晋级为正式 SOP。
- 自动 patch 核心代码。
- 把完整 prompt、完整工具正文、完整截图写入公共 tracing sink。

原因：可观测性是安全能力，不应先变成新的攻击面。

## 8. 推荐下一步

下一步直接进入 P0：

```text
internal/observability
-> run.log.jsonl
-> Runner lifecycle events
-> tool/context/llm metrics
-> redaction
```

这一步完成后，Cohort 后续做 FinishGuard、Plan Mode、Computer Use OAV、SOP candidate mining、Langfuse tracing 都会有统一证据来源。
