# Cohort Control Center V2：本地数据与评测可视化

> 状态：`[完成]`
>
> 目标：把本地数据自动发现、零 ID 操作和 Agent 评测可视化整合进 Control Center。

## 1. 问题定义

当前 Control Center 已有安全控制面、Action Catalog 和 Operation，但产品层仍有三类断点：

1. 本地数据存在，页面却可能显示为空。
   - 查询错误没有展示，前端将“目录不存在”“解析失败”“确实没有记录”统一表现为暂无数据。
   - 页面只展示少量摘要，没有数据源路径、同步时间和加载状态。
   - Project Root、Config Workspace、Session Root 等路径之间的关系对用户不可见。
2. Action 表单要求手填 `delivery_id`、`session_id`、`action_id`、`skill_id` 等内部标识。
   - 用户不知道 ID 在哪里找。
   - 复制 ID 容易选错对象，也无法提前知道该对象当前是否允许执行目标动作。
   - 列表与动作割裂，用户先看到对象，再去 `Command Center` 重新搜索动作和填写 ID。
3. Eval、Stability、Trace Graph、Tuning 仍以独立 HTML 为主要可视化载体。
   - Control Center 只能看到列表摘要。
   - Eval Case、Trace、Session 和修复动作之间不能跳转。
   - HTML 模板和 API 各自组装数据，继续扩展会产生两套业务逻辑。

V2 的产品原则是：

```text
用户选择“对象”和“动作”，系统负责解析 ID、校验状态并展示影响。
内部 ID 可以被查看和复制，但不能成为常规操作的前置知识。
```

当前仓库审计结果证明问题位于展示链路，而不是缺少本地数据：

```text
temp/sessions              111 sessions
.cohort/evals/runs          28 eval runs
.cohort/deliveries           2 deliveries
.cohort/hermes/action_queue 57 actions
```

## 2. 目标与非目标

### 2.1 目标

- 启动后自动发现当前项目已有的 Session、Eval、Delivery、Hermes、Trace 和 Reflection 数据。
- 明确区分 `ready / empty / unavailable / error / stale` 五种数据源状态。
- 页面内所有已有对象都能通过搜索选择器或上下文按钮操作，不要求手填 ID。
- 将单次 Eval、稳定性、因果 Trace Graph 和 Tuning 原生嵌入 React 页面。
- 保留离线 HTML 导出，HTML 与 Web API 使用同一份 ViewModel。
- 实体详情、Action、Operation 和 Evidence 之间可追踪、可深链。
- 不放宽现有 loopback、Session、CSRF、审批和 Evidence 安全边界。

### 2.2 非目标

- 不把任意本地目录暴露为文件浏览器。
- 不允许前端提交任意 Store 路径。
- 不用 iframe 直接执行历史 HTML 中的内联脚本。
- 不用 SQLite 复制所有领域数据；各领域 Store 继续作为事实源。
- 不删除 CLI 的 ID 参数，CLI 和自动化脚本保持兼容。

## 3. 目标体验

### 3.1 启动与数据发现

启动 `cohort ui` 后，Overview 顶部展示：

```text
Cohort / feature/cohort-control-center
Sessions 126 · Eval Runs 18 · Deliveries 2 · Hermes Actions 7
数据同步于 14:32:08
```

页面提供“数据来源”抽屉：

| 数据源 | 状态 | 路径 | 数量 | 最近更新 |
| --- | --- | --- | ---: | --- |
| Sessions | ready | `temp/sessions` | 126 | 1 min ago |
| Evaluations | ready | `.cohort/evals` | 18 | 3 min ago |
| Deliveries | ready | `.cohort/deliveries` | 2 | 2 h ago |
| Hermes | stale | `.cohort/hermes` | 7 | 4 h ago |

数据解析失败时显示具体错误和“重新扫描”，不能伪装成空列表。

### 3.2 零 ID 操作

Delivery 列表中的每一行直接提供当前状态允许的动作：

```text
实现 Control Center V2    verifying
[查看证据] [重新验证] [取消]
```

从 `Command Center` 选择“恢复 Session”时，表单展示可搜索对象：

```text
Session
> 重庆天气查询
  deepseek-v4-pro · 8 messages · 2 min ago
```

原始 ID 只在详情页的“技术信息”区域显示，并提供复制按钮。

