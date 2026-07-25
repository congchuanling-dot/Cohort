# 浏览器 OCR 与系统级真实输入兜底技术方案

> 文档状态：`[部分完成]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> DOM 摘要、浏览器 OCR、macOS 受控桌面输入和视觉焦点令牌已经落地。通用 UI detector、
> browser_visual_snapshot 高级模式、Windows helper 和视觉候选系统仍是规划。

## 背景

Cohert 当前浏览器主链路已经是：

```text
browser_open / browser_tabs
  -> browser_wait_for_load / browser_wait_for_stable / browser_wait_for_selector / browser_wait_for_text / browser_wait_for_url
  -> browser_scan / browser_execute_js / browser_snapshot
  -> browser_click_element / browser_type_element / browser_press_key
  -> browser_screenshot
```

底层由 `internal/browser.Bridge` 通过本机 WebSocket 连接 `assert/cohert_browser_bridge` Chrome MV3 插件。插件侧用 `chrome.scripting` 读 DOM，用 `chrome.debugger` 发送 CDP 输入事件和截图命令。普通网页读取和交互不需要 OCR，也不应该默认走系统鼠标键盘。

本方案只补齐两类兜底能力：

1. OCR/视觉定位：当 DOM 文本不可用、内容在 canvas/image/PDF 预览/验证码说明图中，基于截图识别文字和候选区域。
2. 系统级真实输入 fallback：当 DOM、JS、CDP 输入都无法完成，且确实需要像用户一样操作前台 Chrome 窗口时，通过 OS 鼠标键盘执行最后一步。

更完整的 GenericAgent 功能调研与 Cohert 可借鉴清单见 [GenericAgent 调研与 Cohert 可借鉴能力清单](file:///Users/bytedance/Desktop/myOwnProject/Cohort/docs/genericagent_borrowing_research.md)。本文只展开其中“浏览器视觉和真实输入 fallback”这一条技术路线。

## GA 调研结论

GenericAgent 的相关能力分散在 `memory/` 下，核心取舍如下：

- 浏览器主链路不是 Selenium/Playwright，而是真实 Chrome 扩展桥，保留用户登录态。
- 网页自动化优先级是 `DOM/JS -> CDP -> OCR/系统输入`，OCR 不是普通网页主路径。
- `simphtml.py` 会把真实 DOM 压缩成低噪声 HTML：过滤脚本/样式/广告，保留表单值、同源 iframe、open shadowRoot 和固定弹窗。这说明 Cohert 在进入 OCR 前还可以先补一层 `browser_dom_summary`。
- GA 插件侧的 `web_execute_js` 支持 JSON 命令路由到 `cdp/cookies/tabs/management/contentSettings/batch`。Cohert 已有 CDP 高层工具，但 cookies、extension management、contentSettings、移除 CSP 等高权限能力不应默认照搬。
- GA 的 `batch` 能复用一次 debugger attach，并支持 `$N.path` 引用前序结果；Cohert 可借鉴到 `browser_execute_js` 的内部 JSON 路由，但应继续隐藏底层 CDP。
- OCR 使用 `rapidocr-onnxruntime`，返回全文、行文本和 bbox。无文本时返回空结果，不应当做异常。
- UI 视觉检测使用 OmniParser/YOLO + RapidOCR，输出 `bbox/type/label/confidence`；该输出自带 OCR，不需要再单独跑 OCR。
- 系统输入必须先枚举窗口、激活目标窗口、使用物理像素坐标，动作后用像素变化或页面状态验证。
- macOS 侧推荐 `macljqCtrl`：`ListWindows`、`ActivateApp(pid)`、`GrabWindow`、`GrabScreen(bbox)`、`CropToScreen`、`Click`、`Press`、`TypeText`、`AXElements`、`AXClick`。
- Windows 侧推荐 `ljqCtrl`：用 `ClientToScreen(hwnd, (0,0)) / dpi_scale + bbox中心` 转物理坐标，禁止直接用 `GetWindowRect` 加截图坐标。
- 禁止盲目重试同一坐标。点击后像素变化约 0% 时，应停下诊断坐标、前台窗口或遮挡问题。

## 和 GA 的取舍边界

Cohert 当前的 Go 结构比 GA 的 Python 原型更强调工具边界、测试和权限收敛，因此应“借鉴路径，不复制权限面”。

建议借鉴：

- 真实 Chrome 会话桥，而不是另起隔离浏览器。
- DOM/JS 优先，CDP 动作第二，OCR/系统输入最后。
- `simphtml` 式低噪声 DOM/表单摘要，减少过早截图。
- `batch` 式浏览器内部复合动作，减少反复 attach debugger。
- OCR/OS input 作为 helper，通过 JSON 协议和 Go 工具隔离。
- 点击后验证：页面状态、截图像素变化、焦点窗口一致性。

不建议默认借鉴：

- cookies 读取/写入。
- extension management。
- 默认移除 CSP。
- 默认放开 Chrome `contentSettings`。
- 让模型直接调用任意底层 CDP。

这些能力不是不能做，而是只能放在开发诊断模式或用户明确授权的专项工具里，不能进入普通 browser fallback 链路。

## 设计原则

- DOM 优先：普通页面查询、表单填写、按钮点击继续走 `browser_scan`、`browser_snapshot`、高层 CDP 工具。
- DOM 摘要优先于 OCR：当 `browser_scan` 不够但 DOM 仍可访问时，应先尝试 `browser_dom_summary`，而不是直接截图 OCR。
- OCR 是读图兜底，不是动作工具。OCR 只产生文本和 bbox，动作仍由高层 browser 工具或系统输入工具执行。
- 系统输入是最后兜底。只有 CDP 失败、页面拒绝调试输入、Chrome 原生弹窗/扩展弹窗/文件选择器等不在页面 DOM 内的目标，才升级。
- 坐标系显式化。所有工具结果必须标注坐标空间：viewport、screenshot-local、window-physical、screen-physical。
- 工具结果节制。OCR 返回摘要、候选行和 bbox；截图路径落盘，不把 base64 或完整图片塞进模型上下文。
- 安全优先。验证码登录、支付、审批、敏感账号操作不自动绕过，必要时走 `ask_user`。
- 计划和验证前置：一旦使用 `browser_real_input`，必须先说明 CDP/DOM 为什么失败，执行后必须验证状态变化；同一坐标失败不能重复盲点。

## 总体架构

```text
LLM
  -> browser_scan / browser_snapshot / browser_dom_summary
      -> DOM/表单/iframe/shadowRoot 低噪声摘要
  -> browser_screenshot
      -> Chrome 插件 CDP Page.captureScreenshot
      -> workspace/.cohert/screenshots/*.png
  -> browser_ocr
      -> OCR backend
      -> text / lines / bbox / coordinate_space=screenshot-local
  -> browser_visual_snapshot
      -> OCR + UI detector
      -> candidates / bbox / labels / confidence
  -> browser_click / browser_click_element / browser_type_element
      -> 优先 CDP viewport 坐标
  -> os_input_* fallback
      -> WindowLocator
      -> OSA/AX/macljqCtrl 或 Win32/ljqCtrl
      -> screen-physical 鼠标键盘
      -> 截图/页面状态验证
```

建议保持两条边界：

- `internal/browser` 继续只表达浏览器桥能力，不直接依赖 OCR 或 OS GUI 库。
- `internal/tools` 新增工具组合能力，必要时调用 OCR helper 或 OS input helper。

## 前置增强：browser_dom_summary

### 目标

在进入截图/OCR 前，先补一层更强的 DOM 摘要能力。它借鉴 GA `simphtml.py` 的思路，把真实页面结构压缩成模型可读、低噪声、可操作的摘要。

这一步的价值是减少误用 OCR：

```text
browser_scan 只看到正文文本
browser_snapshot 只看到可交互元素
browser_dom_summary 看到表单值、隐藏提示、同源 iframe、open shadowRoot、固定弹窗
browser_screenshot / OCR 才处理 DOM 不可得或图像化内容
```

### 工具形态

```text
browser_dom_summary
```

参数：

```json
{
  "tab_id": "可选",
  "max_chars": 20000,
  "include_iframes": true,
  "include_shadow_dom": true,
  "include_form_values": true,
  "include_fixed_overlays": true
}
```

返回：

```json
{
  "status": "success",
  "url": "...",
  "title": "...",
  "summary": "<main>...</main>",
  "forms": [
    {
      "selector": "form#login",
      "fields": [
        {"name": "email", "type": "text", "value_present": true, "placeholder": "邮箱"}
      ]
    }
  ],
  "iframes": [{"src": "...", "same_origin": true, "included": true}],
  "shadow_roots": 3,
  "fixed_overlays": 1,
  "truncated": false
}
```

### 实现建议

第一版可以直接放在浏览器插件 JS 中实现：

- clone `document.documentElement`，不要修改真实页面。
- 移除 `script/style/meta/link/noscript/template/svg` 等高噪声节点。
- 对 `input/textarea/select` 复制当前 value、checked、selected 状态，但不要返回 password 值。
- 读取同源 iframe 的 body；跨域 iframe 只返回 src/title/尺寸。
- 读取 open shadowRoot；closed shadowRoot 无法读取则标记数量。
- 优先保留可见元素、表单、按钮、固定定位弹窗。
- 删除过长 data URL、内联样式和事件 handler。
- 超长文本按节点截断，而不是最后粗暴截断整份 HTML。

不建议第一版做：

- 解析跨域 iframe 内容。
- 返回 password、cookie、localStorage。
- 为了读 DOM 默认移除 CSP。

## 新增能力一：browser_ocr

### 目标

对 `browser_screenshot` 产物或指定图片路径做 OCR，返回低噪声文本和 bbox，供模型判断页面中 DOM 不可见的文字。

### 工具形态

```text
browser_ocr
```

参数：

```json
{
  "image_path": "workspace 内图片路径，可选；为空时先截当前浏览器视口",
  "tab_id": "可选；image_path 为空时使用",
  "full_page": false,
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
  "width": 1280,
  "height": 720,
  "text": "截断后的全文",
  "lines": [
    {
      "index": 1,
      "text": "登录",
      "confidence": 0.98,
      "bbox": [100, 200, 160, 230],
      "center": {"x": 130, "y": 215}
    }
  ],
  "truncated": false
}
```

### 实现建议

第一版不要把 OCR 引擎写进 Go 进程。原因是 Go 生态下高质量中英文 OCR 集成成本高，Cohert 当前依赖也很小。

建议新增一个受控 helper：

```text
internal/vision/ocr_runner.go
scripts/browser_ocr.py
```

Go 层职责：

- 校验 `image_path` 必须在 workspace 内。
- 需要截图时复用现有 `browser_screenshot` 流程。
- 调用 Python helper，设置超时，例如 20 秒。
- 解析 JSON 输出，按 `max_lines` 和 `max chars` 截断。
- 缺依赖时返回结构化错误和安装提示。

Python helper 职责：

- 使用 `rapidocr-onnxruntime` + PIL。
- `enhance=false` 默认关闭，避免对清晰文字造成伤害。
- 兼容 OCR 无结果时返回空列表。
- 输出 bbox 时统一成 `[x1,y1,x2,y2]`，不暴露 RapidOCR 原始四点结构给模型。

依赖策略：

```text
pip install rapidocr-onnxruntime pillow numpy
```

不要把依赖隐式装进工具执行过程；缺依赖时明确提示用户。

## 新增能力二：browser_visual_snapshot

### 目标

在 OCR 不足以定位图标、无文字按钮、canvas UI 时，返回视觉候选元素。它是 `browser_snapshot` 的视觉补充，不替代 DOM snapshot。

### 工具形态

```text
browser_visual_snapshot
```

参数：

```json
{
  "image_path": "可选；为空时先截当前视口",
  "tab_id": "可选",
  "mode": "ocr|ui_detect",
  "max_elements": 80
}
```

返回：

```json
{
  "status": "success",
  "coordinate_space": "screenshot-local",
  "image_path": "...",
  "elements": [
    {
      "index": 1,
      "type": "text",
      "label": "继续",
      "confidence": 0.96,
      "bbox": [420, 500, 490, 536],
      "center": {"x": 455, "y": 518}
    }
  ],
  "truncated": false
}
```

### 实现建议

第一版可以只实现 `mode=ocr`，把 `browser_ocr` 的行结果包装成视觉 snapshot。`mode=ui_detect` 作为 P2，因为 OmniParser/YOLO 权重和启动 daemon 会明显增加安装成本。

如果后续接入 UI detector：

- helper 输出沿用 GA `ui_detect.py` 的 `bbox/type/label/confidence`。
- 权重放在 `temp/weights/icon_detect/model.pt` 或配置项指定路径。
- 缺权重时只返回明确错误，不自动联网下载。
- 文档说明 `ui_detect` 已包含 OCR，不要重复跑 `browser_ocr`。

## 新增能力三：系统级真实输入 fallback

### 目标

给浏览器自动化提供最后兜底：操作页面外的 Chrome 原生 UI、系统文件选择器、插件弹窗、被页面反自动化逻辑拒绝的控件。

这不是普通网页点击工具，不应在默认交互链路里优先出现。

### 工具边界

建议新增独立包：

```text
internal/osinput
```

职责：

- 枚举窗口。
- 激活窗口。
- 截取窗口或局部区域。
- 坐标转换。
- 鼠标点击、按键、文本输入。
- 动作后验证。

`internal/browser` 不依赖 `internal/osinput`。工具层可以编排二者。

### 最小工具集

```text
os_window_list
os_window_activate
os_screenshot_window
os_click
os_press_key
os_type_text
```

为了降低误用，第一版也可以只公开一个高层浏览器兜底工具：

```text
browser_real_input
```

参数：

```json
{
  "action": "click|press_key|type_text",
  "reason": "为什么 DOM/JS/CDP 不可用",
  "tab_id": "可选，用于先定位 Chrome tab",
  "window_id": "可选，来自 os_window_list",
  "coordinate_space": "screenshot-local|screen-physical",
  "image_path": "当 coordinate_space=screenshot-local 时必填",
  "bbox": [100, 200, 160, 230],
  "x": 130,
  "y": 215,
  "key": "Enter",
  "text": "输入内容",
  "verify": true
}
```

返回：

```json
{
  "status": "success",
  "window_id": "12345",
  "app": "Google Chrome",
  "title": "...",
  "coordinate_space": "screen-physical",
  "clicked_at": {"x": 1820, "y": 960},
  "pixel_change_percent": 3.2,
  "focused_window_changed": false,
  "verification": "pixel_changed"
}
```

### macOS 实现路线

当前开发环境是 macOS。第一版优先支持 macOS，Windows 只设计接口。

macOS helper 可用 Python + PyObjC，参考 GA `macljqCtrl.py`：

- `check_permissions()`：检测辅助功能与屏幕录制授权。
- `ListWindows()`：返回 `id/app/title/bbox/pid`，bbox 使用物理像素。
- `ActivateApp(pid)`：用 pid 精确激活，不按应用名子串误伤。
- `GrabWindow(window_id)` / `GrabScreen(bbox)`：截图使用物理像素。
- `CropToScreen(bbox, px, py)`：把截图内坐标转屏幕物理坐标。
- `Click(x,y,check=true)`：点击后比较周边像素变化。
- `Press(cmd)` / `TypeText(text)`：按键和文本输入。
- `AXElements(pid)` / `AXClick(node)`：能走辅助功能控件树时，优先免坐标点击。

依赖：

```text
pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices pillow numpy
```

权限缺失时返回：

```json
{
  "status": "error",
  "code": "os_input_permission_missing",
  "message": "需要辅助功能或屏幕录制权限",
  "hint": "系统设置 > 隐私与安全性 > 辅助功能/屏幕录制，授权 Cohert 宿主进程后重启"
}
```

### Windows 实现路线

Windows 后续参考 GA `ljqCtrl.py`：

- 进程启动时调用 DPI aware，明确 `dpi_scale`。
- 窗口客户区截图与坐标转换必须使用 `ClientToScreen(hwnd,(0,0)) / dpi_scale`。
- 禁止用 `GetWindowRect` 或 DWM 窗口矩形直接加截图 bbox。
- 文本输入优先剪贴板粘贴或平台 helper，不假设 `TypeText` 存在。

### 坐标转换规则

必须在工具结果中显式标记坐标空间：

```text
viewport
  Chrome CDP Input.dispatchMouseEvent 坐标，来自 getBoundingClientRect。

screenshot-local
  browser_screenshot / os_screenshot_window 图片内坐标，原点是图片左上角。

screen-physical
  OS 鼠标键盘使用的物理像素坐标。

window-physical
  窗口截图对应的物理像素坐标，转 screen-physical 需要加窗口截图原点。
```

转换策略：

- `browser_click` 只接受 viewport 坐标。
- `browser_real_input` 若接收 `screenshot-local`，必须同时知道截图来源窗口或截图 bbox。
- `browser_screenshot` 是页面视口图，不包含 Chrome 顶栏；不能直接把其中 bbox 加到屏幕坐标。要做 OS 点击时，必须先定位 Chrome 内容区在屏幕上的物理原点，或者重新用 OS 截取 Chrome 窗口/客户区。
- macOS 区域截图用 `CropToScreen(bbox, x, y)` 转屏幕物理坐标。
- Windows 客户区截图用 `ClientToScreen` 转屏幕逻辑坐标，再除以 `dpi_scale` 后加截图内坐标。

## 决策流程

普通网页：

```text
browser_open
browser_wait_for_load
browser_wait_for_stable
browser_scan
browser_dom_summary  // scan/snapshot 不足但 DOM 可访问时
```

普通交互：

```text
browser_snapshot
browser_click_element / browser_type_element / browser_press_key
wait
scan / execute_js 验证
```

DOM 不可读但页面仍在浏览器内容区：

```text
browser_screenshot
browser_ocr 或 browser_visual_snapshot
如果能映射到 viewport 坐标：
  browser_click 或 browser_press_key
否则：
  browser_real_input，且必须说明 fallback reason
```

Chrome 原生 UI / 文件选择器 / 扩展弹窗：

```text
os_window_list
os_window_activate
os_screenshot_window
browser_visual_snapshot 或 browser_ocr
browser_real_input
os_screenshot_window 或 browser_scan 验证
```

必须停下并诊断的情况：

- 同一坐标点击后像素变化约 0%。
- `browser_dom_summary` 仍能读到目标控件，却直接准备使用系统输入。
- 前台窗口不是目标 Chrome/文件选择器窗口。
- OCR bbox 来自 browser viewport 截图，但工具准备按 screen-physical 点击且没有内容区原点。
- 权限缺失导致截图黑屏或输入无效。
- 涉及验证码、支付、审批、账号安全确认。

## 安全与权限

- 默认工具列表中可以公开 `browser_ocr`，但 `browser_real_input` 建议描述为最后兜底，并要求 `reason` 必填。
- `browser_real_input` 对点击/按键/文本输入都应返回动作验证结果，不能只返回 sent。
- 对系统级输入增加风险文案：该工具会控制当前桌面前台窗口。
- 对敏感场景不自动操作：验证码识别后只能提示用户介入；不实现自动过验证码。
- 不读取或返回剪贴板原内容；如需要粘贴文本，应只写入用户明确提供的文本，并在结果中说明是否使用剪贴板。
- 截图保存继续放在 workspace `.cohert/screenshots` 或 `.cohert/os_screenshots`，避免散落临时文件。

## 配置建议

新增配置段：

```yaml
vision:
  ocr:
    enabled: true
    python: python3
    timeout_seconds: 20
    max_lines: 80
  ui_detect:
    enabled: false
    model_path: temp/weights/icon_detect/model.pt
    timeout_seconds: 30

os_input:
  enabled: false
  platform: auto
  require_reason: true
  click_pixel_check: true
  max_retry_same_point: 1
```

`os_input.enabled` 默认建议为 `false`。用户明确要真实鼠标键盘兜底时再启用，避免 Agent 在普通网页任务中误用桌面输入。

## 分阶段落地

### P0：DOM 摘要与 OCR 读图

- 新增 `browser_dom_summary` 工具或先内置到插件 `scan` 的增强模式。
- 新增 OCR helper。
- 新增 `browser_ocr` 工具。
- 复用 `browser_screenshot` 落盘图片。
- 单测覆盖 DOM 摘要截断、密码字段脱敏、参数校验、workspace 路径限制、helper JSON 解析、缺依赖错误。

验收：

- 同源 iframe/open shadowRoot/表单值能被摘要看到。
- password 字段不返回明文。
- 对固定测试图片返回文字和 bbox。
- 对无文字图片返回空 `lines`，不是错误。
- 工具结果不包含 base64。

### P1：OCR 与浏览器截图闭环

- `browser_ocr` 支持未传 `image_path` 时自动截图。
- 增加 `coordinate_space=screenshot-local`。
- 更新 `sops/browser_sop.md`：DOM 读不到时才截图 OCR。

验收：

- DOM 文本为空但截图含文字时，能返回 OCR 文本。
- 普通网页 SOP 仍然优先 `browser_scan`。

### P2：macOS 系统输入 helper

- 新增 `internal/osinput` 接口。
- 新增 macOS Python helper，提供窗口枚举、激活、截图、点击、按键、输入。
- 新增权限检测错误。
- 第一版不公开细碎 OS 工具，只公开受限 `browser_real_input` 或开发态命令。

验收：

- 能枚举 Chrome 窗口并激活。
- 能截取窗口局部。
- 能点击指定物理坐标并返回像素变化。
- 权限缺失时返回明确提示。

### P3：视觉候选与坐标映射

- 新增 `browser_visual_snapshot(mode=ocr)`。
- 可选接入 `ui_detect`。
- 增加从 OS 窗口截图 bbox 到 screen-physical 的转换。

验收：

- 对截图中的文字按钮返回 bbox。
- 点击前能确认 bbox 来源和坐标空间。
- 同一错误坐标不会被重复点击。

### P4：Windows 支持

- 实现 Windows helper。
- 处理 DPI aware、ClientToScreen、客户区截图。
- 补 Windows 专项测试或手工验收文档。

## 需要修改的文件

已完成的 P0/P1 OCR 文件：

```text
internal/tools/registry.go
internal/tools/browser_tools.go
internal/tools/browser_tools_test.go
internal/vision/ocr_runner.go
internal/vision/ocr_runner_test.go
scripts/browser_ocr.py
docs/testing.md
sops/browser_sop.md
```

P2/P3：

```text
internal/osinput/types.go
internal/osinput/helper.go
internal/osinput/helper_darwin.go
scripts/osinput_darwin.py
internal/tools/browser_real_input.go
internal/tools/browser_real_input_test.go
```

可选：

```text
scripts/ui_detect.py
configs/config.yaml
internal/app/config.go
```

## 测试策略

单元测试：

- DOM 摘要过滤脚本/样式/敏感字段。
- DOM 摘要保留表单当前值、placeholder、checked/selected 状态。
- DOM 摘要对同源 iframe/open shadowRoot 的合并和跨域 iframe 的安全跳过。
- OCR helper 输出 JSON 的解析。
- 图片路径必须限制在 workspace 内。
- `max_lines`、`min_confidence`、截断逻辑。
- `browser_real_input` 参数组合校验。
- `coordinate_space` 不匹配时拒绝执行。

集成测试：

- 使用固定 fixture 图片测试 OCR。
- 使用 fake browser client 测试自动截图后 OCR。
- 使用 fake osinput client 测试窗口激活、截图、点击返回。

手工验收：

- Chrome 页面中 canvas/image 文字，DOM 读不到但 OCR 可读。
- Chrome 原生文件选择器或扩展弹窗，CDP 无法操作时能通过系统输入完成单步动作。
- macOS 未授权辅助功能/屏幕录制时，错误提示明确。

不要把系统输入测试放进默认 `go test ./...` 依赖真实桌面；真实桌面验收应单独标记。

## 风险与取舍

- Python helper 增加运行时依赖，但比把 OCR/GUI 库引入 Go 主进程更低风险。
- `browser_screenshot` 坐标不是屏幕坐标，直接用于 OS 点击会点偏；必须通过 OS 窗口截图或内容区原点映射。
- 系统输入会影响用户当前桌面，必须默认关闭或强约束使用场景。
- OCR 对图标语义不可靠，图标类按钮优先走 DOM/AX/UI detector；VLM 只能做语义辅助，不可信其坐标。
- Retina/高 DPI 是高风险点，所有平台都必须以物理坐标为最终点击坐标。

## 最终建议

`browser_dom_summary` 已作为前置增强落地。下一步先补只读 `browser_ocr`，不急着直接做系统级鼠标键盘。Cohert 当前已有 DOM、CDP、等待、截图闭环，DOM 摘要能减少过早截图，OCR 能补上“看不见文字”的缺口，风险最低。

系统级真实输入应作为第二阶段，并且默认关闭。它的价值在 Chrome 原生 UI、文件选择器、扩展弹窗和少数 CDP 被拒绝的页面，但一旦误用会破坏普通网页自动化的可控性。
