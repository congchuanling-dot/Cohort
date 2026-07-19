# GA 浏览器操作技术路线记录

本文记录 GenericAgent 中浏览器操作能力的完整链路，以及 Cohert 下一步应如何对齐这条路线。

## 1. 核心结论

GA 的浏览器能力不是 Selenium、Playwright，也不是 OCR 主链路。

它的核心路线是：

```text
LLM 工具调用
  -> GA 工具层 web_scan / web_execute_js
  -> TMWebDriver.py 本地 WebSocket/HTTP 服务
  -> Chrome MV3 扩展 tmwd_cdp_bridge
  -> chrome.tabs / chrome.scripting / chrome.debugger
  -> 用户真实 Chrome 标签页
  -> 执行结果 / 页面变化 / 标签页状态回传给 Agent
```

这条路线的关键价值是：**接管用户真实浏览器会话**，复用已有登录态、Cookie、扩展环境和正常浏览器指纹，而不是启动一个新的无头浏览器。

## 2. GA 的工具入口

GA 对模型暴露两个核心浏览器工具：

```text
web_scan
web_execute_js
```

### 2.1 web_scan

`web_scan` 用于感知页面。

主要能力：

- 初始化浏览器 driver。
- 获取所有可用标签页。
- 支持切换当前 tab。
- 返回 tab 元信息。
- 可选返回页面简化 HTML。
- 可选返回纯文本。
- 对页面内容做截断。

它不是直接返回完整 `document.documentElement.outerHTML`。真实网页 HTML 太大，且包含大量隐藏节点、边栏、浮层、脚本和样式，直接塞给模型会浪费上下文。

GA 使用 `simphtml.get_html()` 对页面做压缩和简化：

- 过滤弱相关元素。
- 保留主体文本和关键可交互节点。
- 对长列表只保留少量代表项。
- 超长内容做智能截断。

### 2.2 web_execute_js

`web_execute_js` 用于控制页面。

主要能力：

- 在指定 tab 中执行 JS。
- 支持从参数或代码块读取脚本。
- 执行前记录页面快照。
- 执行后等待页面变化。
- 返回 JS 结果。
- 返回是否刷新、是否新开 tab、DOM 变化摘要。
- 长结果可写入文件，只把摘要回灌给模型。

GA 的重要经验是：浏览器操作不能只返回“成功/失败”，还要告诉模型“页面有没有变化”。否则模型很容易盲目重复点击。

## 3. TMWebDriver 本地桥

GA 的本地桥在 `TMWebDriver.py`。

它默认开启两个端口：

```text
18765  WebSocket，Chrome 扩展主动连接
18766  HTTP，remote/link/longpoll 兼容入口
```

Chrome 扩展连接后，会发送：

```json
{
  "type": "ext_ready",
  "tabs": []
}
```

之后每个 Chrome tab 会被注册成一个 session：

```text
session_id = tab.id
type = ext_ws
url/title = tab 当前信息
```

也就是说，GA 的浏览器 session 本质上就是 Chrome tab id。

当 Agent 要执行 JS 时，Python 侧构造：

```json
{
  "id": "uuid",
  "code": "...",
  "tabId": 123
}
```

然后通过 WebSocket 发给扩展。扩展执行完成后，再用相同的 `id` 返回 `result` 或 `error`。

## 4. Chrome 扩展层

GA 的扩展位于：

```text
GenericAgent/assets/tmwd_cdp_bridge
```

核心文件：

```text
manifest.json
background.js
content.js
disable_dialogs.js
popup.html
popup.js
```

### 4.1 manifest 权限

GA 扩展权限较重，包括：

```text
cookies
tabs
activeTab
debugger
scripting
alarms
declarativeNetRequest
management
contentSettings
```

这些权限让 GA 能做更多高级操作，比如读 Cookie、管理扩展、放开自动下载、移除 CSP、通过 CDP 控制页面。

Cohert 第一版不应全量照搬这些权限。当前应保持最小权限，先把主链路跑稳，再按真实需求增加权限。

### 4.2 background.js

`background.js` 是 GA 扩展的大脑。

它负责：

- 连接本地 WebSocket。
- 连接后发送 `ext_ready`。
- 监听 tab 变化并发送 `tabs_update`。
- 处理普通 JS 执行。
- 处理扩展内部命令。
- 处理 CDP 命令。
- 捕获新开 tab。
- 在 CSP 导致普通 JS 执行失败时尝试 CDP fallback。

### 4.3 普通 JS 执行