### 3.3 原生质量页面

Quality 使用多级路由：

```text
/quality
/quality/evals/:run_id
/quality/stability
/quality/traces/:session_id/:run_id
/quality/tuning
```

- `/quality`：通过率、分数、稳定性、回归和失败签名总览。
- Eval 详情：Case、Assertion、Judge、Token、工具调用、Action Item。
- Stability：趋势、Suite 对比、Flaky Case、Regression、Failure Signature。
- Trace：可缩放因果 DAG、关键路径、异常节点和节点详情。
- Tuning：慢 LLM、失败工具、Context/Request/Schema 膨胀和建议。

点击失败 Eval Case 的“查看 Trace”直接进入对应因果图，不再手填 Session/Run ID。

## 4. 总体架构

```text
React Routes
  |- Overview / Data Sources
  |- Entity Lists / Detail Drawers
  |- Quality Visualizations
  |- Context Action Menu / Entity Picker
             |
        REST + SSE
             |
internal/controlplane
  |- ProjectDataHub
  |- EntityIndex
  |- ActionPreparationService
  |- OperationManager
  |- Resource / Visualization API
             |
Domain Adapters
  |- session.Store
  |- evaluation.Store
  |- delivery.Store
  |- hermes.Store
  |- traceview
  |- tuning
```

核心变化不是再增加一层目录扫描，而是增加 `ProjectDataHub`，统一调用现有 Store，
生成轻量索引和数据源健康状态。详情仍按需从领域 Store 加载。

## 5. ProjectDataHub

### 5.1 数据模型

```go
type SourceState string

const (
    SourceReady       SourceState = "ready"
    SourceEmpty       SourceState = "empty"
    SourceUnavailable SourceState = "unavailable"
    SourceError       SourceState = "error"
    SourceStale       SourceState = "stale"
)

type SourceHealth struct {
    Kind        EntityKind  `json:"kind"`
    State       SourceState `json:"state"`
    RelativePath string     `json:"relative_path"`
    Count       int         `json:"count"`
    UpdatedAt   time.Time   `json:"updated_at,omitempty"`
    ScannedAt   time.Time   `json:"scanned_at"`
    ErrorCode   string      `json:"error_code,omitempty"`
    Error       string      `json:"error,omitempty"`
}

type EntityDescriptor struct {
    Kind        EntityKind         `json:"kind"`
    ID          string             `json:"id"`
    Title       string             `json:"title"`
    Subtitle    string             `json:"subtitle,omitempty"`
    Status      string             `json:"status,omitempty"`
    UpdatedAt   time.Time          `json:"updated_at,omitempty"`
    SearchText  string             `json:"search_text,omitempty"`
    Version     string             `json:"version"`
    Badges      []string           `json:"badges,omitempty"`
    Actions     []ContextAction    `json:"actions,omitempty"`
}

type ResourceEnvelope[T any] struct {
    Data       T            `json:"data"`
    Source     SourceHealth `json:"source"`
    NextCursor string       `json:"next_cursor,omitempty"`
}
```

`Version` 使用领域对象的更新时间、状态或内容 Hash 生成，用于动作前的并发变更校验。

### 5.2 索引策略

- 启动时并行调用领域 Store 建立元数据索引。
- 索引只保存 ID、标题、状态、时间和搜索字段，不复制 History、Event 或 Trace 正文。
- 详情点击后惰性读取，使用有界 LRU 缓存。
- 使用文件系统事件触发增量刷新，并以周期性 reconciliation 防止事件丢失。
- 索引原子持久化到 `.cohort/control/index-v1.json`，启动时先快速展示，再后台核对。
- 所有路径由服务端根据 canonical project root 推导，浏览器不能指定磁盘路径。
- 单个数据源失败不阻塞其他数据源，错误进入 `SourceHealth`，禁止静默吞掉。

### 5.3 数据源适配

| Kind | 事实源 | 索引字段 |
| --- | --- | --- |
| `session` | `session.Store` | title、model、message count、updated_at |
| `eval_run` | `evaluation.Store` | suite、model、score、pass rate、gate |
| `delivery` | `delivery.Store` | requirement、status、base、updated_at |
| `hermes_action` | `hermes.Store` | title、severity、category、status |
| `trace_run` | `traceview` | session、run、duration、status、errors |
| `skill` | `skill.Store` | name、scope、status |
| `capability` | `capability.Store` | type、status、source |
| `mcp_server` | `mcp.Store` | name、scope、type、readiness |
| `model_profile` | `app.Config` | name、provider、model、active |

