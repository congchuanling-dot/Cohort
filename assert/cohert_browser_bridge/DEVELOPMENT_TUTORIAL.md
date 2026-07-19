# Cohert Browser Bridge 开发教程

这份教程不是给浏览器插件老手看的，而是给第一次接触 Chrome 插件和 JavaScript 的开发者看的。

目标是让你能做到三件事：

- 看懂这个插件每个文件在干什么。
- 能在面试里讲清楚为什么要这样设计。
- 后续能自己继续改 `browser_tabs`、`browser_scan`、`browser_execute_js`。

## 1. 先记住一句话

这个插件的本质是：

```text
Cohert Go 进程
  <-> 本地 WebSocket
  <-> Chrome 插件 background.js
  <-> Chrome Extension API
  <-> 用户真实浏览器页面
```

它不是独立的网页应用，也不是普通前端页面。

它是一个安装在 Chrome 里的桥。Cohert 以后通过本地 WebSocket 给它发命令，它再用 Chrome 给插件开放的 API 去读取或操作真实浏览器页面。

## 2. 为什么要做插件

如果只用 Go 后台直接请求网页，有几个问题：

- 拿不到用户已经登录的浏览器状态。
- 碰到验证码、Cookie、前端渲染页面会很麻烦。
- 很多页面内容是 JavaScript 动态生成的，普通 HTTP 请求看不到最终 DOM。

插件的价值是：

- 它运行在用户真实 Chrome 里。
- 它能看到当前打开的标签页。
- 它能在真实网页里执行 JavaScript。
- 它能复用用户已经登录过的网站状态。

所以面试里可以这么讲：

> 我们不是用 headless browser 临时打开一个页面，而是通过 Chrome 插件接入用户真实浏览器会话。这样可以保留登录态、Cookie、当前 tab 和真实页面环境，更适合 Agent 做网页任务。

## 3. 这个插件和 GA 的关系

GA 的实现位置：

```text
GenericAgent/assets/tmwd_cdp_bridge
```

GA 的核心链路：

```text
GA Agent
  -> TMWebDriver.py
  -> Chrome extension WebSocket
  -> background.js
  -> chrome.tabs / chrome.scripting / chrome.debugger
```

Cohert 版参考了这个链路，但第一版刻意收窄权限。

GA 里有这些能力：

- cookies
- CDP bridge
- batch command
- management
- contentSettings
- CSP header 移除

Cohert 第一版只保留：

- tabs
- scan
- execute_js
- WebSocket 连接
- popup 状态查看

这样做的原因是：先把主链路跑通，避免第一版插件权限过重。

## 4. Chrome 插件最小知识

一个 Chrome 插件至少要理解四个概念。

### 4.1 manifest.json

`manifest.json` 是插件身份证。

它告诉 Chrome：

- 插件叫什么。
- 插件版本是多少。
- 需要什么权限。
- 后台脚本是谁。
- popup 页面是谁。
- 哪些脚本要注入到网页里。

本项目对应文件：

```text
manifest.json
```

你可以把它理解为：

```text
manifest.json = 插件配置入口
```

### 4.2 background.js

`background.js` 是插件的大脑。

它不属于某个网页，而是跑在 Chrome 插件后台。

它负责：

- 连接 Cohert 本地 WebSocket。
- 监听 Cohert 发来的命令。
- 调用 Chrome API 列 tab、扫描页面、执行 JS。
- 把结果发回 Cohert。

本项目对应文件：

```text
background.js
```

你可以把它理解为：

```text
background.js = 插件后台控制器
```

### 4.3 content.js

`content.js` 是注入到网页里的脚本。

它能访问当前网页的 DOM，但它和 `background.js` 不在同一个运行环境。

当前第一版里，`content.js` 只做一件很轻的事：在页面右下角放一个 `Cohert bridge` 标记，证明插件已经注入到了页面。

本项目对应文件：

```text
content.js
```

你可以把它理解为：

```text
content.js = 页面内脚本
```

### 4.4 popup.html / popup.js

popup 是你点击 Chrome 插件图标后弹出来的小面板。

它负责：

- 显示插件是否连上 Cohert。
- 显示 WebSocket 地址。
- 显示当前可脚本化的标签页。
- 手动触发 reconnect。

本项目对应文件：

```text
popup.html
popup.js
```

你可以把它理解为：

```text
popup = 插件状态面板
```

