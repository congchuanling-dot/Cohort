# Cohort 全电脑操控能力路线图

> 文档状态：`[部分完成]`。
> 目标：把 Cohort 从“具备浏览器和 macOS 受控桌面工具的本地 Agent”，推进到“可以稳定观察、理解、执行、验证大部分电脑操作的 Computer Use Agent”。
> 代码和真实端到端验收仍是最终事实来源。
> 当前进展：`cohort doctor computer` 已实现基础检查和 `--smoke-app <app_name>` 真实 App 只读 E2E，可检查 workspace/artifact 目录、macOS 权限、desktop helper、OCR helper、Chrome bridge，并对指定 App 执行窗口枚举、激活、截图、AX snapshot 和 OCR smoke。
> P1 第一批操作原语已实现：`computer_scroll`、`computer_drag`、`computer_clipboard_write`、`computer_paste`。
> P1 第二批操作原语已实现：`computer_double_click`、`computer_right_click`、`computer_window_switch`。
> P1 第三批操作原语已实现：`computer_menu`、`computer_file_dialog`、`computer_window_move`、`computer_window_resize`。
> P1 收尾原语已实现：`computer_drop`。
> P2 第一版已实现：`computer_visual_snapshot(mode=ocr/all)`，复用 `computer_see` 的 AX、OCR 和启发式 vision 候选，不返回截图内容。
> P3 第一版已实现：`computer_execute_step`，支持 `target_query -> click/type/press_key -> verify_contains_text` 的单步 OAV 执行。
> P3.1 第二版已实现：`computer_execute_plan` 接入结构化 recover policy，失败时返回刷新原因、fallback order、retry eligibility、next action 和原始失败码。
> 视觉候选第一版 detector 协议已接入：`computer_see` 通过 `heuristic_ui_detector` 消费真实截图、OCR 行和 AX 上下文，统一输出 `SourceVision` 候选和 detector 元数据。
> 下一步开发重点：接入模型/SDK 级 UI detector、多显示器坐标校准，以及更多真实 App 回归样例。

## 1. 目标定义

这里的“操控电脑的所有操作”不等于给模型暴露任意 `click(x,y)`、`type(text)`、`key(cmd+s)`。

Cohort 应该提供一层高层 Computer Use 操作协议：

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

需要把 `cohort doctor` 扩展成 Computer Use 环境诊断入口。

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

当前主要覆盖点击、双击、右键、输入、按键、等待、滚动、确认拖拽/拖放、剪贴板写入、粘贴、窗口切换、菜单选择、文件对话框路径操作、窗口移动和窗口缩放。要更接近“所有操作”，还需要：

- 多显示器坐标处理
- 更完整快捷键策略

这些能力不能直接暴露成裸坐标工具，应继续绑定 target、窗口、风险等级和验证。

`computer_clipboard_read` 暂不实现。读取剪贴板会把用户已有剪贴板内容暴露给模型，除非未来有明确的用户授权和脱敏策略。

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

第一版 `mode=ocr/all` 已完成：它读取最近 `computer_see` 的截图引用、OCR 文本候选、detector 视觉候选和可选 AX target，返回可继续操作的 `target_id`、bbox、坐标空间、置信度和风险提示，不把截图 base64 塞进模型上下文。

当前 `computer_see` 已有 detector 协议层，默认 detector 为 `heuristic_ui_detector`：它消费真实截图元数据、OCR 行与 AX target，输出统一的 `SourceVision` 候选。后续可以在同一接口下接入模型/SDK 级 UI detector。

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

当前 `computer_execute_plan` 已把恢复策略结构化为 recover policy payload：包含失败原因、是否允许自动 retry、刷新 app/title、AX/OCR/vision fallback 顺序、下一步动作和原始失败码。第一版自动恢复仍保持有界 retry，不做无限重试。

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

目标：让用户一眼知道本机是否能被 Cohort 操作。

任务：

1. 扩展 `cohort doctor computer`。
2. 检查 macOS Accessibility / Screen Recording。
3. 检查 desktop helper。
4. 检查 OCR helper 和依赖。
5. 检查 Chrome bridge。
6. 输出修复建议。
7. 增加 `cohort doctor --all` 汇总入口。

验收：

- 没有权限时能明确告诉用户去哪里开启。
- OCR 依赖缺失时能给出安装命令。
- Chrome bridge 未连接时能给出插件/启动指引。
- 权限齐全时能完成只读窗口、截图、AX、OCR smoke test。

### P1：补齐核心操作原语

目标：覆盖日常电脑操作的主要动作。

优先级：