## 6. Entity Picker 与上下文动作

### 6.1 Action Schema 扩展

新增实体类型，替代 UI 中的裸字符串 ID：

```go
const FieldEntity FieldType = "entity"

type EntitySelector struct {
    Kind          EntityKind        `json:"kind"`
    Status        []string          `json:"status,omitempty"`
    DependsOn     map[string]string `json:"depends_on,omitempty"`
    RecentFirst   bool              `json:"recent_first,omitempty"`
    AllowMissing  bool              `json:"allow_missing,omitempty"`
}

type InputField struct {
    // 现有字段保持不变。
    Entity *EntitySelector `json:"entity,omitempty"`
}
```

例如：

```go
InputField{
    Name:     "delivery_id",
    Label:    "Delivery",
    Type:     FieldEntity,
    Required: true,
    Entity: &EntitySelector{
        Kind:        EntityDelivery,
        Status:      []string{"integrated", "verified", "approved"},
        RecentFirst: true,
    },
}
```

领域 Handler 仍收到字符串 ID，因此无需重写 Delivery、Hermes、Session 状态机。

### 6.2 Action Preparation

危险动作不能只依赖前端选择结果。增加准备阶段：

```text
POST /api/v1/actions/:id/prepare
POST /api/v1/actions/:id/execute
```

`prepare` 完成：

1. 将实体选择解析成当前项目中的 canonical ID。
2. 校验实体状态和 Action 前置条件。
3. 返回影响摘要、Evidence 状态、可执行性和确认文本。
4. 生成短期 `preparation_token`，绑定 Action、输入、实体 Version 和当前 Git tree。

`execute` 对高风险动作必须携带 `preparation_token`。实体在确认期间发生变化时拒绝执行，
要求重新准备，避免 TOCTOU。

### 6.3 上下文动作

每个 `EntityDescriptor` 由后端根据状态返回 `Actions`：

```go
type ContextAction struct {
    ActionID       string `json:"action_id"`
    Label          string `json:"label"`
    Risk           string `json:"risk"`
    Enabled        bool   `json:"enabled"`
    DisabledReason string `json:"disabled_reason,omitempty"`
}
```

前端列表、详情页和可视化节点统一使用该结构。用户点击动作时，实体字段自动预填且隐藏，
只填写真正需要的人类参数，例如批准人或修复说明。

### 6.4 兼容策略

- CLI 保持 `cohort deliver review <id>` 等现有接口。
- HTTP 继续接受字符串 ID，但必须在服务端解析和校验。
- 没有可选实体时，Picker 展示对应数据源状态和修复建议，不能回退成空文本框。
- 高级模式允许查看和复制原始 ID，不允许绕过状态机。

## 7. 评测可视化原生嵌入

### 7.1 不使用 iframe

直接 iframe 旧 HTML 存在以下问题：

- 模板包含内联 Style/Script，与当前 CSP 冲突。
- 页面无法复用 Control Center 的认证、路由、筛选和实体动作。
- Eval、Trace 和 Operation 无法共享选中状态。
- 后续会形成 HTML 模板与 React 两套逻辑。

因此采用“共享 ViewModel，双渲染器”：

```text
Domain Data
    |
Build ViewModel
    |----------------------|
JSON API -> React          Offline HTML Renderer
```

### 7.2 后端重构

将 HTML 中的数据准备逻辑导出为纯函数：

```go
evaluation.BuildDashboardData(store, result) (DashboardData, error)
evaluation.BuildStabilityView(store, options) (StabilityIndex, error)
traceview.BuildGraphView(view) GraphPage
tuning.Analyze(views) Report
```

现有 `WriteReports`、`WriteStabilityReports`、`WriteGraphHTML` 和 `tuning.Generate`
继续调用这些函数。Web API 也调用同一函数，保证指标和离线报告一致。

### 7.3 API

