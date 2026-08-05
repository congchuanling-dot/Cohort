# Cohort 使用教程

> 文档状态：`[维护]`。当前实现状态和设计文档导航见 [docs/README.md](README.md)。

这份文档说明当前 Cohort 怎么启动、有哪些命令、怎么恢复 session。

## 0. 安装和配置路径

开发阶段可以继续在 Cohort 项目根目录运行：

```bash
cd /Users/bytedance/Desktop/myOwnProject/Cohort
```

也可以通过 npm 安装成用户级命令：

```bash
npm install -g @cohort-ai/cohort@latest
export DEEPSEEK_API_KEY="sk-xxx"
cohort --version
cohort
```

npm 包已发布到 npm 官方 registry。安装时会从 GitHub Release 下载匹配当前 macOS 架构的二进制，并校验 SHA256，同时随包提供桌面自动化和 OCR helper。当前已验证版本为 `v1.0.0`。

如果不想使用 npm，也可以直接使用 GitHub installer：

```bash
curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- --repo https://github.com/congchuanling-dot/Cohort.git
export PATH="$HOME/.cohort/bin:$PATH"
```

如果已经在仓库根目录：

```bash
./scripts/install.sh
export PATH="$HOME/.cohort/bin:$PATH"
cohort config
cohort doctor
```

npm wrapper 会把二进制安装到 npm 包目录并暴露 `cohort` 命令。GitHub installer 会优先下载 Release 里的 macOS 二进制；如果 release 不可用，才回退到源码构建。installer 最终会把二进制写入 `~/.cohort/bin/cohort`，把用户级配置写入 `~/.cohort/config.yaml`。它不会写入 API key。macOS zsh 下会自动把 `~/.cohort/bin` 写入 `~/.zshrc`；不希望修改 shell 配置时使用：

```bash
./scripts/install.sh --no-shell
```

私有仓库或非默认分支可以显式传参：

```bash
curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- \
  --repo git@github.com:congchuanling-dot/Cohort.git \
  --ref master
```

安装指定 release：

```bash
curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- \
  --version v1.0.0
```

Cohort 配置文件查找顺序：

1. `--config <file>` 或 `-c <file>`
2. `COHORT_CONFIG`
3. 当前目录的 `configs/config.yaml`
4. `~/.cohort/config.yaml`

查看当前安装版本：

```bash
cohort --version
```

例如：

```bash
cohort --config ~/.cohort/config.yaml config
COHORT_CONFIG=~/.cohort/config.yaml cohort ask "用一句话介绍 Cohort"
```

初始化或覆盖用户级配置：

```bash
cohort init
cohort init --provider anthropic --force
cohort --config ./my-cohort.yaml init --provider local
```

当前内置原生支持两类模型 API：

- `provider: openai` / `openai-compatible`：适合 DeepSeek、Ollama、LM Studio 和其他兼容 `/v1/chat/completions` 的服务
- `provider: anthropic` / `claude`：适合 Anthropic 原生 `/v1/messages` API

也支持显式 `llm.profiles` 和 `fallback_profiles`，可以把多个 provider 组成主链路和备用链路。

这还不等于“所有类型 API 都能直接用”。Gemini 原生 API、Bedrock、Vertex，以及 Azure OpenAI 的特殊路径或鉴权形式，目前还没有原生适配层。

诊断当前环境：

```bash
cohort doctor
cohort doctor --connect
cohort doctor computer
```

`doctor` 默认检查配置解析、API key、provider、api_base 格式、workspace/log/session 目录可写性、MCP 配置和权限文件、Skill doctor、Chrome 扩展目录、desktop helper 和 OCR helper。`--connect` 会额外访问 `api_base` 检查网络可达性，并 probe 已配置 MCP Server，但不会发起模型补全请求。

`doctor computer` 检查 macOS Computer Use 环境，包括 Accessibility、Screen Recording、desktop helper、OCR helper、Chrome bridge 和截图/OCR artifact 目录。它只做只读诊断，不会默认点击、输入或修改系统设置。

## 0.2 Project / Plan Mode

Project Mode 使用显式项目文件，不依赖隐藏默认状态：

```bash
cohort project init "My Project"
cohort project status
```

初始化后会创建：

- `.cohort/project.md`：项目目标、规则、项目记忆指针、计划状态指针。
- `.cohort/config.json`：项目级配置入口，指向 project/plan/memory 文件。

Plan Mode 使用 `.cohort/plan.json` 保存可恢复计划状态：

```bash
cohort plan create "P0 hardening" -- "implement runtime guard" -- "run tests"
cohort plan start 1
cohort plan verify 1 "go test ./internal/agent -count=1"
cohort plan status
```

