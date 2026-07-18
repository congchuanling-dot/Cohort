# Cohert 开发任务拆解表

这份文档用于指导 Cohert 后续开发。

它不是照搬 GA 的功能列表，而是结合三类信息重新排序：

- Cohert 当前已经实现的能力。
- GA 项目中已经验证过的工具形态和工程经验。
- Cohert 实际运行中暴露的问题，例如 `code_run` 扫描用户目录导致卡住。

当前开发原则：

- 先保证命令行 Agent 核心可靠，再做 UI、浏览器、多模型等外围能力。
- 先解决真实阻塞问题，再做体验优化。
- 任务拆解必须能落到具体文件、测试和验收标准。
- 每完成一个开发任务，都要同步更新 `docs/开发记录文档.md`。

## 1. 当前状态

### 1.1 已完成能力

| 模块 | 当前状态 | 说明 |
| --- | --- | --- |
| CLI 入口 | 已完成 | 支持 `go run .`、`go run . ask`、`go run . tools`、`go run . config` |
| Agent Loop | 已完成 | 支持用户输入、模型调用、工具调用、工具结果回灌、最大轮数保护 |
| LLM Client | 已完成 | 支持 OpenAI-compatible SSE 流式输出 |
| 基础工具 | 已完成 | `file_read`、`file_write`、`file_patch`、`code_run`、`ask_user` |
| 运行状态常量 | 已完成 | `done/exited/max_turns_exceeded` 已抽成常量 |
| 工具名常量 | 已完成 | 基础工具名已集中定义 |
| 工具错误格式 | 已完成 | 支持 `ToolErrorData{status/code/message/hint}` |
| bad JSON 提示 | 已完成 | 工具参数 JSON 解析失败会回灌结构化错误 |
| OpenAI SSE 测试 | 已完成 | 覆盖文本流和 tool_calls 分片拼接 |
| Session 数据结构 | 已完成 | 已有 `Session`、`HistoryEntry`、`Store` |
| history.jsonl 写入 | 已完成 | Runner 会把 user/assistant/tool 消息追加落盘 |
| session list/resume | 已完成 | 支持列出本地会话并恢复历史上下文 |
| 开发记录文档 | 已完成 | 已建立 `docs/开发记录文档.md` |

### 1.2 当前主要问题

| 问题 | 严重程度 | 原因 | 处理优先级 |
| --- | --- | --- | --- |
| `code_run` 可能卡住 | 高 | 模型可能执行大范围 `grep -r`、`find`，当前超时和进程树清理不够硬 | 最高 |
| `code_run` 可跳出 workspace | 高 | 模型可在脚本里写 `cd /Users/...` | 最高 |
| 工具输出可能撑爆上下文 | 中 | 长 stdout 和长 history 缺少统一裁剪策略 | 高 |
| 运行链路缺少结构化日志 | 中 | 只有模型 raw log，缺少 turn/tool/run 级别日志 | 中 |
| 文件工具反馈不够细 | 中 | 读错路径、patch 成功后缺少候选和摘要 | 中 |
| doctor 优先级偏低 | 低 | 它主要是环境诊断体验，不是 Agent 核心链路 | 后置 |

## 2. GA 经验对 Cohert 的影响

### 2.1 code_run

GA 的 `code_run` 主要特点：

- 默认超时 60 秒。
- Python 模式会写临时 `.py` 文件执行。
- Python 临时文件会插入 `assets/code_run_header.py`，用于兼容编码、隐藏窗口和错误提示。
- Shell 模式在 macOS/Linux 使用 `bash -c`，不会像 `bash -lc` 那样加载用户 shell 配置。
- 通过后台线程持续读取 stdout，并把输出流式打印出来。
- 超时后调用 `process.kill()`。
- 支持 `stop_signal`，用户可中断。
- 输出会做 `smart_format` 截断。

GA 暴露出的不足：

- 只 kill 当前进程，不保证杀掉整个子进程树。
- 没有强制阻止脚本里 `cd /Users/...`、`cd ~`、`cd /`。
- 没有禁止大范围 `grep -r .`、`find /`。
- timeout 结果主要写在 stdout 中，不够结构化。

