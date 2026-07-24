# Browser SOP

## 触发场景

- 用户要求打开网页、查询网页、登录、点击、输入、搜索、提取页面信息。
- 浏览器工具返回空内容、页面未加载、按钮点了没反应、模型开始猜 selector 或 CDP 参数。
- 需要使用 CDP JSON 路由、截图、OCR、iframe 或新 tab。

## 默认网页读取流程

```text
browser_open
browser_wait_for_load
browser_wait_for_stable
browser_scan
```

禁止：

- 网页还没加载完成就直接判断失败。
- `browser_scan` 为空就立刻换方案。
- 页面跳转、点击、输入后不等待就继续判断。

如果读取为空：

```text
1. browser_wait_for_load
2. browser_wait_for_stable
3. browser_scan
4. 仍为空再 browser_execute_js 读取 document.readyState、location.href、document.body.innerText.length
```

## 浏览器交互流程

读取 DOM 状态：

```text
browser_execute_js
```

真实点击：

```text
browser_click_element
```

真实输入：

```text
browser_type_element
```

按键/快捷键：

```text
browser_press_key
```

Selector 不明确时，先用：

```text
browser_snapshot
```

动作后必须根据场景等待：

```text
browser_wait_for_load
browser_wait_for_url
browser_wait_for_selector
browser_wait_for_text
browser_wait_for_stable
```

点击/输入后的判断顺序：

```text
1. 看工具 diff
2. 等待 selector/text/url/stable 中最贴近目标的状态
3. 再 scan 或 execute_js 验证
4. 没有新信息时不要重复点击同一位置
```

## CDP JSON 路由

`browser_cdp` 不作为默认公开工具使用。高级浏览器能力通过 `browser_execute_js` 的 JSON 命令路由进入插件内部：

```json
{"cmd":"cdp","method":"Runtime.evaluate","params":{"expression":"document.title"}}
```

```json
{"cmd":"batch","commands":[{"cmd":"tabs"},{"cmd":"wait","mode":"stable"}]}
```

普通动作优先使用高层工具，不要让模型手拼 CDP 鼠标和键盘事件。

适合 JSON 路由的场景：

- 需要 `Page.captureScreenshot`。
- 需要 `Runtime.evaluate` 在 CDP 侧执行。
- 多个内部命令必须减少 LLM 往返时。
- 高层工具暂时没有覆盖的 CDP 能力。

不适合 JSON 路由的场景：

- 普通点击，用 `browser_click_element`。
- 普通输入，用 `browser_type_element`。
- 普通按键，用 `browser_press_key`。
- 查找按钮/输入框，用 `browser_snapshot`。
- 普通页面读取，用 `browser_scan` 或 `browser_execute_js` 普通 JS。

## 快照与按键

- 找按钮、链接、输入框、发送入口时，优先 `browser_snapshot`。
- `browser_snapshot` 返回 selector 建议、文字、aria-label、role、rect、visible、disabled。
- 回车搜索、Esc 关弹窗、Tab 切焦点、Cmd+Enter/Ctrl+Enter 发送消息时，优先 `browser_press_key`。
- 不要为了按 Enter 或找按钮手写 `Runtime.evaluate` / `Input.dispatchKeyEvent`。

## 点击与输入

- `browser_click_element` 会滚动元素到视口内，重测 rect，并通过 `elementFromPoint` 检查遮挡。
- 如果中心点被挡，工具会尝试多个候选点；都不可点击时会返回遮挡元素信息。
- `browser_type_element` 会先确认目标是 `input`、`textarea`、`select` 或 `contenteditable`。
- 输入后会回读 `value` 或文本内容，并返回 `verified`，不要忽略校验结果。

## 新 tab 与跳转

- 点击可能新开 tab 时，先看动作 diff 是否出现 tab count 变化。
- 新 tab 出现后先 `browser_tabs`，确认目标 tab，再切换或指定 tab 操作。
- URL 变化后优先用 `browser_wait_for_url` 等待目标 URL，再 wait stable 和 scan。
- 登录、搜索、详情页跳转这类场景，不要只等 stable 后猜是否成功。

## 截图和 OCR

- 截图优先用 `browser_screenshot`，图片会保存到 workspace，只返回路径和尺寸。
- OCR 只在 DOM 文本不可用、canvas/image 渲染、验证码旁说明等场景兜底。
- 优先顺序：`browser_scan` -> `browser_dom_summary` -> `browser_screenshot` / `browser_ocr`。
- `browser_ocr` 可以读取 workspace 内的 `image_path`；不传时会先截取当前浏览器视口。
- OCR 返回 `coordinate_space=screenshot-local` 和 bbox。它们只表示图片内坐标，不能直接作为系统屏幕坐标点击。
- 默认 `enhance=false`；清晰页面文字不应先做放大/高对比度处理。
- 缺少 `rapidocr-onnxruntime`、`pillow` 或 `numpy` 时，按工具提示由用户手动安装依赖，不在任务中隐式安装。
- 普通网页不要默认 OCR。

## 验收标准

- 查询类任务：页面已 wait，`browser_scan` 或 JS 返回了目标信息。
- 动作类任务：动作后有 diff 或明确 wait 结果，再做结论。
- 登录/验证码类任务：验证码必须通过 `ask_user` 人工介入，不自动绕过。

## 常见坑

- JS 的 `element.click()` 和 `dispatchEvent` 是 `isTrusted=false`，敏感按钮可能拒绝。
- 导航后不能在同一段 JS 继续操作，页面上下文会销毁。
- 首次 CDP attach 可能影响坐标，点击前要重新测 rect 或先做无害预热。
- 后台标签页可能节流，必要时先让页面到前台。
- OCR 只做 DOM/CDP 都不可用时的兜底，不是普通网页主链路。
