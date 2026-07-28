# GenericAgent 调研与 Cohort 可借鉴能力清单

> 文档状态：`[历史]`。这是对 GenericAgent 的调研快照，不直接描述 Cohort 当前实现。
> 当前开发优先级和已实现状态请看 [docs/README.md](README.md) 与
> [development_task_breakdown.md](development_task_breakdown.md)。

## 目标

本文档基于 `/Users/bytedance/Desktop/myOwnProject/GenericAgent` 的代码和记忆/SOP 文件，整理 GenericAgent 的功能结构、关键实现思路，以及 Cohort 后续还能借鉴哪些能力。

结论先行：

- GenericAgent 的强点不是某个单点工具，而是“极小核心 + 原子工具 + SOP/helper 自举 + 记忆分层 + 浏览器真实会话 + 桌面兜底”的组合。
- Cohort 已经在 Go 架构里复刻并增强了一部分：工具注册、会话落盘、上下文管理、长期记忆候选、SOP 索引、Chrome 插件 bridge、CDP 高层动作和截图。
- Cohort 下一步最值得借鉴的是工程机制：混合工具调用兜底、事件 hook、项目模式、计划/验证模式、浏览器高级路由的取舍、OS 视觉/输入 helper、诊断命令和前端/运行日志。

## GA 总体架构

GA 的核心代码主要集中在：

```text
agent_loop.py
ga.py
llmcore.py
TMWebDriver.py
simphtml.py
assets/tmwd_cdp_bridge/
plugins/
memory/
frontends/
```

它的自我定位是：

```text
约 3K 行种子核心
9 个原子工具
约 100 行 Agent Loop
运行中通过 code_run / file / browser / memory 扩展能力
```

GA 默认工具集：

```text
code_run
file_read
file_write
file_patch
web_scan
web_execute_js
ask_user
update_working_checkpoint
start_long_term_update
```

Cohort 当前已经有更结构化的 Go 版本工具集：

```text
file_read / file_write / file_patch / code_run / ask_user
update_working_checkpoint
start_long_term_update / memory_propose_update / memory_apply_update
browser_tabs / browser_open / browser_scan / browser_execute_js
browser_dom_summary
browser_click / browser_click_element / browser_type / browser_type_element
browser_press_key / browser_snapshot
browser_wait_for_* / browser_screenshot
```

因此 Cohort 不需要照搬 GA 的工具数量，而应借鉴 GA 的“最小稳定面 + 用 SOP/helper 扩展复杂能力”的方法。

## SOP / Skill 体系对齐结论

GA 的 Skill 不是独立插件市场式对象，而是由几层资产共同形成：

```text
原子工具
  -> 工作 checkpoint
  -> memory L1/L2/L3/L4
  -> 专项 SOP / helper 脚本
  -> 反射和历史挖掘继续生成新候选
```

Cohort 当前更适合把这套体系显式化为能力等级：

| 等级 | Cohort 资产 | 对齐 GA 能力 | 说明 |
| --- | --- | --- | --- |
| C0 | Tool Registry | 9 个原子工具 | 文件、命令、浏览器、记忆等执行基座 |
| C1 | `sops/*.md` | L3 SOP | 场景化约束，全文按需读 |
| C2 | `update_working_checkpoint` | working checkpoint | 当前任务约束和进度 |
| C3 | `memory/global.md` / project memory | L2/L3 经验库 | 经过证据验证的长期 entry |
| C4 | `memory/reflection/sop_candidates.md` | 反射生成 Skill 候选 | 稳定流程先作为候选沉淀 |
| C5 | reviewed `sops/*.md` + `sops/index.md` | 可主动路由 Skill | 人工确认后进入主动索引 |

关键取舍：

- Cohort 不让每次任务自动生成正式 Skill，而是先生成 SOP candidate。
- `sops/index.md` 更新需要确认，避免错误经验变成每轮路由规则。
- SOP 文档强调触发场景、禁止事项和验收标准，避免变成长教程。
- `memory_sop.md` 作为长期记忆和 Skill 晋级的统一入口。

## 1. Agent Loop

### GA 实现

关键文件：

```text
agent_loop.py
ga.py
```

`agent_loop.py` 的核心流程很短：

```text
构造 system/user messages
  -> client.chat(messages, tools)
  -> 解析 response.tool_calls
  -> 没有工具时自动走 no_tool
  -> dispatch 到 handler.do_<tool>
  -> 收集 tool_results 和 next_prompt
  -> turn_end_callback 注入工作记忆、长期记忆提示、失败提醒
```

