# Computer Use 跨 OS 操作层技术方案

> 文档状态：`[规划]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 本文是 `desktop_computer_use_technical_design.md` 的上位方案：目标不是只做 macOS 受控桌面工具，
> 而是把 Cohort 演进为可以像人一样观察、理解和操作任意 OS GUI 的 Computer Use Agent。
> 文件名仍保留 `human_os_operation_technical_design.md`，但工具命名和开发口径统一改为 `computer_*`。

## 目标

Cohort 的长期目标是：

```text
通过模拟人类观察和操作电脑，完成一切人类可以通过 GUI 完成的操作。
```

这里的“模拟人类操作”不是简单暴露 `click(x,y)`、`type(text)`、`key(cmd+s)` 这类底层原语，而是建设一层 Computer Use 操作层：

```text
观察屏幕
  -> 理解当前应用和任务状态
  -> 定位可操作目标
  -> 生成动作计划
  -> 执行键盘/鼠标/文本输入
  -> 验证结果是否符合预期
  -> 必要时回滚、暂停或询问用户
```

最终能力边界应覆盖：

- 任意桌面应用：IDE、终端、浏览器、飞书、微信、系统设置、文件管理器、设计软件等。
- 任意 OS：macOS 优先，Windows 和 Linux 通过同一 driver interface 扩展。
- 任意 UI 形态：原生控件、WebView、自绘 UI、Canvas、图片按钮、菜单栏、系统弹窗。
- 任意输入方式：鼠标移动、点击、拖拽、滚动、快捷键、文本输入、剪贴板、输入法确认。
- 任意后验验证：截图差异、OCR、AX/UIA/AT-SPI 状态、窗口焦点、文件系统或命令结果。

## 核心判断

这项能力必须做，而且它是 Cohort 追上 Claude Code / GenericAgent 使用体验后的下一阶段形态。

但实现方式必须分层，不能让模型直接拿到底层键鼠自由操作：

```text
LLM 不直接调用 raw mouse/keyboard
LLM 调用的是 computer_* 高层工具
runtime 负责窗口绑定、坐标换算、风险判断、执行节流、结果验证和审计
```

这样既能达成“像人一样操作一切 GUI”的目标，又不会把系统变成不可控的自动点击器。

## 非目标

- 不绕过登录、验证码、人机校验、支付确认、审批确认、系统安全提示。
- 不读取应用私有数据库、cookie、token 或缓存来绕过 UI 权限。
- 不默认后台静默执行高风险操作。
- 不把跨平台细节泄漏给模型，让模型猜 AX/UIA/AT-SPI/坐标换算。
- 不在第一阶段追求全平台全能力；先把协议和 macOS 参考实现打稳。

## 设计原则

### 1. 人类操作闭环，而不是裸键鼠

每次真实输入都必须进入闭环：

```text
computer_see -> computer_find -> computer_click/type/press -> computer_check
```

工具层返回的不只是“点击成功”，还要返回：

- 点击前目标窗口和元素。
- 动作风险等级。
- 实际输入坐标或按键。
- 点击后截图或结构化状态。
- 验证结论。
- 是否需要用户接管。

### 2. 目标窗口绑定

所有副作用动作都必须绑定窗口身份：

```text
target:
  os: darwin | windows | linux
  app_name: ...
  pid: ...
  window_id: ...
  window_title: ...
```

执行前必须确认：

- 目标窗口仍存在。
- 目标窗口已激活。
- 当前前台窗口与请求一致。
- 坐标属于目标窗口边界内。

禁止裸 `click(x,y)`。

### 3. 多通道观察

不同 UI 要用不同观察源互相校验：

| 观察源 | macOS | Windows | Linux | 用途 |
| --- | --- | --- | --- | --- |
| 截图 | Quartz / screencapture | DXGI / Win32 screenshot | X11 / Wayland portal | 最通用视觉状态 |
| 可访问性树 | AX | UI Automation | AT-SPI | 语义控件、按钮、文本框 |
| OCR | Vision / tesseract / OCR service | OCR engine | OCR engine | 识别图片文字 |
| UI detector | 视觉模型 | 视觉模型 | 视觉模型 | 图标、按钮、输入框候选 |
| 应用状态 | ps / window server | Win32 process/window | wmctrl / compositor | 窗口、进程、焦点 |

优先级：

```text
结构化可访问性树
  -> OCR / UI detector
  -> 纯坐标视觉推断