步骤只能通过 `plan verify <id> <evidence>` 标记完成；空 evidence 会被拒绝。REPL 中同样支持 `/project ...` 和 `/plan ...`，状态变更后会刷新当前 Runner 的系统提示词。

## 0.2 Chrome Bridge 扩展

浏览器工具依赖 Cohort Browser Bridge Chrome 扩展。npm 和 GitHub installer 都会准备本地扩展目录，但 Chrome 出于安全限制仍需要用户手动加载 unpacked extension。

查看扩展目录：

```bash
cohort extension path
```

打开 Chrome 扩展页并打印加载步骤：

```bash
cohort extension open
```

然后在 `chrome://extensions` 开启 Developer mode，点击 `Load unpacked`，选择 `cohort extension path` 输出的目录。

## 1. 准备 API Key

如果只是查看配置、工具列表、session 列表，不需要 API Key。

如果要让模型真正回答问题或继续 session，需要给当前激活 profile 设置 API Key 或占位 key，例如：

```bash
# DeepSeek / 其他 OpenAI-compatible 云服务
export DEEPSEEK_API_KEY="sk-xxx"

# Anthropic Claude
export ANTHROPIC_API_KEY="sk-ant-xxx"

# 本地 OpenAI-compatible 网关（如 Ollama / LM Studio）
# 如果服务端不校验鉴权，也需要给一个非空值
export LOCAL_OPENAI_API_KEY="dummy"
```

检查配置：

```bash
go run . config
```

看到下面结果表示 Key 已被识别：

```text
api_key: set
```

## 2. 启动交互模式

最常用的启动方式：

```bash
go run .
```

启动后会看到：

```text
╭────────────────────────────────────────────────────────────╮
│ Cohort                                                     │
│ Command-line Agent Runtime                                │
├────────────────────────────────────────────────────────────┤
│ Model      deepseek-v4-pro                                 │
│ Workspace  workspace                                       │
│ Session    new session                                     │
│ Tools      5                                               │
├────────────────────────────────────────────────────────────┤
│ 直接输入任务开始执行                                      │
│ 输入 / 打开命令菜单；用 ↑↓ 选择，Enter 执行              │
╰────────────────────────────────────────────────────────────╯

cohort ›
```

然后直接输入任务：

```text
读取 README.md 并总结
```

退出交互模式：

```text
/exit
```

清空当前内存上下文，并开启新 session：

```text
/clear
```

查看当前可用工具：

```text
/tools
```

查看所有对话内命令：

```text
/help
```

在真实终端里，输入 `/` 后回车会打开可选择菜单，可以用上下键选择命令并按回车执行。

在脚本、测试或管道输入里，`/` 会退化为文本命令面板：

```text
Slash commands

  /help                 显示命令帮助
  /model                查看当前模型
  /config               查看运行配置
  /tools                查看工具列表
  /project status       查看 Project Mode 文件和指针
  /project init <title> 初始化 .cohort/project.md
  /plan status          查看可恢复计划状态
  /plan create <title> -- <step1> -- <step2>
                         创建 .cohort/plan.json
  /plan start <id>      标记一个步骤进行中
  /plan verify <id> <evidence>
                         用验证证据完成步骤
  /session              查看当前 session
  /session list         列出历史 session
  /session memory       查看 session memory
  /resume <id>          恢复 session
  /compact              生成或更新 session memory
  /full-compact         生成或更新 compact summary
  /memory               查看 session memory
  /sop candidates       列出 SOP 候选
  /sop promote <id>     升级候选 SOP；--confirm-index 显式更新索引
  /diff                 审阅 Git 变更摘要
  /diff show [file]     查看完整 diff
  /diff rollback <file> --confirm
                         受限回滚一个已跟踪文件
  /clear                清空当前内存上下文
  /exit                 退出
```

如果已经输入了命令前缀，也可以按 `Tab` 补全，例如 `/se` 可以补到 `/session`。

`/diff` 是本地审阅命令，不会发送给模型：

- `/diff`：显示 `git status --short` 和 `git diff --stat HEAD --`。
- `/diff show [file]`：显示完整 diff；传入文件时会限制在当前 Git 仓库内。
- `/diff accept`：保留当前变更，只输出确认说明，不提交、不隐藏。
- `/diff rollback <file> --confirm`：回滚单个已跟踪文件的 staged/worktree 变更；拒绝未确认、目录、仓库外路径和未跟踪文件。

Skill 可以在 `SKILL.md` frontmatter 中声明运行权限：

```yaml
---
name: safe workflow
permissions:
  allow-tools: [file_read, code_run]
  deny-tools:
    - mcp_prod_delete
---
```

