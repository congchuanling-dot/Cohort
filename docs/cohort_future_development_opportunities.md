# Cohort 后续值得开发的能力建议

> 文档状态：`[规划]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 本文是能力池和中长期路线，不是实现清单。当前已落地 MCP P0、MCP 权限与工具级
> `run.log`、浏览器/桌面/SOP/受控长期记忆、`NoToolPolicy`、交互式 diff、Project/Plan Mode、
> Plugin manifest 第一版、Go/TypeScript/Python 只读诊断入口、可运行只读 explorer 和轻量 TUI 子视图；
> Lifecycle Hook、daemon、真正并行 subagent、全屏 TUI 和 definition/references 等完整 LSP 查询仍按本文路线保留为未完成规划。

## 结论摘要

Cohort 当前已经不是早期 MVP：它已经具备 CLI/REPL、会话恢复、上下文压缩、浏览器自动化、桌面 Computer Use、SOP 路由、工作记忆和受控长期记忆。接下来最值得做的不是继续横向堆工具，而是把它从“能执行任务的本地 Agent”推进到“可扩展、可观察、可协作、可长期运行的开发者 Agent Runtime”。

建议优先发展五条主线：

| 主线 | 目标 | 代表能力 |
| --- | --- | --- |
| 开发者体验 | 让 Cohort 更像每天可用的编码助手 | 交互式 diff、计划模式、任务面板、doctor、运行日志 |
| 扩展体系 | 让外部能力能安全接入 | Skill/Plugin manifest、MCP、LSP、Hook、Monitor |
| 长任务治理 | 让复杂任务可拆、可查、可恢复 | Plan Mode、验证 session、子任务 lane、checkpoint 质量检查 |
| 常驻自动化 | 让 Cohort 从 CLI 进化为本地 daemon | Gateway、调度、消息路由、后台 monitor、通知 |
| 安全与治理 | 让更强能力不破坏边界 | Permission policy、sandbox、审批、审计、成本和 token 预算 |

一句话路线：

```text
先补开发者体验和可观测性，再做扩展协议和计划模式，最后再做常驻网关、多渠道和多 Agent。
```

## 1. 参考对象

### 1.1 Claude Code 值得借鉴什么

Claude Code 的强项不是某一个工具，而是一整套开发者工作流平台能力：

- Skills/Commands：用 Markdown 组织可复用工作流。
- Plugins：插件可以包含 Skills、Agents、Hooks、MCP servers、LSP servers、Monitors、Themes。
- Hooks：在 SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、FileChanged、PreCompact、PostCompact、SessionEnd 等生命周期点执行动作。
- Subagents：通过专门 agent 执行代码审查、检索、验证等任务。
- MCP：把外部系统能力纳入工具体系。
- LSP：让 Agent 看到实时诊断、定义跳转、引用搜索、类型信息。
- Checkpointing：复杂代码修改中可以回退。

对 Cohort 的启发：应该把“工具”上升为“可安装、可审计、可禁用、可配置的能力包”，并把 Runner 生命周期事件显式化。

### 1.2 OpenClaw 值得借鉴什么

OpenClaw 的强项是自托管、常驻、可编程自动化框架：

- 本地/自托管优先，支持 OpenAI、Anthropic、Ollama 等 BYOM。
- 多渠道消息路由，能接 Telegram、Discord、WhatsApp、Slack、Signal、iMessage 等。
- 持久上下文管理，支持多轮会话和可插拔存储。
- 插件架构，支持内置集成和自定义扩展。
- 工作流引擎，支持 TypeScript/YAML 描述触发器、条件和动作。
- 常驻 gateway/daemon，支持 heartbeat、cron、后台任务。
- 安全策略，强调可审计、可自部署、权限可控。

对 Cohort 的启发：Cohort 可以继续保持 Go 内核和本地优先，但应逐步提供 daemon、API gateway、任务队列和 channel adapter，而不是永远只停留在交互式 CLI。

## 2. Cohort 当前基础

从仓库现状看，Cohort 已经具备这些可复用基座：

| 模块 | 当前资产 |
| --- | --- |
| Agent Loop | `internal/agent/runner.go`，支持工具循环、最大轮次、长期记忆 final review |
| 工具注册 | `internal/tools/registry.go`，已有文件、命令、浏览器、桌面、记忆工具 |
| 会话 | `internal/session`，支持 `history.jsonl`、list、resume |
| 上下文 | `internal/contextmgr`，支持 token 预算、tool result 压缩、compact、相关长期记忆 |
| 浏览器 | Chrome bridge、DOM scan、snapshot、真实点击输入、wait、screenshot、OCR |
| 桌面 | macOS AX、截图、OCR、受控点击、受限按键、一次性确认 token |
| 记忆 | `start_long_term_update`、`memory_propose_update`、`memory_apply_update`、EvidenceLedger |
| SOP | `sops/index.md`、`sops/*_sop.md`、SOP candidate/promote |
| 文档 | 已有 GA 借鉴、自进化、浏览器、桌面、上下文等设计文档 |

因此后续重点不是补“有没有某个工具”，而是补：

- 任务如何被计划和验证。
- 能力如何被安装和治理。
- 运行过程如何被观察和复盘。
- 多渠道和常驻任务如何安全接入。

## 3. 最值得开发的能力清单

### 3.1 交互式 Diff 与变更审阅

**价值**

Cohort 现在能改文件，但用户对“改了什么、是否接受、是否回滚”的控制还不够强。编码 Agent 的核心体验应该是让用户快速审阅变更，而不是只看最终文字总结。

**建议能力**

- `/diff`：展示本轮或当前 session 的文件改动。
- `/accept`、`/reject`：接受或回滚本轮改动。
- `/changes`：按文件列出新增、修改、删除、测试状态。
- 文件工具返回 `changed_files`、`changed_lines`、`old_hash/new_hash`。
- 对外部命令造成的文件变化也做快照检测。

**落地方式**

```text
RunStarted
  -> 记录 git status 和 workspace 文件快照
ToolFinished(file_write/file_patch/code_run)
  -> 标记可能变更
RunFinishing
  -> 计算 diff summary
REPL
  -> /diff /accept /reject
```

**优先级：P0**

这是最直接提升编码体验的能力，且不要求引入新外部系统。

### 3.2 NoToolPolicy 与早停治理

**价值**

当模型应该调用工具却直接 final、输出大段代码但没有写入文件、或者空回复时，Cohort 应该自动把它拉回执行循环。这个能力比新工具更重要，因为它能提升所有任务的可靠性。

**建议能力**

- 用户请求包含“写入、修改、执行、检查、读取”等动作但无 tool_calls：继续下一轮提示必须用工具。
- 输出大代码块但未调用 `file_write` / `file_patch`：提示落盘或解释为何不落盘。
- 有长期记忆信号但未显式 `skip`：final review。
- 无内容或只有泛泛总结：返回结构化 retry prompt。

**落地位置**

- `internal/agent/runner.go`：在 `len(resp.ToolCalls)==0` 的分支前增加 policy。
- 后续迁移到 Lifecycle Hook 的 `LLMResponded` 事件。

**优先级：P0**

### 3.3 Lifecycle Hook 与 `run.log`

**价值**

现在 Runner 里已经有 checkpoint、SOP、memory hint、final review、evidence 等逻辑。继续堆下去会让 Runner 变成策略泥球。Claude Code 和 OpenClaw 都证明：生命周期事件是扩展能力的基础。

**建议事件**

| 事件 | 用途 |
| --- | --- |
| `SessionStart` | 初始化运行状态、加载项目配置 |
| `UserPromptSubmit` | prompt 改写、SOP 路由、计划模式入口 |
| `TurnStart` | 注入 checkpoint、memory、compact |
| `LLMResponded` | no-tool、空回复、文本工具兜底 |
| `PreToolUse` | 权限策略、危险命令确认、参数审计 |
| `PostToolUse` | evidence、diff、日志、checkpoint 提醒 |
| `PostToolBatch` | 多工具批次摘要 |
| `PreCompact` / `PostCompact` | 压缩审计和摘要质量评估 |
| `RunFinishing` | final review、diff summary、记忆候选 |
| `SessionEnd` | audit、指标、后台挖掘任务 |

**第一版不要做动态插件。**

先定义内部接口和默认 JSONL sink：

```text
temp/sessions/<session_id>/run.log
```

记录：

- session_id、turn、event、tool、status。
- args 安全摘要，不记录 secret 和大文本。
- result 摘要、耗时、错误码。
- context stats、memory stats、diff stats。

**优先级：P0**

### 3.4 Project Mode

**价值**

Claude Code 的 `CLAUDE.md` 和 OpenClaw 的工作区 bootstrap 文件都说明：项目级上下文应该是显式资产。Cohort 已有 `memory/projects/<project_id>/project.md`，但还缺一套清晰的项目入口。

**建议能力**

```text
.cohort/
  project.md        # 项目固定约定
  commands/         # 项目 slash commands
  skills/           # 项目技能
  hooks.json        # 项目 hook 配置，后续启用
  settings.yaml     # 项目局部配置
```

REPL 命令：

```text
/init
/project
/project memory
/project doctor
```

注入策略：

- 每轮只注入 `.cohort/project.md` 的短索引或摘要。
- 需要细节时通过工具读取全文。
- 项目记忆更新仍走 `memory_propose_update` / `memory_apply_update`。

**优先级：P1**

### 3.5 轻量 Plan Mode

**价值**

复杂任务不能只靠模型上下文记忆推进。Cohort 应该把计划变成外部状态机，这一点可以借鉴 Claude Code 的 task management，也可以借鉴 GA 的 `plan.md` 文件模式。

**建议能力**

```text
/plan start <name>
/plan status
/plan next
/plan done <step_id>
/plan stop
```

文件结构：

```text
workspace/plans/<name>/
  plan.md
  evidence.md
  decisions.md
  review.md
```

计划项状态：

```text
[ ] pending
[/] in_progress
[x] done
[?] blocked
[!] needs_review
```

执行规则：

- 每次执行前读取 plan。
- 每完成一步 patch plan。
- 每个 done 必须有 evidence 或用户确认。
- 任务结束前生成 review。

**优先级：P1**

### 3.6 独立验证 Session

**价值**

同一个模型在同一上下文里验证自己的工作，容易确认偏误。Cohort 可以先不做真正并行 subagent，但可以启动“独立只读验证 session”。

**建议能力**

```text
/verify
/verify diff
/verify tests
/verify doc
```

验证 session 只拿：

- 用户原始需求。
- 最终 diff。
- 测试输出摘要。
- 关键文件路径。

不继承执行过程中的推理和失败路径。

**优先级：P1**

### 3.7 Skill / Plugin Manifest

**价值**

Cohort 已有 SOP，但 SOP 更像内部操作规则，还不是可安装能力包。Claude Code 的 plugin 体系值得借鉴，但 Cohort 第一版要更收敛。

**建议目录**

```text
.cohort/plugins/<plugin_name>/
  plugin.yaml
  skills/<skill_name>/SKILL.md
  commands/<command>.md
  hooks.json
  mcp.json
  scripts/
```

`plugin.yaml` 示例：

```yaml
name: go-quality
version: 0.1.0
description: Go 项目质量检查插件
skills:
  - skills/go-test/SKILL.md
commands:
  - commands/review.md
hooks:
  - hooks.json
permissions:
  tools:
    allow:
      - file_read
      - code_run
```

第一版只支持：

- 发现本地 plugin。
- 列出 plugin。
- 读取 skill/command。
- 不自动执行脚本 hook。

**优先级：P2**

### 3.8 MCP Client

**价值**

MCP 已经是 Agent 连接外部工具的事实标准之一。Cohort 如果要接数据库、GitHub、内部 API、文档系统，不应该为每个系统都写死 Go 工具。

**建议能力**

```text
cohort mcp list
cohort mcp add <name>
cohort mcp tools <name>
cohort mcp remove <name>
```

配置：

```yaml
mcp:
  servers:
    github:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env:
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

安全边界：

- MCP 工具进入 Tool Registry 前必须带 namespace，例如 `mcp_github_search_issues`。
- 默认只读。
- 写操作需要 permission policy 和确认。
- MCP 输出必须走 context trimming。

**优先级：P2**

### 3.9 LSP 集成

**价值**

编码 Agent 只靠 `rg` 和文件读取会缺少类型信息。Claude Code 的 LSP 插件方向值得 Cohort 借鉴，尤其是 Go 项目可以先接 `gopls`。

**建议工具**

```text
lsp_diagnostics
lsp_definition
lsp_references
lsp_hover
lsp_symbols
```

第一版先做只读诊断：

- Go：检测 `gopls`，通过 `gopls check` 暴露 diagnostics。
- TypeScript：检测 `tsc`，通过 `tsc --noEmit --pretty false` 暴露 diagnostics。
- Python：检测 `pyright`，通过 `pyright` 暴露 diagnostics。
- `cohort lsp doctor --install` 可通过 `npm install -g typescript` 和 `npm install -g pyright` 显式补装缺失后端；默认 doctor 仍只读。
- definition、references、hover、symbols 仍是下一阶段。

**优先级：P2**

### 3.10 Daemon / Gateway

**价值**

OpenClaw 的核心不是 CLI，而是常驻 gateway。Cohort 如果想承载定时任务、长任务、外部消息、IDE 插件，就需要一个本地 daemon。

**建议能力**

```text
cohort daemon start
cohort daemon stop
cohort daemon status
cohort api serve
```

本地 API：

```text
POST /sessions
POST /sessions/{id}/messages
GET  /sessions/{id}/events
POST /tasks
GET  /tasks/{id}
POST /tools/{name}
```

内部组件：

- session lane：同一个 session 串行。
- global lane：全局并发上限。
- task queue：长任务可取消。
- event stream：给 TUI/IDE/Web 订阅。
- permission broker：统一处理确认。

**优先级：P3**

### 3.11 Scheduler / Monitor

**价值**

常驻后，Cohort 可以支持“每天检查构建状态”“监听日志错误”“定时整理记忆”等任务。但这类能力必须先只读、可审计。

**建议能力**

```text
/schedule list
/schedule add "每天 10:00 跑 go test ./..."
/monitor add "tail -F logs/app.log"
```

第一版场景：

- 只读健康检查。
- 测试/构建报告。
- 记忆质量报告。
- SOP candidate 挖掘报告。

禁止默认后台改代码、装依赖、发送外部消息。

**优先级：P3**

### 3.12 多模型与 BYOM

**价值**

OpenClaw 的 BYOM 对 Cohort 也重要。Cohort 当前已有 OpenAI-compatible 和 `provider` 字段，但还缺 profile、fallback、local model 体验。

**建议能力**

```yaml
llm:
  active_profile: deepseek
  profiles:
    deepseek:
      provider: openai
      api_base: ...
      model: deepseek-v4-pro
    local:
      provider: openai
      api_base: http://127.0.0.1:11434/v1
      model: qwen3-coder
```

CLI：

```text
/model switch local
/model profiles
cohort ask --model local "..."
```

增强：

- fallback 链。
- 失败分类。
- usage/cost 统计。
- reasoning effort / thinking 参数适配。

**优先级：P1-P2**

### 3.13 Sandbox 与权限策略

**价值**

Cohort 已有桌面 R1/R2/R3 和 confirmation token，这是很好的安全基础。但 `code_run`、MCP、未来插件和 daemon 都需要统一权限策略。

**建议策略**

```yaml
permissions:
  code_run:
    mode: allowlist
    allow:
      - "go test ./..."
      - "go test ./internal/..."
      - "rg *"
    confirm:
      - "git commit"
      - "go get"
    deny:
      - "rm -rf"
      - "git reset --hard"
  tools:
    mcp_*: read_only
```

建议补充：

- `PreToolUse` policy pipeline。
- command classifier。
- workspace snapshot。
- 可选 Docker sandbox。
- 危险操作必须确认。

**优先级：P1-P2**

### 3.14 TUI / 任务面板

**价值**

当 Cohort 有计划、diff、日志、后台任务后，纯文本 REPL 会吃力。TUI 比 Web/IM 更贴近当前定位，也更容易保持本地优先。

**建议界面**

- 左侧：session / plan / tasks。
- 中间：对话流。
- 右侧：tools、diff、evidence、context stats。
- 底部：命令输入和 slash menu。

**优先级：P2**

## 4. 推荐路线图

### P0：立刻值得做

目标：把 Cohort 变成更可靠的编码助手。

1. `NoToolPolicy`。
2. 交互式 `/diff` 和本轮变更摘要。
3. Runner 生命周期事件接口。
4. `run.log` JSONL。
5. `doctor` 命令。
6. `file_read` 路径候选和 `file_patch` 变更摘要。

验收：

- 该用工具时不会轻易空 final。
- 用户能看清本轮改了什么。
- 出问题能追到 turn/tool/event。
- 环境问题能用 `doctor` 定位。

### P1：长任务能力

目标：让 Cohort 能稳定处理多步骤开发任务。

1. Project Mode。
2. 轻量 Plan Mode。
3. 独立验证 session。
4. 多模型 profile 和 fallback。
5. 统一 permission policy。
6. 每轮 summary / action summary。

验收：

- 一个复杂需求可以拆计划、执行、验证、复盘。
- 多模型失败可切换。
- 危险命令和高风险工具有统一确认。

### P2：扩展协议

目标：让 Cohort 能接外部生态，但仍保持安全边界。

1. Skill / Plugin manifest。当前已有 `.cohort/plugins/*/plugin.json` 发现与 doctor。
2. MCP client。
3. LSP tools，当前已有 `cohort lsp doctor/diagnostics --language go|typescript|python|all`、`cohort lsp doctor --install` 和 `lsp_diagnostics` Agent tool。
4. TUI。当前已有 `cohort tui status|plan|diff|logs|explorers`，全屏交互式 TUI 仍待补。
5. 相关记忆语义检索。
6. tracing sink。

验收：

- 本地插件能被发现、列出、读取。
- MCP 只读工具能安全进入 Tool Registry。
- Go 代码诊断不只依赖命令输出。

### P3：常驻自动化

目标：把 Cohort 从 CLI runtime 扩展为本地 Agent daemon。

1. daemon / local API gateway。
2. session lane / task queue / cancel。
3. scheduler。
4. monitor。
5. 多渠道 adapter。
6. 只读后台反射和报告。

验收：

- 后台任务有审计、有取消、有权限边界。
- 外部 channel 只是入口，不改变核心安全模型。

## 5. 不建议近期做的事

| 能力 | 不建议原因 | 替代方案 |
| --- | --- | --- |
| 直接做多 IM 前端 | 维护成本高，容易稀释内核开发 | 先做 daemon/local API，再接 adapter |
| 一步到位并行 subagent | 需要 session 隔离、取消、结果合并、权限治理 | 先做独立验证 session |
| 自动安装/执行任意插件脚本 | 供应链和本地安全风险高 | 插件先只读，hook 脚本后置且需确认 |
| 默认开放 cookie/浏览器扩展管理 | 高敏感权限 | 仅诊断模式或显式授权 |
| 后台自动改代码 | 难审计，容易破坏工作区 | 后台只产 report 或 candidate |
| 先做 Web UI | 当前核心体验瓶颈不在 UI 框架 | 先 TUI 和 local API |
| 追求完整 OpenClaw 网关 | Cohort 还需先稳住 Runner/权限/日志 | 分阶段做 daemon |

## 6. 建议的目标定位

Cohort 不需要变成 Claude Code 的复制品，也不需要完整复刻 OpenClaw。更合理的定位是：

```text
一个 Go 编写的本地优先 Agent Runtime：
  面向代码和本地工作流，
  默认安全、可审计、可恢复，
  通过 SOP/Skill/MCP/LSP/Hook 扩展，
  未来可作为 daemon 支撑 TUI、IDE 和消息渠道。
```

相比 Claude Code，Cohort 可以更强调：

- Go 单二进制和可测试内核。
- 本地优先、安全边界清晰。
- 证据驱动记忆，而不是泛化 memory。
- 桌面 Computer Use 的受控输入模型。

相比 OpenClaw，Cohort 可以更强调：

- 编码和本地工程任务。
- 更小、更审计友好的工具面。
- 先 CLI/TUI，再 gateway。
- 先安全策略，再多渠道。

## 7. 资料来源

- Claude Code Plugins reference: https://code.claude.com/docs/en/plugins-reference
- OpenClaw official site: https://openclaw.im/
- OpenClaw architecture analysis: https://github.com/0xZakk/ai-agent-architectures/blob/main/analyses/openclaw.md
- Cohort local docs: `README.md`、`docs/usage.md`、`docs/genericagent_borrowing_research.md`、`docs/cohort_self_evolution_research.md`