值得注意的点：

- `BaseHandler.dispatch` 用 `do_<tool>` 约定直接分发，简单但很灵活。
- 没有工具调用时自动触发 `no_tool`，可拦截空回复、坏格式、计划模式误完成等。
- `turn_end_callback` 是关键扩展点：更新 summary、工作记忆、轮数提醒、master 干预和 done hook。
- 工具可以返回 `StepOutcome{data,next_prompt,should_exit}`，用 next_prompt 精确控制下一轮模型输入。

### Cohort 当前状态

Cohort 的 `internal/agent/runner.go` 已有更类型安全的 Runner：

```text
Runner.history
ToolRunner interface
ToolCallContext
Outcome{Data, NextPrompt}
RunStatus
session history.jsonl
ContextManager.Build
```

优势：

- Go 类型边界更清晰。
- 工具注册和 schema 输出稳定。
- 会话落盘和 ContextManager 比 GA 原始核心更工程化。

缺口：

- 没有 GA 那种 `no_tool` 自修复层。
- 没有通用 `turn_end_callback` / hook 分发。
- 轮数触发的“必须更新 checkpoint / 停止盲试 / 汇报用户”策略还较弱。

### 建议借鉴

P0：

- 增加 `NoToolPolicy`：当模型无 tool_calls 时检查是否为空回复、是否只输出大代码块、是否在未完成任务时过早结束。
- 增加 turn 级策略：第 N 轮提醒更新 checkpoint，第 M 轮提醒重读 SOP，第 K 轮要求总结进展或询问用户。

P1：

- 把 Runner 生命周期抽象成事件：`agent_before/after`、`turn_before/after`、`llm_before/after`、`tool_before/after`。

不建议照搬：

- 不要用 Python 式动态 `do_<tool>` 分发替代当前 Tool Registry。Cohort 当前注册方式更适合 Go 测试和维护。

## 2. 工具调用协议兜底

### GA 实现

关键文件：

```text
llmcore.py
```

GA 同时支持两类模式：

- 原生工具调用：Native Claude / OpenAI tool use。
- 文本工具调用：要求模型输出 `<tool_use>{"name":"...","arguments":{...}}</tool_use>`，再由 `ToolClient._parse_mixed_response` 解析。

兜底解析包含：

- `<tool_use>` / `<tool_call>` XML 标签。
- 误输出的 JSON 对象。
- JSON 解析失败时生成 `bad_json` 工具调用，让下一轮自修复。
- 对 `file_write` 特殊处理：要求正文 `<file_content>` 承载内容，避免大内容塞进 JSON 参数。

### Cohort 当前状态

Cohort 当前主要依赖 OpenAI-compatible 原生 tool calling。`runner.go` 已能处理 tool args JSON 解析错误，并返回 `bad_json` 工具错误。

缺口：

- 如果模型不触发原生 tool_calls，但在文本里写了工具意图，Cohort 不会解析。
- 对大文件写入的协议约束主要靠 schema 和工具实现，没有类似 GA 的“正文承载大内容”协议。

### 建议借鉴

P0：

- 增加可选文本工具调用兜底解析，优先只支持严格 `<tool_use>{...}</tool_use>`。
- 当检测到“纯大代码块但未调用 file_write/code_run”时，返回下一轮提示，而不是直接结束。

P1：

- 为 `file_write` 增加“大内容正文承载”模式，或者至少在 schema/系统提示中明确：大文本优先写入文件而不是 JSON 参数。

不建议照搬：

- 不要支持太多弱格式解析。GA 的弱解析适合探索型 Python agent，但 Cohort 应保持协议面收敛，避免误解析用户普通文本。

## 3. 浏览器能力

### GA 实现

关键文件：

```text
TMWebDriver.py
simphtml.py
assets/tmwd_cdp_bridge/background.js
memory/tmwebdriver_sop.md
```

GA 浏览器链路：

```text
web_scan
  -> TMWebDriver
  -> Chrome tab session
  -> simphtml 简化 DOM/HTML

web_execute_js
  -> 页面 JS
  -> 如 script 是 JSON 命令，则路由到扩展内部
  -> CDP/cookies/tabs/management/contentSettings/batch
```

GA 的高级点：

