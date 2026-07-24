# 通用桌面端 Computer Use 技术方案

## 背景

Cohert 当前已经具备文件、命令、长期记忆、SOP、Chrome 浏览器桥接、DOM 摘要、浏览器截图和 `browser_ocr` 等能力。浏览器场景内的主链路已经比较清晰：

```text
DOM / browser_scan / browser_dom_summary
  -> browser_screenshot
  -> browser_ocr
  -> browser_click_element / browser_type_element / browser_press_key
```

但这条链路只覆盖 Chrome 内部页面。真实电脑操作需要面对任意桌面应用、系统弹窗、原生菜单、文件选择器、聊天工具、IDE、终端、设置页等对象。这类目标通常没有浏览器 DOM，也不能依赖 CDP，因此需要补齐一套通用桌面端感知与受控输入能力。

本方案目标是把 Cohert 从“浏览器 Agent”扩展为“通用 Computer Use Agent”的基础框架。它参考 GenericAgent 的桌面操作实践，但不做微信专属实现，也不直接开放任意坐标点击能力。

## 目标

- 提供通用桌面应用发现、激活、截图、OCR、AX 控件树读取能力。
- 优先通过 Accessibility / AXElements 获取语义控件，视觉 OCR 作为降级路径。
- 所有副作用操作前必须激活目标应用窗口，避免输入落到错误窗口。
- 点击、文本输入、按键等真实输入必须走受限工具，并在必要场景通过 `ask_user` 二次确认。
- 坐标系显式化，避免把截图局部坐标误当成屏幕物理坐标。
- 保持 Go 工具层稳定，把 macOS 底层操作隔离到 Python helper。

## 非目标

- 不做微信、飞书、Chrome、IDE 等单一应用的专属自动化工具。
- 不读取应用数据库、缓存文件或私有存储绕过 UI 权限。
- 不默认绕过系统权限、验证码、登录、人机校验、支付、审批等安全边界。
- 不把任意底层键鼠 API 直接暴露给模型。
- 第一阶段不追求跨平台完整实现，优先实现 macOS，接口预留 Windows / Linux 扩展。

## GA 调研结论

GenericAgent 的桌面操作能力主要来自 `memory/macljqCtrl.py`、`memory/ljqCtrl.py`、`memory/ui_detect.py`、`memory/ocr_utils.py` 以及 `memory/computer_use.md` 中的 SOP 约束。

可借鉴结论：

- 真实电脑操作的第一步不是点击，而是窗口枚举和目标确认。
- 对桌面应用执行动作前，必须先按 PID 激活目标应用。
- macOS 上优先使用 Accessibility 读取 AX 控件树，能用 `AXPress` 就不要先走坐标点击。
- OCR 和 UI detector 适合处理图像化文字、图标候选、不可读控件，但它们输出的是截图局部坐标。
- macOS Retina 下 AX 返回逻辑点，屏幕截图和真实鼠标输入通常使用物理像素，需要明确换算。
- 物理点击后要验证状态变化。像素变化接近 0 时应停止诊断，不能反复盲点同一坐标。
- 视觉链路优先级应为：

```text
窗口枚举
  -> PID 激活
  -> AX 控件树
  -> 窗口截图
  -> OCR / UI detector
  -> 坐标换算
  -> 受控真实输入
  -> 截图或状态验证
```

不建议照搬：

- 不把 Python helper 变成模型可任意调用的脚本执行入口。
- 不让模型直接拿底层 `Click(x,y)`、`TypeText`、`Press` 自由组合。
- 不把视觉检测结果直接当动作指令，必须经过目标窗口、坐标空间和风险确认。

## 设计原则

### 1. AX 优先，OCR 降级

桌面应用的结构化控件信息比截图 OCR 更稳定。工具选择顺序应是：

```text
desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot / desktop_ocr
  -> desktop_click / desktop_type_text
```

只有在 AX 控件树不可用、控件无文本、内容是图片/Canvas/自绘 UI 时，才进入 OCR 或视觉候选。

### 2. 读写分离

M1 阶段只做只读感知，不做真实输入：

```text
desktop_permissions
desktop_windows
desktop_activate
desktop_screenshot
desktop_ax_snapshot
desktop_ocr
```