1. `computer_scroll` `[完成：基础版]`
2. `computer_double_click` `[完成：缓存 target，R2 需确认]`
3. `computer_right_click` `[完成：缓存 target，打开上下文菜单]`
4. `computer_clipboard_write` `[完成：不读取原剪贴板内容]`
5. `computer_paste` `[完成：可选写入后粘贴，不发送]`
6. `computer_drag` `[完成：必须一次性确认]`
7. `computer_window_switch` `[完成：按 app/title/window_id 切换并更新 active state]`
8. `computer_menu` `[完成：AX 菜单路径，R2 需确认，R3 拒绝]`
9. `computer_file_dialog` `[完成：绝对路径跳转，可选确认需授权]`
10. `computer_window_move` `[完成：最新/指定窗口，受限坐标]`
11. `computer_window_resize` `[完成：最新/指定窗口，受限尺寸]`
12. `computer_drop` `[完成：source/destination target 拖放，必须一次性确认]`

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
5. `[完成：第一版协议]` `computer_see` 接入 detector 协议，默认 `heuristic_ui_detector` 消费真实截图、OCR 行和 AX 上下文。
6. 后续接入模型/SDK 级 UI detector。

当前完成：

- `computer_visual_snapshot(mode=ocr)` 返回 OCR + detector vision target。
- `computer_visual_snapshot(mode=all)` 可同时返回 AX + OCR + detector vision target。
- 支持 `query` 过滤排序。
- 只返回 screenshot artifact 引用和 bbox 元数据，不返回截图内容。

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

当前完成：

- `computer_execute_step` 支持单步 `click`、`type`、`press_key`。
- `click/type` 动作会从最近 `computer_see` 的 target cache 中按 `target_query` 选择目标。
- 动作执行继续复用 `computer_click`、`computer_type`、`computer_press`，不绕过 R2 确认和 R3 拒绝。
- 可选 `verify_contains_text` 会在动作后自动等待并验证可见文本。
- 返回 `action_outcome` 和 `verification_outcome`，方便定位失败阶段。
- `computer_execute_plan` 支持 `observe/find/click/type/press_key/wait_text/check_text` 多步计划。
- `computer_execute_plan` 每步记录 step index、action、attempt、action outcome、verification outcome。
- target 缺失、过期或未验证时，先生成结构化 recover policy，再执行一次 `computer_see` 恢复后重试。
- R2 动作会中断计划并返回 `approval_request`、`resume_from_step_index` 和已执行步骤。
- 重复点击同一 target / bbox 会直接阻断并返回 handoff outcome。

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

已完成：

```text
cohort doctor computer
computer_scroll
computer_drag
computer_drop
computer_clipboard_write
computer_paste
computer_double_click
computer_right_click
computer_window_switch
computer_menu
computer_file_dialog
computer_window_move
computer_window_resize
computer_visual_snapshot
computer_execute_step
computer_execute_plan
```

它已经覆盖：

- workspace 和 computer artifact 目录可写性。
- macOS Accessibility / Screen Recording / Input Monitoring 状态。
- desktop helper 存在性和窗口枚举。
- OCR helper 存在性和依赖探测。
- Chrome bridge server 与插件连接状态。
- 只读诊断，不执行点击、输入或系统设置修改；`--smoke-app <app>` 可对真实 App 做窗口枚举、激活、截图、AX snapshot 和 OCR smoke。
- R1 滚动只作用于最近 `computer_see` 的目标窗口。
- 拖拽必须引用缓存 target，且必须用户一次性确认。
- 拖放必须引用同一窗口内的 source/destination target，且必须用户一次性确认。
- 剪贴板只支持写入和粘贴，不读取原剪贴板内容，不回显文本。
- 双击和右键必须引用缓存 target，不暴露裸坐标。
- 窗口切换支持 app/title/window_id 匹配，并写入最新 active state。
- 菜单选择通过 AX 菜单路径执行，外部影响动作必须确认，高危动作拒绝。
- 文件对话框只支持绝对路径跳转；`confirm=true` 必须用户确认。
- 窗口移动/缩放只作用于最新或指定窗口，并限制坐标和尺寸范围。
- 视觉快照可返回 OCR/vision/AX 候选和 `target_id`，不把截图内容塞进上下文。
- `computer_see` 已接入 detector 协议，默认 `heuristic_ui_detector` 消费真实截图、OCR 行和 AX 上下文。
- 单步 OAV 执行器可按 `target_query` 找目标、执行动作，并用 `verify_contains_text` 自动验证。
- 多步 OAV 计划可串联 observe/find/action/wait/check，并在目标缺失或过期时用结构化 recover policy 做一次受控恢复。

## 6. 下一步开发计划

### P3.1：多步 OAV Action Plan `[完成：第一版]`

目标：把 `computer_execute_step` 从“单步执行器”推进到“多步计划执行器”，让模型不用手写脆弱的 `find -> click -> wait -> check` 链路。

已新增：

```text
computer_execute_plan
```

已完成第一版能力：

1. 接收结构化 steps：
   - `observe`
   - `find`
   - `click`
   - `type`
   - `press_key`
   - `wait_text`
   - `check_text`
2. 每步自动记录：
   - step index
   - action
   - target query / target id
   - before state id
   - action outcome
   - verification outcome