- 保留用户真实 Chrome 会话、cookie、扩展和指纹。
- `simphtml` 会复制 DOM、过滤脚本/样式/广告/浮层、保留 input value/autofill 提示、同源 iframe 和 shadowRoot 内容。
- CDP fallback：普通 `chrome.scripting.executeScript` 因 CSP 失败时，转 `Runtime.evaluate`。
- `batch` 能复用一次 debugger attach，并支持 `$N.path` 引用前序结果。
- 扩展可操作 cookies、extension management、contentSettings、移除 CSP、下载弹窗策略。

### Cohort 当前状态

Cohort 已经有：

```text
assert/cohort_browser_bridge
internal/browser
internal/tools/browser_tools.go
browser_execute_js JSON command route
browser_cdp 内部调试能力
browser_click/type/press_key/snapshot/wait/screenshot
```

Cohort 相比 GA 更收敛：

- 没有 cookies。
- 没有 management。
- 没有 contentSettings。
- 没有 declarativeNetRequest 移除 CSP。
- `batch` 第一版没有 `$N.path` 引用。
- `browser_scan` 读文本，不做 `simphtml` 级 HTML 简化。

### 建议借鉴

P0：

- 保持当前高层 browser 工具优先，不重新暴露底层 `browser_cdp`。
- 为 `browser_execute_js` 的 JSON 路由补更强的 `batch`：支持继承 tab_id、子命令失败标记、必要时支持 `$N.path` 引用。
- 增加 `browser_scan_html` 或 `browser_dom_summary`，借鉴 `simphtml` 思路返回低噪声 HTML/表单摘要，而不只是纯文本。

P1：

- 增加 CSP fallback：当 `chrome.scripting.executeScript` 明确失败时，内部尝试 CDP `Runtime.evaluate`。
- 增加 iframe/shadow DOM 读取策略。先做同源 iframe 和 open shadowRoot，跨域 iframe 后置。
- 文件上传可借鉴 GA SOP：优先 DataTransfer API，CDP `DOM.setFileInputFiles` 只作为特定场景。

P2：

- 视真实需求增加 `contentSettings`，用于处理“下载多个文件”类 Chrome 原生阻塞。
- cookies 能力必须谨慎，默认不公开给模型，只能内部诊断或用户明确授权。

不建议照搬：

- 不建议默认移除 CSP。它权限高，且会改变目标网页安全边界。
- 不建议默认开放 extension management。它可禁用用户扩展，风险高。
- 不建议把 cookies 作为普通工具暴露。cookie 属于敏感数据。

## 4. 截图、OCR、视觉与系统输入

### GA 实现

关键文件：

```text
memory/ocr_utils.py
memory/ui_detect.py
memory/computer_use.md
memory/ljqCtrl.py
memory/ljqCtrlBg.py
memory/macljqCtrl.py
memory/ljqCtrl_sop.md
memory/vision_sop.md
memory/adb_ui.py
```

GA 的设计顺序：

```text
窗口枚举
  -> UIA/AX 控件树
  -> 窗口/局部截图
  -> ui_detect / OCR
  -> VLM 语义辅助
  -> 物理鼠标键盘
```

关键原则：

- 禁止默认全屏截图，优先窗口或局部截图。
- 坐标最终使用物理像素。
- macOS 用 `macljqCtrl`，支持 `ListWindows`、`ActivateApp(pid)`、`GrabWindow`、`Click`、`Press`、`TypeText`、`AXElements`、`AXClick`。
- Windows 用 `ljqCtrl`，必须处理 `dpi_scale` 和 `ClientToScreen`。
- `Click` 后检查像素变化，0% 变化要停下诊断。
- OCR 用 RapidOCR，ui_detect 用 YOLO + OCR。

### Cohort 当前状态

Cohort 已有 `browser_screenshot` 和 `browser_ocr`：

- `browser_ocr` 使用隔离的 Python RapidOCR helper，支持 workspace 图片或自动浏览器截图。
- 返回低噪声文字和 `screenshot-local` bbox，不会执行点击。
- `rapidocr-onnxruntime`、`pillow`、`numpy` 缺失时返回结构化错误，不会隐式安装依赖。

仍未实现 UI detector、OS window/input helper。

### 建议借鉴

