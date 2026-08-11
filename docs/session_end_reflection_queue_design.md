# SessionEnd Hook 自动反思队列接入方案

> 文档状态：`[完成]`。`SessionEnd Hook -> 持久队列 -> Reflect Worker` 已接入，
> 包含去重、水位、批处理、失败恢复、CLI 和 Hermes daemon 调度；仍不开放自动晋级
> SOP、Skill 或自动修改核心代码。

## 1. 目标

把当前需要手动执行的：

```bash
cohort reflect once --task session-archive
cohort reflect once --task mine-sop-candidates
cohort reflect once --task mine-skill-candidates
cohort reflect once --task memory-quality-report
cohort reflect once --task tool-failure-report
```

接入 Agent 生命周期：

```text
Runner.Run 完成
  -> SessionEnd Hook
  -> 持久化 ReflectionTrigger
  -> 后台 Worker 批量合并
  -> 执行现有 Manager.ReflectOnce
  -> 生成 archive / report / candidate
  -> 人工 review 和 promote
```

核心要求：

- 用户回答主路径不能同步执行反思任务。
- 进程退出或崩溃后，待处理任务不能丢失。
- 同一 session 的重复 Hook 不得造成重复候选或并发写文件。
- 反思阶段只生成归档、报告和 candidate，不直接晋级正式资产。
- 队列中只保存索引、计数和路径，不复制用户正文、工具结果或密钥。

## 2. 当前代码基线

现有能力可以直接复用：

| 能力 | 当前实现 |
| --- | --- |
| 生命周期 Hook | `internal/hooks/hooks.go` |
| Runner Hook 分发 | `internal/agent/hooks.go` |
| `SessionEnd` 触发点 | `internal/agent/runner.go` 的 `finishRun` |
| Session 落盘 | `internal/session/store.go` |
| 离线反思 | `internal/evolution/reflector.go` |
| 手动入口 | `cohort reflect once --task ...` |

当前有两个必须先处理的事实：

1. `app.NewRunner` 没有给 `Runner.Hooks` 装配 Registry，因此 Hook 接口存在，但默认运行时没有实际 Handler。
2. 当前 `SessionEnd` 在每次 `Runner.Run` 结束时触发。REPL 中同一 session 会触发多次，它实际表示
   “一次用户任务结束”，不是 Runner 真正关闭。

本方案第一版保留现有触发语义。它比只在 `Runner.Close` 时触发更可靠：长时间运行的 REPL、
异常退出和进程被终止时，已经完成的任务仍能及时入队。队列通过 history watermark 合并重复事件。

## 3. 总体架构

```text
Runner.finishRun
  -> hooks.Registry.Emit(SessionEnd)
       -> SessionEndReflectionHandler
            -> EligibilityPolicy
            -> ReflectionQueue.Enqueue
                 -> .cohort/reflection/queue/pending/<job_id>.json

Reflect Worker
  -> RecoverExpiredClaims
  -> ClaimDueBatch
  -> ReflectionPlanner
  -> evolution.Manager.ReflectOnce(...)
  -> UpdateWatermark
  -> ArchiveDone / Retry / DeadLetter
```

职责边界：

| 组件 | 职责 |
| --- | --- |
| `SessionEndReflectionHandler` | 校验事件并生成轻量 trigger |
| `ReflectionQueue` | 持久化、去重、claim、重试和崩溃恢复 |
| `ReflectionPlanner` | 根据累计变化选择本批需要执行的反思任务 |
| `ReflectionWorker` | 串行执行已有 `ReflectOnce` |
| `evolution.Manager` | 保持现有归档、报告和候选生成逻辑 |

Hook 不读取完整 history，不执行 LLM，不运行候选挖掘。

## 4. 事件契约

### 4.1 SessionEnd 数据

扩展 `finishRun` 发出的 Hook data：

```go
map[string]any{
    "status":       data["status"],
    "history_len":  len(r.history),
    "session_root": absSessionRoot,
    "memory_root":  absWorkspace,
    "trigger_kind": "run_boundary",
}
```

Hook 信封已经包含：

- `RunID`
- `SessionID`
- `Turn`
- `Workspace`
- `Time`

