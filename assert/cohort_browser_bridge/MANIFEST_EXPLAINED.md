# manifest.json 逐行讲解

`manifest.json` 是 Chrome 插件的入口配置文件。

注意：这个文件必须是严格 JSON，不能写 `//`、`/* */` 这类注释。否则 Chrome 加载插件时会直接报错。所以真实可加载文件保持为：

```text
manifest.json
```

逐行解释放在当前文档。

## 原文件

```json
{
  "manifest_version": 3,
  "name": "Cohort Browser Bridge",
  "version": "0.1.0",
  "description": "Connect Cohort to a real Chrome session for tab scanning and controlled JavaScript execution.",
  "permissions": [
    "tabs",
    "activeTab",
    "scripting",
    "debugger",
    "alarms"
  ],
  "host_permissions": [
    "http://*/*",
    "https://*/*"
  ],
  "background": {
    "service_worker": "background.js"
  },
  "content_scripts": [
    {
      "matches": [
        "http://*/*",
        "https://*/*"
      ],
      "js": [
        "content.js"
      ],
      "run_at": "document_idle",
      "all_frames": false
    }
  ],
  "action": {
    "default_popup": "popup.html",
    "default_title": "Cohort Browser Bridge"
  }
}
```

## 顶层结构

### 第 1 行

```json
{
```

JSON 对象开始。

Chrome 插件的 manifest 本质就是一个 JSON 对象，里面用 key/value 描述插件。

### 第 2 行

```json
"manifest_version": 3,
```

声明使用 Chrome Manifest V3。

现在 Chrome 新插件基本都要求用 MV3。MV3 和旧版 MV2 最大的区别之一是：后台脚本从长期常驻的 background page 变成了可能被挂起的 service worker。

这也是为什么我们在 `background.js` 里用了 `chrome.alarms` 做重连和 keepalive。

### 第 3 行

```json
"name": "Cohort Browser Bridge",
```

插件名。

这个名字会显示在：

- `chrome://extensions`
- 插件工具栏
- 插件详情页

这里定名为 `Cohort Browser Bridge`，意思是它是 Cohort 和真实 Chrome 浏览器之间的桥。

### 第 4 行

```json
"version": "0.1.0",
```

插件版本号。

第一版是 `0.1.0`，表示还处在早期开发阶段。

以后如果改了协议或能力，可以升级到：

```text
0.2.0
1.0.0
```

### 第 5 行

```json
"description": "Connect Cohort to a real Chrome session for tab scanning and controlled JavaScript execution.",
```

插件描述。

它会显示在 Chrome 扩展管理页面。这里说明了两个核心能力：

- tab scanning：读取标签页和页面内容。
- controlled JavaScript execution：受控执行 JS。

## permissions

### 第 6 行

```json
"permissions": [
```

开始声明插件需要的 Chrome API 权限。

`permissions` 控制的是插件能调用哪些 Chrome 扩展 API。

### 第 7 行

```json
"tabs",
```

允许插件使用 `chrome.tabs` API。

我们用它做几件事：

- `chrome.tabs.query({})`：列出所有标签页。
- `chrome.tabs.get(tabId)`：读取指定 tab 的标题、URL 等信息。
- 监听 `chrome.tabs.onUpdated/onCreated/onRemoved`：标签页变化时通知 Cohort。

对应 Cohort 后续工具：

```text
browser_tabs
browser_scan
browser_execute_js
```

面试说法：

> tabs 权限用于发现真实浏览器里的可用标签页，并在标签页变化时同步给 Agent。

### 第 8 行

```json
"activeTab",
```

允许插件访问当前激活标签页。

当 Go 侧没有指定 `tab_id` 时，我们会默认使用当前窗口的 active tab：

```javascript
chrome.tabs.query({ active: true, currentWindow: true })
```

这个权限让“扫描当前页面”这件事更自然。

### 第 9 行

```json
"scripting",
```

允许插件使用 `chrome.scripting.executeScript`。

这是当前插件最关键的权限之一。

我们用它在真实网页里执行脚本：

```javascript
chrome.scripting.executeScript({
  target: { tabId },
  world: "MAIN",
  func: () => document.body.innerText
})
```

`browser_scan` 就是靠它读取：

- `document.title`
- `location.href`
- `document.body.innerText`

`browser_execute_js` 也是靠它执行 Cohort 发来的 JS。

面试说法：

> scripting 权限是页面扫描和 JS 执行的核心。它让插件能在真实页面上下文中读取渲染后的 DOM，而不是只拿 HTTP 原始响应。

### 第 10 行

```json
"debugger",
```

允许插件使用 `chrome.debugger` API。

当前第一版还没有真正启用 CDP fallback，但保留这个权限是为了下一步做：

- `Runtime.evaluate`
- 绕过部分 `chrome.scripting` 无法执行的页面限制
- 获取更底层的页面状态

GA 的 `tmwd_cdp_bridge` 就大量使用了 `chrome.debugger.sendCommand`。

这里先声明出来，是因为 browser bridge 的后续增强大概率会用到。

