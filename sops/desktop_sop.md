# Desktop Computer Use SOP

## 触发场景

- 用户要求查看、定位或理解 macOS 桌面应用、原生窗口、系统弹窗、文件选择器或 IDE 界面。
- 浏览器 DOM/CDP 无法覆盖目标，且目标位于真实桌面应用。
- 需要读取桌面截图、Accessibility 控件树或桌面 OCR。

## 当前能力边界

当前桌面工具：

```text
desktop_permissions
desktop_windows
desktop_activate
desktop_screenshot
desktop_ax_snapshot
desktop_ocr
desktop_ax_press
desktop_press_key
```

当前仅允许两类真实输入：`desktop_ax_press` 对刚刚读取、仍支持 `AXPress` 的语义节点执行动作；`desktop_press_key` 只发送 allowlist 中的受限按键。当前没有 `desktop_click` 或 `desktop_type_text`。不得使用 `code_run`、AppleScript 或自写脚本绕过该边界模拟真实输入。

## 默认探测流程

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> desktop_screenshot(pid, window_id)
  -> desktop_ocr(image_path)
```

- 先检查权限；缺少屏幕录制或辅助功能权限时，返回工具提供的人类授权指引，不自动修改系统设置。
- 先从 `desktop_windows` 获取 PID 和窗口 ID；不凭应用名猜 PID。
- 读取或截取目标前，先 `desktop_activate(pid)` 并确认 `verified=true`。
- 优先使用 `desktop_ax_snapshot` 读取语义控件树；只有 AX 无法读取、自绘 UI 或图片文字时才使用截图/OCR。
- 进入未知界面时只做探测。读完窗口、AX 或截图的实际结果后，再决定下一步。

## AXPress 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> 选择 node_id 并复制 role/title/description
  -> 风险判断
  -> desktop_ax_press
  -> 自动 AXSnapshot 验证
```

- 只能使用紧邻 `desktop_ax_snapshot` 返回的 `node_id`、`role`、`title`、`description`；即使 title 或 description 为空，也必须原样传入空字符串。
- 工具会再次读取 AX 树，并验证 PID 前台状态、节点路径、节点语义、enabled 状态和 `AXPress` action。任一项变化都必须停止并重新探测。
- R1 可恢复操作（例如明确的展开、收起、菜单、tab）可直接执行。
- R2 外部副作用（发送、提交、上传、保存、发布、关闭或语义不明确的按钮）首次会返回 `desktop_action_confirmation_required`。
  模型必须调用 `ask_user`，并将工具返回的 `approval_request` 原样传入 `approval`。用户明确回答“确认”后，`ask_user` 返回一次性 `confirmation_token`；只能用同一 `pid`、`node_id`、`reason` 和该令牌重试一次。
- R3 高风险（支付、审批、授权、登录验证、删除等）由 `desktop_ax_press` 直接拒绝，要求用户手动完成。
- 工具会在 AXPress 前后读取 AX 快照。没有可观察状态变化时返回 `desktop_ax_press_unverified`，不得重复点击。

## PressKey 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_press_key(pid, key, reason)
  -> 工具验证前后台 PID
```

- 低风险导航键可直接执行：`Escape`、`Tab`、`Shift+Tab`、方向键、`PageUp`、`PageDown`、`Home`、`End`。
- 外部副作用按键必须先确认：`Enter`、`Cmd+Enter`、`Ctrl+Enter`、`Delete`、`Backspace`、`Cmd+Backspace`、`Ctrl+Backspace`。
- 确认流程与 AXPress 一致：先调用 `desktop_press_key`，收到 `desktop_action_confirmation_required` 后，把 `approval_request` 原样传给 `ask_user`；只能用同一 `pid`、`key`、`reason` 和一次性令牌重试一次。
- 不支持任意快捷键，不支持字符输入；需要文本输入时等待 `desktop_type_text`，不得用反复按键模拟输入。

## 坐标纪律

- `desktop_windows` 和 `desktop_ax_snapshot` 的 bounds 使用 `screen-physical`。
- `desktop_screenshot` 返回的图片内部坐标，以及 `desktop_ocr` 的 bbox，使用 `screenshot-local`。
- 不得把 `screenshot-local` 坐标直接当作系统屏幕坐标。
- OCR 只能用于理解界面，不能转化为鼠标点击；M2.1 不提供 `desktop_click`。

## 敏感信息与安全

- AX 快照不会返回 secure text field 的真实值；不得通过其他方式读取密码或敏感输入。
- 登录验证码、人机校验、支付、审批、删除、发送消息等外部副作用均不自动处理。
- `desktop_ax_press` 已绑定 PID、前台验证、风险确认和动作后 AX 验证。
- `desktop_press_key` 只做受限按键，绑定 PID 并验证按键前后目标仍为前台。后续坐标点击和文本输入仍需单独评审。

## 验收标准

- 能列出目标窗口并获得 PID。
- 激活工具返回 `active=true` 和 `verified=true`。
- 截图保存到 workspace，工具只返回路径和尺寸，不返回图片 base64。
- AX 快照受到深度和节点数限制，secure 文本不泄露。
- OCR bbox 明确标为 `screenshot-local`。
- AXPress 只能执行当前语义匹配、enabled 且支持 `AXPress` 的节点；R2 需要一次性确认令牌，R3 被拒绝。
- PressKey 只接受 allowlist 按键；R2 按键需要一次性确认令牌。

## 常见坑

- macOS Retina 下 AX 逻辑点与截图/真实输入物理像素不同；工具输出已转换，模型不应手工换算。
- 只按应用名匹配可能误中同厂商应用，后续操作必须使用窗口枚举返回的 PID。
- 窗口可能在探测后退出或重建；失败时重新 `desktop_windows`，不要沿用旧 PID 或旧窗口 ID。
- AX 不可用不等于应用不存在；先改走截图/OCR，而不是反复请求同一 AX 快照。
