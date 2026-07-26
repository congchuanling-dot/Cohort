# Cohort 与 GenericAgent 当前差距分析

> 文档状态：`[部分完成]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 本文保留当前差距，但已按 MCP P0/P1 基础、工具级 `run.log` 和渐进式 Skill Runtime
> 实现修正。未完成的核心差距是 FinishGuard / NoToolPolicy、文本工具兜底、完整 lifecycle hook、
> Project/Plan Mode、多模型、全局 doctor、daemon 和长期自治，而不是早期 MVP 基础能力。

## 结论摘要

仓库里已有的 `docs/cohert_vs_ga_gap.md` 是早期 MVP 阶段的差距清单，其中“无 session、无浏览器、无长期记忆、无工作记忆”等判断已经过期。当前 Cohort 已经补齐并部分增强了这些基础能力：

- 会话落盘、list、resume。
- Context Manager、工具结果压缩、token 预算。
- Chrome browser bridge、DOM scan、snapshot、点击、输入、等待、截图、OCR。
- macOS desktop sensing 和受控真实输入。
- `update_working_checkpoint`。
- `start_long_term_update` / `memory_propose_update` / `memory_apply_update`。
- SOP 路由、SOP candidate 和 `/sop promote`。
- 渐进式 Skill Runtime：本地/Git 安装、预览确认、版本锁定、manifest hash、`skill_read`、
  `/skill run` 和 `/<skill-alias>`。
- EvidenceLedger、长期记忆写入审计。
- Go 单元测试体系，当前 `go test ./...` 通过。

所以现在的问题不是“Cohort 还缺不缺 Agent MVP”，而是：

```text
Cohort 已经是一个更工程化、更安全的 Go Agent Runtime；
GA 仍然是一个更成熟、更自举、更前端完备、更会长期自治的个人 Agent 系统。
```

一句话差距：

```text
Cohort 强在类型边界、受控工具、安全确认、显式 MCP/Skill 装配、证据化记忆和可测试性；
GA 强在自举生态、前端入口、计划/目标/反射、多模型适配、低 token 成本和长期使用经验。
```

## 1. 当前对照总表

| 领域 | GA 当前优势 | Cohort 当前状态 | 差距判断 |
| --- | --- | --- | --- |
| Agent Loop 韧性 | 有 `no_tool` 自修复、`turn_end_callback`、done hooks | 有工具循环、bad JSON、final review，但 no-tool 治理弱 | Cohort 仍缺保守的 `FinishGuard` 和生命周期 hook |
| 工具协议 | 原生 tool use + 文本 `<tool_use>` 兜底 | 主要依赖 OpenAI-compatible 原生 tool_calls | Cohort 缺严格文本工具兜底 |
| 文件写入协议 | 大文本用 `<file_content>` 承载，避免 JSON 参数污染 | `file_write` 直接从参数接收内容 | Cohort 缺大内容写入协议和变更摘要 |
| 浏览器 | TMWebDriver 注入真实浏览器，长期实战 SOP 多 | Chrome bridge 已有高层工具和 OCR | Cohort 基础能力已接近，但缺 GA 的高权限诊断与长期 SOP 积累 |
| 桌面/OS | 鼠标键盘、视觉、ADB、移动设备能力更广 | macOS AX + OCR + 受控输入更安全 | Cohort 更安全，GA 覆盖面更广 |
| 自进化 | L1-L4 记忆、Skill crystallization、L4 会话归档 | 有受控长期记忆、SOP candidate 和人工 promote | Cohort 缺自动候选挖掘和 L4 历史挖掘 |
| Skill / 工作流生态 | 自动 crystallize skill，长期使用中形成个人能力树 | 有渐进式 Skill Runtime、安装预览、版本锁定、doctor、`skill_read`、`/skill run` | Cohort 已有安全安装与调用基础，但缺自动挖掘、内置常用包和运行时权限拦截 |
| Project Mode | `plugins/project_mode.py` 通过 hook 注入项目记忆指针 | 有项目记忆路径，但缺显式 Project Mode | Cohort 缺项目级 bootstrap 和命令 |
| Plan Mode | `plan.md` 状态机、验证拦截、前端 plan bar | 只有文档建议，无实现 | Cohort 缺计划工具和验证态 |
| Goal / Autonomous | `reflect/goal_mode.py`、`reflect/autonomous.py` | 无后台反射执行器 | Cohort 缺长期目标和自主报告 |
| 多 Agent | Goal Hive、BBS worker、subagent helper | 无 subagent 运行器 | Cohort 缺进程/会话隔离的并行协作 |
| 前端 | TUI v3、桌面 GUI、Streamlit、Qt、IM bot、conductor | CLI/REPL 为主 | Cohort 前端入口明显少 |
| 多模型 | Claude/OpenAI/Gemini 等适配、thinking、prompt cache、fallback | OpenAI-compatible，`provider` 字段预留 | Cohort 缺 provider profile、fallback、Anthropic 原生 |
| 用量/成本 | token/cost tracker、cache 命中展示 | 有 context stats，但无完整 usage/cost | Cohort 缺用量闭环 |
| 安装运维 | 一键安装、桌面包、配置向导、hub/service 面板 | 本地 go run / go build | Cohort 缺 install/doctor/service 管理 |
| 可观测性 | Langfuse hook、前端 token 页面、服务日志 | raw model response、context.log、memory audit、工具级 `run.log` | Cohort 缺完整生命周期事件和 tracing sink |
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

### 2.4 渐进式 Skill Runtime

早期差距文档把 Skill 能力归为后续生态项。当前这个判断已经过期。

Cohort 现在具备：

- 项目级 `.cohort/skills/<name>/SKILL.md` 和用户级 `~/.cohert/skills/<name>/SKILL.md`。
- `skill install` 安装前预览候选 `SKILL.md`，并明确提示安装阶段不会执行命令、安装依赖或授权 MCP。
- `--dry-run`、`--yes`、`--force`、`--scope project|user`、`--name`、Git URL 安装和 `--pin` 版本锁定。
- `.cohert-skill.json` 记录 source、ref、resolved commit、pinned、alias、installed_at 和 content hash。
- `skill update`、`skill update --check`、`skill uninstall`、`skill doctor`。
- 启动时只把 Skill 摘要注入系统提示词，命中后用 `skill_read` 按需读取完整正文。
- REPL 支持 `/skill run <id>` 和声明 `user-invocable: true` 的 `/<skill-alias>`。

这部分 Cohort 已经比“只靠 SOP 文档”前进了一大步，但和 GA 的差距仍然存在：GA 更强在自动从任务经验中 crystallize skill、长期个人能力树、前端入口和大量实战 SOP；Cohort 当前更强在安装安全、版本可追溯和显式依赖诊断。

## 3. Cohort 仍明显落后的部分

### 3.1 FinishGuard / NoToolPolicy 和早停治理

GA 在没有工具调用时会进入 `no_tool` 分支，能处理：

- 空回复。
- max_tokens 截断。
- 计划模式下过早宣称完成。
- 没验证就 final。
- done hook 追加检查。

Cohort 当前在 `len(resp.ToolCalls)==0` 时主要做长期记忆 final review，然后结束。这里确实缺少一个无工具回复的结束守卫，但它不能破坏 Agent Loop 的核心规则。

设计风险记录：

- Agent Loop 的基本语义应保持为“模型不再调用工具 => 默认结束”。
- 因此这里不应实现成激进的“无 tool 就强制继续”，否则会让 runtime 和模型互相拉扯，破坏 Claude Code / GA 这类 Agent 的自然停止机制。
- 更准确的命名应是 `FinishGuard`，`NoToolPolicy` 只作为历史叫法。
- 默认行为必须是放行；只在强异常下拦截，例如空回复、流式中断、`max_tokens` 截断、plan 模式未验证却声称完成、几乎只有大代码块但无工具调用。
- 对“用户要求修改文件但模型未调用文件工具”这类场景，应先谨慎做成可观测 warning 或一次性重试，不能无限 retry。

建议补齐：

```text
LLMResponded
  -> 如果无 tool_calls
     -> 默认允许结束
     -> 仅强异常进入 FinishGuard
     -> 空回复 / 流异常 / max_tokens / 大代码块误输出 / plan 未验证
     -> 最多注入一次具体 retry prompt，避免循环纠缠
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
- 覆盖 LLM、compact、session 事件的完整 `run.log`。
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