M2 阶段再增加受限输入：

```text
desktop_ax_press
desktop_click
desktop_press_key
desktop_type_text
```

这样可以先验证权限、坐标、窗口、AX 树、截图和 OCR 的可靠性，再开放副作用动作。

### 3. 坐标空间显式化

所有带 bbox 或坐标的结果必须返回 `coordinate_space`：

| 坐标空间 | 含义 | 用途 |
| --- | --- | --- |
| `screenshot-local` | 当前截图内坐标，原点是截图左上角 | OCR / UI detector 输出 |
| `window-logical` | macOS AX 逻辑点，原点是窗口或屏幕逻辑坐标 | AX 元素位置 |
| `window-physical` | 窗口物理像素坐标 | 窗口内截图和点击换算 |
| `screen-physical` | 屏幕物理像素坐标 | 真实鼠标输入 |

工具不能让模型自己猜坐标换算。需要由 Go 工具层或 Python helper 返回转换后的候选点。

### 4. 激活窗口前置

任何真实输入动作都必须绑定目标窗口：

```text
app_name / pid / window_id
  -> desktop_activate
  -> verify active pid/window
  -> input action
  -> verify result
```

禁止裸 `click(x,y)`，禁止在未确认前台窗口的情况下输入文本或按快捷键。

### 5. 副作用动作受控

以下动作必须在工具层保留 `ask_user` 或等价确认逻辑：

- 发送消息、提交表单、确认支付、审批、删除、发布、授权。
- 对外部应用产生不可逆影响的点击。
- 粘贴或输入敏感信息。
- 自动处理验证码、人机校验或登录确认。

普通只读动作如窗口列表、截图、AX 快照、OCR 不需要确认。

## 总体架构

```text
LLM
  -> internal/tools
      -> desktop_permissions
      -> desktop_windows
      -> desktop_activate
      -> desktop_screenshot
      -> desktop_ax_snapshot
      -> desktop_ocr
      -> desktop_ax_press
      -> desktop_click
      -> desktop_press_key
      -> desktop_type_text

internal/tools
  -> internal/desktop
      -> Driver interface
      -> request / response structs
      -> coordinate conversion helpers
      -> risk policy helpers

internal/desktop
  -> scripts/desktop_darwin.py
      -> PyObjC / Quartz / Accessibility
      -> JSON stdin/stdout protocol
      -> structured error code

scripts/desktop_darwin.py
  -> macOS Accessibility
  -> Quartz CGWindowList / CGEvent
  -> screencapture / CGImage
  -> AXUIElement tree
```

Go 侧负责工具边界、参数校验、workspace 文件安全、错误码、上下文裁剪和测试。Python 侧只负责调用系统 API，不持有 Agent 业务逻辑。

## 模块设计

### internal/desktop

建议新增目录：

```text
internal/desktop/
  driver.go
  types.go
  darwin_runner.go
  errors.go
  coordinates.go
  policy.go
  driver_test.go
```

核心接口：

```go
type Driver interface {
    Permissions(ctx context.Context) (PermissionsResult, error)
    ListWindows(ctx context.Context, req ListWindowsRequest) (ListWindowsResult, error)
    Activate(ctx context.Context, req ActivateRequest) (ActivateResult, error)
    Screenshot(ctx context.Context, req ScreenshotRequest) (ScreenshotResult, error)
    AXSnapshot(ctx context.Context, req AXSnapshotRequest) (AXSnapshotResult, error)
    OCR(ctx context.Context, req OCRRequest) (OCRResult, error)
    AXPress(ctx context.Context, req AXPressRequest) (ActionResult, error)
    Click(ctx context.Context, req ClickRequest) (ActionResult, error)
    PressKey(ctx context.Context, req PressKeyRequest) (ActionResult, error)
    TypeText(ctx context.Context, req TypeTextRequest) (ActionResult, error)
}
```

第一阶段可以只实现前 6 个只读/低副作用方法，输入方法先保留接口或延后添加。

### scripts/desktop_darwin.py

建议采用单脚本多命令 JSON 协议：

```bash
python3 scripts/desktop_darwin.py --command list_windows --json '{"name":"Chrome"}'
```

返回统一结构：