## 5. 文件逐个讲

当前目录：

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

### 5.1 manifest.json

核心片段：

```json
{
  "manifest_version": 3,
  "name": "Cohert Browser Bridge",
  "permissions": ["tabs", "activeTab", "scripting", "debugger", "alarms"],
  "background": {
    "service_worker": "background.js"
  },
  "action": {
    "default_popup": "popup.html"
  }
}
```

逐行理解：

- `manifest_version: 3`：使用 Chrome MV3 插件规范。
- `name`：浏览器扩展页里显示的插件名。
- `permissions`：插件需要的 Chrome 能力。
- `background.service_worker`：后台入口文件。
- `action.default_popup`：点击插件图标时打开的页面。

权限解释：

- `tabs`：列出和读取标签页信息。
- `activeTab`：访问当前激活标签页。
- `scripting`：往网页里执行脚本。
- `debugger`：后续预留给 CDP fallback。
- `alarms`：定时重连和 keepalive。

面试说法：

> 我用 MV3 manifest 声明插件入口和最小权限。第一版只申请 tabs、scripting、alarms 等必要权限，没有申请 cookies、management 这类高风险权限。

### 5.2 config.js

核心内容：

```javascript
self.COHERT_BRIDGE_CONFIG = {
  wsUrl: "ws://127.0.0.1:18766/browser",
  maxScanChars: 12000,
  maxJsReturnChars: 8000
};
```

它集中放插件配置。

为什么用 `18766`：

- GA 默认使用 `18765`。
- Cohert 用 `18766` 避免和 GA 冲突。

为什么写到 `self`：

- `background.js` 通过 `importScripts("config.js")` 加载它。
- 在 service worker 环境里，挂到 `self` 上更明确。

面试说法：

> 插件默认连接本机 `ws://127.0.0.1:18766/browser`，这个端口后续由 Cohert Go 侧 bridge server 监听。配置单独放在 `config.js`，便于后面改端口或扫描长度。

### 5.3 background.js

这是最重要的文件。

它可以分成五块。

#### 第一块：状态和连接

```javascript
let ws = null;
let lastStatus = {
  connected: false,
  wsUrl: COHERT_BRIDGE_CONFIG.wsUrl,
  lastError: "",
  tabCount: 0
};
```

含义：

- `ws` 保存 WebSocket 连接。
- `lastStatus` 保存 popup 要展示的状态。

#### 第二块：列标签页

```javascript
async function listTabs() {
  const tabs = await chrome.tabs.query({});
  return tabs
    .filter((tab) => isScriptable(tab.url))
    .map((tab) => ({
      id: String(tab.id),
      title: tab.title || "",
      url: tab.url || "",
      active: !!tab.active,
      windowId: tab.windowId
    }));
}
```

关键点：

- `chrome.tabs.query({})` 获取所有 tab。
- 只保留 `http://` 和 `https://` 页面。
- 过滤掉 `chrome://`、扩展页等不能执行脚本的内部页面。

这就是后续 `browser_tabs` 工具的数据来源。

#### 第三块：扫描页面

```javascript
async function scanTab(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    func: () => {
      const text = document.body ? document.body.innerText || "" : "";
      return {
        title: document.title || "",
        url: location.href,
        text
      };
    }
  });
}
```

这段是 `browser_scan` 的本质。

它让 Chrome 在指定页面里执行一段函数：

```javascript
document.body.innerText
```

然后拿到：

- 页面标题。
- 页面 URL。
- 页面可见文本。

为什么不用完整 HTML：

- HTML 太长。
- 包含大量脚本、样式、隐藏节点。
- 模型真正需要的是可读文本和少量结构信息。

所以第一版只读 `innerText`，并且做 `max_chars` 截断。

面试说法：

> browser_scan 第一版不是抓完整 DOM，而是通过 `chrome.scripting.executeScript` 在真实页面里读取 `document.body.innerText`、`document.title` 和 `location.href`，再做字符截断，避免撑爆模型上下文。

#### 第四块：执行 JS

```javascript
async function executeJs(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const script = request.script || request.code || "";
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    func: async (wrapped) => await eval(wrapped),
    args: [buildPageEvalScript(script)]
  });
}
```

这就是 `browser_execute_js` 的核心。

Cohert 后续会给插件发：

```json
{
  "id": "1",
  "command": "execute_js",
  "tab_id": "123",
  "script": "return document.title"
}
```