### 3.7 Skill 自举、内置包和运行时权限

GA 的 Skill 生态不是只靠手动安装。它的核心优势是“做过一次的任务会沉淀成未来可复用能力”，并且项目内已经有大量实战 SOP 和前端入口。

Cohort 当前 Skill Runtime 解决了安装、版本、读取和诊断，但还缺：

- 自动从 session / run.log / evidence 中挖掘候选 Skill。
- `commit`、`code-review`、`unit-test`、`debug` 等内置高频 Skill 包。
- `SKILL.md` frontmatter 中的 `permissions` 声明。
- `/skill run` 期间的 active policy：调用未声明 tools / MCP / commands 时提示确认。
- Skill 运行后的质量反馈、失败归因和更新建议。

建议顺序：

```text
内置常用 Skill 包
  -> permissions frontmatter
  -> /skill run active policy
  -> session/evidence 挖掘 Skill candidate
  -> candidate review 后安装或更新
```

优先级：P1。

### 3.8 多 Agent 协作

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

### 3.9 前端和入口生态

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

### 3.10 多模型、fallback、cache 和 usage

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

### 3.11 安装、doctor 和运维

GA 有安装脚本、配置向导、桌面打包和 hub/service 管理。Cohort 目前需要在项目目录里 `go run .`，没有覆盖全局运行环境的 doctor 总入口。