通过 `/skill run <id>` 直接执行时，Cohort 会启用 active policy：只向模型暴露允许的工具，并在运行时拒绝越权工具调用。普通任务命中 Skill 时仍需先 `skill_read`，permissions 会进入 Skill Index 供模型遵守。

Cohort 默认内置 5 个只读高频 Skill：`builtin/code-review`、`builtin/unit-test`、`builtin/browser-debug`、`builtin/desktop-debug`、`builtin/release-check`。项目级同名 Skill 会优先于 builtin alias，避免内置包抢占项目定制流程。

MCP 配置支持导入导出和 per-tool policy：

```bash
cohort mcp import --scope project ./mcp.json
cohort mcp export --scope project ./backup.mcp.json
cohort mcp policy list
cohort mcp policy set docs search allow R1 --args-policy=tool_scope
cohort mcp policy remove docs search
```

旧配置里的 `type: "sse"` 会按 HTTP/SSE 兼容传输处理。

Runner 会在 `run.log.jsonl` 的 `RunFinished` 汇总 usage。成本估算没有隐藏默认价格，只有显式配置后才输出：

```bash
export COHORT_COST_INPUT_USD_PER_1M=0.14
export COHORT_COST_OUTPUT_USD_PER_1M=0.28
export COHORT_COST_CACHE_READ_USD_PER_1M=0.01
export COHORT_COST_CACHE_WRITE_USD_PER_1M=0.20
```

运行观测可以直接从本地 `run.log.jsonl` 读取，不需要启动 LLM：

```bash
cohort trace last
cohort trace show <session_id> [--run <run_id>]
cohort perf last
cohort perf show <session_id> [--run <run_id>]
```

`trace` 按时间线展示 `ContextBuilt`、`LLMRequestStarted`、`LLMResponseFinished`、`ToolStarted`、`ToolFinished` 等事件。`perf` 汇总总耗时、LLM 耗时、工具耗时、最近一次请求大小、工具 schema 数量、usage 和最大事件间隔，用于快速判断慢在模型、工具、上下文构建还是外部观测链路。

离线 Skill 候选挖掘：

```bash
cohort reflect once --task mine-skill-candidates
```

报告会写入 `memory/reflection/skill_candidates.md`，只包含工具名、计数、session ID 和候选 `SKILL.md` 草案，不会自动安装或启用。

## 3. 执行单次任务

如果不想进入交互模式，可以用 `ask`：

```bash
go run . ask "读取 README.md 前 40 行，并用 5 条 bullet 总结"
```

`ask` 会执行一次任务，任务结束后进程退出。

## 4. 当前所有命令

### 4.1 查看帮助

```bash
go run . help
```

作用：查看当前支持的命令。

### 4.2 进入交互模式

```bash
go run .
```

等价于：

```bash
go run . run
```

作用：启动一个持续对话的本地 Agent。

### 4.3 执行单次任务

```bash
go run . ask "任务内容"
```

作用：执行一次任务，完成后退出。

### 4.4 查看工具列表

推荐在交互模式里输入：

```text
/tools
```

外部 CLI 也保留：

```bash
go run . tools
```

当前工具：

```text
file_read
file_write
file_patch
code_run
ask_user
update_working_checkpoint
start_long_term_update
memory_propose_update
memory_apply_update
browser_tabs
browser_open
browser_scan
browser_dom_summary
browser_execute_js
browser_click
browser_click_element
browser_type
browser_type_element
browser_press_key
browser_snapshot
browser_wait_for_load
browser_wait_for_selector
browser_wait_for_text
browser_wait_for_url
browser_wait_for_stable
browser_screenshot
browser_ocr
desktop_permissions
desktop_windows
desktop_activate
desktop_screenshot
desktop_ax_snapshot
desktop_ocr
desktop_ax_press
desktop_ax_focus
desktop_click
desktop_visual_click
desktop_press_key
desktop_type_text
```

这个命令不需要 API Key。

### 4.5 查看配置

推荐在交互模式里输入：

```text
/model
/config
```

外部 CLI 也保留：

```bash
go run . config
```

作用：查看当前 provider、模型、API 地址、工作区、API Key 是否已设置。

这个命令不需要 API Key。

### 4.6 管理 MCP Server

MCP 让 Cohort 连接飞书、GitHub、数据库等外部系统。项目级配置使用 Claude Code 兼容的 `.mcp.json`，所以现有 Claude Code 配置可以直接复用。

查看当前生效的 server：

```bash
go run . mcp list
go run . mcp status
```

也可以进入 `go run .` 后直接使用，不需要退出 REPL：

```text
/mcp list
/mcp status
/mcp tools github
/mcp probe github
```

`/mcp` 只查看和诊断你已经显式装配的 Server，不会添加默认 MCP，也不会修改配置。

