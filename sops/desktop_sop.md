# Desktop Computer Use SOP

## 触发场景

- 用户要求查看、定位或理解 macOS 桌面应用、原生窗口、系统弹窗、文件选择器或 IDE 界面。
- 浏览器 DOM/CDP 无法覆盖目标，且目标位于真实桌面应用。
- 需要读取桌面截图、Accessibility 控件树或桌面 OCR。

## M1 能力边界

当前只允许桌面感知和前台激活：

```text
desktop_permissions
desktop_windows
desktop_activate
desktop_screenshot
desktop_ax_snapshot
desktop_ocr
```

当前没有 `desktop_click`、`desktop_type_text`、`desktop_press_key` 或 `desktop_ax_press`。不得使用 `code_run`、AppleScript 或自写脚本绕过该边界模拟真实输入。

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

## 坐标纪律

- `desktop_windows` 和 `desktop_ax_snapshot` 的 bounds 使用 `screen-physical`。
- `desktop_screenshot` 返回的图片内部坐标，以及 `desktop_ocr` 的 bbox，使用 `screenshot-local`。
- 不得把 `screenshot-local` 坐标直接当作系统屏幕坐标。
- M1 没有任何真实输入工具，因此 OCR 只能用于理解界面，不能转化为鼠标点击。

## 敏感信息与安全

- AX 快照不会返回 secure text field 的真实值；不得通过其他方式读取密码或敏感输入。
- 登录验证码、人机校验、支付、审批、删除、发送消息等外部副作用均不自动处理。
- 后续 M2 增加真实输入时，必须先绑定 PID、验证前台应用、按风险调用 `ask_user`，并在动作后复查截图或 AX 状态。

## 验收标准

- 能列出目标窗口并获得 PID。
- 激活工具返回 `active=true` 和 `verified=true`。
- 截图保存到 workspace，工具只返回路径和尺寸，不返回图片 base64。
- AX 快照受到深度和节点数限制，secure 文本不泄露。
- OCR bbox 明确标为 `screenshot-local`。

## 常见坑

- macOS Retina 下 AX 逻辑点与截图/真实输入物理像素不同；工具输出已转换，模型不应手工换算。
- 只按应用名匹配可能误中同厂商应用，后续操作必须使用窗口枚举返回的 PID。
- 窗口可能在探测后退出或重建；失败时重新 `desktop_windows`，不要沿用旧 PID 或旧窗口 ID。
- AX 不可用不等于应用不存在；先改走截图/OCR，而不是反复请求同一 AX 快照。
