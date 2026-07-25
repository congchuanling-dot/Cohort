# Cohort 与 GenericAgent 当前差距分析

## 结论摘要

仓库里已有的 `docs/cohert_vs_ga_gap.md` 是早期 MVP 阶段的差距清单，其中“无 session、无浏览器、无长期记忆、无工作记忆”等判断已经过期。当前 Cohort 已经补齐并部分增强了这些基础能力：

- 会话落盘、list、resume。
- Context Manager、工具结果压缩、token 预算。
- Chrome browser bridge、DOM scan、snapshot、点击、输入、等待、截图、OCR。
- macOS desktop sensing 和受控真实输入。
- `update_working_checkpoint`。
- `start_long_term_update` / `memory_propose_update` / `memory_apply_update`。
- SOP 路由、SOP candidate 和 `/sop promote`。
- EvidenceLedger、长期记忆写入审计。
- Go 单元测试体系，当前 `go test ./...` 通过。

所以现在的问题不是“Cohort 还缺不缺 Agent MVP”，而是：

```text
Cohort 已经是一个更工程化、更安全的 Go Agent Runtime；
GA 仍然是一个更成熟、更自举、更前端完备、更会长期自治的个人 Agent 系统。
```

一句话差距：

```text
Cohort 强在类型边界、受控工具、安全确认、证据化记忆和可测试性；
GA 强在自举生态、前端入口、计划/目标/反射、多模型适配、低 token 成本和长期使用经验。
```

## 1. 当前对照总表

| 领域 | GA 当前优势 | Cohort 当前状态 | 差距判断 |
| --- | --- | --- | --- |
| Agent Loop 韧性 | 有 `no_tool` 自修复、`turn_end_callback`、done hooks | 有工具循环、bad JSON、final review，但 no-tool 治理弱 | Cohort 仍缺 `NoToolPolicy` 和生命周期 hook |
| 工具协议 | 原生 tool use + 文本 `<tool_use>` 兜底 | 主要依赖 OpenAI-compatible 原生 tool_calls | Cohort 缺严格文本工具兜底 |
| 文件写入协议 | 大文本用 `<file_content>` 承载，避免 JSON 参数污染 | `file_write` 直接从参数接收内容 | Cohort 缺大内容写入协议和变更摘要 |
| 浏览器 | TMWebDriver 注入真实浏览器，长期实战 SOP 多 | Chrome bridge 已有高层工具和 OCR | Cohort 基础能力已接近，但缺 GA 的高权限诊断与长期 SOP 积累 |
| 桌面/OS | 鼠标键盘、视觉、ADB、移动设备能力更广 | macOS AX + OCR + 受控输入更安全 | Cohort 更安全，GA 覆盖面更广 |
| 自进化 | L1-L4 记忆、Skill crystallization、L4 会话归档 | 有受控长期记忆和 SOP candidate | Cohort 缺自动候选挖掘和 L4 历史挖掘 |
| Project Mode | `plugins/project_mode.py` 通过 hook 注入项目记忆指针 | 有项目记忆路径，但缺显式 Project Mode | Cohort 缺项目级 bootstrap 和命令 |
| Plan Mode | `plan.md` 状态机、验证拦截、前端 plan bar | 只有文档建议，无实现 | Cohort 缺计划工具和验证态 |
| Goal / Autonomous | `reflect/goal_mode.py`、`reflect/autonomous.py` | 无后台反射执行器 | Cohort 缺长期目标和自主报告 |
| 多 Agent | Goal Hive、BBS worker、subagent helper | 无 subagent 运行器 | Cohort 缺进程/会话隔离的并行协作 |
| 前端 | TUI v3、桌面 GUI、Streamlit、Qt、IM bot、conductor | CLI/REPL 为主 | Cohort 前端入口明显少 |
| 多模型 | Claude/OpenAI/Gemini 等适配、thinking、prompt cache、fallback | OpenAI-compatible，`provider` 字段预留 | Cohort 缺 provider profile、fallback、Anthropic 原生 |
| 用量/成本 | token/cost tracker、cache 命中展示 | 有 context stats，但无完整 usage/cost | Cohort 缺用量闭环 |
| 安装运维 | 一键安装、桌面包、配置向导、hub/service 面板 | 本地 go run / go build | Cohort 缺 install/doctor/service 管理 |
| 可观测性 | Langfuse hook、前端 token 页面、服务日志 | raw model response、context.log、memory audit | Cohort 缺统一 `run.log` 和 tracing sink |
| 安全治理 | 实战灵活，但工具边界更宽 | 桌面 R1/R2/R3、confirmation token、EvidenceLedger | Cohort 在安全模型上更强 |

