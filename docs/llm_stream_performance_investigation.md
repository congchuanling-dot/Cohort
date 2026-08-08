# LLM 流式输出变慢排查与优化记录

## 背景

用户反馈打开 Cohort 对话时，LLM 响应明显变慢，流式输出卡顿。问题发生在新增 Hook、Context Auto Compact、LSP、Explorer、Capability Adapter 等能力之后。

## 结论

本次卡顿不是 Auto Compact 触发，也不是 Hook 执行导致。主要原因有三类：

1. `Langfuse` 外部观测 sink 同步 HTTP 上报，阻塞 Agent 主循环。
2. 启动 Runner 时默认同步加载项目 `.cohort/local.mcp.json` 里的 MCP Server，普通聊天也会等待 `npx -y` 冷启动。
3. 默认工具面过大，普通对话也向模型暴露 Browser、Desktop、Computer、MCP 等全量工具 schema，导致请求 payload 变大。

## 关键证据

最小复现命令：

```bash
COHORT_DEBUG_PERF=1 /usr/bin/time -p go run . ask "只回复 OK，不要调用工具。"
```

优化前观测：

- 总耗时约 `9.55s`。
- MCP 加载耗时约 `1.87s`。
- 暴露给模型的工具 schema 数量：`81`。
- OpenAI-compatible 请求 payload：约 `77KB`。
- 项目 `.env` 开启 `COHORT_LANGFUSE_ENABLED=true`，Langfuse 上报在主路径同步执行。
- `run.log.jsonl` 显示 `SessionStarted -> RunStarted` 出现约 `1.8s` 间隔，后续观测事件也有明显同步等待。

轻量工具组模式下的优化后观测：

- 总耗时约 `2.44s`。
- Runner 启动约 `7ms`。
- `SessionStarted`、`RunStarted`、`UserPromptSubmitted`、`ContextBuilt`、`TurnStarted`、`LLMRequestStarted` 均在约 `10ms` 内完成。
- 工具 schema 数量下降到 `15`，请求 payload 下降到约 `22KB`。
- LLM 响应本身约 `888ms`。

## 已实施优化

### 1. 工具组显式配置

新增可选配置：

```yaml
tools:
  enabled_groups: [core, lsp, browser, memory, skill, ask]
```

规则：

- `enabled_groups` 为空或省略时，保持全量工具注册。
- 显式配置后，只注册列出的工具组。
- `[*]` 用于明确声明全量工具。
- `mcp` 组未启用时，不加载 MCP Manager，也不启动外部 MCP Server。

当前项目显式注册全量工具：

```yaml
tools:
  enabled_groups: [*]
```

轻量模式建议保留：

- `core`：文件、命令行等核心工具。
- `lsp`：代码诊断与符号查询。
- `browser`：浏览器工具。
- `memory`：短期/长期记忆工具。
- `skill`：Skill 读取工具。
- `ask`：向用户提问工具。

轻量模式默认不启用：

- `desktop`
- `computer`
- `mcp`
- `adapter`

这些能力需要时再显式加入，避免普通对话被重型工具面拖慢。

### 2. MCP 按需加载

优化前：只要项目存在 `.cohort/local.mcp.json`，Runner 启动就会加载 MCP Server。

优化后：全量模式会加载 MCP Manager；只有显式精简列表未包含 `mcp` 时，才跳过 MCP Manager 和外部 MCP Server。

### 3. 本地观测日志异步化

`run.log.jsonl` sink 改为异步队列写入：

- `Emit` 只入队，不阻塞 Agent 主循环。
- 后台 goroutine 负责写文件。
- `Close` 时 flush 队列。
- 队列满时丢弃观测事件，保持观测旁路原则。

### 4. Langfuse 异步化

新增 `observability.AsyncSink`，将 Langfuse HTTP ingestion 放入后台队列。

原则：

- 外部观测失败或变慢不能影响 LLM 请求与流式输出。
- `Close` 有短超时预算，避免退出时长时间等待外部网络。

## 注意事项

浏览器工具恢复后，如果仍返回不可用，优先检查 Chrome bridge/扩展是否连接，而不是工具注册配置：

```bash
go run . tools | rg '^browser_'
go run . doctor computer
go run . extension path
go run . extension open
```

如果需要轻量模式但保留外部 MCP：

```yaml
tools:
  enabled_groups: [core, lsp, browser, memory, skill, ask, mcp]
```

如果需要轻量模式但保留桌面或 Computer Use：

```yaml
tools:
  enabled_groups: [core, lsp, browser, desktop, computer, memory, skill, ask]
```

## 验证命令

```bash
go test ./...
go run . tools | rg '^(browser_|desktop_|computer_|mcp_)'
COHORT_DEBUG_PERF=1 /usr/bin/time -p go run . ask "只回复 OK，不要调用工具。"
```
