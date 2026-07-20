# Browser SOP

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

动作后必须根据场景等待：

```text
browser_wait_for_load
browser_wait_for_selector
browser_wait_for_text
browser_wait_for_stable
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

## 常见坑

- JS 的 `element.click()` 和 `dispatchEvent` 是 `isTrusted=false`，敏感按钮可能拒绝。
- 导航后不能在同一段 JS 继续操作，页面上下文会销毁。
- 首次 CDP attach 可能影响坐标，点击前要重新测 rect 或先做无害预热。
- 后台标签页可能节流，必要时先让页面到前台。
- OCR 只做 DOM/CDP 都不可用时的兜底，不是普通网页主链路。