禁止放入 Hook data：

- 用户 prompt 或 assistant 正文。
- 工具参数、工具结果正文。
- API key、cookie、token。
- 完整 checkpoint 或长期记忆内容。

### 4.2 触发条件

满足以下条件才入队：

- 自动反思配置已启用。
- `SessionID`、`session_root`、`memory_root` 非空且路径合法。
- `history_len > 0`。
- 相同 session 的 `history_len` 大于已成功处理的 watermark。
- 运行来源不是 Eval、Hermes Repair、Explorer 等隔离执行。

因此 Runner 需要增加明确来源字段：

```go
type RunMode string

const (
    RunModeInteractive RunMode = "interactive"
    RunModeEval        RunMode = "eval"
    RunModeRepair      RunMode = "repair"
    RunModeExplorer    RunMode = "explorer"
)
```

第一版只接受 `interactive`。不能依赖 prompt 内容推断运行来源。

错误状态仍允许入队，因为失败模式是反思的重要输入；取消、空 session 和初始化失败不入队。

## 5. 队列设计

### 5.1 存储位置

队列属于运行状态，不属于长期记忆：

```text
<project_root>/.cohort/reflection/
  queue/
    pending/
    running/
    done/
    dead/
  state.json
  runs.jsonl
  worker.lock
```

反思产物继续写入现有位置：

```text
<memory_workspace>/memory/raw_sessions/
<memory_workspace>/memory/reflection/
```

不要把队列文件放进 `memory/reflection/`，否则运行状态会和可审阅资产混在一起。

### 5.2 Trigger 数据结构

```go
type ReflectionTrigger struct {
    SchemaVersion int       `json:"schema_version"`
    ID            string    `json:"id"`
    DedupeKey     string    `json:"dedupe_key"`
    ProjectRoot   string    `json:"project_root"`
    MemoryWorkspace string  `json:"memory_workspace"`
    SessionRoot   string    `json:"session_root"`
    SessionID     string    `json:"session_id"`
    RunID         string    `json:"run_id"`
    HistoryLen    int       `json:"history_len"`
    RunStatus     string    `json:"run_status"`
    CreatedAt     time.Time `json:"created_at"`
    AvailableAt   time.Time `json:"available_at"`
    Attempt       int       `json:"attempt"`
    MaxAttempts   int       `json:"max_attempts"`
    ClaimedAt     time.Time `json:"claimed_at,omitempty"`
    LeaseUntil    time.Time `json:"lease_until,omitempty"`
    LastError     string    `json:"last_error,omitempty"`
}
```

`DedupeKey`：

```text
sha256(project_root + "\x00" + session_id + "\x00" + history_len)
```

同一 Run 重复派发、进程重试或 Hook 重放时，只产生一个 trigger。

### 5.3 文件队列而不是单个 queue.json

每个 trigger 使用独立文件：

```text
pending/<job_id>.json
```

原因：

- 临时文件写完后 `rename`，单条任务原子落盘。
- `pending -> running` 使用同文件系统原子 `rename` 完成 claim。
- 不需要每次入队都读取和重写整个队列。
- 多个 Cohort CLI 进程同时结束时不会互相覆盖。

文件权限：

- 目录 `0700`。
- trigger 和状态文件 `0600`。
- 写文件必须 `write -> fsync -> close -> rename`。

## 6. 幂等和 watermark

`state.json` 保存每个 task 已处理到的 session 水位：

```json
{
  "schema_version": 1,
  "sessions": {
    "session-id": {
      "history_len": 42,
      "processed_at": "2026-08-12T12:00:00Z"
    }
  },
  "tasks": {
    "session-archive": {
      "last_run_at": "2026-08-12T12:00:00Z"
    }
  }
}
```

处理语义采用：

```text
at-least-once delivery + idempotent reflection output
```

不追求分布式 exactly-once。原因是：

- 报告类产物本来就是确定性重建或覆盖。
- `mine-sop-candidates` 已有候选去重。
- 只有整批计划成功后才推进 watermark。
- 部分成功后重试允许重复执行已完成 task，但不能重复生成同一 candidate。