```

### 4. 坐标空间显式化

任何 bbox 和坐标都必须标注坐标空间：

| 坐标空间 | 含义 |
| --- | --- |
| `screen_physical` | 屏幕物理像素坐标，真实鼠标输入使用 |
| `screen_logical` | OS 逻辑点，常见于 macOS AX |
| `window_physical` | 窗口内物理像素坐标 |
| `window_logical` | 窗口内逻辑坐标 |
| `screenshot_local` | 当前截图图片内坐标 |

模型只选择目标，不负责坐标换算。

### 5. 风险分级

动作必须按风险分级：

| 等级 | 示例 | 策略 |
| --- | --- | --- |
| R0 只读 | 截图、窗口列表、OCR、AX 树 | 直接允许 |
| R1 可逆输入 | 聚焦输入框、输入草稿、滚动、选择文本 | 默认允许，记录日志 |
| R2 外部副作用 | 发送消息、提交表单、保存文件、安装扩展 | 用户确认或策略授权 |
| R3 高风险 | 支付、删除、审批、授权、发布、泄露敏感信息 | 默认拒绝或强确认 |

这一层和 MCP/Skill 的权限模型应复用同一套 policy 思路。

## 总体架构

```text
LLM
  -> agent runner
      -> computer_see
      -> computer_find
      -> computer_click
      -> computer_type
      -> computer_press
      -> computer_check
      -> computer_wait
      -> computer_handoff

agent runner
  -> internal/computeruse
      -> Driver interface
      -> screen/window state model
      -> target cache
      -> Risk policy
      -> Coordinate converter
      -> Verification engine
      -> Audit logger

internal/computeruse
  -> drivers/darwin
  -> drivers/windows
  -> drivers/linux
```

关键点：模型看到的是统一工具协议，不直接知道底层是 AX、UIA、AT-SPI、Quartz、Win32 还是 X11。

## 核心数据模型

### ComputerState

```go
type ComputerState struct {
    OS            string
    ActiveApp     string
    ActivePID     int
    ActiveWindow  WindowRef
    Windows       []WindowInfo
    ScreenshotRef string
    AXTree        []UINode
    OCR           []TextRegion
    Candidates    []ComputerTarget
    Timestamp     time.Time
}
```

### ComputerTarget

```go
type ComputerTarget struct {
    ID              string
    Label           string
    Role            string
    Confidence      float64
    Source          string // ax | ocr | vision | heuristic
    Bounds          Rect
    CoordinateSpace string
    Window          WindowRef
    SuggestedAction string
}
```

### ComputerAction

```go
type ComputerAction struct {
    TargetID       string
    Window         WindowRef
    Action         string // click | double_click | type_text | press_key | drag | scroll
    Text           string
    Key            string
    Risk           string
    ExpectedChange string
}
```

### ComputerActionResult

```go
type ComputerActionResult struct {
    Status            string
    ExecutedAction    string
    BeforeScreenshot  string
    AfterScreenshot   string
    Verification      VerificationResult
    AuditID           string
    NeedsUserDecision bool
}
```

## 工具协议

### computer_see

看当前电脑状态。它是所有 GUI 操作的起点。

```text
输入：可选 app/window 过滤条件
输出：窗口列表、当前窗口、截图引用、AX/UIA/AT-SPI 摘要、OCR 摘要
```

第一版内部复用：

```text
desktop_windows
desktop_activate
desktop_screenshot
desktop_ax_snapshot
desktop_ocr
```

### computer_find

在上一次 `computer_see` 的结果里找目标。

```text
输入："点击右上角保存按钮" / "找到消息输入框"
输出：target_id 列表，包含 label、role、source、bounds、window、confidence、suggested_action、risk_hint
```

`target_id` 是关键。模型不应该再拼 `pid/node_id/image_path/bbox` 这种低层参数，runtime 要缓存映射关系：

```text
target_id
  -> pid / window_id
  -> ax node 或 screenshot bbox
  -> source: ax | ocr | vision | heuristic
  -> coordinate_space
  -> risk_hint
  -> created_at / expires_at
```

### computer_click

点击一个 `target_id`。

```text
输入：target_id + reason + expected_change
输出：点击结果 + 后验验证
```

内部决策顺序：

```text
AXPress
  -> AX 节点中心点击
  -> screenshot manifest + OCR/UI bbox 视觉点击
