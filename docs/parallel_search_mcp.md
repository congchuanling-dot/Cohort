# Parallel Search MCP

> 文档状态：`[维护]`。这是显式启用的 HTTP MCP 示例，不会默认装配到 Cohort。

[Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp) 提供 `web_search`（搜索并返回 URL 和相关摘录）与 `web_fetch`（提取指定 URL 的内容）。匿名端点不需要 Parallel API Key 或 OAuth，适合探索和轻量使用，有免费额度与速率限制。模型推理仍使用你自己的 Cohort LLM 配置，可能产生独立费用。

## 从源码安装与导入

需要 Go 1.21+ 和可访问端点的网络。在 Cohort 仓库根目录构建：

```bash
go build -o cohort ./cmd/cohort
./cohort mcp import --scope project docs/examples/parallel-search.mcp.json
./cohort mcp list
./cohort mcp probe parallel
./cohort mcp tools parallel
```

导入会合并到当前项目的 `.mcp.json`，保留其他名称的 Server；如已存在名为 `parallel` 的配置，请先备份或修改示例中的名称，导入会覆盖同名定义。无需 Node、Python 或额外 MCP SDK。配置通过 `User-Agent` 标识 Cohort，不携带认证头。不要把 API Key 写入这个匿名示例。

`probe` 成功时应列出 `web_search` 与 `web_fetch`。Agent 中对应的工具名是 `mcp_parallel_web_search` 与 `mcp_parallel_web_fetch`；改名后命名空间也会改变。

## 在 Agent 中搜索与读取

先按 [使用教程](usage.md#0-安装和配置路径) 配置模型，再运行：

```bash
./cohort ask "使用 mcp_parallel_web_search 查找 Go 官方关于 context 取消的说明，给出来源 URL；再用 mcp_parallel_web_fetch 读取 https://go.dev/blog/context 并总结取消信号的传播。"
```

默认外部工具权限仍为 `R2 + ask`。Cohort 会在调用前请求确认，请检查参数后授权。不要为了这个示例放开所有 MCP 工具。查询、目标 URL 与服务返回内容会经由 Parallel，工具结果也会进入你的模型上下文；只提交适合向这些服务公开的信息。远程页面内容始终视为不可信数据。

`web_search` 的必填参数为 `objective` 和 `search_queries`；`web_fetch` 必填 `urls`。Agent 启动时会动态发现参数 schema；`mcp tools parallel` 仅列出工具名称和描述，参数说明也可查阅上述官方文档。若提供 `session_id`，同一任务的搜索与抓取应复用同一个值；`model_name` 仅在确知当前模型标识时填写。通常保留抓取的默认摘录模式，较长摘录或完整页面可能超出 Cohort 的工具结果长度限制并被截断。

## 排障与移除

- `probe` 不可用：检查网络和端点，再查看 `mcp status`。HTTP 超时仍使用现有客户端的 30 秒上限。
- 工具请求被拒绝：检查项目权限与交互确认，不要修改全局默认策略。
- 免费额度或速率限制：遵循服务返回的重试提示；不要把免费理解为无限调用。
- 同名配置被用户级或本地级配置影响：使用 `mcp list` 检查生效 scope，按 [使用教程](usage.md#46-管理-mcp-server) 检查配置优先级。

移除项目级配置：

```bash
./cohort mcp remove --scope project parallel
```

移除后新启动的 Agent 不再加载该 Server；已经运行的会话需要重启。
