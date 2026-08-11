# Cohort Control Center

> 状态：`[完成]`
>
> 分支：`feature/cohort-control-center`

## 1. 目标

把 Cohort 的 CLI、Agent Session、Delivery、Hermes、Eval、Trace、Capability、MCP、Skill、
LSP 和配置能力收敛到一个本地优先的可视化控制台，同时保留 CLI 作为脚本和高级操作入口。

稳定入口：

```bash
cohort ui
cohort ui --no-open
cohort ui --listen 127.0.0.1:0
```

默认只监听 loopback，端口可随机分配，浏览器静态资源嵌入 Cohort 二进制。

## 2. 产品原则

1. 不把两百个命令平铺成两百个按钮。
2. 高频闭环使用专用页面，长尾能力使用可搜索 Action Catalog 自动生成表单。
3. CLI 与 UI 复用同一领域 Store/Service/状态机，不在 HTTP 后端拼接 Shell 命令。
4. 所有长任务持久化为 Operation，支持进度、取消、失败、重启恢复和审计。
5. 高风险动作继续执行现有状态机、Evidence 和人工审批门禁。
6. API Key 等 Secret 永远不通过查询 API 回传前端。

## 3. 页面

| 页面 | 核心能力 |
| --- | --- |
| Overview | 项目健康、Agent/Delivery/Hermes 运行状态、风险和快捷动作 |
| Sessions | 创建任务、恢复历史、实时输出、停止和 Operation 审计 |
| Deliveries | 状态列表、详情资源、Integrate、Review、Approve、Merge、Recover、Cancel |
| Operations | Operation 历史/详情/取消、Hermes Action、Reflection Queue 动作 |
| Quality | Eval 历史、通过率、Trace Session、Token 和回归状态 |
| Capabilities | MCP、Skill、Capability、LSP 管理及 Plugin 诊断资源 |
| Settings | 模型 Profile 状态、API Key 配置状态和原子 Profile 切换 |
| Command Center | `⌘K` 搜索所有 Action，按 Schema 自动生成参数表单 |

## 4. 架构

```text
React + TypeScript SPA
        |
    REST + SSE
        |
internal/controlplane
  |- Action Catalog
  |- Operation Manager
  |- Project Registry
  |- Security Middleware
  |- Domain Adapters
        |
现有 Store / Service / Runner / EventBus / Worktree
```

前端构建产物由 `go:embed` 打入单二进制。REST 负责查询和发起动作，SSE 负责 Operation
状态和进度事件。Agent 任务和续跑通过异步 Operation 管理，增量输出、Tool 名称和终态进入本地审计；
工具参数不写入 Operation。

## 5. Action 合同

每个可视化动作必须声明：

- 稳定 `id`、分类、标题、说明和搜索关键词。
- `read / execute / confirm / danger` 风险级别。
- 强类型输入字段、默认值、枚举和敏感字段。
- 是否异步。
- 高风险动作的精确确认文本。
- 直接调用领域 Service 的 Handler。

前端只消费 Catalog 元数据，不自行推断命令参数。

## 6. 安全边界

- 默认拒绝非 loopback 监听。
- 启动时生成随机 bootstrap token，兑换 HttpOnly + SameSite=Strict Session Cookie。
- 所有写操作校验 Origin、CSRF Token、Content-Type 和请求体上限。
- CSP 禁止外部脚本、内联任意 HTML 和跨域连接。
- 不提供任意命令、任意文件读取或任意路径写入 API。
- Secret 输入仅传给当前 Handler，Operation 审计中固定写为 `[REDACTED]`。
- Approve、Merge、Promote、Install、Delete 等动作必须展示影响并二次确认。
- 每次操作记录 actor、action、输入摘要、起止状态和结果。

## 7. 交付门禁

每个模块完成后：

1. 单元测试和 Race Test。
2. `go vet ./...` 与前端 typecheck/build。
3. 浏览器真实交互验收。
4. 独立 commit 并 push。

## 8. 已实现范围

- 真实 Dashboard：Git、模型、Session、Delivery、Hermes、Eval、Explorer、Reflection。
- 44 个类型化 Action：Delivery、Hermes、Agent、Capability、MCP、Skill、LSP、
  Reflection 和 Settings。
- `⌘K` Action 搜索、Schema 自动表单、风险标签、精确确认文本。
- 持久 Operation：异步执行、SSE、详情、取消、Secret 脱敏、重启中断恢复。
- Delivery/Eval/Trace/Hermes 状态面板和 Capability/MCP/Skill/LSP/Settings 能力中心。
- 项目登记、嵌入式 React 生产构建和单二进制启动。

查询 API：

```text
GET /api/v1/catalog
GET /api/v1/snapshot
GET /api/v1/projects
GET /api/v1/resources/{deliveries|hermes|evaluations|traces|sessions}
GET /api/v1/resources/{capabilities|skills|mcp|lsp|plugins|settings}
GET /api/v1/operations
GET /api/v1/operations/<id>
GET /api/v1/events
```

写 API 只接受同源、有效 Session Cookie、CSRF Token 和 JSON：

```text
POST /api/v1/actions/<action_id>/execute
POST /api/v1/operations/<id>/cancel
```

## 9. 验收记录

2026-08-12 已完成：

- Go 单元测试、Race Test、`go vet ./...`。
- TypeScript typecheck、Vite production build、npm audit。
- 真实浏览器一次性 token 登录，URL fragment 成功清除。
- Catalog 展示 44 个 Action；`system.ping` 从表单提交后经 SSE 到 `succeeded`。
- Operation 详情抽屉展示结果；Danger Action 展示精确确认输入。
- 浏览器 Console 无错误。