```json
{
  "status": "success",
  "data": {}
}
```

错误结构：

```json
{
  "status": "error",
  "code": "desktop_permission_denied",
  "message": "Accessibility permission is not granted",
  "hint": "Open System Settings -> Privacy & Security -> Accessibility and allow the terminal app."
}
```

Python helper 需要支持：

- 检查 Accessibility 权限。
- 枚举窗口：应用名、PID、窗口标题、bounds、是否可见、是否前台。
- 按 PID 激活应用。
- 获取窗口截图并写入 workspace 下文件。
- 获取 AX 控件树，限制深度和节点数量。
- 后续支持 AXPress、CGEvent 点击、按键和文本输入。

## 工具设计

### desktop_permissions

用途：检查 macOS 权限和运行环境。

参数：

```json
{}
```

返回：

```json
{
  "status": "success",
  "platform": "darwin",
  "accessibility": true,
  "screen_recording": true,
  "input_monitoring": false,
  "missing": [],
  "hints": []
}
```

验收标准：

- 权限缺失时返回结构化错误和人工操作指引。
- 不隐式引导安装或修改系统设置。

### desktop_windows

用途：枚举可见桌面窗口，定位目标应用。

参数：

```json
{
  "app_name": "可选，按应用名过滤",
  "title": "可选，按标题过滤",
  "limit": 50
}
```

返回：

```json
{
  "status": "success",
  "windows": [
    {
      "window_id": "12345",
      "pid": 888,
      "app_name": "WeChat",
      "title": "文件传输助手",
      "bounds": {"x": 100, "y": 80, "width": 900, "height": 700},
      "coordinate_space": "screen-physical",
      "is_visible": true,
      "is_active": false
    }
  ]
}
```

### desktop_activate

用途：激活目标应用或窗口。

参数：

```json
{
  "pid": 888,
  "window_id": "可选"
}
```

返回：

```json
{
  "status": "success",
  "pid": 888,
  "active": true,
  "verified": true
}
```

要求：

- 优先按 PID 激活。
- 激活后再次读取前台应用验证。
- 失败时不能继续执行输入动作。

### desktop_screenshot

用途：截取目标窗口或全屏，落盘到 workspace。

参数：

```json
{
  "pid": 888,
  "window_id": "可选",
  "scope": "window",
  "max_width": 1600
}
```

返回：

```json
{
  "status": "success",
  "image_path": ".cohert/desktop/screenshots/20260725-xxxx.png",
  "width": 1200,
  "height": 900,
  "window": {
    "pid": 888,
    "window_id": "12345",
    "bounds": {"x": 100, "y": 80, "width": 1200, "height": 900},
    "coordinate_space": "screen-physical"
  },
  "coordinate_space": "screenshot-local"
}
```

### desktop_ax_snapshot

用途：读取目标应用 AX 控件树。

参数：

```json
{
  "pid": 888,
  "max_depth": 8,
  "max_nodes": 300,
  "include_zero_size": false
}
```

返回：

```json
{
  "status": "success",
  "pid": 888,
  "root": {
    "id": "ax:0",
    "role": "AXWindow",
    "title": "窗口标题",
    "value": "",
    "description": "",
    "enabled": true,
    "bounds": {"x": 100, "y": 80, "width": 900, "height": 700},
    "coordinate_space": "screen-physical",
    "actions": ["AXPress"],
    "children": []
  },
  "truncated": false
}
```

要求：

- 默认过滤零尺寸节点。
- 限制深度和节点数，避免上下文爆炸。
- password / secure text field 不返回真实值。
- 返回节点 `id`，后续 `desktop_ax_press` 只能按节点 id 操作。

### desktop_ocr

用途：对 `desktop_screenshot` 产物做 OCR。

参数：

```json
{
  "image_path": ".cohert/desktop/screenshots/20260725-xxxx.png",
  "min_confidence": 0.5,
  "max_lines": 80,
  "enhance": false
}
```

返回：

```json
{
  "status": "success",
  "image_path": "...",
  "coordinate_space": "screenshot-local",
  "width": 1200,
  "height": 900,
  "text": "识别出的文本",
  "lines": [
    {
      "index": 1,
      "text": "确认",
      "confidence": 0.96,
      "bbox": [100, 200, 160, 232],
      "center": {"x": 130, "y": 216}
    }
  ]
}
```