Cohert 的改进方向：

- 保留结构化结果。
- 改掉 `bash -lc`，避免加载用户 `.bashrc`。
- 保留 GA 风格 60 秒默认超时，并增加最大超时上限。
- Unix 下超时杀整个进程组。
- command guard 后置；当前先不增加独立工具拦截层，避免过早复杂化调用链。

### 2.2 文件工具

GA 的文件工具更成熟，主要体现在：

- 修改前强调先读文件。
- `file_read` 支持行号、分页和关键字搜索。
- `file_patch` 强调唯一匹配。
- 写入结果会尽量让模型知道下一步怎么修正。

Cohert 的改进方向：

- 保留 `file_patch` 唯一匹配机制。
- 增加路径不存在时的候选建议。
- 增加文件读取结构化结果。
- 增加写入和 patch 的变更摘要。

### 2.3 工作记忆和长期记忆

GA 有 `update_working_checkpoint`、memory、SOP、长期经验沉淀等能力。

Cohert 当前不急着做完整 memory 系统，但要保留方向：

- P0 先做 session 和 run.log。
- P1 再做 memory/checkpoint。
- 长任务前期不引入复杂多 Agent 或 SOP 系统。

### 2.4 浏览器和多前端

GA 有 Web、TUI、桌面、IM、浏览器工具等大量外围能力。

Cohert 当前不优先做这些：

- CLI 核心稳定前，不做 Web UI。
- 浏览器工具先放 P1，只做只读能力。
- 多前端和 IM 集成放更后面。

## 3. 新的里程碑

### M0：已完成的基础闭环

目标：

- Cohert 能作为最小命令行 Agent 跑通。
- 基础工具、错误格式、SSE 测试、session 写入具备雏形。

已覆盖任务：

- P0-001：补齐运行状态常量。
- P0-002：补齐工具名常量。
- P0-003：更新引用和基础验证。
- P0-010：定义 Session 数据结构。
- P0-011：增加 session 存储目录。
- P0-012：Runner 写入 history.jsonl。
- P0-030：定义错误结果格式。
- P0-031：unknown tool 提示。
- P0-032：bad JSON 提示。
- P0-080：OpenAI SSE 文本测试。
- P0-081：OpenAI tool_calls 测试。

### M1：命令执行必须可靠

目标：

- `code_run` 不再因为大范围命令长期卡住。
- 即使命令范围过大，也能通过 timeout 和进程组清理尽快收住。
- 超时后能可靠结束整个命令树。

为什么优先：

- 这是当前真实遇到的问题。
- 命令工具是 Agent 最危险也最常用的工具。
- 不先处理这里，后续任何任务都可能被模型一个坏命令拖住。

### M2：session 可列出、可恢复

目标：

- 已写入的 `history.jsonl` 能被列出和恢复。
- 用户退出后可以继续上次会话。

为什么优先：

- P0-012 已经完成写入，如果不做 resume，session 价值只完成了一半。
- 恢复能力是长任务的基础。

### M3：上下文和日志可控

目标：

- 工具输出不会无限塞进下一轮模型请求。
- 每轮模型和工具执行都能在 `run.log` 中追踪。

为什么优先：

- 长任务稳定性依赖上下文裁剪。
- 出问题时需要结构化日志定位是哪一轮、哪个工具、什么参数导致的。

### M4：文件工具更稳

目标：

- 文件路径错时给候选建议。
- 文件读取、写入、patch 返回结构化结果。
- file_patch 测试覆盖 0 次、1 次、多次匹配。

为什么优先：

- Cohert 是编码 Agent，文件工具质量直接影响修改质量。

### M5：工具调用和错误兜底

目标：

- 模型原生 tool calling 失败时，可以通过文本工具块兜底。
- 空响应、工具失败、坏格式都能给模型明确修复建议。

为什么优先级低于 M1-M4：

- 当前 DeepSeek 原生 tool calling 已经能跑。
- 兜底解析重要，但不如命令卡死和 session 恢复紧急。

### M6：doctor 和配置体验

目标：

- 提供 `go run . doctor` 做环境检查。

为什么后置：

