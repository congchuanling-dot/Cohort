# Cohert

Cohert 是一个用 Go 编写的本地命令行 Agent Runtime。它把 OpenAI-compatible LLM、工具调用、本地文件系统、Shell、浏览器自动化、会话恢复、上下文压缩、SOP 路由和长期记忆沉淀组合成一个可演进的工程化智能体。

它不是一个聊天壳子，而是一个面向真实开发任务的本地执行层：模型负责规划和推理，Runner 负责控制循环和工具协议，工具层负责可审计的本地动作，Context Manager 负责在长任务里保持上下文可用，Evolution Memory 负责把验证过的经验沉淀到后续任务可检索的记忆中。

## 当前定位

Cohert 的目标是做一个稳定、可控、可观察的本地 Agent 基座：

- 能执行真实文件修改、命令运行和浏览器操作。
- 能保存完整 session，并从历史会话继续工作。
- 能在长上下文下压缩旧工具结果和历史消息，但不破坏原始 history。
- 能通过 SOP 约束高风险或高频操作流程。
- 能把经过工具验证的复用经验写入长期记忆，并在后续任务开始前自动检索相关 entry。

## 快速开始

默认配置位于：

```bash
configs/config.yaml
```

设置 DeepSeek API Key：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
```

检查配置：

```bash
go run . config
```

启动交互模式：

```bash
go run .
```

执行单次任务：

```bash
go run . ask "读取 README.md 并总结当前项目能力"
```

构建二进制：

```bash
go build -o cohert ./cmd/cohert
./cohert
```

完整使用说明见 [docs/usage.md](docs/usage.md)。

## 命令入口

外部 CLI：

```bash
go run .                  # 进入交互模式
go run . ask "任务"       # 执行一次任务后退出
go run . tools            # 查看已注册工具
go run . config           # 查看有效配置
go run . session list     # 列出本地 session
go run . session resume <session_id>
```

交互模式 slash 命令：

```text
/help
/model
/config
/tools
/session
/session list
/session memory
/resume <session_id>
/compact
/full-compact
/memory
/clear
/exit
```

在真实终端中输入 `/` 会打开命令菜单；输入命令前缀后可用 Tab 补全。

## 核心能力

### Agent Loop

`internal/agent` 实现主循环：

- 维护完整 `Runner.history`。
- 支持 OpenAI-style tool calling。
- 支持流式输出。
- 控制最大轮数，默认 `max_turns: 100`。
- 工具调用前后要求模型输出可见行动说明，便于用户理解当前证据、意图和下一步。
- 每轮请求前调用 Context Manager 构造真正发给模型的 request messages。

### 本地工具系统

当前已注册工具包括：

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
```

工具通过 `internal/tools.Registry` 注册，Runner 只依赖统一接口，不直接耦合具体工具实现。

### 文件与命令执行

Cohert 以 `workspace` 作为文件和命令工具的默认工作目录。默认配置：

```yaml
workspace: ./workspace
```

文件工具用于读取、写入和 patch 文本文件；`code_run` 用于在工作区执行 shell 命令。命令输出和工具结果会进入 session history，但旧的大型工具结果在请求模型前可被 Context Manager 进行头尾压缩。

### 浏览器自动化

Cohert 内置 Chrome Browser Bridge，默认监听：

```text
ws://127.0.0.1:18777/browser
```

浏览器能力覆盖：

- 打开或导航页面。
- 扫描 DOM 文本。
- 执行特定 JavaScript。
- 读取可交互元素快照。
- 点击、输入、按键。
- 等待 load、selector、text、url、stable。
- 截图并保存到 workspace。

如果浏览器工具返回 `browser_not_connected`，需要在 Chrome 中加载扩展：

```text
assert/cohert_browser_bridge
```

系统提示会约束浏览器流程：打开页面后先等待加载和稳定，交互前优先 `browser_snapshot`，点击或输入后必须等待明确成功信号再判断结果。

### Session 与可恢复历史

会话保存在：

```text
temp/sessions/<session_id>/
  meta.json
  history.jsonl
  memory.md
  compact.md
```

设计原则：

- `history.jsonl` 保存完整消息历史。
- Context Manager 只裁剪“本轮发给模型的副本”，不修改原始 history。
- `session list` 只读取元信息和行数，避免加载大历史。
- `session resume <id>` 读取 history 并进入交互模式继续工作。

### Context Manager

`internal/contextmgr` 负责请求前上下文构造。

它做几件事：

- 清理协议非法的孤立 tool result。
- 注入长期记忆索引 `memory/index.md`。
- 根据当前任务关键词自动注入相关长期记忆 entry。
- 注入 session memory：`temp/sessions/<id>/memory.md`。
- 注入 full compact 摘要：`temp/sessions/<id>/compact.md`。
- 在触发阈值后压缩旧工具结果。
- 超预算时按 message group 裁剪旧历史，保护 tool call/result 结构完整。

默认上下文配置：

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

上下文统计写入：