添加本地 stdio server：

```bash
go run . mcp add github \
  -e GITHUB_PERSONAL_ACCESS_TOKEN='${GITHUB_TOKEN}' \
  -- npx -y @modelcontextprotocol/server-github
```

添加远程 HTTP server：

```bash
go run . mcp add --transport http docs https://code.claude.com/docs/mcp
```

查看或验证 server：

```bash
go run . mcp tools github
go run . mcp probe github
```

首次使用 `npx` 安装某个 MCP Server 时，npm 需要下载依赖，可能需要几十秒；等 `probe` 成功列出工具后再启动 `ask`，后续启动会命中本机缓存。

默认写入项目级 `.mcp.json`。可加 `--scope user` 写入 `~/.cohort/mcp.json`，或加 `--scope local` 写入默认 gitignore 的 `.cohort/local.mcp.json`：

```bash
go run . mcp remove github
go run . mcp remove --scope local github
```

MCP 工具会自动变成 `mcp_<server>_<tool>` 并进入 Agent Tool Registry。没有显式项目规则的外部工具默认按 `R2 + ask` 处理；用户可选择一次、同参数 session 或同参数 project 授权。名称明确包含删除、审批、授权和支付语义的工具默认 `R3 + deny`。显式配置为 `R1 + allow` 的只读工具才会直接执行。

### 4.6.1 能力边界拓展

`capability` 命令用于记录“当前 Cohort 做不到什么”，并把补能力流程推进到可验证的项目级 Skill 候选。它不需要 API Key，也不会自动安装 pip/npm/brew 依赖。

查看已注册能力：

```bash
cohort capability list
```

查看已记录的能力缺口：

```bash
cohort capability gaps
```

查看重复出现、仍未解决的能力缺口建议：

```bash
cohort capability suggestions
```

手动记录一个能力缺口并生成 proposal：

```bash
cohort capability propose "处理一种新的本地文件格式"
```

根据 proposal 生成项目级 Skill scaffold：

```bash
cohort capability build <proposal_id>
```

检查候选能力的文件、依赖和验证状态：

```bash
cohort capability doctor <capability_id>
```

如果 proposal 声明了 `python`、`npm` 或 `brew` 依赖，可以先生成安装计划，不会立即安装：

```bash
cohort capability deps plan <proposal_id>
```

确认计划后显式批准，再执行安装；安装记录会写入审计文件：

```bash
cohort capability deps approve <plan_id>
cohort capability deps install <plan_id>
```

只预演安装命令、不写安装记录：

```bash
cohort capability deps install <plan_id> --dry-run
```

查看依赖计划：

```bash
cohort capability deps list
```

运行候选能力的 smoke test：

```bash
cohort capability verify <capability_id>
```

验证通过后将能力标记为可用，或在不需要时禁用：

```bash
cohort capability promote <capability_id>
cohort capability disable <capability_id>
```

Tool/MCP adapter 还需要额外执行显式启用，避免 `promote` 后未经审核就进入运行时：

```bash
cohort capability enable <capability_id>
```

Tool adapter 启用记录会写入：

```text
.cohort/capabilities/enabled_adapters.json
```

下一次 Runner 启动时会读取这份 allowlist，把已启用 Tool adapter 的 command 注册为工具。MCP adapter 启用后不会自动启动外部服务，命令会输出明确的 `cohort mcp import --scope project --merge ...` 下一步。

查看某个 capability、gap 或 proposal：

```bash
cohort capability show <id>
```

首版会把数据写到当前项目的：

```text
.cohort/capabilities/registry.json
.cohort/capabilities/deps.json
```

交互运行时，如果模型没有调用工具就明确表示“缺少工具 / 无法处理 / 不支持该能力”，Runner 会自动记录一个 `runner:no_tool` 来源的 capability gap，并在 `run.log.jsonl` 里写入 `CapabilityGapRecorded` 观测事件。

注意：当前 Cohort 可以通过 `code_run` 执行 shell 命令，因此技术上可以运行 `python3 -m pip install --user xxx` 这类安装命令。但完整能力拓展不会直接放开任意安装。当前 `build/doctor/deps/verify/promote/enable` 负责生成项目级 Skill 候选、诊断文件与依赖、生成依赖安装计划、显式批准后安装并记录审计、运行 smoke test、更新 registry，并在审核后启用 adapter。已 promote 为 `available` 的 skill capability 会进入系统提示词里的 Capability Index，模型命中后仍需先 `skill_read`。`suggestions` 只根据重复 unresolved gaps 给出下一步建议，不会自动创建 proposal 或安装依赖。更完整离线反思汇总仍需要后续按 [能力边界拓展技术方案](capability_evolution_technical_design.md) 继续实现。

