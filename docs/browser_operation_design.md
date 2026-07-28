# Cohort 浏览器操作技术方案

> 文档状态：`[部分完成]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 已完成：Chrome bridge、tab/scan、DOM 摘要、snapshot、受控 JS、点击、输入、按键、
> 等待、截图与 OCR。未完成：浏览器扩展桥、长期页面变化监控和更强的跨浏览器连接治理。

本文档描述 Cohort 后续实现浏览器操作能力的技术路径。

这份方案参考 GA 已验证过的浏览器工具形态，但不会直接照搬 GA 的 Python 代码和前端体系。Cohort 的目标是保留“真实浏览器会话”和“少量高价值工具”的核心能力，并用 Go 项目的模块边界重新设计。

## 1. 背景

Cohort 当前已经具备命令行 Agent 主链路：

- 用户输入。
- 模型流式响应。
- 工具调用。
- 工具结果回灌。
- session 落盘和恢复。

后续如果要处理网页任务，仅靠 `code_run` 不够。因为网页任务通常需要：

- 读取当前打开的浏览器标签页。
- 复用用户已经登录过的网站状态。
- 查看页面主体内容。
- 点击按钮、填写输入框、触发页面交互。
- 观察交互后的 DOM 变化。
- 避免把完整 HTML 一次性塞进模型上下文。

所以需要单独设计浏览器操作层，而不是把所有逻辑都塞进 `code_run`。

## 2. GA 浏览器能力参考

GA 的浏览器能力主要由三部分组成：

```text
GA 工具层
  -> web_scan / web_execute_js
  -> simphtml 页面简化和变化监控
  -> TMWebDriver 真实浏览器桥接
```

### 2.1 TMWebDriver

GA 底层通过 `TMWebDriver` 控制真实浏览器。

它的价值是：

- 不是临时 headless 浏览器。
- 可以复用真实浏览器的登录态、Cookie 和页面状态。
- 可以看到用户当前打开的标签页。
- 能在当前页面执行 JavaScript。

这点比直接启动 Playwright 新浏览器更适合 Agent，因为很多网站需要用户登录、验证码或已有浏览器环境。

### 2.2 web_scan

GA 的 `web_scan` 用于读取页面信息。

核心行为：

- 初始化浏览器 driver。
- 获取所有标签页。
- 支持切换当前 tab。
- 返回标签页元信息。
- 可选返回简化 HTML。
- 可选只返回纯文本。
- 对过长内容做截断。

它的工具参数大致是：

```json
{
  "tabs_only": true,
  "switch_tab_id": "xxx",
  "text_only": true
}
```

GA 这里有一个重要经验：页面读取不能直接返回完整 DOM。真实网页的 HTML 非常大，还包含大量隐藏节点、浮层、脚本和样式。必须做简化和截断，否则上下文会快速膨胀。

### 2.3 web_execute_js

GA 的 `web_execute_js` 用于在页面里执行 JavaScript。

核心行为：

- 支持指定 tab 执行。
- 支持从参数或代码块读取 JS。
- 执行前可以抓取页面快照。
- 执行后等待页面变化。
- 返回 JS 执行结果。
- 返回页面是否刷新、新开 tab、DOM 变化摘要。
- 长结果可以保存到文件，只把摘要返回给模型。

GA 的经验是：浏览器操作不能只有“执行成功/失败”，还要告诉模型页面有没有变化。否则模型很容易盲目重复点击。

### 2.4 simphtml 页面简化

GA 的 `simphtml` 负责把页面内容变成模型更容易消费的结构。

它做的事情包括：

- 过滤隐藏元素。
- 过滤浮层、边栏等弱相关区域。
- 保留主要文本和可交互元素。
- 控制最大字符数。
- JS 执行后计算 DOM 变化摘要。

Cohort 第一版不需要完整复刻 `simphtml`，但必须保留它背后的原则：浏览器工具返回的是“Agent 可用的页面摘要”，不是原始网页源码。

## 3. Cohort 设计目标

### 3.1 第一版目标

第一版只做能稳定支撑网页理解和轻量操作的能力：

- 连接真实浏览器。
- 列出标签页。
- 切换标签页。
- 读取当前页面标题、URL、主体文本和简化节点。
- 执行受控 JavaScript。
- 返回页面变化摘要。
- 对网页内容和 JS 返回结果做截断。

### 3.2 非目标

第一版不做：

- 完整 UI 自动化框架。
- 复杂 Playwright 脚本编排。
- 跨浏览器兼容。
- 自动处理验证码。
- 自动绕过网站安全机制。
- 后台常驻浏览器管理 UI。
- 录制和回放用户操作。

这些可以等基础链路稳定后再做。

## 4. 总体架构

建议新增一个独立浏览器层：

```text
internal/browser/
  types.go             浏览器数据结构
  client.go            浏览器客户端接口
  cdp_client.go        Chrome DevTools Protocol 实现
  snapshot.go          页面文本和节点提取
  truncate.go          页面内容截断
  js_monitor.go        JS 执行前后变化摘要