## 2. Cohort 已经不落后的部分

### 2.1 基础 Agent Runtime

旧差距文档认为 Cohort 只是命令行 MVP。当前已经不准确。

Cohort 现在具备：

- `internal/agent/runner.go`：完整工具循环、最大轮次、history、checkpoint、长期记忆信号。
- `internal/session`：`history.jsonl`、`meta.json`、session list/resume。
- `internal/contextmgr`：上下文预算、tool result 压缩、compact、相关长期记忆注入。
- `internal/tools`：文件、命令、浏览器、桌面、记忆工具。
- `internal/evolution`：长期记忆验证、写入、审计、SOP candidate 晋级。

当前 `go test ./...` 通过，说明 Cohort 的工程回归能力已经比 GA 的探索型 Python 核心更清晰。

### 2.2 受控长期记忆

GA 的长期记忆更自由、更自举；Cohort 的长期记忆更安全、更可审计。

Cohort 优势：

- `start_long_term_update` 只返回 evidence 和策略，不直接写入。
- `memory_propose_update` 只验证候选，不写文件。
- `memory_apply_update` 只允许低风险 append。
- 每个 candidate 必须引用 verified evidence。
- 写入后有 audit 和 read-back confirmation。
- SOP candidate 到正式 SOP 需要 `/sop promote`，更新 `sops/index.md` 需要显式确认。

这套机制不如 GA 灵活，但更适合长期工程维护。

### 2.3 桌面输入安全

GA 的 OS 控制覆盖面广，但风险边界更宽。Cohort 的 desktop 工具链更保守：

- 优先 AX 语义控件。
- OCR bbox 不能直接当屏幕坐标。
- 视觉点击必须绑定 screenshot manifest。
- R2 操作需要一次性 confirmation token。
- R3 高风险操作拒绝自动执行。
- `desktop_type_text` 只起草，不发送。

这部分 Cohort 不必追求“像 GA 一样什么都能点”，应保留当前安全边界。

## 3. Cohort 仍明显落后的部分

### 3.1 NoToolPolicy 和早停治理

GA 在没有工具调用时会进入 `no_tool` 分支，能处理：

- 空回复。
- max_tokens 截断。
- 计划模式下过早宣称完成。
- 没验证就 final。
- done hook 追加检查。

Cohort 当前在 `len(resp.ToolCalls)==0` 时主要做长期记忆 final review，然后结束。缺少通用 no-tool 策略。

建议补齐：

```text
LLMResponded
  -> 如果无 tool_calls
     -> 判断是否应继续
     -> 空回复 / 大代码块 / 文件任务未落盘 / 计划未验证
     -> 注入 retry prompt
```

优先级：P0。

### 3.2 文本工具调用兜底

GA 的 `llmcore.py` 支持模型输出：

```text
<tool_use>{"name":"file_read","arguments":{"path":"README.md"}}</tool_use>
```

即使模型没有走原生 tool calling，也能解析文本工具块。Cohort 目前主要依赖 OpenAI-compatible tool_calls。对于某些模型或中转服务，原生工具调用不稳时会丢执行能力。

建议：

- 只支持严格 `<tool_use>{...}</tool_use>`。
- 不支持过多弱格式，避免误解析用户文本。
- 解析失败返回 `bad_json` 风格下一轮提示。
- 展示给用户的正文要剥离 tool block。

优先级：P0。

### 3.3 生命周期 Hook 和结构化运行日志

GA 有 `plugins/hooks.py`，并通过 `agent_before/after`、`llm_before/after`、`tool_before/after` 等事件支持 Project Mode、Langfuse tracing 等能力。Cohort 的策略仍集中在 Runner 里。