插件把这段 JS 放到真实页面里执行，然后把结果回传。

注意：

现在要求脚本显式 `return`：

```javascript
return document.title
```

如果不写 `return`，很多脚本会返回空。

面试说法：

> JS 执行走 `chrome.scripting.executeScript`，插件把 Cohert 发来的脚本包装到页面上下文里执行，并把返回值序列化。第一版要求读取型 JS 显式 return，后续再做风险识别和用户确认。

#### 第五块：WebSocket 协议

```javascript
function connectWS() {
  ws = new WebSocket(COHERT_BRIDGE_CONFIG.wsUrl);
}
```

连接成功后发送：

```json
{
  "type": "ext_ready",
  "name": "Cohert Browser Bridge",
  "version": "0.1.0",
  "tabs": []
}
```

收到 Cohert 命令后：

```javascript
ws.onmessage = async (event) => {
  const payload = JSON.parse(event.data);
  sendResult(payload.id, await handleCommand(payload));
};
```

命令分发：

```javascript
if (command === "tabs") return { status: "success", tabs: await listTabs() };
if (command === "scan") return await scanTab(message);
if (command === "execute_js") return await executeJs(message);
```

面试说法：

> 插件主动连接本地 WebSocket。Go 侧只需要维护一个 browser bridge server，通过 request id 发送命令，插件执行后用同一个 id 返回 result 或 error。

### 5.4 content.js

当前只做状态标记：

```javascript
indicator.textContent = "Cohert bridge";
document.body.appendChild(indicator);
```

它的作用：

- 证明插件已经注入页面。
- 帮助开发时肉眼确认插件工作。

它现在不负责通信。

为什么不让 `content.js` 负责通信：

- 通信放到 `background.js` 更集中。
- 第一版避免 DOM 消息桥带来的复杂度。
- 后续需要页面内更复杂交互时，再增强 content script。

### 5.5 popup.html / popup.js

popup 用于人工调试。

它会向 `background.js` 发消息：

```javascript
chrome.runtime.sendMessage({ cmd: "status" })
chrome.runtime.sendMessage({ cmd: "tabs" })
```

然后显示：

- 是否连接 Cohert。
- WebSocket URL。
- 当前 tab 数量。
- 当前 tab 列表。

面试说法：

> popup 不是核心链路，只是调试面板。它通过 `chrome.runtime.sendMessage` 问 background 当前状态，便于手动确认插件是否连接上 Go 侧服务。

## 6. 怎么本地安装

打开 Chrome：

```text
chrome://extensions
```

步骤：

1. 打开右上角 Developer mode。
2. 点击 Load unpacked。
3. 选择目录：

```text
/Users/bytedance/Desktop/myOwnProject/Cohort/assert/cohert_browser_bridge
```

安装成功后：

- Chrome 工具栏会出现 `Cohert Browser Bridge`。
- 打开一个普通网页，例如 `https://example.com`。
- 页面右下角应该出现 `Cohert bridge` 小标记。
- 点击插件图标，popup 会显示状态。

当前 Go 侧 server 还没实现，所以状态应该是：

```text
waiting for Cohert
```

这是正常的。

## 7. 怎么调试插件

### 7.1 调试 background.js

打开：

```text
chrome://extensions
```

找到 `Cohert Browser Bridge`。

点击：

```text
service worker
```

会打开 DevTools。

这里能看：

- `console.log`
- WebSocket 连接错误
- JS 报错

### 7.2 调试 content.js

打开一个网页。

按：

```text
Command + Option + I
```

在 Console 里看页面脚本错误。

如果右下角没有 `Cohert bridge` 标记，说明 content script 没有注入。

常见原因：

- 当前页面是 `chrome://`，插件不能注入。
- 当前页面是扩展页面，插件不能注入。
- 插件没有重新加载。

### 7.3 调试 popup.js

右键插件图标，选择 Inspect popup。

或者打开 popup 后右键检查。

这里能看 popup 的 DOM 和 console。

## 8. 怎么验证 JS 语法

在项目根目录运行：

```bash
node --check assert/cohert_browser_bridge/background.js
node --check assert/cohert_browser_bridge/content.js
node --check assert/cohert_browser_bridge/popup.js
```

这只能检查语法，不能证明 Chrome API 一定可用。

Chrome API 只能在插件环境里真实测试。

## 9. 后续 Go 侧怎么接

