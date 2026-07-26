# 人类级跨 OS 操作执行层技术方案

> 文档状态：`[规划]`。状态基线为 2026-07-26；完整文档导航见 [docs/README.md](README.md)。
>
> 本文是 `desktop_computer_use_technical_design.md` 的上位方案：目标不是只做 macOS 受控桌面工具，
> 而是把 Cohort 演进为可以像人一样观察、理解和操作任意 OS GUI 的 Computer Use Agent。

## 目标

Cohort 的长期目标是：

```text
通过模拟人类观察和操作电脑，完成一切人类可以通过 GUI 完成的操作。
```

这里的“模拟人类操作”不是简单暴露 `click(x,y)`、`type(text)`、`key(cmd+s)` 这类底层原语，而是建设一层人类级 OS 操作执行层：

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
LLM 调用的是 task-level 或 guarded action tool
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
Observe -> Decide -> Act -> Verify
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
      -> human_os_observe
      -> human_os_locate
      -> human_os_act
      -> human_os_verify
      -> human_os_handoff

agent runner
  -> internal/humanos
      -> Driver interface
      -> Observation model
      -> Action planner
      -> Risk policy
      -> Coordinate converter
      -> Verification engine
      -> Audit logger

internal/humanos
  -> drivers/darwin
  -> drivers/windows
  -> drivers/linux
```

关键点：模型看到的是统一工具协议，不直接知道底层是 AX、UIA、AT-SPI、Quartz、Win32 还是 X11。

## 核心数据模型

### Observation

```go
type Observation struct {
    OS            string
    ActiveApp     string
    ActivePID     int
    ActiveWindow  WindowRef
    Windows       []WindowInfo
    ScreenshotRef string
    AXTree        []UINode
    OCR           []TextRegion
    Candidates    []TargetCandidate
    Timestamp     time.Time
}
```

### TargetCandidate

```go
type TargetCandidate struct {
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

### ActionRequest

```go
type ActionRequest struct {
    TargetID       string
    Window         WindowRef
    Action         string // click | double_click | type_text | press_key | drag | scroll
    Text           string
    Key            string
    Risk           string
    ExpectedChange string
}
```

### ActionResult

```go
type ActionResult struct {
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

### human_os_observe

获取当前 OS 状态：

```text
输入：可选 app/window 过滤条件
输出：窗口列表、当前窗口、截图引用、AX/UIA/AT-SPI 摘要、OCR 摘要
```

### human_os_locate

把自然语言目标定位成候选操作对象：

```text
输入："点击右上角保存按钮" / "找到消息输入框"
输出：候选目标列表，包含来源、置信度、坐标空间和风险提示
```

### human_os_act

执行单个受控动作：

```text
输入：target_id + action + expected_change
输出：执行结果 + 后验验证
```

约束：

- 必须引用 `human_os_locate` 或 `human_os_observe` 产生的目标。
- 必须绑定窗口。
- R2/R3 必须走确认策略。
- 失败后不能无限重试同一动作。

### human_os_verify

验证任务状态：

```text
输入：期望状态描述
输出：截图/OCR/AX/文件/命令等证据和结论
```

### human_os_handoff

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
observe
  -> locate target
  -> check target still valid
  -> activate window
  -> execute action
  -> observe again
  -> verify expected change
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

1. 重新 observe。
2. 检查窗口是否变化。
3. 检查坐标空间是否错误。
4. 检查目标是否被遮挡。
5. 换用 AX/OCR/视觉候选。
6. 最多重试有限次数。
7. 仍失败则 handoff 给用户。

## 审计与可中断

每个真实输入动作都写入 `run.log`：

```json
{
  "event": "human_os.action",
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

### M0：统一协议和 macOS 能力盘点

- 新增 `internal/humanos` 接口和数据模型。
- 把现有 macOS desktop 工具能力映射到 `observe/locate/act/verify`。
- 只支持 R0/R1。
- 完成日志和截图 artifact。

### M1：macOS 人类级操作 MVP

- 支持窗口绑定、AX 优先定位、OCR 降级定位。
- 支持 click、type_text、press_key、scroll。
- 支持单步后验验证。
- 支持 R2 ask_user 确认。

### M2：视觉候选和复杂动作

- 支持图标、按钮、输入框视觉候选。
- 支持 drag、菜单、文件选择器。
- 支持多显示器和 Retina 完整换算。
- 支持任务级动作预算和失败诊断。

### M3：Windows / Linux Driver

- Windows UI Automation + SendInput。
- Linux AT-SPI + portal / 授权输入 helper。
- 保持工具协议不变。

### M4：人类级长任务

- 与 Plan Mode 结合。
- 与 diff / run.log / replay 结合。
- 与 Skill Runtime 结合，把稳定操作流程沉淀为可复用 Skill。

## 验收标准

M1 最小验收：

- 能枚举窗口、激活窗口、截图、读取 AX 树和 OCR。
- 能在指定应用内定位按钮/输入框并执行点击或输入。
- 每个动作都有前后证据。
- 错误窗口不会误输入。
- R2 操作会停下确认。
- 连续失败不会盲目重复点击。

长期验收：

- 能通过 GUI 完成安装软件、配置系统、操作 IDE、发送消息草稿、管理文件、处理网页和原生应用等任务。
- 对不可逆、高敏感和越权操作能主动停下。
- 用户可以审计 Cohort 做过什么、为什么这么做、每一步结果是什么。