Cohort 缺：

- 通用 lifecycle event。
- `run.log` JSONL。
- tracing sink。
- policy sink。
- diff/evidence/context 统一事件流。

建议第一版不要做动态插件，只做内部事件接口：

```text
SessionStart
UserPromptSubmit
TurnStart
LLMResponded
PreToolUse
PostToolUse
PostToolBatch
PreCompact
PostCompact
RunFinishing
SessionEnd
```

优先级：P0。

### 3.4 Project Mode

GA 的 `plugins/project_mode.py` 能把项目记忆作为轻量指针注入，并让模型按需读取详情。Cohort 虽然有 project memory 路径，但没有显式项目模式。

Cohort 缺：

- `.cohort/project.md` 或等价 bootstrap。
- `/init`、`/project`、`/project memory`。
- 项目级 commands/skills/hooks 配置。
- 每轮只注入项目指针而不是全文的策略。

建议：

```text
.cohort/
  project.md
  commands/
  skills/
  settings.yaml
```

优先级：P1。

### 3.5 Plan Mode 和验证态

GA 有 `memory/plan_sop.md`、`ga.py` 中的 `enter_plan_mode`、前端 plan bar、验证拦截。它把复杂任务变成外部 `plan.md` 状态机。

Cohort 目前只有文档建议，没有实际 plan 命令或状态机。

Cohort 缺：

- `/plan start/status/next/done/stop`。
- `workspace/plans/<name>/plan.md`。
- 每轮执行前读 plan。
- 完成步骤后 patch plan。
- 未验证不得宣称完成。
- 独立验证 session。

建议先做轻量 Plan Mode，不急着做 subagent。

优先级：P1。

### 3.6 Goal / Autonomous / Reflect

GA 支持：

- `agentmain.py --reflect reflect/scheduler.py`
- `reflect/goal_mode.py`
- `reflect/autonomous.py`
- L4 session archive 定时归档。
- 自主行动报告。

Cohort 目前没有后台 reflect runner。

Cohort 缺：

- `cohert reflect once`。
- `cohert reflect scheduler`。
- session archive。
- failure pattern mining。
- SOP candidate mining。
- autonomous report。

建议先做只读：

```text
cohert reflect once --task session-archive
cohert reflect once --task mine-sop-candidates
cohert reflect once --task memory-quality-report
```

优先级：P1-P2。

### 3.7 多 Agent 协作

GA 有 Goal Hive、BBS worker、UltraPlan subagent 等协作机制。Cohort 当前没有 subagent runner。

Cohort 缺：

- 子任务 session 隔离。
- 子 agent tool allowlist。
- 并行任务取消和超时。
- 结果合并。
- 验证 agent。
- 多 worker 通讯板。

建议不要马上照搬 GA 的 heavy subagent。先做独立验证 session，再做只读 explorer，再考虑并行 worker。

优先级：P2-P3。

### 3.8 前端和入口生态

GA 有：

- TUI v3。
- Streamlit / Qt / Desktop GUI。
- Telegram、Discord、微信、QQ、飞书、企业微信、钉钉等 IM 前端。
- conductor 协作界面。
- token/cost 页面。
- service 面板。
- one-line installer 和 desktop package。

Cohort 目前主要是 CLI/REPL。

Cohort 缺：

- TUI。
- 任务/计划面板。
- diff 面板。
- 服务管理。
- 安装器。
- desktop app。
- IM adapter。

建议顺序：

```text
CLI polish -> TUI -> local API/daemon -> IDE/Web/IM adapter
```

不要直接做多 IM 前端。

优先级：P2-P3。

### 3.9 多模型、fallback、cache 和 usage

GA 的 `llmcore.py` 适配面更广：

- Claude Messages。
- OpenAI-compatible。
- Gemini/Kimi/MiniMax 等配置。
- thinking budget。
- prompt cache / cache_control。
- token usage 输出。
- 多模型 fallback。

Cohort 当前：

- OpenAI-compatible client。
- 配置中有 `provider` 字段。
- Context window 按模型名映射。
- 还没有多 profile、fallback、Anthropic 原生、usage/cost 汇总。