internal/tools/
  browser_tabs.go      标签页列表工具
  browser_scan.go      页面读取工具
  browser_js.go        JS 执行工具
```

工具调用链保持清晰：

```text
Runner
  -> Registry.Run()
    -> browser_* tool
      -> internal/browser.Client
        -> Chrome / Browser Bridge
```

`internal/browser` 负责浏览器协议和页面处理，`internal/tools` 只负责把能力暴露给模型。

## 5. 浏览器连接方案

### 5.1 推荐第一版：Chrome DevTools Protocol

第一版建议使用 Chrome DevTools Protocol，也就是连接本机 Chrome 调试端口。

用户启动 Chrome：

```bash
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome \
  --remote-debugging-port=9222 \
  --user-data-dir="$HOME/.cohort/chrome-profile"
```

Cohort 读取：

```text
http://127.0.0.1:9222/json
```

拿到 tab 列表后，通过 WebSocket 连接指定 tab。

优点：

- Go 里实现成本可控。
- 不需要一开始写 Chrome 扩展。
- 可以复用一个持久 user-data-dir。
- 适合先做只读和 JS 执行。

缺点：

- 需要用户用指定参数启动浏览器。
- 如果想接管用户日常正在使用的 Chrome，体验不如 GA 的扩展桥接。

### 5.2 后续增强：浏览器扩展桥接

如果后续想更接近 GA，可以做浏览器扩展或本地桥接服务。

目标：

- 自动发现用户真实浏览器 tab。
- 不要求用户手动开 remote debugging。
- 更稳定地复用日常浏览器登录态。

这部分复杂度高，放在第二阶段。

## 6. 数据结构设计

### 6.1 Tab

```go
type Tab struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    URL    string `json:"url"`
    Active bool   `json:"active"`
}
```

字段说明：

- `ID`：浏览器标签页标识，用于后续切换或执行 JS。
- `Title`：页面标题，帮助模型判断页面内容。
- `URL`：页面地址，返回时需要截断过长 URL。
- `Active`：是否为当前默认 tab。

### 6.2 PageSnapshot

```go
type PageSnapshot struct {
    Status     string `json:"status"`
    TabID      string `json:"tab_id"`
    Title      string `json:"title"`
    URL        string `json:"url"`
    Text       string `json:"text,omitempty"`
    HTML       string `json:"html,omitempty"`
    Truncated  bool   `json:"truncated"`
    CharCount  int    `json:"char_count"`
    Omitted    int    `json:"omitted"`
}
```

第一版优先返回 `Text`，HTML 只在必要时返回简化版。

### 6.3 JSResult

```go
type JSResult struct {
    Status     string `json:"status"`
    TabID      string `json:"tab_id"`
    Return     any    `json:"return,omitempty"`
    Error      string `json:"error,omitempty"`
    Reloaded   bool   `json:"reloaded"`
    NewTabs    []Tab  `json:"new_tabs,omitempty"`
    Diff       string `json:"diff,omitempty"`
    Truncated  bool   `json:"truncated"`
}
```

`Diff` 用于告诉模型执行 JS 后页面是否发生变化。第一版可以先做简单版本：

- 执行前读取 `document.body.innerText` 长度和标题。
- 执行后再次读取。
- 对比标题、URL、文本长度、可见按钮数量。

后续再做更细的 DOM diff。

## 7. 工具设计

### 7.1 browser_tabs

用途：列出当前浏览器标签页。

参数：

```json
{}
```

返回：

```json
{
  "status": "success",
  "tabs": [
    {
      "id": "tab-1",
      "title": "Example",
      "url": "https://example.com",
      "active": true
    }
  ]
}
```

模型使用策略：

- 不知道当前页面时先调用。
- 多个页面相关时先让用户或模型选择 tab。

### 7.2 browser_scan

用途：读取页面内容。

参数：

```json
{
  "tab_id": "tab-1",
  "text_only": true,
  "max_chars": 12000
}
```

规则：

- `tab_id` 为空时使用当前 active tab。
- 默认 `text_only=true`。
- 默认最大 12000 字符。
- 返回内容必须经过截断。

返回：

```json
{
  "status": "success",
  "tab_id": "tab-1",
  "title": "Example",
  "url": "https://example.com",
  "text": "页面主体文本...",
  "truncated": false,
  "char_count": 1200,
  "omitted": 0
}
```

### 7.3 browser_execute_js

用途：执行页面 JavaScript。

参数：

```json
{
  "tab_id": "tab-1",
  "script": "document.title",
  "no_monitor": false,
  "save_to_file": ""
}
```

规则：

- 默认开启轻量页面变化监控。
- 长返回值必须截断。
- `save_to_file` 只允许保存到 workspace 内。
- 第一版不允许执行明显危险脚本，例如大规模删除 DOM、读取 cookie、提交表单前不确认。

返回：

```json
{
  "status": "success",
  "tab_id": "tab-1",
  "return": "Example",
  "reloaded": false,
  "diff": "页面标题未变化，正文长度未变化",
  "truncated": false
}
```

## 8. 安全边界

浏览器工具比文件工具更敏感，因为它可能操作真实登录状态下的网站。

第一版安全规则：

- 默认只读工具 `browser_tabs`、`browser_scan` 可直接执行。
- `browser_execute_js` 可执行读取型 JS。
- 涉及提交、支付、删除、发布、发送消息等动作时，必须先调用 `ask_user` 确认。
- 禁止直接返回 cookie、localStorage 中明显敏感字段。
- `save_to_file` 必须限制在 workspace 内。
- JS 执行结果要限制长度。

后续可以增加 JS 风险识别：

```text
document.cookie
localStorage
sessionStorage
form.submit()
click()
fetch(..., {method:"POST"})
delete
remove()
```

第一版不需要做到完美拦截，但系统提示词必须要求模型在高风险动作前询问用户。

## 9. 上下文管理要求

浏览器能力必须和后续 Context Manager 配合。

规则：

- 页面内容不能完整长期塞进 `history`。
- `browser_scan` 返回内容需要带 `truncated`。
- 超长页面应保存到文件，模型只拿摘要和路径。
- JS 返回值过长时保留首尾。
- 最近一次页面状态可以进入上下文，旧页面扫描结果应该被 Context Manager 压缩。

建议默认值：

```text
browser_scan 默认 max_chars = 12000
browser_execute_js 默认 max_return_chars = 8000
保留最近 3 次浏览器工具结果原文
更早浏览器结果压缩成摘要
```

## 10. 系统提示词调整

接入浏览器工具后，系统提示词需要补充：

```text
浏览器任务规则：
- 不知道当前页面时，先用 browser_tabs。
- 读取页面内容时，优先 browser_scan text_only=true。
- 不要要求返回完整 HTML，除非确实需要分析 DOM。
- 能用选择器读取的内容，不要全量扫描页面。
- 涉及提交、删除、支付、发送消息、发布内容等真实操作前，必须 ask_user 确认。
- JS 执行后根据 diff 判断页面是否变化，不要盲目重复点击。
```

## 11. 开发阶段拆分

### 阶段一：只读浏览器能力

目标：

- 浏览器连接可用。
- 能列 tab。
- 能读取页面标题、URL 和主体文本。

任务：

- 新增 `internal/browser/types.go`。
- 新增 `internal/browser/client.go`。
- 新增 CDP tab list 实现。
- 新增 `browser_tabs` 工具。
- 新增 `browser_scan` 工具。
- 增加工具 schema 和系统提示词。
- 增加单元测试，使用 fake browser client。

验收：

- `go run . tools` 能看到浏览器工具。
- 模型能调用 `browser_tabs`。
- 模型能调用 `browser_scan` 读取页面文本。
- 没启动浏览器时返回结构化错误和启动提示。

### 阶段二：JS 执行和页面变化监控

目标：

- 能在指定 tab 执行 JS。
- 能返回 JS 结果。
- 能给出轻量页面变化摘要。

任务：

- 新增 `browser_execute_js` 工具。
- 实现 `Runtime.evaluate`。
- 实现执行前后 URL、title、body text length 对比。
- 增加 `no_monitor` 参数。
- 增加长结果截断。
- 增加 JS 执行测试。

验收：

- 可以执行 `document.title`。
- 可以执行读取 DOM 的 JS。
- 页面无变化时返回明确提示。
- JS 报错时返回结构化错误。

### 阶段三：真实交互安全增强

目标：

- 支持点击、输入等真实操作。
- 高风险动作前有确认机制。

任务：

- 增加 JS 风险识别。
- 高风险操作接入 `ask_user`。
- 增加 selector 辅助函数文档。
- 增加操作后等待策略。

验收：

- 普通读取 JS 不询问。
- 提交表单、删除、支付、发送等动作前必须询问。
- 用户拒绝后不执行。

### 阶段四：真实浏览器桥接增强

目标：

- 体验接近 GA 的真实浏览器 session。

任务：

- 评估是否实现浏览器扩展。
- 评估是否实现本地 WebSocket bridge。
- 自动发现日常浏览器 tab。
- 保存浏览器连接配置。

验收：

- 用户不需要手动记 remote debugging 参数。
- Cohort 能稳定连接真实浏览器。

## 12. 配置设计

建议在 `configs/config.yaml` 后续增加：

```yaml
browser:
  enabled: false
  provider: cdp
  cdp_url: http://127.0.0.1:9222
  default_scan_chars: 12000
  default_js_return_chars: 8000
  allow_execute_js: true
  require_confirm_for_actions: true