- doctor 主要解决“用户不知道为什么启动失败”的体验问题。
- 当前更大的问题是 Agent 运行过程中工具安全和恢复能力。

## 4. P0 任务拆解

### 4.1 已完成基础任务

| ID | 任务 | 状态 | 关键文件 | 验收 |
| --- | --- | --- | --- | --- |
| P0-001 | 补齐运行状态常量 | 已完成 | `internal/agent/types.go` | Runner 状态使用常量 |
| P0-002 | 补齐工具名常量 | 已完成 | `internal/tools/registry.go` | 工具名集中定义 |
| P0-003 | 更新引用和测试 | 已完成 | 多文件 | `go test ./...` 通过 |
| P0-010 | 定义 Session 数据结构 | 已完成 | `internal/session/types.go` | `Session`、`HistoryEntry` 可用 |
| P0-011 | 增加 session 存储目录 | 已完成 | `internal/session/store.go` | 可创建 `temp/sessions/<id>` |
| P0-012 | Runner 写入 history.jsonl | 已完成 | `internal/agent/runner.go` | user/assistant/tool 消息可落盘 |
| P0-030 | 定义错误结果格式 | 已完成 | `internal/agent/types.go` | `ToolErrorData` 可用 |
| P0-031 | unknown tool 提示 | 已完成 | `internal/tools/registry.go` | 未知工具返回可用工具提示 |
| P0-032 | bad JSON 提示 | 已完成 | `internal/agent/runner.go` | 坏参数回灌结构化错误 |
| P0-080 | OpenAI SSE 文本测试 | 已完成 | `internal/llm/openai_test.go` | 文本流拼接被测试覆盖 |
| P0-081 | OpenAI tool_calls 测试 | 已完成 | `internal/llm/openai_test.go` | 工具调用分片拼接被测试覆盖 |

### 4.2 命令执行安全和超时

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-060 | 按 GA 思路稳住 code_run 执行器 | 不增加独立 guard 层，先把执行器本身改稳 | 无 | 0.4 |
| P0-061 | 调整 shell 启动方式 | Unix 下从 `bash -lc` 改为 `bash -c`，避免加载 `.bashrc` | P0-060 | 0.2 |
| P0-062 | 优化 timeout 策略 | 保留 60 秒默认值，增加最大 timeout 上限 | P0-060 | 0.3 |
| P0-063 | 超时杀进程组 | Unix 下创建独立进程组，超时杀整组 | P0-062 | 0.4 |
| P0-064 | 命令结果结构优化 | 返回 `status/stdout/exit_code/timeout/timeout_seconds/hint` | P0-062 | 0.3 |
| P0-065 | code_run timeout 测试 | 覆盖长命令、timeout 结构化结果、timeout 上限 | P0-062、P0-063 | 0.4 |
| P0-066 | shell 启动方式测试 | 覆盖 Unix 下不加载 bash 启动文件 | P0-061 | 0.3 |
| P0-067 | 更新测试文档和开发记录 | 写明 code_run 的 GA 风格执行策略和超时行为 | P0-060 到 P0-066 | 0.2 |
| P0-068 | 危险命令确认策略 | 后置任务，必要时再在 `code_run.go` 内部做轻量拦截 | P0-060 | 0.5 |
| P0-069 | command guard 测试 | 后置任务，覆盖 `rm -rf`、`git reset --hard`、大范围扫描 | P0-068 | 0.4 |

交付物：

- `internal/tools/code_run.go`
- `internal/tools/code_run_test.go`
- Unix/Windows 平台辅助文件。

验收标准：

- Unix 下 `code_run` 不再因为 `bash -lc` 加载用户 shell 配置。
- 超时命令能返回结构化结果，包含 `timeout` 和 `timeout_seconds`。
- Unix 下超时会尽量杀掉整组子进程。
- 普通 `go test ./...`、`rg xxx internal`、`pwd` 不受影响。
- `go test ./...` 通过。

