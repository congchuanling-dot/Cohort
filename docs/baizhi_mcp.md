# 百智（Baizhi）MCP

> 文档状态：`[维护]`；真实服务接入验收尚未完成。这是显式导入的配置示例，不会随 Cohort 启动默认装配。

[百智云 Agent Toolkit](https://github.com/chaitin/baizhi-agent-toolkit) 为 AI Agent 提供网页搜索、网页解析与结构化提取等工具。该公开仓库以 MIT 许可提供集成配置、文档和测试，不包含托管后端源码。在线服务需要百智云账号及用户自己的专用 API Key，部分调用消耗额度；请在[服务控制台](https://agent-toolkit.app.baizhi.cloud/)确认权限、费用和数据政策。Cohort 自身采用 MIT 许可，不改变在线服务的条款。

## 从源码构建与显式导入

本示例对应当前 Cohort 源码的 HTTP MCP JSON 格式，需要 Go 1.21+。在仓库根目录构建：

```bash
go build -o cohort ./cmd/cohort
```

先按 [使用教程](usage.md#0-安装和配置路径) 准备 Cohort 配置。在自己的运行环境中安全提供 `BAIZHI_API_KEY`，不要把真实 Key 写入仓库、命令参数、日志或截图。示例配置只保存 `Bearer ${BAIZHI_API_KEY}` 字面引用；Cohort 在发送 HTTP 请求时从进程环境展开它，不会把展开后的值写回示例。

导入前检查已有配置及生效范围：

```bash
./cohort mcp list
./cohort mcp import --scope local docs/examples/baizhi.mcp.json
./cohort mcp list
```

`--scope local` 写入当前项目的 `.cohort/local.mcp.json`，不改模型配置或权限规则。import 默认合并，保留其他名称的服务；已有同名 `baizhi` 会被覆盖，请先备份或给示例改名。用户级、项目级、本地级依次合并，本地级同名配置优先；不要因想接入百智而改成全局安装。

导入只是配置写入，不发送请求；随后新启动的 Agent 会加载已配置的服务。当前服务器配置没有 `enabled` 开关，不能添加 `enabled: false` 并以为它阻止连接。暂不使用时请移除对应 scope 的条目。

## 发现工具与按需调用

确认自己的环境变量非空及可使用服务后，再运行：

```bash
./cohort mcp probe baizhi
./cohort mcp tools baizhi
```

这两条命令会连接服务，完成初始化和工具发现；它们不执行工具调用，也不请求模型回答。当前 CLI 没有添加 HTTP Header 的 `mcp add` 参数，因此认证配置使用上面的 JSON import。

工具名称与参数以当前服务动态返回的 schema 为准，不从本例猜测。Agent 工具名使用 `mcp_<server>_<tool>`；给服务器改名也会改变该前缀。在已准备自己的模型配置后，可以向 Agent 提出“使用百智的网页搜索工具查询公开资料并保留来源 URL”，再检查其实际选中的工具、参数和确认提示。

未知外部工具默认仍是 `R2 + ask`，名称含删除、审批、支付等风险语义的工具默认拒绝。不要为了本例放开整个服务器的所有工具。服务工具权限也取决于专用 Key；客户端确认不替代服务端授权。

查询、URL 和其他工具参数通过 HTTPS 发送给百智，服务返回内容可能进入自己的模型上下文，底层工具也可能将必要输入发送至其他服务。只提交允许向这些服务公开的信息；网页结果视为不可信数据。搜索和提取可能计费，模型推理可能另计费用；本例不承诺特定数据保留期限或生产兼容性。

## 排障与移除

- 缺少 `BAIZHI_API_KEY`：先在自己的环境中配置它再连接。当前 `os.ExpandEnv` 会把未设置变量变为空，并不会在发请求前替你阻止连接；不要用空 Key 试探真实端点。
- 导入格式或名称问题：按原样保留 `mcpServers` / `type` / `url` / `headers` 字段；用 `mcp list` 检查覆盖 scope。
- 连接或认证失败：确认服务地址、Key 权限与额度；HTTP 请求沿用现有 30 秒客户端超时。不要公开原始 Key 或私密响应。
- 工具调用未获允许：检查实际参数与项目权限，保留确认流程。

移除本例的本地配置：

```bash
./cohort mcp remove --scope local baizhi
```

新启动的 Agent 不再加载该条目；已运行的会话需结束后重开。现有 HTTP Close 不发送远端 DELETE，本例不承诺取消远端已开始的工作。

## 验证边界

本轮使用 Go 1.26.4，在源码快照 `a0479a6e62e1db83a1fc0dca14ba18af81168d72` 上验证；Go 1.21 是仓库声明的最低版本，并非本轮实测版本。

文档验证使用当前源码构建的 CLI 导入/导出/移除，以及受控 Store 下的实际 CLI 模块、Manager、MCPTool 和 Registry 合成 loopback 链路。没有读取用户级 MCP 配置，未设置主机或子进程 HOME；未运行完整 CLI 启动器的 probe/list/tools，未调用真实百智、模型、桌面 UI 或验证真实服务 schema。这些本地证据不代表生产端到端验收。