```text
GET /api/v1/data-sources
GET /api/v1/entities/:kind?q=&status=&cursor=&limit=
GET /api/v1/entities/:kind/:id
GET /api/v1/entities/:kind/:id/actions

GET /api/v1/quality/summary
GET /api/v1/quality/evals/:run_id
GET /api/v1/quality/stability?window=&suite=&profile=
GET /api/v1/quality/traces/:session_id/:run_id
GET /api/v1/quality/tuning?limit=

GET /api/v1/exports/evals/:run_id.html
GET /api/v1/exports/stability.html
GET /api/v1/exports/traces/:session_id/:run_id.html
GET /api/v1/exports/tuning.html
```

Export API 只允许已索引实体，响应使用 attachment 下载，不开放任意文件读取。

### 7.4 前端组件

```text
QualityOverview
  |- MetricCards
  |- PassRateTrend
  |- RegressionList
  |- FailureSignatureTable

EvalRunPage
  |- EvalHeader
  |- CaseTable
  |- AssertionDrawer
  |- ActionItemPanel
  |- TraceLink

StabilityPage
  |- StabilityTrend
  |- SuiteComparison
  |- CaseHeatmap
  |- FlakyCaseTable

TraceGraphPage
  |- GraphCanvas
  |- CriticalPath
  |- NodeInspector
  |- BottleneckPanel

TuningPage
  |- DurationBreakdown
  |- SlowLLMTable
  |- FailedToolTable
  |- RecommendationList
```

趋势图和因果图优先复用现有 SVG 布局算法，增加缩放、平移、节点选中和键盘访问，
不为了简单图表引入大型可视化运行时。

## 8. 数据关联

建立轻量关联，不修改领域事实：

```text
EvalRun
  -> CaseResult.TracePath / TraceRunID
  -> TraceRun
  -> Session

HermesAction
  -> EvidenceRef
  -> EvalRun / Delivery / Session

Operation
  -> Action Input EntityRef
  -> Entity Detail
```

Operation 审计新增脱敏后的实体引用：

```json
{
  "kind": "delivery",
  "id": "delivery_...",
  "title": "实现 Control Center V2",
  "version": "sha256:..."
}
```

这样可以从 Operation 详情跳回目标对象，也能解释一次失败操作针对的具体状态。

## 9. 前端状态与错误模型

React Query Key 必须包含 Project 和实体维度：

```text
["sources", projectID]
["entities", projectID, kind, filters]
["entity", projectID, kind, id]
["quality", projectID, "eval", runID]
```

页面分别处理：

- `loading`：Skeleton。
- `empty`：事实源正常但没有记录，提供创建动作。
- `unavailable`：组件未配置，提供配置入口。
- `error`：展示错误码、数据路径和重试按钮。
- `stale`：展示最后成功数据和过期标识。

SSE 新增：

```text
source.updated
entity.created
entity.updated
entity.deleted
operation.updated
```

前端只失效对应 Query，不再每次 Operation 更新后刷新全部资源。

## 10. 安全与一致性

- Project Root 只取服务端已登记项目，不接收浏览器提交的任意路径。
- Entity ID 必须经过 Kind 对应 Store 解析，拒绝 `..`、绝对路径和跨项目引用。
- HTML Export 只渲染领域 ViewModel，不读取用户指定文件。
- Secret、Prompt 和工具结果正文不进入 EntityIndex。
- Trace API 延续当前脱敏事件模型，不返回原始 Prompt 和敏感工具参数。
- Danger Action 使用 Preparation Token、精确确认和实体 Version 三重校验。
- 数据源索引损坏时可重建，不能反向覆盖领域 Store。

## 11. 实施拆分

### 模块一：Local Data Hub

- `ProjectDataHub`、Source Adapter、Resource Envelope。
- 数据源健康页和重新扫描。
- 现有页面不再静默吞错。
- 索引缓存、增量刷新和周期 reconciliation。

验收：当前仓库已有 Session、Delivery、Hermes、Eval 数据能自动出现；错误路径有明确诊断。

### 模块二：零 ID Action

- `FieldEntity`、Entity API、搜索 Picker。
- Delivery、Hermes、Session、Skill、Capability、MCP、Profile Action 全量迁移。
- 列表行和详情页上下文动作。
- Prepare Token 和并发版本校验。

验收：常规 Web 操作不出现要求用户手填内部 ID 的文本框。

### 模块三：路由与详情页