已在 [browser_ocr_real_input_fallback_design.md](file:///Users/bytedance/Desktop/myOwnProject/Cohort/docs/browser_ocr_real_input_fallback_design.md) 中展开。

补充建议：

- P1 做 macOS `osinput` helper，但默认关闭。
- P2 再做 `browser_visual_snapshot` 和 Windows helper。
- VLM 只能做语义判断，不可信坐标。

不建议照搬：

- 不要把 GA 的 `macljqCtrl.py` / `ljqCtrl.py` 原样塞进 Cohort 核心。应作为 helper 脚本或可选扩展，Go 层只认稳定 JSON 协议。

## 5. 记忆与自我进化

### GA 实现

关键文件：

```text
ga.py
memory/memory_management_sop.md
memory/global_mem_insight.txt
memory/global_mem.txt
memory/*.md
plugins/project_mode.py
```

GA 分层：

```text
L1 global_mem_insight.txt：极简索引
L2 global_mem.txt：环境事实/稳定偏好
L3 memory/*.md/*.py：SOP/helper
L4 raw sessions：历史会话归档
```

核心规则：

```text
No Execution, No Memory.
```

GA 还有一个重要模式：Project Mode。

实现思路：

- 当前 agent 实例保存 `_ga_project_mode_name`。
- `plugins/project_mode.py` 在 `agent_before` hook 注入轻量 L1 指针。
- `project_memory.md` 全文不每轮注入，模型按需读取。
- 收尾时判断是否有值得写入项目记忆的信息。

### Cohort 当前状态

Cohort 已有：

- `update_working_checkpoint`
- `start_long_term_update`
- `memory_propose_update`
- `memory_apply_update`
- `contextmgr` 注入 `memory/index.md` 和相关长期记忆
- 受控证据账本 `evolution.Evidence`

这比 GA 原始实现更安全。

缺口：

- 项目模式还没有用户可操作入口。
- L4 原始会话归档/挖掘还没有。
- SOP 候选到正式 SOP 的评审流程还不完整。

### 建议借鉴

P0：

- 实现 Cohort Project Mode：`cohort project enter <name>` 或 REPL `/project <name>`。
- 每轮只注入项目记忆指针，不全文注入。
- 项目私域目录放 `workspace/memory/projects/<project_id>/`。

P1：

- 做 L4 会话归档：压缩 `history.jsonl` 和 `model_responses`，生成 `memory/raw_sessions/all_histories.md`。
- 做 `memory/reflection/sop_candidates.md -> sops/*.md` 的人工确认流程。

不建议照搬：

- 不要允许模型直接 patch 全局 memory。Cohort 现有 propose/apply 两阶段更安全，应保留。

## 6. 计划模式、验证模式与 Subagent

### GA 实现

关键文件：

```text
memory/plan_sop.md
memory/subagent.md
assets/ga_ultraplan.py
```

GA 的复杂任务流程：

```text
探索态：委托 subagent 只读探测
规划态：生成 plan.md，标记 [D]/[P]/[?]
执行态：每轮读 plan，执行第一项，完成后 patch 标记
验证态：独立验证 subagent 对抗性检查
```

特点：

- 主 agent 通过文件和 subagent 协作，减少上下文污染。
- 验证 subagent 不继承执行过程，降低确认偏误。
- `plan.md` 作为外部状态机，不依赖模型记忆。

### Cohort 当前状态

Cohort 当前没有 subagent/plan mode。已有会话恢复和文件工具，可以支撑文件状态机，但没有并行 agent 运行器。

### 建议借鉴

P1：

- 先做轻量 Plan Mode，不做 subagent：
  - `/plan start <name>` 创建 `workspace/plans/<name>/plan.md`
  - 系统提示要求每步前读 plan，完成后 patch。
  - 提供 `/plan status` 和 `/plan stop`。

P2：

- 做验证模式：
  - 不是多 agent，也可以先用同一 Runner 新建独立 session 执行验证 prompt。
  - 验证只读交付物，不继承执行历史。

P3：

- 再考虑 subagent 并行执行。Go 版需要进程/会话隔离、任务目录、结果收集和取消机制。

不建议照搬：

- GA 的 Plan SOP 强制 subagent 探索，这依赖它成熟的 agentmain/task_dir 机制。Cohort 目前不应先做重型多 agent。

## 7. Hooks 与插件

### GA 实现

关键文件：

```text
plugins/hooks.py
plugins/langfuse_tracing.py
plugins/project_mode.py
agent_loop.py
```

hook 事件：

```text
agent_before / agent_after
turn_before / turn_after
llm_before / llm_after
tool_before / tool_after
```

用途：

- Langfuse tracing。
- Project Mode 注入。
- 外部观察和调试。

### Cohort 当前状态

Cohort 暂无 hook 系统。日志主要是 raw model responses。

### 建议借鉴

P0：

- 先做内部事件接口，不做动态插件加载：

```go
type EventSink interface {
  AgentBefore(...)
  AgentAfter(...)
  TurnBefore(...)
  TurnAfter(...)
  LLMBefore(...)
  LLMAfter(...)
  ToolBefore(...)
  ToolAfter(...)
}
```

- 默认实现写入 `run.log` JSONL。

P1：

- 增加 tracing sink，例如 Langfuse/OpenTelemetry。
- Project Mode 可以通过 hook 注入实现。

不建议照搬：

- 不要一开始支持任意 Go plugin 或动态脚本插件。Go 动态插件跨平台成本高，安全边界也差。

## 8. LLM 层与多模型 fallback

### GA 实现

关键文件：

```text
llmcore.py
mykey.py / mykey.json
```

能力：

- OpenAI-compatible。
- Claude Messages。
- Native Claude tool use。
- Native OpenAI。
- 多模型 fallback：`MixinSession` 在失败时切换节点，成功后可 spring back。
- streaming parser。
- prompt caching / thinking / context management 参数适配。

### Cohort 当前状态

Cohort 只有 OpenAI-compatible client，但已有 `provider` 字段预留。

### 建议借鉴

P0：

- 支持多 OpenAI-compatible endpoint fallback：

```yaml
llm:
  profiles:
    - name: primary
      api_base: ...
      model: ...
    - name: backup
      api_base: ...
      model: ...
```

- 失败类型区分：连接失败、读超时、429、5xx、流中断、模型空响应。

P1：

- 增加 Anthropic client。
- 将 `thinking`/`reasoning_effort` 作为 provider-specific options。

不建议照搬：

- 不建议伪装 Claude Code native headers。Cohort 应走明确、可维护、合规的 provider 接口。

## 9. Context 与工具结果压缩

### GA 实现

关键文件：

```text
llmcore.py
ga.py
```

GA 通过几层降低上下文：

- `compress_history_tags` 压缩旧 `<thinking>/<tool_use>/<tool_result>`。
- `trim_messages_history` 超预算时保留前缀和最近消息。
- `ToolClient` 若 tools schema 未变化，只提示“工具仍有效”，减少重复 schema。
- `file_read`、`web_scan`、`web_execute_js` 都按上下文倍率和工具数量动态限制输出。
- `<summary>` 每轮进入 `history_info`，形成轻量工作历史。

### Cohort 当前状态

Cohort 的 `contextmgr` 更系统：

- 完整 `Runner.history` 不裁剪。
- 请求副本里做 orphan tool 清理。
- 注入长期记忆索引/相关记忆/session memory/compact。
- 70% 阈值后先压缩旧 tool result。
- 再按 message group 裁旧历史。

缺口：

- 没有 GA 的每轮 `<summary>` 压缩轨迹。
- 没有 tools schema cache 提示机制。
- 工具结果限额还可以按“同轮工具数量”动态分配。

### 建议借鉴

P0：

- 增加每轮 action summary，写入 session `memory.md` 或独立 `turn_summaries.md`。
- 多工具并行时，每个工具输出预算按 `1/tool_count` 缩小。

P1：

- 如果模型端不支持缓存，考虑 schema 简化策略；如果支持缓存，再做 provider-specific cache_control。

## 10. 文件与代码执行工具体验

### GA 实现

关键文件：

```text
ga.py
assets/code_run_header.py
```

特点：

- `code_run` Python 文件模式执行，shell/PowerShell 命令模式执行。
- 输出实时流式读取，超时 kill。
- `file_read` 支持 keyword 周边读取、行号、路径不存在时近似候选。
- `file_patch` 要求 old_content 唯一，否则拒绝。
- `file_write` 支持 `<file_content>` 正文提取、append/prepend/overwrite。

### Cohort 当前状态

Cohort 已有 `code_run`、`file_read`、`file_patch`、`file_write`，并且有路径限制和测试。

建议补充：

- `file_read` 路径不存在时给候选文件建议。
- `file_patch` 返回变更摘要和匹配上下文。
- `code_run` 支持后台任务更清晰的生命周期日志。
- 增加危险命令确认策略。

## 11. 前端与交互形态

### GA 实现

关键目录：

```text
frontends/tui_v3.py
frontends/desktop_bridge.py
frontends/desktop/
frontends/*app.py
```

GA 支持：

- TUI。
- Streamlit/Qt/桌面桥。
- Telegram/Discord/飞书/微信/QQ/钉钉等 IM。
- 多会话、导出、继续、成本统计等前端命令。

### Cohort 当前状态

Cohort 只有 CLI/REPL。

建议：

- P0：把 CLI 体验打磨好：`doctor`、`run.log`、`session resume`、`tools --json`。
- P1：做 TUI，而不是先做桌面/IM。
- P2：如果要多端，优先做稳定 JSON-RPC/HTTP local API，再挂前端。

不建议照搬：

- 不建议近期做多个 IM 前端。Cohort 当前价值在 Go 内核和工程可控性。

## 12. 自主行动与后台反射

### GA 实现

关键文件：

```text
reflect/autonomous.py
memory/autonomous_operation_sop.md
memory/scheduled_task_sop.md
```

GA 的自主行动不是无约束自动改代码，而是：

- 从 TODO/history 里选一条任务。
- 探测、实验、报告。
- 写入 `autonomous_reports/`。
- 需要人类审批的变更写报告待审。

### Cohort 当前状态

Cohort 暂无后台反射或定时任务。

建议：

- P2 之后再做。
- 先做“只读健康检查/记忆整理报告”类后台任务。
- 不允许后台任务默认改代码或装依赖。

## 优先级路线图

### P0：低风险高收益

1. `NoToolPolicy`：无工具调用时的空回复/大代码块/过早完成拦截。
2. 文本 `<tool_use>` 严格兜底解析。
3. Runner 事件接口 + JSONL `run.log`。
4. `doctor` 命令：API key、模型连通、workspace、browser bridge、Python helper 依赖。
5. `file_read` 候选路径建议。
6. `browser_ocr`：`browser_dom_summary` 已落地，下一步只补只读 OCR，不做 OS 输入。

### P1：工程能力增强

1. Project Mode：每轮注入项目记忆指针，按需读取全文。
2. 轻量 Plan Mode：文件状态机，不先做多 agent。
3. `browser_execute_js` batch 增强。
4. 多 OpenAI-compatible fallback。
5. 每轮 summary 写入 session 摘要。

### P2：复杂能力

1. macOS `osinput` helper，默认关闭。
2. 独立验证 session。
3. L4 会话归档和历史挖掘。
4. TUI。
5. tracing sink。

### P3：谨慎评估

1. Windows `osinput` helper。
2. ui_detect / VLM。
3. scheduler / autonomous reports。
4. browser contentSettings。
5. cookies/extension management，仅用户明确授权或开发诊断模式。

## 不建议 Cohort 照搬的 GA 能力

| GA 能力 | 不建议照搬原因 | Cohort 建议 |
| --- | --- | --- |
| 默认移除 CSP | 改变网页安全边界，权限高 | 只做失败时内部 CDP fallback |
| cookies 普通工具 | 敏感数据风险高 | 默认不公开，必要时用户授权 |
| extension management | 可禁用用户扩展 | 仅开发诊断模式 |
| 多 IM 前端 | 维护面大，偏离内核稳定 | 先 CLI/TUI |
| 强制 subagent plan 流 | 依赖 GA 成熟 task_dir 机制 | 先文件 Plan Mode |
| Python 动态分发工具 | Go 项目可测试性下降 | 保持 Tool Registry |
| 自动安装复杂依赖 | 不可控、跨平台风险 | `doctor` 提示，用户确认后安装 |

## 最终建议

Cohort 的路线不应是“把 GA 翻译成 Go”，而是：

```text
保留 Cohort 的 Go 类型边界、测试和安全门
借鉴 GA 的成熟操作模式和长期经验
把复杂平台能力放到 helper/SOP/可选扩展
默认工具面保持收敛
```

最近最值得做的三件事：

1. 补 `NoToolPolicy + 文本工具兜底 + run.log`，提升 Agent Loop 韧性。
2. 做 `Project Mode + 轻量 Plan Mode`，提升长任务和跨会话连续性。
3. 在已落地 `browser_dom_summary` 的基础上实现 `browser_ocr`，再谨慎推进 OS 真实输入 fallback。