### 4.7 LSP 查询

诊断：

```bash
cohort lsp diagnostics --language go ./...
cohort lsp diagnostics --language typescript
cohort lsp diagnostics --language python .
```

符号查询：

```bash
cohort lsp definition --language go internal/foo.go:12:8
cohort lsp references --language typescript src/main.ts:5:17 --declaration
cohort lsp hover --language python app.py:10:4
cohort lsp symbols --language typescript src
```

Go 的 `definition/references/hover/symbols` 走 `gopls`。TypeScript/Python 第一版走只读 `symbol_scan` fallback，用源文件扫描提供近似定义、引用、hover 和 symbols，不等同于长驻 language server 的类型级精确结果。

### 4.8 Explorer Batch

创建只读验证任务：

```bash
cohort explorer create "verify plan mode status"
cohort explorer create "verify capability adapter docs"
```

并行运行多个 lane 并生成聚合报告：

```bash
cohort explorer run-batch <id1> <id2> --with-tests
```

聚合报告写入：

```text
.cohort/explorers/aggregate_result.md
```

### 4.9 查看 session 列表

推荐在交互模式里输入：

```text
/session list
```

外部 CLI 也保留：

```bash
go run . session list
```

作用：列出本地保存过的会话。

这个命令不需要 API Key。

输出示例：

```text
ID                        TITLE           MESSAGES  UPDATED              CWD
20260718-223408-8af91b03  你的session有什么效果  8         2026-07-18 22:36:49  /Users/bytedance/Desktop/myOwnProject/Cohort
```

字段含义：

- `ID`：session 的唯一标识，恢复时要用它。
- `TITLE`：会话标题，默认来自第一条用户输入。
- `MESSAGES`：`history.jsonl` 里已经保存的消息数，包括 user、assistant、tool。
- `UPDATED`：最后更新时间。
- `CWD`：创建 session 时所在目录。

### 4.10 恢复 session

推荐在交互模式里输入：

```text
/resume <session_id>
```

也可以写成：

```text
/session resume <session_id>
```

外部 CLI 兼容入口：

```bash
go run . session resume <session_id>
```

例如：

```bash
go run . session resume 20260718-223408-8af91b03
```

作用：

- 读取 `temp/sessions/<session_id>/history.jsonl`。
- 把历史消息恢复到 `Runner.history`。
- 进入交互模式，等待你继续输入新任务。
- 后续新消息继续追加到同一个 `history.jsonl`。

恢复成功后会看到类似：

```text
resumed session 20260718-223408-8af91b03 (8 messages): 你的session有什么效果
```

然后可以继续问：

```text
继续基于刚才的内容讲 session 是怎么落盘的
```
继续基于刚才的内容讲 session 是怎么落盘的
```

## 5. session 怎么用

推荐流程：

1. 先启动交互模式。

```bash
go run .
```

2. 输入你的任务。

```text
帮我看一下这个项目的 session 设计
```

3. 退出。

```text
/exit
```

4. 下次回来先列出 session。

```text
/session list
```

5. 复制要恢复的 ID。

```text
/resume 20260718-223408-8af91b03
```

6. 继续提问。

```text
基于刚才的上下文，下一步应该开发什么
```

## 6. session 文件保存在哪里

默认目录：

```text
temp/sessions/
```

每个 session 一个子目录：

```text
temp/sessions/<session_id>/
```

目录里主要有两个文件：

```text
meta.json
history.jsonl
```

`meta.json` 保存轻量信息：

- session ID
- 标题
- 工作目录
- 模型
- 创建时间
- 更新时间

`history.jsonl` 保存真正的上下文：

- 用户消息
- 模型回复
- 模型工具调用
- 工具执行结果

## 7. 什么时候用 ask，什么时候用 resume

用 `ask` 的场景：

- 一次性问题。
- 不需要保留上下文。
- 例如总结一个文件、跑一次命令、问一个独立问题。

```bash
go run . ask "总结 README.md"
```

用交互模式的场景：

- 连续开发。
- 多轮追问。
- 希望自动保存 session。

```bash
go run .
```

用 `/resume` 的场景：

- 上次聊到一半退出了。
- 想让模型继续看到之前上下文。
- 想继续往同一个 `history.jsonl` 追加消息。

```text
/resume <session_id>
```

## 8. 常见问题

### 8.1 `session list` 没有内容

说明还没有产生过本地 session。

先执行一次：

```bash
go run . ask "用一句话介绍 Cohort"
```

或者进入交互模式问一个问题：

```bash
go run .
```

然后在交互模式里执行：

```text
/session list
```

### 8.2 `session resume` 后会不会新建 session

不会。

恢复后会继续使用原来的 session ID。新产生的 user、assistant、tool 消息会继续追加到原来的：

```text
temp/sessions/<session_id>/history.jsonl
```

### 8.3 `/clear` 和 `session resume` 有什么关系

`/clear` 只在当前交互进程里生效。

它会清空当前 Runner 的内存上下文，并重置当前 session。之后你再输入新任务，会创建新的 session。

它不会删除磁盘上的旧 session 文件。

### 8.4 恢复很久以前的 session 会有什么问题

当前版本会把 `history.jsonl` 里的历史完整恢复进 Runner，但不会把完整历史原样塞给模型。

每次请求模型前，Context Manager 会构造本轮可见上下文：

- `history.jsonl` 保持完整，不会被裁剪。
- `Runner.history` 保持完整，不会被裁剪。
- 只裁剪发给模型的 `messages`。
- 旧的超长工具结果会被压缩成头尾保留格式。
- 过长历史会按消息 group 从旧到新裁剪。
- `assistant tool_calls` 和对应 `tool` 结果会成组保留，不会被拆散。

如果旧消息被裁剪，本轮请求最前面会插入一条 context notice，说明完整历史仍保存在 `history.jsonl`。

### 8.5 可以直接修改 `history.jsonl` 吗

不建议。

`history.jsonl` 是一行一个 JSON。如果手动改坏其中一行，`session resume` 读取时会失败。

需要排查时可以只读文件：

```bash
sed -n '1,20p' temp/sessions/<session_id>/history.jsonl
```

## 9. Context Manager 配置

Context Manager 默认开启。配置文件位置：

```text
configs/config.yaml
```

可调配置：

```yaml
context:
  max_history_messages: 40
  keep_recent_tool_results: 2
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  max_request_chars: 100000
  max_session_memory_chars: 20000
  max_memory_index_chars: 12000
  max_relevant_memory_chars: 16000
  max_relevant_memory_entries: 3
  max_compact_summary_chars: 60000
  enable_micro_compact: true