`state.json` 只在反思任务成功后更新。入队成功不能提前推进 watermark。

## 7. Worker 和批处理

### 7.1 串行边界

同一项目只允许一个反思 Worker：

```text
<project_root>/.cohort/reflection/worker.lock
```

使用 PID 锁并清理 stale lock，可复用 Hermes 的进程锁语义。这样现有
`ReflectOnce` 无需立即承担并发写 `sop_candidates.md` 的责任。

### 7.2 批量合并

Worker 不按一个 SessionEnd 执行一次全量反思。默认等待短暂 quiet window，然后一次 claim
同项目的全部 due trigger：

```text
多个 SessionEnd
  -> pending triggers
  -> 30 秒 debounce
  -> claim batch
  -> 对全部 session 数据执行一次任务集合
  -> 成功后批量推进 watermark
```

这样 REPL 连续多轮不会反复扫描所有历史。

### 7.3 Planner 默认策略

| 反思任务 | 默认触发 |
| --- | --- |
| `session-archive` | 每个非空 batch |
| `tool-failure-report` | batch 中有 error run，或累计 5 个新 run |
| `memory-quality-report` | 累计 5 个新 run，且距离上次执行超过 30 分钟 |
| `mine-sop-candidates` | 累计 5 个新 run，且距离上次执行超过 1 小时 |
| `mine-skill-candidates` | 累计 10 个新 run，且距离上次执行超过 6 小时 |

这些是调度阈值，不是安全边界。候选仍必须满足现有重复模式和去重规则。

### 7.4 失败恢复

状态流转：

```text
pending -> running -> done
                   -> pending  (retry)
                   -> dead     (exhausted)
```

默认：

- 最大尝试次数：3。
- 重试间隔：1 分钟、5 分钟、30 分钟。
- lease：10 分钟。
- Worker 启动时将过期 `running` 任务移回 `pending`。
- `LastError` 只保存错误分类和截断后的安全摘要。
- `done` 最多保留最近 200 条，较老记录只保留在 `runs.jsonl`。

## 8. 运行时装配

### 8.1 新增文件

```text
internal/evolution/reflection_queue.go
internal/evolution/reflection_worker.go
internal/evolution/reflection_planner.go
internal/evolution/reflection_hook.go
```

### 8.2 app.NewRunner

在应用装配层创建真实 Hook Registry：

```go
queue := evolution.NewReflectionQueue(projectRoot)
reflectionHook := evolution.NewSessionEndReflectionHandler(
    queue,
    absSessionRoot,
    absMemoryWorkspace,
    runMode,
)

runner.Hooks = hooks.NewRegistry(reflectionHook)
```

不要在 `Runner` 内直接构造 queue。Runner 继续只依赖 `hooks.Registry`，避免 Agent Loop
耦合具体的反思存储。

### 8.3 Hook 错误行为

当前 `hooks.Registry.Emit` 的错误不会中断 Runner，这个行为保持不变。

入队失败时：

- 用户任务仍按原结果结束。
- `HookDispatched` 观测事件记录 handler 失败。
- `cohort trace last` 可以定位最近的 Hook 入队错误。
- 不允许退化为同步执行反思。

## 9. 配置和 CLI

配置保持收敛：

```yaml
reflection:
  auto_enqueue: true
  debounce_seconds: 30
  max_attempts: 3
```

安全默认值：

- 普通交互 Runner：`auto_enqueue=true`。
- Eval、Repair、Explorer：强制关闭，不接受配置覆盖。
- 自动消费默认由常驻 Worker 执行。

CLI 只增加三个入口：

```bash
cohort reflect status
cohort reflect drain
cohort reflect retry <job_id>
```

- `status`：展示 pending/running/dead 数量、watermark 和最近执行结果。
- `drain`：前台消费当前 due batch，便于开发和排障。
- `retry`：把指定 dead trigger 放回 pending。

常驻消费优先接入现有本地 daemon 的调度 ticker，不再新造第二套 daemon 管理命令。
若 Hermes 未启动，任务仍可靠保留在 pending，可由 `drain` 手动消费。

## 10. 安全边界

自动 Worker 允许：