```

约束：

- 必须引用 `computer_find` 或 `computer_see` 产生的 `target_id`。
- 必须绑定窗口。
- R2/R3 必须走确认策略。
- 失败后不能无限重试同一动作。

### computer_type

向输入目标输入文字。默认语义是“起草”，不是发送。

```text
输入：target_id 或 current_focus + text + reason
输出：输入结果，不回显完整 text，只返回长度、行数、焦点验证方式
```

约束：

- 只能输入到可编辑控件或已验证的视觉焦点。
- 不承担发送、提交、确认。
- 发送必须拆成 `computer_press` 或 `computer_click`，并按风险确认。

### computer_press

按键或快捷键。

```text
输入：key + reason + 可选 intent
输出：按键结果 + 前台窗口验证
```

第一版只封装当前 `desktop_press_key` 的白名单：

```text
Escape / Tab / Shift+Tab / Arrow* / PageUp / PageDown / Home / End
Enter / Cmd+Enter / Ctrl+Enter / Delete / Backspace
```

其中发送、提交、删除类按键必须确认。

### computer_check

验证任务状态：

```text
输入：期望状态描述
输出：截图/OCR/AX/文件/命令等证据和结论
```

示例：

```text
computer_check("消息输入框中已有草稿")
computer_check("刚才的消息已出现在会话气泡里")
computer_check("保存按钮消失，文件名出现在窗口标题里")
```

### computer_wait

等待界面变化。

```text
输入：等待窗口/文字/控件/截图变化 + timeout
输出：命中证据或超时原因
```

用于替代模型盲目 sleep。

### computer_handoff

把控制权交还用户：

```text
场景：登录、验证码、支付、审批、系统权限、模型无法确认的高风险操作
输出：需要用户完成什么，完成后如何继续观察
```

## OS Driver 设计

### macOS Driver

优先实现，复用现有桌面能力：

- Quartz 窗口枚举和截图。
- Accessibility AX 树。
- AXPress / AXSetValue。
- CGEvent 鼠标、键盘、滚动。
- Retina 坐标换算。
- 权限检查和 doctor 提示。

### Windows Driver

后续实现：

- Win32 EnumWindows / GetForegroundWindow。
- UI Automation 控件树。
- SendInput 键鼠输入。
- DXGI 或 BitBlt 截图。
- DPI awareness 和多显示器坐标换算。

### Linux Driver

后续实现：

- AT-SPI 控件树。
- X11 / Wayland portal 截图。
- xdotool / ydotool / compositor API 输入。
- Wayland 下优先走 portal 或用户显式授权 helper。

## 执行策略

### 单步动作策略

```text
computer_see
  -> computer_find
  -> 检查 target_id 仍有效
  -> 激活目标窗口
  -> computer_click / computer_type / computer_press
  -> computer_check