普通 JS 走：

```text
chrome.scripting.executeScript
```

执行环境使用：

```text
world: "MAIN"
```

这样可以在页面主世界里执行 JS，更接近真实页面环境。

GA 会把模型传来的 JS 包装成 async 函数，并对返回值做序列化处理：

- DOM 元素转 `outerHTML`。
- NodeList / HTMLCollection 转 HTML 数组。
- 普通对象转 JSON。
- 无法序列化的对象返回可读错误。

### 4.4 CDP fallback

如果普通 `chrome.scripting.executeScript` 因 CSP 或上下文问题失败，GA 会尝试：

```text
chrome.debugger.attach
chrome.debugger.sendCommand Runtime.evaluate
chrome.debugger.detach
```

这让它在部分页面上仍然能执行 JS。

## 5. CDP 操作路线

GA 的高级操作不是靠 OCR，而是靠 CDP。

CDP 命令通过 `web_execute_js` 传 JSON 字符串进入扩展：

```json
{
  "cmd": "cdp",
  "tabId": 123,
  "method": "Input.dispatchMouseEvent",
  "params": {}
}
```

扩展识别 `cmd: "cdp"` 后，调用：

```text
chrome.debugger.sendCommand
```

### 5.1 CDP 点击

GA SOP 中已验证的通用点击生命周期是：

```text
mouseMoved
mousePressed
mouseReleased
```

也就是三次 CDP 命令：

```text
Input.dispatchMouseEvent type=mouseMoved
Input.dispatchMouseEvent type=mousePressed button=left clickCount=1
Input.dispatchMouseEvent type=mouseReleased button=left clickCount=1
```

这样比 JS 的 `element.click()` 更接近真实用户点击。

### 5.2 JS 点击和 CDP 点击的区别

JS 点击：

```js
document.querySelector("button").click()
```

问题：

```text
event.isTrusted = false
```

很多敏感操作会拦截这种点击，例如登录、支付、打开新窗口、文件上传、自定义下拉框。

CDP 点击通过浏览器调试协议派发输入事件，更接近真实用户行为，适合做：

- 自定义下拉框。
- 需要 hover 的菜单。
- JS 点击打不开的新 tab。
- 部分框架拦截 `isTrusted=false` 的按钮。

### 5.3 坐标原则

GA SOP 的结论是：

```text
稳定状态下，CDP 坐标 = getBoundingClientRect() 坐标
```

也就是使用元素在 viewport 内的坐标，不需要转成屏幕物理坐标。

需要注意首次 attach 的陷阱：

- 首次 `chrome.debugger.attach` 可能让 Chrome 顶部出现 debugger infobar。
- 这个 infobar 可能把页面内容向下推，导致 attach 前测到的坐标失效。
- 解决方式是先做一次无害 CDP 预热，再测量坐标。

推荐流程：

```text
1. 对 tab 执行一次无害 mouseMoved(0, 0)，完成 CDP attach 预热。
2. 再用 JS 获取目标元素 getBoundingClientRect()。
3. 用 rect 中心点发送 CDP 三事件点击。
```

## 6. OCR 的位置

GA 浏览器主链路不依赖 OCR。

优先级应是：

```text
DOM 扫描
  -> 普通 JS 操作
  -> CDP 输入事件 / CDP 截图
  -> OCR / 系统级鼠标键盘兜底
```

OCR 适合这些场景：

- 页面文字是图片。
- DOM 读不到验证码或图片文字。
- 需要识别截图里的视觉元素。
- 浏览器 DOM/CDP 都无法完成任务。

对于普通网页查询、Google 搜索、天气卡片、表单填写，优先使用 DOM + JS + CDP。

## 7. Cohert 当前状态

Cohert 当前已经有第一版浏览器桥：

```text
assert/cohert_browser_bridge
internal/browser
internal/tools/browser_tools.go
```

已经具备：

```text
browser_tabs
browser_open
browser_scan
Chrome 插件 WebSocket 连接
tab 列表上报
页面 DOM 文本扫描
```

当前还缺：

```text
browser_execute_js
browser_cdp
browser_click
browser_click_element
页面变化监控
CDP 预热和坐标稳定处理
```

## 8. Cohert 下一步开发路线

### P0：暴露 browser_execute_js

目标：先让 Cohert 具备 GA 的 `web_execute_js` 基础能力。

开发内容：

- Go 工具层新增 `browser_execute_js`。
- 参数：