- 读取 session metadata、history 结构和脱敏后的 observation。
- 写 session archive。
- 覆盖确定性质量报告。
- 追加经过现有规则去重的 SOP/Skill candidate。

自动 Worker 禁止：

- 调用 `PromoteSOPCandidate` 或 capability `Promote`。
- 修改 `sops/index.md`。
- 启用 Tool/MCP Adapter。
- 安装依赖。
- 修改 `internal/`、`cmd/` 或其他产品代码。
- 调用 LLM。

后续即使增加 LLM Candidate Extractor，也必须写入独立 candidate/report，不能绕过
EvidenceLedger、verify 和人工 promote。

## 11. 实施顺序

### P0：可靠入队

1. 增加 reflection 配置。
2. 实现目录型 `ReflectionQueue.Enqueue` 和 deterministic dedupe。
3. 实现 `SessionEndReflectionHandler`。
4. 在 `app.NewRunner` 装配 Hook Registry。
5. 给 Eval、Repair、Explorer 设置明确 `RunMode`。
6. 增加 `cohort reflect status`。

验收：

- 普通 `cohort ask` 完成后生成一个 pending trigger。
- 相同 `session_id + history_len` 重放不会重复入队。
- Hook 入队失败不改变用户任务结果。
- Eval、Repair、Explorer 不产生 trigger。

### P1：可恢复消费

1. 实现 claim、lease、retry、dead-letter。
2. 实现 Worker 单项目锁。
3. 实现 `cohort reflect drain`。
4. 复用 `Manager.ReflectOnce` 执行 batch。
5. 成功后更新 watermark。

验收：

- Worker 中途退出后，lease 到期可以恢复。
- 两个 Worker 同时启动时只有一个能写反思产物。
- 失败三次进入 dead，能够显式 retry。

### P2：自动调度

1. 将 `DispatchReflections` 接入现有 daemon ticker。
2. 增加 debounce 和 Planner 阈值。
3. 增加 queue/runs 观测指标。
4. 完成 done 压缩和 state 清理。

验收：

- REPL 连续多轮只触发一次批量反思。
- daemon 重启不丢任务、不重复生成候选。
- 反思执行不增加 LLM 首 token 和流式输出延迟。

## 12. 测试矩阵

单元测试：

- `SessionEnd` 合法事件成功入队。
- 空 session、非法路径、隔离 RunMode 被拒绝。
- deterministic dedupe。
- pending 到 running 的原子 claim。
- lease 过期恢复。
- 指数退避和 dead-letter。
- watermark 只在整批成功后推进。
- Planner 阈值和 cooldown。

集成测试：

```text
Runner.Run
  -> SessionEnd
  -> pending trigger
  -> drain
  -> session archive/report
  -> state watermark
```

回归测试：

- `go test ./...`
- `cohort reflect once --task ...` 原手动入口行为不变。
- `cohort ask` 的最终输出不等待反思完成。
- `run.log.jsonl` 不包含 trigger 中禁止保存的正文和 secret。

## 13. 不采用的方案

### Hook 内直接调用 ReflectOnce

拒绝。它会让全量 session 扫描、文件写入和候选挖掘阻塞用户回答，重复引入已经在
LLM 流式性能排查中确认过的“旁路能力阻塞主循环”问题。

### 只用内存 channel

拒绝。`cohort ask` 结束后进程立即退出，内存任务会丢失，也无法支持崩溃恢复。

### SessionEnd 直接启动 goroutine

拒绝。CLI 进程可能在 goroutine 完成前退出，而且多个进程会并发写同一反思文件。

### 自动 promote

拒绝。队列自动化只负责发现和生成 candidate；正式 SOP、Skill、Adapter 仍必须经过
人工审阅和现有 verify/promote Gate。

## 14. 最终边界

本接入完成后，Cohort 获得的是：

```text
自动发现新增会话证据
  -> 可靠排队
  -> 后台批量反思
  -> 生成可审阅候选
```

不是：

```text
SessionEnd
  -> 自动修改自己
  -> 自动启用
```

前者能让 Cohort 持续积累经验，同时保留当前 Evidence、Verify、Approval 和 Promote
边界；后者会绕过现有安全模型，不进入本方案。