```

### 多步任务策略

复杂任务必须生成短计划：

```text
1. 打开目标应用
2. 定位目标区域
3. 输入或点击
4. 验证状态
5. 遇到 R2/R3 停下来确认
```

计划不需要一开始完美，但每一步都必须有可观测证据。

### 失败处理

失败时按顺序处理：

1. 重新 `computer_see`。
2. 检查窗口是否变化。
3. 检查坐标空间是否错误。
4. 检查目标是否被遮挡。
5. 换用 AX/OCR/视觉候选。
6. 最多重试有限次数。
7. 仍失败则 `computer_handoff` 给用户。

## 审计与可中断

每个真实输入动作都写入 `run.log`：

```json
{
  "event": "computer.action",
  "risk": "R1",
  "app": "Finder",
  "window": "Downloads",
  "action": "click",
  "target": "Open button",
  "coordinate_space": "screen_physical",
  "before": "artifacts/before.png",
  "after": "artifacts/after.png",
  "verification": "button disappeared, file picker closed"
}
```

执行期间必须支持：

- 用户中断。
- 最大动作数。
- 最大连续失败数。
- R2/R3 确认。
- 动作回放和诊断。

## 实施阶段

### M0：统一 `computer_*` 协议和 target cache

- 新增 `internal/computeruse` 数据模型：`ComputerState`、`ComputerTarget`、`ComputerAction`、`ComputerActionResult`。
- 新增 target cache：把 `target_id` 绑定到 `pid/window/node/bbox/screenshot_manifest/risk/source`。
- 新增 `computer_see`，内部复用 `desktop_windows`、`desktop_activate`、`desktop_screenshot`、`desktop_ax_snapshot`、`desktop_ocr`。
- 新增 `computer_find`，从 AX/OCR 结果中返回可操作 `target_id`。
- 完成 `run.log` 事件和截图 artifact。

### M1：macOS 起草消息 MVP

- 新增 `computer_click`，内部复用 `desktop_ax_press`、`desktop_click`、`desktop_visual_click`。
- 新增 `computer_type`，内部复用 `desktop_ax_focus` 和 `desktop_type_text`。
- 支持“微信/豆包输入框定位 -> 聚焦 -> 起草文本 -> 验证草稿存在”。
- 此阶段不自动发送消息。

### M2：确认后发送和结果检查

- 新增 `computer_press`，内部复用 `desktop_press_key`。
- 新增 `computer_check`，用 AX/OCR/截图验证结果。
- 新增 `computer_wait`，等待窗口、文字、控件或截图变化。
- 支持“用户确认后发送消息，并验证消息出现在会话里”。

### M3：视觉候选和复杂动作

- 新增 `computer_visual_snapshot` 或把 `computer_see` 扩展为 AX + OCR + UI detector 候选融合。
- 支持图标、按钮、输入框视觉候选。
- 支持 drag、菜单、文件选择器。
- 支持多显示器和 Retina 完整换算。
- 支持任务级动作预算和失败诊断。

### M4：Windows / Linux Driver

- Windows UI Automation + SendInput。
- Linux AT-SPI + portal / 授权输入 helper。
- 保持工具协议不变。

### M5：长任务和 Skill 沉淀

- 与 Plan Mode 结合。
- 与 diff / run.log / replay 结合。
- 与 Skill Runtime 结合，把稳定操作流程沉淀为可复用 Skill。

## 下一步开发顺序

第一批编码不要碰 Windows/Linux，也不要先上视觉模型。目标是把现有 macOS `desktop_*` 能力包装成更好用的 Computer Use 工具。

| 顺序 | 开发项 | 目标文件 | 完成条件 |
| --- | --- | --- | --- |
| 1 | `internal/computeruse` 数据模型 | `internal/computeruse/types.go`、`internal/computeruse/cache.go` | 定义 `ComputerState`、`ComputerTarget`、`ComputerActionResult`，并能缓存、过期和查询 `target_id`。 |
| 2 | `computer_see` | `internal/tools/computer_see.go` | 能按 app/window 过滤，返回窗口、截图、AX 摘要、OCR 摘要和 artifact 引用。 |
| 3 | `computer_find` | `internal/tools/computer_find.go` | 能从最近一次 `computer_see` 结果中找到“消息输入框/发送按钮/指定文字”，返回稳定 `target_id`。 |
| 4 | `computer_click` | `internal/tools/computer_actions.go` | 能点击 `target_id`，优先 AXPress，失败再走受控坐标点击；窗口变化或 target 过期时拒绝。 |
| 5 | `computer_type` | `internal/tools/computer_actions.go` | 能向输入框起草文本，不发送，不在结果中回显完整敏感文本。 |
| 6 | `computer_check` | `internal/tools/computer_check.go` | 能用 AX/OCR/截图验证“草稿已出现”等状态。 |
| 7 | `computer_press`、`computer_wait` | `internal/tools/computer_actions.go`、`internal/tools/computer_wait.go` | 能在用户确认后发送，并等待/验证消息出现在会话中。 |

第一条端到端验收链路：

```text
computer_see(app="微信")
-> computer_find("消息输入框")
-> computer_click(target_id)
-> computer_type(target_id, "草稿内容")
-> computer_check("输入框中已有草稿")
-> 用户确认发送
-> computer_press("Enter" 或 "Cmd+Enter")
-> computer_wait("新消息出现在会话中")
-> computer_check("消息已发送")
```

## 验收标准

M0 最小验收：

- `computer_see(app="微信")` 能返回目标窗口、截图、AX/OCR 摘要。
- `computer_find("消息输入框")` 能返回 `target_id`，并说明来源是 AX 还是 OCR。
- `target_id` 过期、窗口变化或 PID 不一致时必须拒绝继续使用。

M1 最小验收：

- 能在微信或豆包里定位输入框。
- 能点击或聚焦输入框。
- 能输入草稿文本。
- `computer_check("输入框中已有草稿")` 能给出证据。
- 不会自动发送。

M2 最小验收：

- 发送动作必须要求确认。
- 用户确认后能用 `computer_press` 或 `computer_click` 发送。
- 能验证消息出现在会话里。
- 错误窗口不会误输入。
- 连续失败不会盲目重复点击。

长期验收：

- 能通过 GUI 完成安装软件、配置系统、操作 IDE、发送消息草稿、管理文件、处理网页和原生应用等任务。
- 对不可逆、高敏感和越权操作能主动停下。
- 用户可以审计 Cohort 做过什么、为什么这么做、每一步结果是什么。