注意：

`debugger` 是比较敏感的权限，Chrome 会在安装时明显提示用户。后续如果确认第一版只做 `tabs/scan`，也可以暂时移除它，等实现 CDP fallback 再加回来。

### 第 11 行

```json
"alarms"
```

允许插件使用 `chrome.alarms`。

MV3 的 background service worker 不是永久常驻的，Chrome 可能会挂起它。

我们用 `alarms` 做两件事：

- 未连接 Cohort 时，定期尝试重连。
- 已连接 Cohort 时，定期发送 keepalive ping。

对应代码在 `background.js`：

```javascript
chrome.alarms.create(...)
chrome.alarms.onAlarm.addListener(...)
```

### 第 12 行

```json
],
```

`permissions` 数组结束。

## host_permissions

### 第 13 行

```json
"host_permissions": [
```

开始声明插件可以访问哪些网页地址。

`permissions` 控制 Chrome API，`host_permissions` 控制网页范围。

### 第 14 行

```json
"http://*/*",
```

允许插件访问所有 HTTP 页面。

含义：

```text
任意 host
任意 path
```

例如：

```text
http://example.com/
http://localhost:3000/
```

### 第 15 行

```json
"https://*/*"
```

允许插件访问所有 HTTPS 页面。

大多数真实网站都是 HTTPS，所以这个必须有。

例如：

```text
https://example.com/
https://github.com/
```

### 第 16 行

```json
],
```

`host_permissions` 数组结束。

为什么不写 `<all_urls>`：

GA 版用了更大的权限范围，但 Cohort 第一版主动收窄，只处理 `http/https`。

这样不会覆盖：

- `chrome://`
- `file://`
- `chrome-extension://`

安全边界更清楚。

## background

### 第 17 行

```json
"background": {
```

开始声明后台脚本。

Chrome 插件通常有一个后台入口，负责处理长期逻辑。

### 第 18 行

```json
"service_worker": "background.js"
```

声明后台入口文件是：

```text
background.js
```

这个文件负责：

- 连接 Cohort WebSocket。
- 维护连接状态。
- 分发 `tabs/scan/execute_js` 命令。
- 监听 tab 变化。
- 给 popup 提供状态。

### 第 19 行

```json
},
```

`background` 配置结束。

## content_scripts

### 第 20 行

```json
"content_scripts": [
```

开始声明要注入网页的脚本。

content script 和 background script 不一样：

- background 跑在插件后台。
- content script 跑在网页里。

### 第 21 行

```json
{
```

开始声明一组 content script 规则。

### 第 22-25 行

```json
"matches": [
  "http://*/*",
  "https://*/*"
],
```

声明哪些网页会注入 `content.js`。

这里和 `host_permissions` 一样，只覆盖 HTTP/HTTPS 页面。

### 第 26-28 行

```json
"js": [
  "content.js"
],
```

声明注入的脚本文件是：

```text
content.js
```

当前 `content.js` 只做一个很轻的调试标记：

```text
页面右下角显示 Cohort bridge
```

它不负责 WebSocket，也不负责核心 browser_scan。

### 第 29 行

```json
"run_at": "document_idle",
```

声明注入时机。

`document_idle` 表示页面主体基本加载完后再注入。

这里适合我们当前的角标逻辑，因为它需要 `document.body` 存在。

常见取值：

- `document_start`：页面刚开始加载。
- `document_end`：DOM 完成。
- `document_idle`：页面空闲时，通常更晚一点。

### 第 30 行

```json
"all_frames": false
```

声明是否注入所有 iframe。

当前设置为 `false`，表示只注入顶层页面，不注入 iframe。

第一版这样更简单，避免一个页面里多个 iframe 都显示角标。

### 第 31-32 行

```json
}
],
```

content script 规则结束，`content_scripts` 数组结束。

## action

### 第 33 行

```json
"action": {
```

开始声明插件工具栏按钮行为。

Chrome 右上角那个插件图标，点击后显示什么，就在这里配置。

### 第 34 行

```json
"default_popup": "popup.html",
```

声明点击插件图标后打开：

```text
popup.html
```

这个 popup 用于调试：

- 看是否 connected。
- 看 WebSocket 地址。
- 看当前 tab 列表。
- 手动 reconnect。

### 第 35 行

```json
"default_title": "Cohort Browser Bridge"
```

声明鼠标悬浮到插件图标上时显示的标题。

### 第 36-38 行

```json
}
}
```

`action` 配置结束，整个 manifest JSON 对象结束。

## 面试版总结

可以这么讲：

> manifest.json 是 Chrome 插件的入口配置。我用 Manifest V3 声明了一个后台 service worker，也就是 background.js。权限上第一版只保留 tabs、activeTab、scripting、alarms，以及为后续 CDP fallback 预留的 debugger。host_permissions 只覆盖 http/https 页面，不碰 chrome:// 和 file://。content_scripts 只注入一个轻量 content.js 做页面标记，核心通信都放在 background.js 里。action 配置 popup.html，作为人工调试面板。
