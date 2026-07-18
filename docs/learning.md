# Cohert 学习文档

这份文档用于快速理解当前 Cohert 项目。Cohert 是一个用 Go 编写的命令行智能体运行时，当前目标是先跑通本地命令行 Agent 闭环，后续再加 UI、浏览器控制、记忆系统和插件。

## 1. 项目定位

Cohert 当前定位是本地命令行 Agent Runtime。

当前最小闭环是：

```text
用户输入任务
  -> Agent Loop
  -> LLM Chat Completions
  -> 模型返回文本或 tool_calls
  -> Tool Registry 执行本地工具
  -> 工具结果回灌给模型
  -> 模型继续推理或给出最终回答
```

第一版只做命令行，不做 UI，也暂时不做全局安装。当前默认在项目根目录启动。

## 2. 目录结构

```text
cmd/cohert/        本地二进制入口
configs/           本地配置
internal/app/      应用装配、配置加载、Runner 创建
internal/agent/    Agent Loop、输出 Sink、运行结果
internal/llm/      OpenAI-compatible LLM Client、SSE 解析、消息类型
internal/tools/    Tool Registry 和基础工具
workspace/         默认工作区，运行时生成
temp/              模型响应日志，运行时生成
```

## 3. 推荐阅读顺序

### 3.1 先看入口

文件：[cmd/cohert/main.go](../cmd/cohert/main.go)

开发阶段推荐直接在项目根目录使用 `go run .`：

```text
go run .
go run . ask "任务"
go run . tools
go run . config
```

如果先构建本地二进制，也可以在项目根目录使用：

```text
cohert
cohert ask "任务"
cohert tools
cohert config
```

关键点：

- `go run .` 进入 REPL。
- `go run . ask` 执行一次任务。
- `go run . tools` 只列工具，不需要 API Key。
- `go run . config` 只看配置，不需要 API Key。
- `cohert` 是构建出来的本地二进制，不代表已经支持任意路径全局启动。
- 真正跑 Agent 时才会初始化 LLM。

### 3.2 再看应用装配

文件：[internal/app/app.go](../internal/app/app.go)

这里负责创建完整 Runner：

```text
Config
  -> OpenAIClient
  -> Tool Registry
  -> Agent Runner
```

当前注册了 5 个工具：

- `file_read`
- `file_write`
- `file_patch`
- `code_run`
- `ask_user`

### 3.3 再看配置加载

文件：[internal/app/config.go](../internal/app/config.go)

当前没有引入 YAML 第三方库，而是用简单解析器读取 `configs/config.yaml` 的常用字段。

重要配置：

```yaml
llm:
  api_key: ${DEEPSEEK_API_KEY}
  api_base: https://api.deepseek.com
  model: deepseek-v4-pro
```

API Key 推荐通过环境变量提供，不写死在仓库里。

### 3.4 再看 Agent Loop

文件：[internal/agent/runner.go](../internal/agent/runner.go)

核心逻辑在 `Runner.Run`：

```text
append user message
for turn := 1; turn <= maxTurns; turn++:
    call LLM
    stream text to console
    if no tool calls:
        append assistant message
        return done
    append assistant tool_calls
    run each tool
    append tool result messages
    continue next turn
```

这里是 Cohert 的核心调度逻辑：轮次控制、工具调用、结果回灌。

### 3.5 再看 LLM Client

文件：[internal/llm/openai.go](../internal/llm/openai.go)

当前只支持 OpenAI-compatible Chat Completions：

- `POST /v1/chat/completions`
- `stream: true`
- `tools`
- `tool_choice: auto`
- SSE 文本流
- 增量 `tool_calls` 参数拼接

DeepSeek 走这个协议。

### 3.6 最后看工具系统

文件：[internal/tools/registry.go](../internal/tools/registry.go)

`Registry` 负责：

- 注册工具
- 输出工具 schema
- 按模型返回的工具名分发执行

每个工具都实现：

```go
type Tool interface {
    Name() string
    Schema() llm.ToolSchema
    Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error)
}
```

## 4. 当前工具说明

### file_read

文件：[internal/tools/file_read.go](../internal/tools/file_read.go)

读取文本文件，支持：

- `path`
- `start`
- `count`
- `show_linenos`

### file_write

文件：[internal/tools/file_write.go](../internal/tools/file_write.go)

写文件，支持：

- `overwrite`
- `append`
- `prepend`

### file_patch

文件：[internal/tools/file_patch.go](../internal/tools/file_patch.go)

替换唯一文本块。

安全规则：

- `old_content` 不能为空。
- 找不到会返回错误。
- 匹配多次会返回错误。
- 只允许唯一匹配时写入。

### code_run

文件：[internal/tools/code_run.go](../internal/tools/code_run.go)

执行 shell 命令。

支持：

- `script`
- `timeout`
- `cwd`

返回：

- `status`
- `stdout`
- `exit_code`
- `timeout`

### ask_user

文件：[internal/tools/ask_user.go](../internal/tools/ask_user.go)

当模型需要用户补充信息时，在命令行阻塞询问。

## 5. 数据流

一次典型工具调用的数据流：

```text
用户：读取 README.md 并总结
  -> Runner.Run
  -> OpenAIClient.Chat
  -> LLM 返回 tool_call: file_read
  -> Registry.Run(file_read)
  -> FileRead.Run
  -> 工具结果作为 role=tool message 放回 history
  -> 下一轮 LLM 基于工具结果生成总结
```

## 6. 当前边界

当前 MVP 故意没做这些能力：

- UI / TUI / Web / Tauri。
- 浏览器控制。
- 全局安装和任意路径启动。
- Claude 原生协议。
- 多模型 fallback。
- 长期记忆系统。
- scheduler / goal mode。
- 插件系统。

这些能力应该在命令行核心稳定之后再加。

## 7. 下一步开发建议

建议按这个顺序继续：

1. 给 `internal/llm/openai.go` 增加单元测试，覆盖 SSE tool_calls 拼接。
2. 给 `file_patch` 增加单元测试，覆盖 0 次、1 次、多次匹配。
3. 增加 `cohert doctor`，检查 Go 版本、API Key、模型连通性。
4. 增加 session 目录和 `history.jsonl`。
5. 增加 `cohert session list/resume`。
6. 再考虑 TUI 或 Web UI。