### 4.3 session list 和 resume

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-013 | 增加 session list 数据读取 | 已完成：读取 `meta.json`，统计 `history.jsonl` 行数 | P0-011 | 0.4 |
| P0-014 | 增加 session resume 数据读取 | 已完成：读取 `history.jsonl`，恢复 `[]llm.Message` | P0-012 | 0.5 |
| P0-015 | Runner 支持加载历史 | 已完成：创建 Runner 后可注入已有 history 和 sessionID | P0-014 | 0.4 |
| P0-016 | CLI 接入 session list | 已完成：`go run . session list` | P0-013 | 0.3 |
| P0-017 | CLI 接入 session resume | 已完成：`go run . session resume <id>` | P0-015 | 0.4 |
| P0-018 | session 测试和文档 | 已完成：覆盖 list/resume，更新测试文档和开发记录 | P0-016、P0-017 | 0.4 |

交付物：

- `internal/session/store.go`
- `internal/session/store_test.go`
- `internal/cli/cli.go`
- `internal/agent/runner.go`

验收标准：

- 退出进程后能列出历史 session。
- resume 后模型能看到之前对话上下文。
- `/clear` 的语义明确：清空当前内存上下文并开启新 session。

### 4.4 上下文裁剪

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-020 | 增加裁剪配置 | `max_history_messages`、`max_tool_result_chars` | P0-010 | 0.3 |
| P0-021 | 裁剪工具结果 | 超长 role=tool 内容保留首尾摘要 | P0-020 | 0.4 |
| P0-022 | 裁剪历史消息 | 超过数量时保留最近 N 条，并保证 tool_call 协议完整 | P0-020 | 0.5 |
| P0-023 | 裁剪提示和日志 | 返回/记录裁剪了哪些内容 | P0-021、P0-022 | 0.3 |
| P0-024 | 裁剪测试 | 覆盖长工具输出、长 history、tool_call 配对 | P0-021、P0-022 | 0.4 |

交付物：

- `internal/agent/context_trim.go`
- `internal/agent/context_trim_test.go`
- 配置字段和文档。

验收标准：

- 工具输出过长不会无限进入下一轮模型请求。
- 裁剪后不会破坏 assistant tool_calls 和 tool 结果的配对关系。

### 4.5 结构化运行日志

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-090 | 定义 run.log 格式 | JSONL，包含 session、turn、event、tool、status | P0-010 | 0.3 |
| P0-091 | 记录 LLM 事件 | 记录每轮开始、完成、错误 | P0-090 | 0.3 |
| P0-092 | 记录工具事件 | 记录工具名、参数摘要、结果摘要、耗时 | P0-090 | 0.4 |
| P0-093 | 敏感信息脱敏 | API Key、长文本、命令输出截断 | P0-092 | 0.4 |
| P0-094 | 日志测试和文档 | 验证日志 JSONL 可解析，更新开发记录 | P0-091 到 P0-093 | 0.3 |

交付物：

- `temp/sessions/<session_id>/run.log`
- 日志结构定义和测试。

验收标准：

- 一次任务能清楚追踪每轮模型、每次工具调用和错误。
- 日志里不泄露 API Key。

### 4.6 文件工具增强

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-050 | 路径不存在候选建议 | `file_read` 失败时扫描相近文件 | 无 | 0.4 |
| P0-051 | 文件读取结果结构化 | 返回 `path/start/count/content/truncated` | P0-050 | 0.3 |
| P0-052 | file_write 变更摘要 | 返回 `written_bytes/mode/path/created` | 无 | 0.3 |
| P0-053 | file_patch 变更摘要 | 返回匹配位置、旧长度、新长度 | 无 | 0.4 |
| P0-054 | 限制超大文件读取 | 大文件默认截断并提示 | P0-051 | 0.3 |
| P0-055 | file_patch 测试 | 覆盖 0 次、1 次、多次匹配 | 无 | 0.4 |
| P0-056 | path 测试 | 覆盖相对路径、绝对路径、空路径、越界路径 | P0-050 | 0.4 |
| P0-057 | 文档和开发记录 | 更新测试文档和开发记录 | P0-050 到 P0-056 | 0.2 |

交付物：

- 更稳定的文件工具返回结构。
- 文件路径错误时有候选提示。

验收标准：

- 模型读错路径后能基于候选重新调用。
- patch 是否生效能从工具结果中看出来。