- 将单文件锚点页面拆为 React Router 页面。
- 实体列表、详情抽屉、深链和返回状态。
- Operation 与实体双向跳转。

验收：刷新详情 URL 后仍能恢复同一对象，不依赖上一页内存状态。

### 模块四：Eval 与 Stability

- 抽取共享 ViewModel。
- Quality Overview、Eval Detail、Stability 原生页面。
- Eval Case 到 Trace 的直接跳转。
- 保留离线 HTML 导出。

验收：React 页面与同一 Run 的离线 HTML 指标完全一致。

### 模块五：Trace 与 Tuning

- 原生因果 DAG、关键路径、节点详情。
- Tuning 性能与失败工具页面。
- Trace/Session/Eval/Operation 关联。

验收：无需输入 Session ID 或 Run ID 即可从失败 Eval 到达对应 Trace。

### 模块六：端到端与迁移

- 大数据量分页、索引恢复、文件事件丢失 reconciliation。
- 数据源损坏、对象删除、状态并发变化和危险动作测试。
- 浏览器 E2E、CSP、CSRF、Race、Vet、前端构建与可访问性验收。
- 更新用户文档和旧 Action 兼容说明。

每个模块独立测试、commit 和 push。

## 12. 验收标准

1. 启动 Control Center 后 2 秒内展示缓存索引，后台完成真实 Store 核对。
2. 当前项目有数据时，Overview 和对应页面不能显示伪空状态。
3. 任一数据源失败不影响其他页面，并展示可操作诊断。
4. Web 高频流程不要求手填 Delivery、Session、Run、Hermes Action、Skill 或 Capability ID。
5. Eval、Stability、Trace 和 Tuning 均为 Control Center 原生页面。
6. 旧离线 HTML 继续可生成，关键指标与 Web 页面一致。
7. 从失败 Eval Case 到 Trace Graph 最多两次点击。
8. 高风险 Action 在实体状态变化后拒绝使用旧确认执行。
9. 不新增任意文件读取、任意命令执行或跨项目路径 API。
10. 全量 Go Test、Race、Vet、TypeScript Typecheck、Production Build 和浏览器 E2E 通过。

## 13. 实现记录

| 模块 | Commit | 结果 |
| --- | --- | --- |
| Local Data Hub | `10e025a` | 六类本地数据源、健康状态、错误隔离和原子索引 |
| 零 ID Action | `371c73f` | Entity Picker、上下文动作、Prepare Token 和版本校验 |
| 路由与详情 | `97aadc6` | React Router、实体深链、领域详情与动作预填 |
| Eval 与 Stability | `41d8367` | 原生质量页面、共享 ViewModel 和受控 HTML 导出 |
| Trace 与 Tuning | `b4d5b7b` | 因果 DAG、关键路径、调优视图和跨实体跳转 |
| 端到端与迁移 | 本次最终提交 | 大历史行兼容、Eval Trace 双数据根、安全回归和浏览器 E2E |

## 14. 最终验收

- 真实项目数据自动发现：Session 111、Eval 28、Delivery 2、Hermes Action 57、Reflection 5、Trace 35，六类数据源均为 `ready`。
- Session Store 可读取超过 Scanner 默认 64 KiB 的历史行，不再因单条大消息导致整个数据源显示为空。
- Delivery 列表、详情深链、Entity Picker、状态过滤、上下文预填和 Action Prepare 影响预览通过浏览器验证。
- Quality Overview、Eval Case Drawer、Stability、Trace DAG、关键路径、节点 Inspector 和 Tuning 通过真实数据验证。
- Eval Trace 和普通 Agent Trace 只在 `.cohort/evals/sessions`、`temp/sessions` 两个受信任根目录内解析；不接受浏览器提交的任意路径。
- Eval、Stability、Trace、Tuning 四类离线 HTML 导出均返回 `200`、`text/html` 和 `attachment`，并与原生页面复用领域 ViewModel。
- Stability 空集合固定序列化为 `[]`，前端兼容历史 `null` 数据，避免 API 成功但页面白屏。
- Bootstrap Token、HttpOnly Cookie、CSRF、Origin、CSP 和高风险 Action Preparation Token 回归通过。
- `go test ./...`、`go test -race ./...`、`go vet ./...`、TypeScript Typecheck、Vite Production Build 全部通过。
- `npm audit` 为 0 vulnerabilities。
