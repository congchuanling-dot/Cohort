# Cohort 自进化能力调研与落地建议

> 文档状态：`[部分完成]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 受控长期记忆、证据、SOP candidate 和相关记忆注入已经落地；生命周期 hook、候选自动
> 挖掘、L4 会话归档、后台反射和质量度量仍是后续路线。

## 结论摘要

GenericAgent 的“自进化”不是让 Agent 无约束地修改自身代码，而是一套受控的经验晋级链路：

```text
执行证据
  -> 当前任务工作记忆
  -> 长期记忆候选
  -> SOP candidate
  -> 人工确认后的正式 SOP / helper
  -> 后台反射继续挖掘历史经验
```

Cohort 已经具备这条链路的主体骨架：`update_working_checkpoint`、`start_long_term_update`、`memory_propose_update`、`memory_apply_update`、`EvidenceLedger`、SOP 路由、SOP candidate 晋级、相关长期记忆注入都已经存在。下一步不应该重写一套 GenericAgent，而应该补齐四类能力：

- 生命周期 hook：把 Runner 中零散的 turn、tool、final review 逻辑整理成可扩展事件。
- 证据驱动的候选挖掘：从工具结果、checkpoint、历史会话中自动提取候选，但写入仍受 EvidenceLedger 校验。
- 原始会话归档和后台反射：定期压缩 `history.jsonl` / `temp/model_responses`，挖掘重复失败、稳定流程和高频需求。
- 质量门禁：SOP、记忆、helper 的晋级必须有触发词、验收标准、回滚路径和人工确认边界。

核心原则应保持不变：

```text
No Execution, No Memory.
没有工具验证、已读文件、成功测试、浏览器确认、用户明确稳定偏好或已有记忆支撑的信息，不进入长期记忆。
```

## 1. GenericAgent 自进化机制

### 1.1 分层记忆

GenericAgent 的记忆不是单文件追加，而是分层管理。

| 层级 | 资产 | 作用 |
| --- | --- | --- |
| L0 | 当前对话和工具结果 | 本轮任务即时上下文 |
| L1 | `memory/global_mem_insight.txt` | 极短索引，保存关键词和指针 |
| L2 | `memory/global_mem.txt` | 稳定事实、环境约定、用户偏好 |
| L3 | `memory/*.md`、helper 脚本 | 专项 SOP、可复用流程、辅助代码 |
| L4 | `memory/L4_raw_sessions/` | 原始会话压缩归档，用于后续挖掘 |

这套设计的重点是“上层放指针，下层放细节”。模型每轮不需要读完整长期记忆，而是先看到轻量索引，再按任务需要读取具体 SOP 或记忆文件。

### 1.2 工作记忆

GenericAgent 通过 `update_working_checkpoint` 保存当前任务的关键约束、进度和下一步。

关键位置：

- `<generic-agent-repo>/ga.py`
- `<generic-agent-repo>/assets/tools_schema.json`

数据流：

```text
模型调用 update_working_checkpoint
  -> handler.working["key_info"] / handler.working["related_sop"]
  -> _get_anchor_prompt 注入后续 turn
  -> turn_end_callback 在多轮、失败、SOP 读取后提醒更新
```

价值：

- 长任务中防止遗忘用户约束、SOP 禁区和阶段性发现。
- 把“当前任务该怎么继续”从完整历史中提炼出来。
- 失败多轮后强制模型停下盲试，重读 SOP 或换策略。

### 1.3 长期记忆沉淀

GenericAgent 通过 `start_long_term_update` 启动任务收尾阶段的经验提炼。

关键位置：

- `<generic-agent-repo>/ga.py`
- `<generic-agent-repo>/memory/memory_management_sop.md`

它不是直接写文件，而是先让模型判断本轮是否有可复用、已验证、值得未来检索的经验。核心公理是：

```text
No Execution, No Memory.
```

因此以下内容不能沉淀：

- 模型推理出来但没有工具证据的结论。
- 失败尝试中的猜测。
- 当前任务的一次性业务正文。
- secret、token、cookie、临时 URL、session ID、PID 等易变或敏感信息。

### 1.4 后台反射

GenericAgent 支持 `agentmain.py --reflect <script>` 后台反射模式。

关键位置：

- `<generic-agent-repo>/agentmain.py`
- `<generic-agent-repo>/reflect/scheduler.py`
- `<generic-agent-repo>/reflect/goal_mode.py`
- `<generic-agent-repo>/reflect/autonomous.py`

运行模式：

```text
reflect script 定期 check()
  -> 返回 prompt
  -> agent.put_task(prompt, source="reflect")
  -> Agent 执行任务
  -> on_done(result) 记录结果或更新状态
```

反射任务承担几类事情：

- 定时任务和长期目标推进。
- 会话日志归档。
- 历史经验整理。
- 从重复失败和稳定流程中挖掘 SOP candidate。

### 1.5 L4 原始会话挖掘

GenericAgent 用 `memory/L4_raw_sessions/compress_session.py` 压缩历史会话，再写入 `all_histories.txt`。这类数据不会每轮直接注入，而是作为后续检索和离线挖掘素材。

它解决的问题是：很多“自进化”经验不是单轮任务结束时能可靠看出来的，而是在多次相似任务、相似失败、相似用户偏好中逐渐显现。

## 2. Cohort 当前状态

代码中名称多处仍写作 Cohort，本文统一称 Cohort。

### 2.1 已具备的能力

| 能力 | Cohort 当前实现 |
| --- | --- |
| 工作记忆 | `internal/tools/working_checkpoint.go`、`internal/agent/runner.go` |
| SOP 路由 | `sops/index.md`、`sops/meta_sop.md`、Runner 的 `addSOPRouteHint` |
| 长期记忆工具 | `internal/tools/memory_evolution.go` |
| 记忆治理核心 | `internal/evolution/evolution.go` |
| 证据账本 | Runner 维护 `EvidenceLedger` 并传给记忆工具 |
| Final review | Runner 在有长期记忆信号但模型准备结束时追加 review prompt |
| 相关记忆注入 | `internal/contextmgr/relevant_memory.go` |
| SOP candidate 晋级 | `/sop candidates`、`/sop promote <id>` |
| 规则文档 | `sops/memory_sop.md`、`docs/self_evolution_technical_design.md` |

其中 `memory_evolution.go` 已经把长期记忆拆成三步：

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

这比 GenericAgent 更工程化：`start_long_term_update` 只初始化和返回证据，不写经验；`memory_propose_update` 只验证候选，不写文件；`memory_apply_update` 才允许低风险 append，并记录 audit。

### 2.2 已经优于 GenericAgent 的部分

Cohort 不需要照搬 GenericAgent 的动态 Python 分发。当前 Go 版本在几个方面更适合长期维护：

- Tool Registry 和 schema 更类型化，测试边界清楚。
- `EvidenceLedger` 已经把“模型声称验证”和“工具实际验证”区分开。
- `memory_apply_update` 默认只允许低风险 append，并记录 `memory/audit.jsonl`。
- SOP candidate 进入正式 SOP 需要 `/sop promote`，更新 `sops/index.md` 还需要显式确认。
- Context Manager 已有预算管理、history 压缩和相关长期记忆注入入口。

这些应该保留，并作为 Cohort 自进化的安全边界。

### 2.3 主要缺口

| 缺口 | 影响 |
| --- | --- |
| Candidate extraction 仍主要依赖模型当轮自觉 | Agent 可能在有价值任务后直接 final，或提炼质量不稳定 |
| 没有通用生命周期 hook | Runner 中 checkpoint、SOP、memory hint、final review 逻辑会继续堆叠 |
| 缺少 `NoToolPolicy` | 模型无工具调用时，无法系统性拦截空回复、未完成任务早停、大代码块未写文件等问题 |
| 相关记忆检索仍是关键词启发式 | 召回容易漏掉语义相关但关键词不同的经验 |
| 没有 L4 原始会话归档和离线挖掘 | 很难从跨会话重复模式中发现 SOP candidate |
| 没有自主质量闭环 | SOP 晋级后缺少“是否真的减少失败/轮数/误操作”的效果评估 |

## 3. Cohort 应采用的目标架构

建议把自进化定义为一个受控管线，而不是一个“自动改自己”的能力。

```text
Runner / ToolRunner / ContextManager
  -> LifecycleEventBus
       -> EvidenceCollector
       -> CheckpointPolicy
       -> MemorySignalDetector
       -> NoToolPolicy
       -> SessionArchiver
  -> EvolutionManager
       -> CandidateExtractor
       -> CandidateValidator
       -> MemoryWriter
       -> SOPCandidatePromoter
       -> AuditReporter
  -> ReflectWorker
       -> raw session mining
       -> repeated failure mining
       -> stale memory review
       -> SOP candidate quality report
```

### 3.1 生命周期 hook

优先抽象以下事件：

| 事件 | 用途 |
| --- | --- |
| `RunStarted` | 初始化 evidence、memory signal、session metadata |
| `TurnStarted` | 注入 checkpoint、SOP hint、相关记忆 |
| `LLMResponded` | 检查 no-tool、空回复、异常大文本 |
| `ToolStarted` | 记录工具调用意图和风险等级 |
| `ToolFinished` | 生成 evidence、更新 memory signal、处理 next_prompt |
| `TurnFinished` | 决定是否提醒 checkpoint、重读 SOP、长期记忆 |
| `RunFinishing` | final review、候选提取、归档 |
| `RunFinished` | audit、指标、后台挖掘任务入队 |

这样可以避免 Runner 成为所有策略的堆放点。

### 3.2 能力等级

Cohort 应继续使用 C0-C5 晋级模型。

| 等级 | 资产 | 自动化边界 |
| --- | --- | --- |
| C0 | 原子工具 | 代码注册和测试控制 |
| C1 | `sops/*.md` | 命中场景后按需读取 |
| C2 | `update_working_checkpoint` | 当前任务内有效 |
| C3 | `memory/global.md` / project memory | 只允许 verified low-risk append |
| C4 | `memory/reflection/sop_candidates.md` | 可自动追加，但不主动路由 |
| C5 | reviewed `sops/*.md` + `sops/index.md` | 需要人工确认，索引更新必须显式确认 |

这条晋级链路比“自动生成 skill 并立即启用”安全。尤其是 C4 到 C5 必须有人审查触发词、禁止事项和验收标准。

### 3.3 记忆和证据模型

长期记忆 candidate 至少包含：

```text
type
target
scene
trigger_keywords
lesson
recommended_steps
evidence_ids
risk
action
promote_to_sop
sop_title
sop_path
```

校验规则：

- `evidence_ids` 必须出现在 `available_evidence` 中。
- 引用的 evidence 必须 `verified=true`。
- `action` 在 P0/P1 只允许 `append`。
- `risk=medium/high` 默认需要用户确认。
- `target` 只能是 allowlist 中的 memory 文件。
- 内容不能包含 secret、一次性业务正文、易变状态、失败猜测。

### 3.4 后台反射边界

Cohort 的反射任务应该先从只读分析开始：

```text
cohort reflect once --task session-archive
cohort reflect once --task mine-sop-candidates
cohort reflect once --task memory-quality-report
```

早期不要让反射任务直接改代码或自动更新 `sops/index.md`。推荐输出：

- 候选列表。
- 证据来源。
- 置信度。
- 建议目标文件。
- 是否需要人工确认。
- 可以复现的测试或验收步骤。

## 4. 推荐路线图

### P0：稳住现有自进化闭环

目标：让当前已有的 memory / SOP / checkpoint 能力更可靠。

建议任务：

1. 增加 `NoToolPolicy`。
   - 无工具调用且内容为空：继续下一轮并提示修复。
   - 用户要求改文件但模型只输出代码块：提示必须调用 file tool。
   - 已有长期记忆信号但未显式 skip：走 final review。
2. 抽出 `LifecycleEventBus` 的最小接口。
   - 先不做插件系统，只把现有 checkpoint、SOP、memory hint 迁进去。
3. 增强 `EvidenceLedger`。
   - 给 evidence 增加 `kind`、`path`、`command_summary`、`read_back_confirmed`、`safety_flags`。
   - 禁止把原始大输出直接写入 evidence。
4. 给长期记忆工具补测试矩阵。
   - 无证据候选必须拒绝。
   - unverified evidence 必须拒绝。
   - secret-like 文本必须拒绝。
   - `skip=true` 是成功路径。

验收标准：

- 模型早停、大代码块不落盘、空回复能被 Runner 拦截。
- 记忆写入必须能追溯到 verified evidence。
- `/sop promote` 不会在未确认时更新 `sops/index.md`。

### P1：引入 L4 会话归档和候选挖掘

目标：让 Cohort 能从历史会话中发现重复模式。

建议任务：

1. 新增 `internal/evolution/session_archive.go`。
   - 从 session `history.jsonl` 和 `temp/model_responses` 提取轻量摘要。
   - 生成 `memory/raw_sessions/all_histories.md`。
   - 原始内容按月份归档，默认不注入上下文。
2. 新增 `cohort reflect once` 命令。
   - 支持 `session-archive`、`mine-sop-candidates`、`memory-quality-report`。
   - 第一期只读分析，输出 report。
3. 新增候选挖掘器。
   - 输入 L4 摘要、audit、失败恢复记录、SOP 读取记录。
   - 输出 C4 SOP candidate，不直接晋级 C5。
4. 给候选挖掘器加去重。
   - 相同 `scene + trigger_keywords + lesson` 合并。
   - 相似候选只增加 evidence，不重复追加。

验收标准：

- 可以离线生成 `memory/raw_sessions/all_histories.md`。
- 可以从多次相似任务中生成 SOP candidate。
- 生成结果不包含用户业务正文和敏感信息。

### P2：升级相关记忆检索

目标：降低长期记忆“写了但用不上”的概率。

建议任务：

1. 将 `relevant_memory.go` 从纯关键词扩展为结构化索引。
   - entry title、trigger keywords、scene、target、evidence id 单独建索引。
   - 保留关键词检索作为 fallback。
2. 增加可选 embedding 检索。
   - 默认关闭或本地配置启用。
   - 命中结果必须保留 source 和 score。
3. 注入策略保持克制。
   - 只注入最相关的少量 entry。
   - 对外部状态仍要求工具验证。

验收标准：

- 同义任务能召回相关 memory。
- 注入内容有来源、分数、命中原因。
- 不把完整 memory 文件塞进上下文。

### P3：质量门禁下的自主改进

目标：允许 Cohort 在受控范围内提出改进，但不自动越权。

建议任务：

1. 建立 SOP 质量评分。
   - 触发词是否清晰。
   - 禁止事项是否明确。
   - 验收标准是否可执行。
   - 是否有至少一个 verified evidence。
2. 建立效果指标。
   - 相同场景平均 turn 数是否下降。
   - 失败恢复次数是否下降。
   - 用户打断或纠错是否减少。
   - 记忆命中后是否真的被使用。
3. 允许生成 helper 草案。
   - 只能写到 `memory/reflection/helper_candidates/` 或 report。
   - 进入正式工具或 SOP 前必须人工确认和测试。

验收标准：

- 反射任务能输出“建议晋级 / 建议废弃 / 需要更多证据”的报告。
- 任何影响主动路由或代码执行面的变更都需要人工确认。
- 自动生成的 helper 不会默认进入 Tool Registry。

## 5. 推荐的近期实现顺序

如果只做一轮迭代，建议按这个顺序：

1. 实现 `NoToolPolicy`。
2. 抽出 Runner 生命周期事件的最小结构。
3. 增强 EvidenceLedger 字段和记忆测试。
4. 新增只读 `reflect once --task session-archive`。
5. 新增 `mine-sop-candidates`，只写 C4 candidate 或 report。
6. 再考虑语义检索和自主质量评估。

原因是：没有 no-tool 和 evidence 稳定性，后面的自动挖掘会把不可靠行为放大；没有 L4 归档，Cohort 很难从跨会话经验中真正进化。

## 6. 风险和边界

### 不应自动化的事情

- 自动修改系统提示词核心规则。
- 自动更新 `sops/index.md` 主动路由。
- 自动注册新工具。
- 自动把 helper 候选加入执行路径。
- 自动保存未验证推理或用户一次性业务数据。

### 应该默认允许的事情

- 低风险、已验证、非重复的长期记忆 append。
- SOP candidate 追加到候选区。
- 只读会话归档和质量报告。
- 对候选提出人工确认建议。

### 失败模式

| 失败模式 | 防护 |
| --- | --- |
| 错误经验被长期保存 | EvidenceLedger + No Execution, No Memory + allowlist target |
| 候选 SOP 过多 | 去重、合并 evidence、质量评分 |
| 记忆污染上下文 | 索引指针优先，少量相关 entry 注入 |
| 后台反射越权 | reflect 初期只读，写入只到 candidate/report |
| 过早自信 final | NoToolPolicy + final review |

## 7. 一句话方案

Cohort 的自进化应该走“证据账本驱动的经验晋级系统”：运行时记录 verified evidence 和 checkpoint，收尾时受控生成长期记忆候选，跨会话由 reflect worker 挖掘 SOP candidate，只有经过质量门禁和人工确认的候选才进入正式 SOP、索引或工具执行面。
