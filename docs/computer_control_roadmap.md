# Cohert 全电脑操控能力路线图

> 文档状态：`[部分完成]`。
> 目标：把 Cohert 从“具备浏览器和 macOS 受控桌面工具的本地 Agent”，推进到“可以稳定观察、理解、执行、验证大部分电脑操作的 Computer Use Agent”。
> 代码和真实端到端验收仍是最终事实来源。
> 当前进展：`cohert doctor computer` 基础版已实现，可检查 workspace/artifact 目录、macOS 权限、desktop helper、OCR helper 和 Chrome bridge。

## 1. 目标定义

这里的“操控电脑的所有操作”不等于给模型暴露任意 `click(x,y)`、`type(text)`、`key(cmd+s)`。

Cohert 应该提供一层高层 Computer Use 操作协议：

```text
observe -> find target -> act -> verify -> recover / ask user
```

模型看到的是 `computer_see/find/click/type/press/wait/check` 这类语义工具；底层才映射到浏览器 DOM/CDP、macOS Accessibility、截图、OCR、鼠标键盘和后验验证。

核心原则：

- 优先语义接口：DOM、AX、UIA、AT-SPI 等结构化能力优先。
- 坐标是最后兜底：必须绑定窗口、截图 manifest、坐标空间和 target cache。
- 每个动作都要可验证：动作前后需要证据，不能只返回 “sent/clicked”。
- 风险分级：导航和编辑草稿可直接执行；发送、提交、删除、购买、转账等必须确认；高危动作默认拒绝。
- 可恢复：失败后重新观察，换 AX/OCR/视觉路径，仍失败再交还用户。

## 2. 当前已有能力

### 2.1 浏览器

当前已有浏览器自动化基础：

- `browser_open`
- `browser_tabs`
- `browser_scan`
- `browser_dom_summary`
- `browser_execute_js`
- `browser_click`
- `browser_click_element`
- `browser_type`
- `browser_type_element`
- `browser_press_key`
- `browser_snapshot`
- `browser_wait_for_*`
- `browser_screenshot`
- `browser_ocr`

当前浏览器路线是：

```text
DOM / JS -> CDP action -> screenshot / OCR fallback
```

普通网页操作不应默认走系统鼠标键盘。OCR 主要用于 DOM 不可见的图片、canvas、PDF 预览、截图文字等场景。

### 2.2 macOS 桌面

当前已有 macOS 受控桌面能力：

- `desktop_permissions`
- `desktop_windows`
- `desktop_activate`
- `desktop_screenshot`
- `desktop_ax_snapshot`
- `desktop_ocr`
- `desktop_ax_press`
- `desktop_ax_focus`
- `desktop_click`
- `desktop_visual_click`
- `desktop_press_key`
- `desktop_type_text`

关键边界：

- 基于 macOS Accessibility / AX。
- `desktop_type_text` 只负责起草，不直接发送。
- `desktop_press_key` 使用受限按键集合。
- 视觉点击需要截图 manifest、bbox、风险分类和必要的一次性确认 token。

### 2.3 高层 computer 工具

当前已有跨工具的高层封装：

- `computer_see`
- `computer_find`
- `computer_click`
- `computer_type`
- `computer_press`
- `computer_wait`
- `computer_check`

这些工具已经开始把底层桌面能力包装成更稳定的 target cache 和动作协议，但还没有形成完整产品化闭环。

## 3. 主要缺口

### 3.1 安装后环境自检不足

需要把 `cohert doctor` 扩展成 Computer Use 环境诊断入口。

应检查：

- macOS Accessibility 权限。
- Screen Recording 权限。
- desktop helper 是否可运行。
- OCR Python 依赖是否可用。
- Chrome bridge / 插件是否连接。
- workspace、session、log、screenshot artifact 目录是否可写。
- Finder、Chrome、VS Code 等真实 app 的只读冒烟。
- 可选：一次低风险窗口激活、截图、AX snapshot、OCR smoke test。

没有这层诊断，用户装完后很难判断是权限、依赖、桥接、模型还是工具协议出了问题。

### 3.2 操作原语还不完整

当前主要覆盖点击、输入、按键和等待。要更接近“所有操作”，还需要：

- `computer_scroll`
- `computer_drag`
- `computer_drop`
- `computer_double_click`
- `computer_right_click`
- `computer_clipboard_read`
- `computer_clipboard_write`
- `computer_paste`
- `computer_menu`
- `computer_file_dialog`
- `computer_window_switch`
- `computer_window_move`
- `computer_window_resize`
- 多显示器坐标处理
- 更完整快捷键策略

这些能力不能直接暴露成裸坐标工具，应继续绑定 target、窗口、风险等级和验证。

### 3.3 视觉 UI detector 缺失

OCR 能识别文字，但对以下场景不够：

- 只有图标、无文字按钮。
- canvas / WebGL / 游戏化界面。
- 自绘桌面应用。
- 图像化列表或复杂设计工具。
- OCR 文本可读但按钮边界不明确。

需要 `computer_visual_snapshot`：

```text
AX tree
+ OCR text regions
+ UI detector candidates
+ screenshot manifest
+ target cache
=> visual candidates
```

第一版可以先做 `mode=ocr` 候选融合；第二版再接入 UI detector。