实现上应复用现有 `internal/vision` OCR runner 和 `scripts/browser_ocr.py` 的 RapidOCR 能力，避免维护两套 OCR 逻辑。桌面工具只负责提供图片来源和坐标上下文。

### desktop_ax_press

M2.1 已实现。用途：对当前 AX 节点执行受控语义点击。

参数：

```json
{
  "pid": 888,
  "node_id": "ax:0/3/2",
  "expected_role": "AXButton",
  "expected_title": "展开",
  "expected_description": "",
  "reason": "展开左侧导航栏",
  "confirmation_token": "R2 时由 ask_user 返回"
}
```

要求：

- 执行前验证 PID 仍为前台应用。
- 先在 Go 层重新读取 AX 快照，验证节点路径、role、title、description、enabled 和 `AXPress` action；helper 在执行前再做一次相同验证。
- R1 可恢复动作直接执行；R2 外部副作用必须消费 `ask_user` 为同一 `pid + node_id + reason` 签发的一次性 `confirmation_token`；R3 高风险动作拒绝自动执行。
- 动作后强制重新读取 AX 快照。树或目标节点状态没有可观察变化时返回 `desktop_ax_press_unverified`，不能盲目重试。
- AXPress 不可用时不降级为物理点击；`desktop_click` 仍等待独立坐标验证方案。

### desktop_click

M2 阶段工具。用途：受控物理点击。

参数：

```json
{
  "pid": 888,
  "point": {"x": 640, "y": 420},
  "coordinate_space": "screen-physical",
  "reason": "点击已识别的确认按钮",
  "confirm": true
}
```

要求：

- 不接受 `screenshot-local` 坐标直接点击。
- 必须绑定 PID。
- 执行前激活并验证前台窗口。
- 执行后截图或状态验证。
- 同一坐标失败后不允许盲目重试。

### desktop_type_text / desktop_press_key

M2 阶段工具。用途：真实键盘输入。

要求：

- 必须绑定 PID。
- 输入前确认焦点窗口。
- 敏感字段不在日志中明文输出。
- 对发送、提交、确认类快捷键需要用户确认。

## 执行链路

### 只读感知链路

```text
desktop_permissions
  -> desktop_windows(app_name/title)
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> desktop_screenshot(pid)
  -> desktop_ocr(image_path)
```

适用任务：

- 查看某个桌面应用当前显示了什么。
- 找到窗口中的按钮、输入框、菜单项。
- 判断某个文本是否出现在原生应用界面中。

### AX 语义操作链路

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_ax_snapshot(pid)
  -> select node_id
  -> ask_user if needed
  -> desktop_ax_press(pid, node_id)
  -> desktop_ax_snapshot / desktop_screenshot verify
```

适用任务：

- 点击原生按钮。
- 切换 tab。
- 打开菜单。
- 对明确 AX 节点执行动作。

### 视觉降级操作链路

```text
desktop_windows
  -> desktop_activate(pid)
  -> desktop_screenshot(pid)
  -> desktop_ocr(image_path)
  -> convert screenshot-local bbox to screen-physical point
  -> ask_user if needed
  -> desktop_click(pid, screen-physical point)
  -> desktop_screenshot verify