```

字段说明：

- 模型上下文窗口不需要用户配置。Cohort 会根据 `llm.model` 查内置 map，`deepseek-v4-pro` / `dsv4pro` 按 1000000 tokens 处理。
- 压缩触发比例固定为可用输入预算的 70%。
- `max_history_messages`：本轮请求最多保留的历史消息数。
- `max_memory_index_chars`：`memory/index.md` 注入请求前允许携带的最大字符数。
- `max_relevant_memory_chars`：根据当前任务关键词自动匹配到的长期记忆注入上限。
- `max_relevant_memory_entries`：单轮最多自动注入几个长期记忆条目。兼容旧配置键 `max_relevant_memory_files`。
- `keep_recent_tool_results`：最近多少条工具结果保持完整。
- `max_tool_result_chars`：单条工具结果超过该字符数后会被压缩。
- `compacted_tool_head_chars`：压缩后保留头部字符数。
- `compacted_tool_tail_chars`：压缩后保留尾部字符数。
- `max_request_chars`：本轮请求消息的字符预算。
- `max_session_memory_chars`：`memory.md` 注入请求前允许携带的最大字符数。
- `max_compact_summary_chars`：`compact.md` 注入请求前允许携带的最大字符数。
- `enable_micro_compact`：是否启用规则压缩。

### 9.1 Session Memory

如果当前 session 目录下存在：

```text
temp/sessions/<session_id>/memory.md
```

Cohort 会在每次请求模型前读取它，并作为 `[Cohort session memory]` 注入到 request messages 前部。

生成或更新 `memory.md`：

```text
/compact
```

`/compact` 会读取当前 Runner.history，调用模型提取稳定事实，并覆盖写入当前 session 的 `memory.md`。这个过程不会写入 `history.jsonl`，也不会调用工具。

如果已有 `memory.md`，覆盖前会先备份为：

```text
temp/sessions/<session_id>/memory.bak.md
```

查看当前 memory：

```text
/memory
/session memory
```

Context Manager 每次构造 request messages 后，会把压缩决策写入：

```text
temp/model_responses/context.log
```

这份日志只包含消息数、估算 token、触发原因、是否注入 memory、是否压缩或裁剪等统计信息，不记录 message 内容。

生成或更新长历史摘要：

```text
/full-compact
```

`/full-compact` 会读取当前 Runner.history，调用模型生成结构化 full compact 摘要，并覆盖写入当前 session 的 `compact.md`。这个过程不会写入 `history.jsonl`，也不会调用工具。

如果已有 `compact.md`，覆盖前会先备份为：

```text
temp/sessions/<session_id>/compact.bak.md
```

只要当前 session 目录下存在 `compact.md`，Cohort 会在每次请求模型前自动读取并注入。长期记忆开启后，Context Manager 会先注入 `memory/index.md` 指针和命中的相关 entry。整体顺序固定为：

```text
memory/index.md -> relevant long-term memory -> memory.md -> compact.md -> 最近对话消息
```

### 9.3 SOP Candidate Promotion

长期记忆可以把稳定流程追加到：

```text
memory/reflection/sop_candidates.md
```

能力晋级链路：

```text
工具能力 -> SOP 约束 -> 工作记忆 -> 长期记忆 entry -> SOP candidate -> 正式 SOP / Skill
```

只有经过工具验证、触发词清晰、推荐步骤可复跑的稳定流程，才应该进入 SOP candidate。一次性事实或本轮业务内容应该在 `memory_propose_update` 中 `skip=true`。

交互模式内先列出候选：

```text
/sop candidates
```

升级候选时，默认只生成 `sops/*.md`，不会把它加入 `sops/index.md`：

```text
/sop promote <candidate_id>
```

更新索引需要人工确认。真实终端会要求输入候选 ID；脚本或管道输入里使用显式参数：

```text
/sop promote <candidate_id> --confirm-index
```

索引更新会写入 `memory/audit.jsonl`，记录确认来源。

### 9.4 Skill Runtime

Skill 是可按需读取的任务工作流包，和 MCP 工具分开管理。Cohort 启动时只扫描并注入 Skill 摘要索引，不把完整 `SKILL.md` 默认塞进系统提示词。

当前扫描目录：

```text
.cohort/skills/<skill_name>/SKILL.md
~/.cohort/skills/<skill_name>/SKILL.md
```

安装本地 Skill 目录：

```bash
go run . skill install ./path/to/skill
```

`skill install` 会先打印安装预览，再提示确认：

```text
Install this skill? [y/N]
```

输入 `y` 或 `yes` 后才会写入目标目录。脚本或自动化场景可以显式跳过人工确认：

```bash
go run . skill install --yes ./path/to/skill
```

只预览，不写入目标目录：

```bash
go run . skill install --dry-run ./path/to/skill
```

安装预览会解析来源、定位候选 `SKILL.md`、计算将安装的文件数和内容 SHA256，并显示目标路径、`requires` 依赖摘要和候选 `SKILL.md` 指令内容。`--dry-run` 使用同一套预览逻辑，但不会进入确认和安装阶段，也不会创建 `.cohort/skills/<skill_name>` 或 `~/.cohort/skills/<skill_name>`。

预览阶段的安全边界：

- 让用户确认来源、git commit、目标目录、是否覆盖和内容 hash。
- 让用户在确认前看到完整或截断后的 `SKILL.md` 指令，理解这个 Skill 之后会让 Agent 做什么。
- 终端输出会过滤控制字符，避免远程内容用转义序列污染终端。
- Cohort 不会在安装阶段自动运行 Skill、安装依赖、授权 MCP、写入环境变量或执行命令。
- 这不是自动安全审计器；第三方 Skill 是否可信仍需要用户根据来源和指令内容判断。

安装 git 仓库里的 Skill：

```bash
go run . skill install https://example.com/org/skill-repo.git
```

安装 git Skill 时锁定版本：

```bash
go run . skill install --pin v1.2.3 https://example.com/org/skill-repo.git
```

`--pin <git-ref>` 会先 checkout 指定 ref，再把解析后的 commit SHA 写入 manifest 的 `source_ref`。后续 `skill update` 和 `skill update --check` 默认继续使用这个 commit；要切到新版本，需要再次传 `--pin <new-ref>`。

默认安装到项目级 `.cohort/skills`。安装到用户全局目录：

```bash
go run . skill install --scope user ./path/to/skill
```

如果一个仓库里有多个 `SKILL.md`，需要指定目录名：

```bash
go run . skill install --name go-test https://example.com/org/skills.git
```

同名 Skill 已存在时不会覆盖；确认替换时显式加：

```bash
go run . skill install --force ./path/to/skill
```

正式安装会写入 `.cohort-skill.json`，记录：

```json
{
  "source": "./path/to/skill",
  "source_type": "local-dir",
  "source_ref": "",
  "requested_ref": "",
  "resolved_ref": "",
  "pinned": false,
  "scope": "project",
  "alias": "skill-name",
  "installed_at": "2026-07-26T12:00:00+08:00",
  "content_hash": "sha256..."
}
```

其中 `content_hash` 覆盖 Skill 包内普通文件，不包含 `.cohort-skill.json` 本身。

Skill 可以在 `SKILL.md` frontmatter 中声明运行前依赖：

```yaml
---
name: lark-doc-helper
description: Work with Lark documents.
requires:
  mcp:
    - lark
  env:
    - LARK_APP_ID
    - LARK_APP_SECRET
  commands:
    - npx
---
```

支持的 `requires` 分类：

- `mcp`：需要用户已通过 `cohort mcp add ...` 显式配置的 MCP Server 名称。
- `env`：需要存在的环境变量名。doctor 只检查是否存在，不会输出变量值。
- `commands`：需要能在 `PATH` 中找到的命令名。

Cohort 不会根据 `requires` 自动安装命令、添加 MCP Server、申请授权或写入环境变量；这些依赖只用于安装预览、`skill list/show` 和 `skill doctor` 的展示与诊断。

更新和删除已安装 Skill：

```bash
go run . skill update project/<skill_name>
go run . skill update project/<skill_name> ./path/to/new-skill
go run . skill update --check project/<skill_name>
go run . skill update --pin v1.2.4 project/<skill_name>
go run . skill uninstall project/<skill_name>
```

`skill update` 默认使用安装时记录的 source；如果旧 Skill 没有安装元数据，可以手动传入新的本地路径或 git URL。

`skill update --check` 只比较当前已安装内容和候选来源内容，不写入目标目录。输出里会包含：

- `status: up-to-date` 或 `status: update-available`。
- 当前已安装内容 hash。
- manifest 记录的 hash。
- 候选来源内容 hash。
- git 来源的 requested/source/resolved ref。

诊断已安装 Skill：

```bash
go run . skill doctor project/<skill_name>
```

`skill doctor` 会检查：

- Skill 路径是否仍在 project/user scope 根目录下。
- `SKILL.md` 是否可读、是否为空。
- frontmatter 名称和描述是否足够用于路由。
- `requires` 声明的 MCP Server 是否已配置。
- `requires` 声明的环境变量是否存在，且不展示变量值。
- `requires` 声明的命令是否能在 `PATH` 中找到。
- `.cohort-skill.json` 是否存在且 JSON 可读。
- manifest 中的 source/source_type 是否完整。
- 当前文件内容 hash 是否和安装时记录一致。

当任务匹配某个 Skill 时，模型应先调用：

```text
skill_read({"skill_id":"project/<skill_name>"})
```

读完后如果决定采用该工作流，应调用 `update_working_checkpoint` 保存关键约束和 `related_skill`，后续不确定时再重读对应 Skill。

交互模式内查看和刷新 Skill：

```text
/skill install [--yes] [--dry-run] <path-or-git-url>
/skill install [--pin git-ref] <git-url>
/skill doctor <skill_id>
/skill list
/skill show <skill_id>
/skill run <skill_id> [arguments...]
/<skill_alias> [arguments...]
/skill update <skill_id> [path-or-git-url]
/skill update --check <skill_id>
/skill update --pin <git-ref> <skill_id>
/skill uninstall <skill_id>
/skill reload
```

`/<skill_alias>` 只对 `SKILL.md` frontmatter 里声明 `user-invocable: true` 的 Skill 生效。`argument-hint` 会在列表里展示，用来提示快捷命令参数。

外部 CLI 也支持：

```bash
go run . skill install ./path/to/skill
go run . skill install --yes ./path/to/skill
go run . skill install --dry-run ./path/to/skill
go run . skill install --pin v1.2.3 https://example.com/org/skill-repo.git
go run . skill doctor project/<skill_name>
go run . skill update --check project/<skill_name>
go run . skill update project/<skill_name>
go run . skill uninstall project/<skill_name>
go run . skill list
go run . skill show project/<skill_name>
go run . skill reload
```

查看当前配置：

```text
/config
```

外部 CLI 也可以查看：

```bash
go run . config
```

## 10. 命令速查

外部 CLI：

```bash
# 查看帮助
go run . help
cohort help

# 进入交互模式
go run .
cohort

# 执行单次任务
go run . ask "任务内容"
cohort ask "任务内容"

# 查看工具
go run . tools
cohort tools

# 查看配置
go run . config
cohort config
cohort --config ~/.cohort/config.yaml config

# 初始化配置
cohort init
cohort init --provider local --force
cohort init --provider anthropic --force

# 诊断环境
go run . doctor
cohort doctor
cohort doctor --connect

# 查看 session 列表，兼容入口
go run . session list
cohort session list

# 恢复 session，兼容入口
go run . session resume <session_id>
cohort session resume <session_id>

# 构建本地二进制
go build -o cohort ./cmd/cohort

# 使用本地二进制
./cohort
./cohort ask "任务内容"
./cohort session list
./cohort session resume <session_id>
```

交互模式内：

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/session resume <session_id>
/compact
/full-compact
/sop candidates
/sop promote <candidate_id> --confirm-index
/clear
/exit
```