```text
temp/model_responses/context.log
```

日志只记录消息数、估算 token、触发原因、是否注入 memory、是否压缩等统计，不记录完整 message 内容。

### Compact 与 Full Compact

交互模式中可以手动触发：

```text
/compact
/full-compact
```

`/compact` 生成当前 session 的 `memory.md`，用于保存稳定事实、当前目标和继续任务所需的短上下文。

`/full-compact` 生成 `compact.md`，用于长历史恢复，保留任务目标、技术背景、关键文件、错误修复、当前进度和下一步。

这两个动作不会写入 `history.jsonl`，也不会调用本地工具；它们是专门的压缩生成流程。

### SOP 路由

项目内置 SOP 索引：

```text
sops/index.md
```

系统提示会把 SOP Index 作为导航注入。任务命中 SOP 场景时，Runner 会提示模型先读取相关 SOP，并在采用后调用：

```text
update_working_checkpoint
```

工作记忆中会保存 `key_info` 和 `related_sop`，防止长任务中逐渐遗忘关键约束。

当前 SOP 覆盖：

```text
sops/browser_sop.md
sops/code_run_sop.md
sops/file_edit_sop.md
sops/context_sop.md
sops/testing_sop.md
sops/meta_sop.md
```

### Evolution Memory

Cohert 已具备受控长期记忆沉淀链路：

```text
start_long_term_update
memory_propose_update
memory_apply_update
```

长期记忆真实存储在 workspace 下：

```text
workspace/
  memory/
    index.md
    global.md
    projects/
      <project_id>/
        project.md
    reflection/
      sop_candidates.md
    audit.jsonl
```

核心约束：

- No Execution, No Memory。
- 候选必须引用 Runner 收集到的 verified evidence。
- 只允许 append 到 allowlist 目标。
- 拒绝敏感信息、未验证信息、重复内容和需要用户确认的候选。
- 写入后必须 read-back 确认。
- 所有 apply 都写入 `memory/audit.jsonl`。

结构化 memory entry 使用：

```text
scene
trigger_keywords
lesson
recommended_steps
evidence_ids
```

后续任务开始前，Context Manager 会按当前用户任务关键词、`trigger_keywords` 和 `scene` 进行 entry 级匹配，只注入最相关的几条长期记忆，而不是整文件塞进上下文。

如果某条经验已经稳定到值得升级为 SOP，可以通过 `promote_to_sop` 写入：

```text
memory/reflection/sop_candidates.md
```

这只是 SOP 候选，不会直接修改活跃 `sops/index.md`，避免未审核流程立即影响系统行为。

## 配置

默认配置：

```yaml
language: zh
workspace: ./workspace
log_dir: ./temp/model_responses
max_turns: 100

llm:
  provider: openai
  name: deepseek
  api_key: ${DEEPSEEK_API_KEY}
  api_base: https://api.deepseek.com
  model: deepseek-v4-pro
  stream: true
  connect_timeout_seconds: 10
  read_timeout_seconds: 120
  max_retries: 2
```

当前 LLM Client 走 OpenAI-compatible Chat Completions。模型上下文窗口由 app 层根据模型名解析，`deepseek-v4-pro` / `dsv4pro` 按大上下文模型处理。

## 目录结构

```text
cmd/cohert/             CLI 入口
configs/                本地配置
docs/                   使用说明、设计文档和开发记录
internal/app/           应用装配、配置加载、系统提示词
internal/agent/         Agent Loop、session compact、工具执行控制
internal/browser/       Browser Bridge 服务端和协议
internal/cli/           外部 CLI 子命令
internal/contextmgr/    请求前上下文构造、压缩和长期记忆注入
internal/evolution/     长期记忆结构、校验、写入和审计
internal/llm/           OpenAI-compatible LLM Client
internal/repl/          交互式命令、slash command、命令菜单
internal/session/       session 元信息和 history.jsonl 存储
internal/tools/         文件、命令、浏览器、记忆等工具实现
sops/                   SOP 索引和专项流程文档
workspace/              默认工作区
temp/                   session、模型响应和上下文日志
assert/                 浏览器桥扩展等辅助资源
```

## 开发验证

运行全量测试：

```bash
go test ./...
```

运行静态检查：

```bash
go vet ./...
```

查看可用工具：

```bash
go run . tools
```

查看有效配置：

```bash
go run . config
```

## 设计原则

- 本地优先：文件、命令、浏览器和记忆都围绕本机工作区组织。
- 原始历史不可变：压缩只影响请求副本，不破坏 session audit trail。
- 工具可审计：每个工具有稳定 schema、结构化结果和错误提示。
- 上下文分层：SOP、session memory、full compact、long-term memory 各司其职。
- 记忆受控写入：长期记忆必须来自 verified evidence，不能让模型把猜测沉淀成事实。
- 渐进演进：先把 CLI Runtime、工具协议和记忆闭环做稳，再扩展 UI、多模型 fallback、插件和更强检索。