### 4.7 工具错误和兜底解析

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-033 | empty response 处理 | 模型空响应时生成重试提示 | P0-030 | 0.3 |
| P0-034 | 工具错误回灌优化 | 所有工具失败尽量返回 `ToolErrorData` | P0-030 | 0.4 |
| P0-035 | 错误处理测试 | 覆盖 unknown tool、bad JSON、工具失败、空响应 | P0-031 到 P0-034 | 0.4 |
| P0-040 | 定义文本工具调用格式 | 支持 `<tool_use>{"name":"...","arguments":{...}}</tool_use>` | P0-030 | 0.3 |
| P0-041 | 增加解析器 | 从模型普通文本中提取工具调用 | P0-040 | 0.5 |
| P0-042 | 接入 Runner | 原生 `ToolCalls` 为空时尝试文本兜底解析 | P0-041 | 0.4 |
| P0-043 | 清理展示文本 | 去除文本中的 tool_use 块，避免重复展示 | P0-042 | 0.3 |
| P0-044 | 增加解析测试 | 覆盖 XML、JSON、坏 JSON、多工具调用 | P0-041 | 0.4 |
| P0-045 | 更新系统提示词 | 原生工具优先，文本工具调用仅兜底 | P0-042 | 0.2 |

交付物：

- `internal/llm/tool_parse.go`
- Runner 中的兜底解析逻辑。

验收标准：

- 模型没有走原生 tool calling 时，仍可通过文本工具块触发工具。
- 坏格式不会导致进程崩溃。

### 4.8 doctor 配置检查

doctor 后置，不进入近期最优先 10 项。

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P0-070 | 定义 doctor 输出结构 | 检查项、状态、建议 | 无 | 0.2 |
| P0-071 | 检查 Go 版本 | 输出当前 Go 版本和建议版本 | P0-070 | 0.2 |
| P0-072 | 检查 API Key | 检查 `DEEPSEEK_API_KEY` 或配置文件 | P0-070 | 0.2 |
| P0-073 | 检查模型连通性 | 发起最小模型请求 | P0-072 | 0.5 |
| P0-074 | 检查 workspace/logdir/session dir | 验证目录是否可创建、可写 | P0-070 | 0.2 |
| P0-075 | CLI 接入和文档 | `go run . doctor` | P0-071 到 P0-074 | 0.2 |

交付物：

- `go run . doctor`

验收标准：

- 用户能通过 doctor 快速定位 API Key、模型连接、目录权限问题。

## 5. P1 任务拆解

P1 只在 P0 核心稳定后推进。

### 5.1 多模型配置

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P1-100 | 抽象 Provider 配置 | 支持多个 provider profile | P0-070 | 0.4 |
| P1-101 | 支持模型选择 | `go run . --model xxx` 或配置默认模型 | P1-100 | 0.4 |
| P1-102 | 支持多 base_url | 同 OpenAI-compatible 不同供应商 | P1-100 | 0.4 |
| P1-103 | 支持 fallback | 主模型失败后切备用模型 | P1-102 | 0.6 |
| P1-104 | 更新 config 文档 | 给出多模型配置示例 | P1-101 | 0.3 |
| P1-105 | 测试 | fake provider 配置测试 | P1-100 到 P1-103 | 0.4 |

### 5.2 Claude 协议

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P1-110 | 定义 Anthropic Client | 实现 `llm.Client` 接口 | P1-100 | 0.6 |
| P1-111 | 消息格式转换 | Cohert Message -> Claude Messages | P1-110 | 0.6 |
| P1-112 | 工具 schema 转换 | OpenAI tool schema -> Claude tool schema | P1-111 | 0.6 |
| P1-113 | 流式解析 | 解析 Claude text/tool_use delta | P1-112 | 0.8 |
| P1-114 | tool result 回灌 | Claude 工具结果格式适配 | P1-113 | 0.5 |
| P1-115 | 配置接入 | `provider: anthropic` | P1-110 | 0.4 |
| P1-116 | 测试和文档 | 覆盖文本和工具调用 | P1-111 到 P1-115 | 0.5 |