下一步不是继续改插件，而是在 Go 里实现本地 bridge server。

目标链路：

```text
internal/browser/server.go
  监听 ws://127.0.0.1:18766/browser
  接收插件 ext_ready / tabs_update
  维护 tabs 快照
  给插件发送 tabs / scan / execute_js 命令
```

建议文件：

```text
internal/browser/
  types.go
  server.go
  client.go
```

`types.go` 放结构体：

```go
type Tab struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    URL    string `json:"url"`
    Active bool   `json:"active"`
}

type PageSnapshot struct {
    Status    string `json:"status"`
    TabID     string `json:"tab_id"`
    Title     string `json:"title"`
    URL       string `json:"url"`
    Text      string `json:"text"`
    Truncated bool   `json:"truncated"`
    CharCount int    `json:"char_count"`
    Omitted   int    `json:"omitted"`
}
```

`client.go` 定义接口：

```go
type Client interface {
    Tabs(ctx context.Context) ([]Tab, error)
    Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error)
    ExecuteJS(ctx context.Context, tabID string, script string, maxChars int) (JSResult, error)
}
```

然后工具层接：

```text
internal/tools/browser_tabs.go
internal/tools/browser_scan.go
internal/tools/browser_js.go
```

## 10. 面试怎么讲

可以按这个结构讲。

### 10.1 背景

> Cohert 是本地 Agent Runtime，后续需要处理网页任务。普通 HTTP 请求不能复用用户真实登录态，也看不到动态渲染后的 DOM，所以我设计了一个 Chrome 插件桥接真实浏览器。

### 10.2 参考

> 我参考了 GenericAgent 的 TMWebDriver。GA 是本地 WebSocket server 加 Chrome extension，插件通过 Chrome API 操作真实浏览器 tab。我保留了这个主链路。

### 10.3 取舍

> 但我没有直接照搬 GA 的高权限能力。第一版 Cohert 插件只保留 tabs、scan、execute_js，不申请 cookies、management、contentSettings，也不移除 CSP header。这样第一版安全边界更清晰。

### 10.4 架构

> 插件的 background service worker 主动连 Cohert 本地 WebSocket。Go 侧发送带 request id 的命令，插件执行后返回 result 或 error。页面扫描通过 `chrome.scripting.executeScript` 读取 `document.body.innerText`，JS 执行也走同一个机制。

### 10.5 后续

> 下一步 Go 侧实现 `internal/browser` bridge server，再把能力封装成 `browser_tabs` 和 `browser_scan` 工具，接入 Runner 的工具系统和 Context Manager。

## 11. 修改路线建议

不要一上来改 `background.js` 大段逻辑。

建议按这个顺序练：

1. 改 `popup.html` 文案，重新加载插件，看 popup 变化。
2. 改 `content.js` 右下角标记颜色，刷新网页，看页面变化。
3. 在 `background.js` 的 `listTabs()` 里多返回一个字段，例如 `favIconUrl`。
4. 给 popup 多展示 `favIconUrl`。
5. 后续 Go server 接好后，再调 `scan`。

这样能逐步建立手感。

## 12. 常见坑

### 12.1 为什么 chrome:// 页面扫不到

Chrome 不允许普通扩展在 `chrome://` 页面注入脚本。

所以只支持：

```text
http://...
https://...
```

### 12.2 为什么插件显示 waiting for Cohert

因为 Go 侧 WebSocket server 还没实现或没启动。

插件会一直尝试连接：

```text
ws://127.0.0.1:18766/browser
```

### 12.3 为什么 execute_js 要写 return

因为脚本被包装到 async function 里执行。

要拿返回值，写：

```javascript
return document.title
```

不要只写：

```javascript
document.title
```

### 12.4 为什么不先做 cookies

cookies 是高敏感能力。

第一版目标是打通浏览器桥，不是拿浏览器所有权限。

后续如果确实需要 cookie 能力，应该单独设计权限开关和用户确认。

## 13. 当前阶段结论

现在插件已经有了最小壳：

- Chrome 可以加载。
- 插件能显示 popup。
- 插件会尝试连 Cohert。
- 插件定义了 tabs、scan、execute_js 协议。

但 Go 侧还没接，所以它现在只是桥的一端。

下一步真正让它工作，需要实现：

```text
internal/browser/server.go
internal/tools/browser_tabs.go
internal/tools/browser_scan.go
```