```

适用任务：

- 自绘 UI 没有 AX 节点。
- 图片化按钮。
- AX 树无法读取但截图可识别。

## 安全策略

工具层需要内置风险分类：

| 风险等级 | 示例 | 策略 |
| --- | --- | --- |
| R0 只读 | 窗口列表、截图、OCR、AX 快照 | 允许直接执行 |
| R1 可恢复操作 | 聚焦窗口、打开菜单、切换 tab | 可直接执行，需验证 |
| R2 外部副作用 | 发送消息、提交表单、上传文件、保存修改 | 必须 `ask_user` |
| R3 高风险 | 支付、审批、删除、授权、登录、人机校验 | 默认拒绝或要求用户手动完成 |

副作用工具需要在参数中带 `reason`，工具结果中带 `verified`。模型不能把“看起来像按钮”作为充分理由直接点击。

## 测试与验收

### 单元测试

- `internal/desktop` JSON 协议解析。
- 坐标空间转换。
- helper 错误码映射。
- AX 节点裁剪和敏感字段过滤。
- workspace 截图路径安全。
- 输入工具风险策略。

建议测试命令：

```bash
go test ./internal/desktop ./internal/tools -run 'Test.*Desktop' -count=1
```

### helper 测试

```bash
python3 -m py_compile scripts/desktop_darwin.py
python3 scripts/desktop_darwin.py --command permissions --json '{}'
python3 scripts/desktop_darwin.py --command list_windows --json '{"limit":5}'
```

### 手工验收

M1 验收：

- 能列出当前桌面窗口。
- 能按 PID 激活目标窗口。
- 能截取目标窗口并保存到 workspace。
- 能读取目标应用 AX 树。
- 能对窗口截图执行 OCR，并返回截图局部 bbox。
- 权限缺失时返回可执行的人类指引。

M2.1 验收：

- 能对当前语义匹配、enabled 且支持 `AXPress` 的节点执行 `AXPress`。
- R2 操作只能使用 `ask_user` 为同一动作签发的一次性确认令牌。
- R3 操作拒绝自动执行。
- AXPress 前后有 AX 快照对比；无可观察变化时必须停止。

## 里程碑

### M1：通用桌面只读感知

状态：已实现并完成 macOS helper 冒烟验证。

交付：

- `internal/desktop` 基础接口和 runner。
- `scripts/desktop_darwin.py` 支持 permissions / list_windows / activate / screenshot / ax_snapshot。
- 工具注册：
  - `desktop_permissions`
  - `desktop_windows`
  - `desktop_activate`
  - `desktop_screenshot`
  - `desktop_ax_snapshot`
  - `desktop_ocr`
- `sops/desktop_sop.md`：桌面操作 SOP。
- 单元测试和 macOS 手工测试文档。

### M2：受限真实输入

状态：M2.1 已完成 `desktop_ax_press`、风险确认和 AX 结果验证；坐标点击与键盘输入尚未开始。

交付：

- `desktop_ax_press`（已完成）
- `desktop_click`
- `desktop_press_key`
- `desktop_type_text`
- 风险分类和 `ask_user` 确认策略。（AXPress 已完成）
- 动作后验证机制。（AXPress 已完成）

### M3：视觉控件候选

交付：

- `desktop_visual_snapshot`
- 复用 OCR + UI detector，输出 text/icon/button 候选。
- bbox 从 `screenshot-local` 到 `screen-physical` 的受控转换。
- 视觉候选和 AX 节点的合并排序。

## 与现有 Cohert 能力的关系

- `browser_ocr` 继续服务浏览器截图，不承担桌面窗口枚举和输入。
- `desktop_ocr` 复用 OCR runner，但输入来源是桌面截图。
- `browser_sop.md` 仍管理 Chrome DOM/CDP/OCR 链路。
- 新增 `desktop_sop.md` 管理通用 Computer Use 链路。
- `docs/browser_ocr_real_input_fallback_design.md` 可保留为浏览器 fallback 方案，本文作为更上层的通用桌面方案。

## 推荐实施顺序

1. 先实现 `desktop_permissions` 和 `desktop_windows`，验证权限和窗口枚举可靠性。
2. 实现 `desktop_activate`，强制后续动作都绑定 PID。
3. 实现 `desktop_screenshot`，明确截图保存路径和坐标空间。
4. 实现 `desktop_ax_snapshot`，优先建立 AX 语义感知能力。
5. 接入 `desktop_ocr`，复用现有 OCR runner。
6. 补 `sops/desktop_sop.md`，把 AX 优先、OCR 降级、输入确认、坐标空间写成执行规则。
7. 完成 M1 验收后，再进入 `desktop_ax_press` 和 `desktop_click`。

## 关键结论

Cohert 后续不应该开发“微信工具”或某个应用的专属自动化，而应该开发一套通用桌面感知和受控输入底座。正确的第一步是 M1 只读感知：权限检查、窗口枚举、PID 激活、窗口截图、AX 快照和桌面 OCR。等这套链路稳定后，再开放受限输入，并且所有副作用动作都必须绑定目标 PID、经过风险判断和结果验证。