### 5.3 browser 只读工具

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P1-120 | 定义 browser 工具接口 | 先只做只读能力 | P0-030 | 0.3 |
| P1-121 | 当前页面读取 | 获取 URL、title、文本摘要 | P1-120 | 0.6 |
| P1-122 | 标签页列表 | 列出可用页面 | P1-121 | 0.4 |
| P1-123 | 文本截断策略 | 避免网页内容过长 | P0-020、P1-121 | 0.4 |
| P1-124 | CLI/配置开关 | 浏览器工具默认可关闭 | P1-120 | 0.4 |
| P1-125 | 文档和手测 | 写浏览器工具测试步骤 | P1-121 到 P1-124 | 0.4 |

### 5.4 memory 和 checkpoint

| ID | 任务 | 说明 | 依赖 | 预估 |
| --- | --- | --- | --- | ---: |
| P1-130 | 定义 memory 目录 | `memory/notes.md`、`memory/checkpoints.jsonl` | P0-010 | 0.3 |
| P1-131 | memory_read 工具 | 允许模型读取指定 memory 文件 | P1-130 | 0.4 |
| P1-132 | memory_write 工具 | 允许模型追加长期记忆 | P1-130 | 0.5 |
| P1-133 | update_checkpoint 工具 | 记录当前任务阶段摘要 | P1-130 | 0.5 |
| P1-134 | 系统提示词接入 | 告诉模型何时使用 checkpoint | P1-133 | 0.3 |
| P1-135 | 安全限制 | memory 写入目录限制和内容长度限制 | P1-132 | 0.4 |
| P1-136 | 文档和测试 | 覆盖读写、checkpoint 更新 | P1-131 到 P1-135 | 0.6 |

### 5.5 hook、TUI、安装、计划和调度

| 功能 | 处理原则 |
| --- | --- |
| hook 机制 | 先等 `run.log` 稳定，再抽象生命周期事件 |
| TUI/终端体验 | CLI 核心稳定后再做折叠、样式和历史输入 |
| 安装脚本 | 当前先不做全局启动，等 P0 基本完成后再做 |
| 计划模式 | 长任务能力稳定后再做 `plan` 工具 |
| scheduler | 不进入近期计划，等 session/memory 稳定后再考虑 |
| 成本统计 | 等 LLM usage 字段和 run.log 稳定后再做 |

## 6. 最新建议优先做的 10 个任务

如果只选当前最值得先做的 10 个任务，建议顺序是：

1. P0-060：按 GA 思路稳住 code_run 执行器。
2. P0-061：Unix 下从 `bash -lc` 改为 `bash -c`。
3. P0-062：优化 timeout 策略。
4. P0-063：超时杀进程组。
5. P0-064：命令结果结构优化。
6. P0-065：code_run timeout 测试。
7. P0-066：shell 启动方式测试。
8. P0-020：增加裁剪配置。
9. P0-021：裁剪工具结果。
10. P0-090：定义 run.log 格式。

doctor 和 command guard 都不再放进最优先 10 项。

## 7. 依赖关系摘要

```text
已完成基础：
P0-001/P0-002/P0-003
P0-010/P0-011/P0-012
P0-030/P0-031/P0-032
P0-080/P0-081

命令执行安全：
P0-060
  -> P0-061
  -> P0-062
  -> P0-064
  -> P0-065
P0-061
  -> P0-066
P0-068 command guard 后置
  -> P0-069

session 恢复：
P0-012
  -> P0-014 已完成
  -> P0-015 已完成
  -> P0-017 已完成
P0-011
  -> P0-013 已完成
  -> P0-016 已完成

上下文和日志：
P0-010
  -> P0-020
  -> P0-090
P0-020
  -> P0-021/P0-022
P0-090
  -> P0-091/P0-092/P0-093

文件工具：
P0-050
  -> P0-051
  -> P0-056
P0-053
  -> P0-055

后置：
P0-070 doctor
  -> P1-100 多模型配置检查
```

## 8. 执行规则

每做一个任务，都必须完成：

1. 先说明技术方案。
2. 修改代码或文档。
3. 增加或更新测试。
4. 执行 `gofmt` 和 `go test ./...`，纯文档改动可不跑测试，但要说明原因。
5. 更新 `docs/开发记录文档.md`。
6. 最终汇报改了什么、怎么验证、还有什么风险。