### 3.4 Observe-Act-Verify 执行器不足

现在工具已经有 `computer_see/find/click/type/press/wait/check`，但还缺一个更强的默认执行策略。

目标链路：

```text
computer_see
-> computer_find
-> computer_click / computer_type / computer_press
-> computer_check
-> computer_wait
-> computer_check
```

失败恢复策略：

1. 重新 `computer_see`。
2. 判断窗口是否变化、target 是否过期。
3. 优先换 AX 路径。
4. 再换 OCR/视觉路径。
5. 限制同一坐标重复点击。
6. 仍失败则调用 `computer_handoff` 或要求用户接管。

### 3.5 统一权限和审计不足

现有桌面动作已有一次性确认 token，但需要进一步统一成 permission broker。

风险等级建议：

| 等级 | 示例 | 策略 |
| --- | --- | --- |
| R0 只读 | 截图、窗口列表、AX/OCR 读取 | 默认允许 |
| R1 可逆操作 | 聚焦、切换窗口、打开下拉、草稿输入 | 通常允许，必要时提示 |
| R2 外部影响 | 发送消息、提交表单、保存、发布 | 必须用户确认 |
| R3 高危 | 删除、付款、转账、授权、安装未知软件 | 默认拒绝或强确认 |

每次动作应写审计事件：

- session id
- tool name
- target id
- app / pid / window
- action
- risk
- before / after artifact
- verification result
- confirmation token id，不保存敏感内容

### 3.6 跨 OS 仍未开始

当前核心是 macOS。要覆盖更多电脑，还需要：

- Windows：UI Automation、Win32 input、截图、OCR、权限和 UAC 边界。
- Linux：AT-SPI、X11、Wayland portal、截图和输入权限。
- 统一 driver 接口：macOS / Windows / Linux 都实现同一套 `computer_*` 上层协议。

跨 OS 不应先做。优先把 macOS 可靠性打稳。

## 4. 建议开发顺序

### P0：Computer Doctor + Setup

目标：让用户一眼知道本机是否能被 Cohert 操作。

任务：

1. 扩展 `cohert doctor computer`。
2. 检查 macOS Accessibility / Screen Recording。
3. 检查 desktop helper。
4. 检查 OCR helper 和依赖。
5. 检查 Chrome bridge。
6. 输出修复建议。
7. 增加 `cohert doctor --all` 汇总入口。

验收：

- 没有权限时能明确告诉用户去哪里开启。
- OCR 依赖缺失时能给出安装命令。
- Chrome bridge 未连接时能给出插件/启动指引。
- 权限齐全时能完成只读窗口、截图、AX、OCR smoke test。

### P1：补齐核心操作原语

目标：覆盖日常电脑操作的主要动作。

优先级：

1. `computer_scroll`
2. `computer_double_click`
3. `computer_right_click`
4. `computer_clipboard_write`
5. `computer_paste`
6. `computer_drag`
7. `computer_window_switch`

验收：

- 每个动作必须引用 target 或 active window。
- 每个动作必须有风险等级。
- 外部影响动作必须确认。
- 动作后必须能返回验证证据或明确说明未验证。

### P2：Computer Visual Snapshot

目标：处理 AX/OCR 不足的图标、canvas、自绘 UI。

任务：

1. 定义 `computer_visual_snapshot` 返回结构。
2. 融合 AX target、OCR line、视觉启发式候选。
3. 统一 target cache。
4. 返回候选置信度和风险提示。
5. 后续接入 UI detector。

验收：

- 能发现 OCR 文本区域并生成可点击 target。
- 能发现常见输入区域和按钮区域。
- 不把截图 base64 塞进模型上下文。
- 所有 bbox 都标注坐标空间。

### P3：Observe-Act-Verify 执行器

目标：让模型不再手写脆弱操作链，而是按稳定协议执行。

任务：

1. 定义 GUI action plan。
2. 每步动作前自动检查 target 是否过期。
3. 动作后自动 `computer_check`。
4. 失败自动 retry/recover。
5. 重复失败后 handoff。

验收：

- 发送消息类流程能做到：起草、检查、确认、发送、等待、验证。
- 不会连续盲点同一个坐标。
- 每个动作都有 before/after 证据。

### P4：真实 App E2E 测试

目标：把能力从单元测试推进到真实桌面可用。

建议场景：

- Finder：打开文件夹、搜索文件、复制路径。
- Chrome：打开页面、填表、点击、等待结果。
- VS Code：打开文件、搜索、编辑草稿。
- 飞书/微信类聊天：只起草消息，确认后发送。
- 系统设置：只读检查权限页面，不自动改高风险设置。

验收：

- 每个场景有 SOP。
- 每个场景有人工可复现步骤。
- 失败时能保存截图和日志。

## 5. 推荐下一步

基础版已完成：

```text
cohert doctor computer
```

它已经覆盖：

- workspace 和 computer artifact 目录可写性。
- macOS Accessibility / Screen Recording / Input Monitoring 状态。
- desktop helper 存在性和窗口枚举。
- OCR helper 存在性和依赖探测。
- Chrome bridge server 与插件连接状态。
- 只读诊断，不执行点击、输入或系统设置修改。

下一步进入：

```text
computer_scroll / computer_drag / computer_clipboard
```

最后再做视觉 detector 和执行器。