Cohort 缺：

- `cohert doctor`。
- `cohert skill doctor` 已有，但还缺覆盖全局运行环境的总入口。
- API key / model connectivity 检查。
- browser bridge 检查。
- Python helper 依赖检查。
- macOS 权限检查汇总。
- workspace/log/session 目录权限检查。
- install script。

优先级：P0-P1。

## 4. Cohort 应追但不能裸露底层风险的部分

| GA 能力 | 直接照搬的风险 | Cohort 应取的路线 |
| --- | --- | --- |
| 任意 OS 鼠标键盘输入 | 裸 `click/type/key` 容易越过用户意图和 UI 安全边界 | 作为长期目标推进 Computer Use 跨 OS 操作层：对模型暴露 `computer_see/find/click/type/press/check/wait`，底层输入 API 不直接暴露给模型，动作必须绑定目标窗口、风险等级、可中断控制和审计日志 |
| 普通任务暴露 cookie/extension management | 高敏感权限 | 仅诊断模式或显式授权 |
| 自动安装复杂依赖 | 跨平台和供应链风险 | doctor 提示，用户确认后安装 |
| 先做全量 IM 前端 | 维护面大，偏离内核稳定 | 先 TUI/local API/daemon |
| Python 动态工具分发 | 会削弱 Go 类型边界和测试性 | 保持 Tool Registry |
| 直接自动晋级 Skill | 错误经验会污染未来任务 | 保持 SOP candidate + 人工 promote |
| 后台自动改代码 | 难审计，容易破坏工作区 | 后台只产 report/candidate |

这里的关键调整是：Cohort 的最终目标可以是模拟人类操作电脑，完成一切人类可以通过 GUI 完成的操作；但实现方式不能是把任意键鼠原语直接交给模型自由组合，而应建设可观测、可验证、可中断、可审计的 Computer Use 操作层。独立方案见 [human_os_operation_technical_design.md](human_os_operation_technical_design.md)。

## 5. 建议的补齐优先级

### P0：Agent Loop 韧性和可观测性

1. `FinishGuard` / `NoToolPolicy`。
2. 严格文本 `<tool_use>` 兜底。
3. Lifecycle event 内部接口。
4. `run.log` 从工具审计扩展为完整 JSONL 事件流。
5. `cohert doctor` 总入口。
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
5. 内置常用 Skill 包和 `/skill run` active policy。
6. 多模型 profile 和 fallback。
7. usage/cost 统计。

验收：

- 多步骤任务能按计划推进和验证。
- 项目约定可被稳定召回。
- 历史会话能沉淀为候选经验。

### P2：扩展和协作

1. MCP import/export、旧 SSE 兼容和真实服务矩阵验收。
2. LSP tools，先接 `gopls`。
3. Computer Use 跨 OS 操作层 M0/M1：先落 `computer_see`、`computer_find`、`computer_click`、`computer_type`，再接确认后的 `computer_press`、`computer_check`、`computer_wait`。
4. Plugin manifest。
5. 只读 subagent / explorer。
6. TUI。
7. tracing sink。

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
FinishGuard / NoToolPolicy
  -> 文本 tool_use 兜底
  -> Lifecycle Hook / run.log 事件流
  -> Diff / cohert doctor
  -> Project / Plan Mode
  -> 内置 Skill 包 / runtime permissions
  -> Reflect archive
  -> MCP 验收矩阵 / LSP / TUI
  -> Daemon / 多端
```

这样 Cohort 才能在保持安全和工程质量的前提下，逐步追上 GA 的自举能力和使用体验。
