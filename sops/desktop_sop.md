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
desktop_ax_focus
desktop_click
desktop_visual_click
desktop_press_key
desktop_type_text
```

当前仅允许六类真实输入：`desktop_ax_press` 对刚刚读取、仍支持 `AXPress` 的语义节点执行动作；`desktop_ax_focus` 只聚焦刚刚读取的可编辑 AX 节点；`desktop_click` 只点击刚刚读取并重新校验的 AX 节点中心点，不接受任意坐标；`desktop_visual_click` 只接受 `desktop_screenshot` 产物和 OCR/UI bbox，由工具读取 manifest 后转换到物理屏幕坐标；`desktop_press_key` 只发送 allowlist 中的受限按键；`desktop_type_text` 只向当前焦点可编辑输入框起草文本。不得使用 `code_run`、AppleScript 或自写脚本绕过该边界模拟真实输入。

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
- 工具执行前会自动激活目标 PID；不要依赖用户确认后终端仍不抢焦点。
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
- 不支持任意快捷键，不支持字符输入；需要文本输入时使用 `desktop_type_text`，不得用反复按键模拟输入。

## AXFocus 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> 选择可编辑 node_id 并复制 role/title/description
  -> desktop_ax_focus
  -> desktop_type_text(pid, text, reason)
```

- 只能聚焦 `AXTextField`、`AXTextArea`、`AXSearchField`、`AXComboBox` 或其他明确可编辑文本节点。
- 工具会重新读取 AX 树，验证 PID 前台、节点路径、节点语义、enabled 状态和可编辑角色。
- 工具执行前会自动激活目标 PID。
- 聚焦后必须确认 `focused=true` 和 `verified=true`，再调用 `desktop_type_text`。
- `desktop_ax_focus` 不输入文本、不发送消息，也不处理发送按钮。

## Click 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> 选择 node_id 并复制 role/title/description
  -> 风险判断
  -> desktop_click
  -> 工具验证前后台 PID
```

- `desktop_click` 是 AXPress 不可用时的受控兜底，只能点击当前 AX 节点的中心点。
- 不接受模型传入 x/y；不得从 OCR bbox、截图坐标或手工估算坐标发起点击。
- 工具执行前会自动激活目标 PID。
- 可编辑输入节点的点击视为 R1，可用于把焦点放到输入框。
- 发送、提交、上传、保存、发布、关闭或语义不明确的节点是 R2，必须走 `ask_user` 一次性确认令牌。
- 支付、审批、授权、登录验证、删除等 R3 操作直接拒绝，要求用户手动完成。

## VisualClick 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_screenshot(pid, window_id)
  -> desktop_ocr(image_path)
  -> 选择 OCR/UI bbox 和 expected_text
  -> desktop_visual_click(pid, image_path, bbox, expected_text, reason)
```

- `desktop_visual_click` 是 WebView、自绘 UI 或 AX 树无法暴露目标控件时的受控兜底。
- 只能传 `desktop_screenshot` 返回的 `image_path`/`manifest_path` 和同一张图上的 `screenshot-local` bbox；不得传裸 x/y、手工估算坐标或其他截图的 bbox。
- 工具会校验路径在 workspace 内、manifest 绑定同一 PID 和图片、bbox 位于截图尺寸内，然后把 bbox 中心换算为 `screen-physical` 坐标。
- 工具执行前会自动激活目标 PID，避免 `ask_user` 在终端确认后抢走焦点。
- 输入框、搜索框、聚焦类视觉点击可视为 R1；发送、提交、保存、发布等 R2 必须走 `ask_user`，确认令牌绑定 `pid + image_path + bbox + reason`。
- 支付、审批、授权、登录验证、删除等 R3 操作直接拒绝，要求用户手动完成。

## TypeText 操作流程

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_focus / desktop_click / desktop_visual_click 聚焦目标输入框
  -> desktop_type_text(pid, text, reason)
  -> 发送动作单独走 desktop_press_key
```

- `desktop_type_text` 只负责起草文本，不负责发送、提交或确认。
- 工具会验证目标 PID 是前台，并通过 AX 检查当前焦点是可编辑控件；焦点不可判断或不是输入框时必须停止。
- 工具执行前会自动激活目标 PID。
- 工具结果只返回 `text_length`、`line_count` 和焦点摘要，不回显完整文本。
- 单次文本长度有限制；长文本应分段输入。发送前必须让用户确认内容。

## 坐标纪律

- `desktop_windows` 和 `desktop_ax_snapshot` 的 bounds 使用 `screen-physical`。
- `desktop_screenshot` 返回的图片内部坐标，以及 `desktop_ocr` 的 bbox，使用 `screenshot-local`。
- 不得把 `screenshot-local` 坐标直接当作系统屏幕坐标。
- OCR bbox 只能交给 `desktop_visual_click`，由工具读取 manifest 后转换；`desktop_click` 只能使用当前 AX 节点，不接受 OCR bbox 或任意坐标。

## 敏感信息与安全

- AX 快照不会返回 secure text field 的真实值；不得通过其他方式读取密码或敏感输入。
- 登录验证码、人机校验、支付、审批、删除、发送消息等外部副作用均不自动处理。
- `desktop_ax_press` 已绑定 PID、前台验证、风险确认和动作后 AX 验证。
- `desktop_ax_focus` 只聚焦可编辑 AX 节点，绑定 PID、节点语义和焦点验证。
- `desktop_click` 只点击已验证 AX 节点中心点，绑定 PID、节点语义、风险确认和前台验证。
- `desktop_visual_click` 只点击 manifest 校验后的 OCR/UI bbox 中心点，绑定 PID、图片、bbox、风险确认和前台验证。
- `desktop_press_key` 只做受限按键，绑定 PID 并验证按键前后目标仍为前台。
- `desktop_type_text` 只做文本起草，绑定 PID 和焦点输入框，不负责发送。

## 验收标准

- 能列出目标窗口并获得 PID。
- 激活工具返回 `active=true` 和 `verified=true`。
- 截图保存到 workspace，工具只返回路径和尺寸，不返回图片 base64。
- 截图同时生成 sidecar manifest，视觉点击必须依赖该 manifest 做坐标换算。
- AX 快照受到深度和节点数限制，secure 文本不泄露。
- OCR bbox 明确标为 `screenshot-local`。
- AXPress 只能执行当前语义匹配、enabled 且支持 `AXPress` 的节点；R2 需要一次性确认令牌，R3 被拒绝。
- AXFocus 只能聚焦当前语义匹配、enabled 且可编辑的节点。
- Click 只能点击当前语义匹配、enabled 且有有效 bounds 的 AX 节点；R2 需要一次性确认令牌，R3 被拒绝。
- VisualClick 只能点击当前截图 manifest 校验过的 bbox；R2 需要一次性确认令牌，R3 被拒绝。
- PressKey 只接受 allowlist 按键；R2 按键需要一次性确认令牌。
- TypeText 不回显完整文本；发送动作必须拆成单独确认。

## 常见坑

- macOS Retina 下 AX 逻辑点与截图/真实输入物理像素不同；工具输出已转换，模型不应手工换算。
- 只按应用名匹配可能误中同厂商应用，后续操作必须使用窗口枚举返回的 PID。
- 窗口可能在探测后退出或重建；失败时重新 `desktop_windows`，不要沿用旧 PID 或旧窗口 ID。
- AX 不可用不等于应用不存在；先改走截图/OCR，而不是反复请求同一 AX 快照。
