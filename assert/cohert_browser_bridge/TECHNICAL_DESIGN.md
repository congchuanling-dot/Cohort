# Cohert Browser Bridge 技术方案

## 插件命名

插件名：`Cohert Browser Bridge`

目录：

```text
assert/cohert_browser_bridge
```

这个名字强调它不是完整自动化框架，而是 Cohert 和用户真实 Chrome 会话之间的桥。

## GA 参考结论

GA 的现成实现位于：

```text
GenericAgent/assets/tmwd_cdp_bridge
```

关键文件：

- `manifest.json`：MV3 Chrome 扩展声明。
- `background.js`：核心桥接层，负责 tab、CDP、JS 执行、WebSocket 连接。
- `content.js`：页面内注入，支持 DOM 方式通信和页面状态标记。
- `disable_dialogs.js`：拦截 alert/confirm/prompt，避免页面阻塞自动化。
- `popup.html` / `popup.js`：扩展弹窗。
- `TMWebDriver.py`：本地 WebSocket/HTTP server，接收扩展连接并分发 JS 命令。

GA 的核心链路：

```text
Agent web_scan / web_execute_js
  -> TMWebDriver.py 本地服务
  -> Chrome extension WebSocket
  -> background.js
  -> chrome.tabs / chrome.scripting / chrome.debugger
  -> 真实网页
```

这条链路的价值是复用真实浏览器登录态，而不是启动一次性 headless 浏览器。

## Cohert 第一版取舍

Cohert 第一版保留：

- MV3 service worker。
- 插件主动连接本地 WebSocket。
- 标签页列表。
- 页面扫描。
- 受控 JS 执行。
- 标签页变化主动上报。

Cohert 第一版不照搬：

- `cookies` 权限。
- `management` 权限。
- `contentSettings` 权限。
- `declarativeNetRequest` 移除 CSP。
- 默认拦截 alert/confirm/prompt。

原因：

- 先把浏览器桥主链路跑通。
- 降低插件权限，避免第一版过度暴露用户浏览器状态。
- 高风险动作后续由 Cohert 工具层和 `ask_user` 控制。

## 当前文件结构

```text
assert/cohert_browser_bridge/
  manifest.json
  config.js
  background.js
  content.js
  popup.html
  popup.js
  README.md
  TECHNICAL_DESIGN.md
  DEVELOPMENT_TUTORIAL.md
```

## 插件内部职责

### manifest.json

声明 Chrome MV3 插件。

当前权限：

```text
tabs
activeTab
scripting
debugger
alarms
```

其中：

- `tabs` 用于列出标签页。
- `scripting` 用于页面扫描和 JS 执行。
- `debugger` 预留给后续 CDP fallback。
- `alarms` 用于 MV3 service worker 保活和重连。

### config.js

集中配置本地连接地址：

```text
ws://127.0.0.1:18766/browser
```

使用 `18766` 是为了避开 GA 默认的 `18765`。

### background.js

核心桥接层。

职责：

- 自动连接 Cohert 本地 WebSocket server。
- 连接成功后发送 `ext_ready`。
- 标签页变化时发送 `tabs_update`。
- 处理 Go 侧发来的命令：
  - `tabs`
  - `scan`
  - `execute_js`
- 对页面文本和 JS 返回值做截断。
- 通过 popup 暴露连接状态。

### content.js

第一版只注入一个轻量状态标记，说明插件已进入当前页面。

它不参与核心命令链路，避免把通信分散到页面 DOM。

### popup.html / popup.js

用于人工确认：

- 插件是否连上 Cohert。
- 当前 WebSocket 地址。
- 当前可脚本化标签页列表。

## WebSocket 协议

### 插件到 Cohert

连接成功：

```json
{
  "type": "ext_ready",
  "name": "Cohert Browser Bridge",
  "version": "0.1.0",
  "tabs": []
}
```

标签页变化：

```json
{
  "type": "tabs_update",
  "tabs": []
}
```

执行成功：

```json
{
  "type": "result",
  "id": "request-id",
  "result": {}
}
```

执行失败：

```json
{
  "type": "error",
  "id": "request-id",
  "error": {
    "message": "error message",
    "stack": ""
  }
}
```

### Cohert 到插件

列标签页：

```json
{
  "id": "request-id",
  "command": "tabs"
}
```

扫描页面：

```json
{
  "id": "request-id",
  "command": "scan",
  "tab_id": "123",
  "max_chars": 12000
}
```

执行 JS：

```json
{
  "id": "request-id",
  "command": "execute_js",
  "tab_id": "123",
  "script": "return document.title",
  "max_return_chars": 8000
}
```

## Go 侧后续开发方案

### P1-Browser-001：定义 browser bridge server

新增：

```text
internal/browser/
  types.go
  server.go
  client.go
```

目标：

- 监听 `127.0.0.1:18766`。
- 接收扩展 WebSocket。
- 维护当前 tabs 快照。
- 提供 `Client` 接口给工具层调用。

### P1-Browser-002：接入 browser_tabs 工具

新增：

```text
internal/tools/browser_tabs.go
```

行为：

- 调用 `browser.Client.Tabs()`。
- 返回结构化 tab 列表。
- 未连接插件时返回 `ToolErrorData{code: "browser_not_connected"}`。

### P1-Browser-003：接入 browser_scan 工具

新增：

```text
internal/tools/browser_scan.go
```

行为：

- 调用插件 `scan`。
- 默认 `max_chars=12000`。
- 返回 `title/url/text/truncated/omitted`。
- 结果进入 Context Manager 后仍可被 Micro Compact 兜底。

### P1-Browser-004：接入 browser_execute_js 工具

新增：

```text
internal/tools/browser_js.go
```

行为：

- 调用插件 `execute_js`。
- 默认 `max_return_chars=8000`。
- 高风险脚本先进入确认策略。

## 安全策略

第一版工具层必须遵守：

- `browser_tabs`、`browser_scan` 默认只读，可以直接执行。
- `browser_execute_js` 第一版只建议执行读取型 JS。
- 涉及提交、删除、支付、发送消息、发布内容等动作前必须 `ask_user`。
- 不提供 cookie 读取工具。
- 不返回 localStorage/sessionStorage 中明显敏感字段。

## 验收标准

插件侧：

- Chrome 可以通过 Load unpacked 加载 `assert/cohert_browser_bridge`。
- popup 可以显示 WebSocket 地址和当前 http/https 标签页。
- 未启动 Cohert bridge server 时状态显示 `waiting for Cohert`。

Go 侧接入后：

- `go run . tools` 可以看到 `browser_tabs` 和 `browser_scan`。
- 模型能调用 `browser_tabs` 获取当前标签页。
- 模型能调用 `browser_scan` 读取当前页面正文。
- 插件未连接时返回清晰的结构化错误和安装/启动提示。