建议：

```yaml
llm:
  active_profile: primary
  profiles:
    primary:
      provider: openai
      api_base: ...
      model: ...
    backup:
      provider: openai
      api_base: ...
      model: ...
```

优先级：P1-P2。

### 3.10 安装、doctor 和运维

GA 有安装脚本、配置向导、桌面打包和 hub/service 管理。Cohort 目前需要在项目目录里 `go run .`，没有 doctor。

Cohort 缺：

- `cohert doctor`。
- API key / model connectivity 检查。
- browser bridge 检查。
- Python helper 依赖检查。
- macOS 权限检查汇总。
- workspace/log/session 目录权限检查。
- install script。

优先级：P0-P1。

## 4. Cohort 不应追 GA 的部分

| GA 能力 | 不建议直接追的原因 | Cohort 应取的路线 |
| --- | --- | --- |
| 任意 OS 鼠标键盘输入 | 容易越过用户意图和 UI 安全边界 | 保持 AX/manifest/token 受控输入 |
| 普通任务暴露 cookie/extension management | 高敏感权限 | 仅诊断模式或显式授权 |
| 自动安装复杂依赖 | 跨平台和供应链风险 | doctor 提示，用户确认后安装 |
| 先做全量 IM 前端 | 维护面大，偏离内核稳定 | 先 TUI/local API/daemon |
| Python 动态工具分发 | 会削弱 Go 类型边界和测试性 | 保持 Tool Registry |
| 直接自动晋级 Skill | 错误经验会污染未来任务 | 保持 SOP candidate + 人工 promote |
| 后台自动改代码 | 难审计，容易破坏工作区 | 后台只产 report/candidate |

## 5. 建议的补齐优先级

### P0：Agent Loop 韧性和可观测性

1. `NoToolPolicy`。
2. 严格文本 `<tool_use>` 兜底。
3. Lifecycle event 内部接口。
4. `run.log` JSONL。
5. `doctor`。
6. `/diff` 和文件变更摘要。

验收：

- 模型该调用工具时不会轻易直接 final。
- 工具调用失败、空回复、大代码块未落盘都能自修复。
- 每轮执行能从 `run.log` 追踪。
- 用户能看清本轮改动。

### P1：项目和长任务

1. Project Mode。
2. Plan Mode。
3. 独立验证 session。
4. L4 session archive。
5. 多模型 profile 和 fallback。
6. usage/cost 统计。

验收：

- 多步骤任务能按计划推进和验证。
- 项目约定可被稳定召回。
- 历史会话能沉淀为候选经验。

### P2：扩展和协作

1. MCP client。
2. LSP tools，先接 `gopls`。
3. Skill / Plugin manifest。
4. 只读 subagent / explorer。
5. TUI。
6. tracing sink。

验收：

- 外部工具能安全进入 Tool Registry。
- 代码智能不只靠 `rg`。
- 复杂任务可有独立只读验证。

### P3：常驻和多端

1. daemon / local API。
2. scheduler / monitor。
3. autonomous reports。
4. 多 agent worker。
5. desktop/web/IM adapter。

验收：

- 后台任务可取消、可审计、有权限边界。
- 多端只是入口，不改变核心安全模型。

## 6. 最终判断

Cohort 和 GA 的差距已经从“基础能力缺失”转为“生态成熟度和长期自治能力差距”。

更准确的定位是：

```text
GA = 极小核心 + 大量自举经验 + 多前端 + 长期自治个人 Agent。
Cohort = Go 类型化内核 + 受控工具 + 安全桌面输入 + 证据化记忆 + 可测试 Agent Runtime。
```

Cohort 下一阶段不应该照搬 GA 的所有外围能力，而应优先把运行时打稳：

```text
NoToolPolicy
  -> Lifecycle Hook
  -> run.log
  -> Diff/Doctor
  -> Project/Plan Mode
  -> Reflect archive
  -> MCP/LSP/TUI
  -> Daemon/多端
```

这样 Cohort 才能在保持安全和工程质量的前提下，逐步追上 GA 的自举能力和使用体验。