```

说明：

- `enabled` 默认 false，避免用户无意中暴露浏览器。
- `provider` 第一版只支持 `cdp`。
- `cdp_url` 指向本机 Chrome DevTools。
- `require_confirm_for_actions` 控制高风险操作确认。

## 13. 错误格式

浏览器工具错误必须使用 Cohort 已有的 `ToolErrorData`：

```json
{
  "status": "error",
  "code": "browser_not_connected",
  "message": "cannot connect to Chrome DevTools at http://127.0.0.1:9222",
  "hint": "请使用 remote debugging 参数启动 Chrome，或在配置中关闭 browser.enabled。"
}
```

建议错误码：

```text
browser_disabled
browser_not_connected
tab_not_found
page_scan_failed
js_execution_failed
js_result_too_large
browser_action_requires_confirmation
```

## 14. 和 GA 的取舍差异

保留 GA 的经验：

- 控制真实浏览器，而不是只用临时 headless。
- 工具分成页面扫描和 JS 执行。
- 页面内容必须简化和截断。
- JS 执行后要观察页面变化。
- 长结果可以保存到文件，模型只拿摘要。

不直接照搬 GA 的部分：

- 不把浏览器逻辑写进一个大工具文件。
- 不一开始实现完整 `simphtml`。
- 不一开始实现浏览器扩展。
- 不让 JS 工具无限制执行高风险操作。

Cohort 的方向是先建立清晰模块边界：

```text
browser layer 管连接和页面
tools layer 管模型工具
context manager 管结果压缩
session layer 管完整历史落盘
```

## 15. 推荐落地顺序

在当前 Cohort 阶段，浏览器能力建议排在 Context Manager 之后。

原因：

- 浏览器页面内容很长，没有上下文管理会很快撑爆模型请求。
- `browser_scan` 和 `browser_execute_js` 都会产生大结果，需要先有压缩策略。
- session resume 已经完成，长任务能力下一步应该先稳住上下文。

推荐顺序：

```text
1. Context Manager 第一版
2. browser_tabs
3. browser_scan
4. browser_execute_js
5. JS 风险确认
6. 浏览器扩展或更强桥接
```

## 16. 最终效果

实现后，用户可以让 Cohort 处理网页任务，例如：

```text
看一下我当前浏览器页面是什么内容
```

模型调用：

```text
browser_tabs
browser_scan
```

也可以处理轻量交互：

```text
帮我读取页面上所有按钮文字
```

模型调用：

```text
browser_execute_js
```

对于高风险动作：

```text
帮我点击提交订单
```

模型必须先调用：

```text
ask_user
```

用户确认后才允许执行。