3. target 缺失、过期或动作未验证时，自动重新 `computer_see` 后重试一次。
4. 限制同一 target / 同一视觉 bbox 连续重复点击。
5. 任一步命中 R2 时中断并返回 `approval_request`，拿到 token 后可用 `start_step_index` + `confirmation_tokens` 从同一步继续。
6. 任一步命中 R3 时由底层工具拒绝，不继续执行后续步骤。
7. 重试后仍失败时返回 handoff outcome。

验收：

- 起草消息类流程可以表达为一个 plan：找输入框、输入草稿、检查草稿出现。
- 点击后等待结果类流程可以表达为一个 plan：找按钮、点击、等待目标文本、检查结果。
- 中途 target 过期时最多自动刷新一次，不盲目重复点击。
- 失败返回明确失败 step 和恢复建议。

### P3.2：Retry / Recover / Handoff `[部分完成：recover policy 已结构化]`

目标：让 OAV 执行器在失败时有受控恢复策略，而不是让模型直接重试同一个动作。

开发项：

1. 增加 retry policy：
   - 每个 step 默认最多 1 次自动恢复。
   - 不允许连续点击同一坐标。
   - 不允许连续发送/提交类动作。
2. 增加 recover policy：
   - `[完成]` 重新 `computer_see`。
   - `[完成]` 对同一 query 重新 rank target。
   - `[完成：policy payload]` 明确 AX/OCR/vision fallback order。
   - `[完成：policy payload]` OCR/vision target 失败时要求重新观察或交还用户。
3. 增加 `computer_handoff` 或在 execute plan 中返回 handoff outcome：
   - 当前窗口。
   - 最后一步。
   - 失败原因。
   - 建议用户手动完成的动作。

验收：

- target 不存在、窗口切走、UI 更新后，执行器能停止并说明卡在哪。
- 不出现盲目循环点击。
- 高风险操作不会被 retry 机制绕过确认。

### P2.1：真实 UI Detector 接入

目标：补足 `computer_visual_snapshot` 第一版只靠 OCR 和启发式 vision 的缺口。

建议顺序：

1. `[完成：第一版]` 先定义 detector 输出协议，不急着绑定具体模型：
   - `label`
   - `role`
   - `bbox`
   - `confidence`
   - `source=vision`
   - `coordinate_space=screenshot-local`
2. `[完成：第一版]` `computer_see` 接入默认 detector，输出 detector 元数据并写入 target cache。
3. 增加 `computer_visual_snapshot(mode=ui_detect)` 或等价 detector-only 视图。
4. detector 不直接授权点击，只生成可缓存 target。
5. 点击仍走 `computer_click`，由 manifest 映射坐标并执行风险确认。

验收：

- 能发现无文字图标按钮。
- 能发现常见输入框、按钮、列表项区域。
- 所有视觉 bbox 都绑定 screenshot manifest。
- 不把 detector 原图或大体积中间结果塞进模型上下文。

### P4：真实 App E2E 测试

目标：从“单元测试通过”推进到“真实桌面可复现”。

优先场景：

1. Finder：
   - 切换窗口。
   - 搜索文件。
   - 打开文件夹。
   - 文件对话框选择路径。
2. Chrome：
   - 打开页面。
   - 填写表单草稿。
   - 点击低风险按钮。
   - 等待结果文本。
3. VS Code：
   - 搜索文件。
   - 聚焦编辑器。
   - 起草文本。
4. 聊天 App：
   - 找输入框。
   - 起草消息。
   - 验证草稿。
   - 发送动作必须停下来要求确认。

验收：

- 每个场景有 SOP 文档。
- 每个场景有可重复执行的本地脚本或手工步骤。
- 失败时保存截图、state id、target id 和最后一步 outcome。

### P5：权限 Broker 和审计日志

目标：把分散在各工具里的 R0/R1/R2/R3 逻辑统一成可审计的 runtime 层。

开发项：

1. 抽象统一 permission broker。
2. 统一 confirmation token 的 operation、binding、reason。
3. 为每次 desktop/computer action 写审计事件：
   - session id
   - tool name
   - app / pid / window id
   - target id
   - risk
   - confirmation token id
   - before / after artifact
   - verification result
4. 不记录敏感正文，不记录剪贴板原内容。

验收：

- 出问题时能回放“模型为什么做了这个动作”。
- R2/R3 策略不会被新工具绕过。
- 审计日志不泄露输入正文、剪贴板内容或 secure field。

### P6：跨 OS Driver

目标：在 macOS 可靠后，再扩 Windows / Linux。

顺序：

1. 先固化 `desktop.Driver` / `computer_*` 上层协议。
2. Windows：
   - UI Automation。
   - Win32 input。
   - 截图和 OCR。
   - UAC 边界。
3. Linux：
   - AT-SPI。
   - X11 / Wayland portal。
   - 截图和输入权限。

跨 OS 不应早于 P3/P4。现在优先把 macOS 的 OAV 和真实 App E2E 做稳。