```json
{
  "tab_id": "可选",
  "script": "要执行的 JS",
  "no_monitor": true
}
```

- 插件侧复用现有 `execute_js` 命令。
- 返回：

```json
{
  "status": "success",
  "tab_id": "123",
  "js_return": "...",
  "new_tabs": []
}
```

第一版可以先不做复杂 diff，但必须保留字段位置，后续补页面变化监控。

验收：

```text
browser_execute_js 执行 document.title 能返回标题
browser_execute_js 执行 document.body.innerText 能返回正文
browser_execute_js 执行 location.href = "..." 能导航
```

### P1：新增 browser_cdp

目标：对齐 GA 的 CDP 桥能力。

开发内容：

- 插件 `background.js` 增加 `cdp` command。
- Go 协议层增加 `CDPCommand`。
- Go 工具层新增 `browser_cdp`。
- 参数：

```json
{
  "tab_id": "必填或默认当前 tab",
  "method": "Input.dispatchMouseEvent",
  "params": {}
}
```

插件执行：

```text
chrome.debugger.attach
chrome.debugger.sendCommand
chrome.debugger.detach
```

验收：

```text
browser_cdp Page.bringToFront 成功
browser_cdp Runtime.evaluate 成功
browser_cdp Input.dispatchMouseEvent 能发出鼠标事件
```

### P2：新增 browser_click

目标：提供稳定坐标点击，不让模型自己拼三段 CDP。

参数：

```json
{
  "tab_id": "可选",
  "x": 120,
  "y": 300
}
```

内部流程：

```text
1. Page.bringToFront
2. Input.dispatchMouseEvent mouseMoved
3. Input.dispatchMouseEvent mousePressed
4. Input.dispatchMouseEvent mouseReleased
```

实现细节：

- click 内部统一做三事件序列。
- 首次点击前可做一次 `mouseMoved(0,0)` 预热。
- 工具结果返回点击坐标和 CDP 执行状态。

验收：

```text
打开一个普通网页
点击页面按钮
按钮状态或页面 DOM 发生变化
```

### P3：新增 browser_click_element

目标：让模型可以按 selector 点元素，而不是自己算坐标。

参数：

```json
{
  "tab_id": "可选",
  "selector": "button[type=submit]"
}
```

内部流程：

```text
1. browser_execute_js 获取元素 getBoundingClientRect()
2. 计算中心点
3. browser_click 走 CDP 三事件点击
```

返回：

```json
{
  "status": "success",
  "selector": "...",
  "rect": {},
  "clicked_at": {"x": 0, "y": 0}
}
```

验收：

```text
可以点击 Google 搜索结果
可以点击普通按钮
可以打开简单下拉菜单
```

### P4：页面变化监控

目标：补齐 GA `execute_js_rich` 的核心反馈机制。

开发内容：

- 执行动作前扫描简化 DOM。
- 执行动作后等待短时间。
- 再扫描简化 DOM。
- 返回：

```text
页面是否刷新
是否新开 tab
DOM 是否变化
显著变化片段
```

第一版不要追求完整 `simphtml`，可以从 `document.body.innerText` 和少量可交互元素摘要开始。

## 9. 推荐开发顺序

下一步建议严格按这个顺序做：

```text
1. browser_execute_js
2. 插件 cdp command
3. browser_cdp
4. browser_click
5. browser_click_element
6. 页面变化监控
```

不要直接跳到 OCR，也不要先做复杂 selector 引擎。

原因：

- `browser_execute_js` 是 DOM 感知和元素定位的基础。
- `browser_cdp` 是真实点击、截图、输入的基础。
- `browser_click` 把 CDP 三事件封装掉，降低模型误用概率。
- `browser_click_element` 建立在 JS 定位和 CDP 点击之上。
- 页面变化监控最后补，可以先让动作闭环跑通。

## 10. Cohert 与 GA 的取舍

Cohert 应继承 GA 的技术路线：

```text
真实浏览器插件桥
DOM/JS 优先
CDP 处理真实输入
OCR 只做兜底
工具结果必须节制返回
```

但不应一开始照搬 GA 的全部权限和复杂能力：

```text
cookies
management
contentSettings
declarativeNetRequest 移除 CSP
自动处理下载策略
复杂 iframe CDP 穿透
验证码视觉链路
```

这些能力可以等基础链路稳定后按需增加。

第一阶段的目标不是“浏览器万能自动化”，而是先让 Cohert 稳定完成：

```text
打开网页
读取页面
执行 JS
真实点击
观察变化
总结结果
```

