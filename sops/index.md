# SOP Index

这个文件只做导航，不写完整操作步骤。任务命中某个场景时，先读取对应 SOP，再把关键约束写入 `update_working_checkpoint`。

## 使用标准

- SOP 是执行约束，不是背景资料；命中后先读，再行动。
- 只读取和当前任务有决策关系的 SOP，不要一次性读取全部 SOP。
- 读完 SOP 后，如果采用其规则，必须用 `update_working_checkpoint` 保存关键约束和 `related_sop`。
- 多次失败、策略不清、上下文变长或切换子任务时，重读 `related_sop`。

## 能力等级

| 等级 | 名称 | 说明 |
| --- | --- | --- |
| C0 | 原子工具 | 文件、命令、浏览器、记忆等工具 schema 已注册，可直接调用 |
| C1 | SOP 约束 | `sops/*.md` 提供场景化流程、禁区和验收标准 |
| C2 | 工作记忆 | `update_working_checkpoint` 保存当前任务关键约束 |
| C3 | 长期记忆 | `memory/*` 保存经过工具验证的可复用经验 |
| C4 | SOP Candidate | `memory/reflection/sop_candidates.md` 保存待审查 Skill |
| C5 | 正式 SOP / Skill | 人工确认后进入 `sops/*.md` 和索引，成为主动路由能力 |

## L0 Meta

- SOP: `sops/meta_sop.md`
- 场景：不知道如何使用 SOP、多个 SOP 同时命中、读完 SOP 后如何提炼 checkpoint、失败后何时重读 SOP。

## Browser

- SOP: `sops/browser_sop.md`
- 场景：浏览器打开网页、等待加载、读取页面、点击、输入、CDP JSON 路由、页面变化判断、截图/OCR 兜底。

## Desktop Computer Use

- SOP: `sops/desktop_sop.md`
- 场景：macOS 桌面窗口、原生应用、系统弹窗、窗口激活、Accessibility/AX 控件树、窗口截图、桌面 OCR。

## Code Run

- SOP: `sops/code_run_sop.md`
- 场景：执行 shell/Python、启动后台服务、长生命周期进程、超时、端口监听、进程清理。

## File Edit

- SOP: `sops/file_edit_sop.md`
- 场景：读取、修改、新增、删除项目文件；处理用户未提交改动；应用 patch；改后验证。

## Context

- SOP: `sops/context_sop.md`
- 场景：上下文压缩、tool result 过长、历史裁剪、assistant tool_calls 与 tool result 配对、token 预算。

## Memory / Skill Evolution

- SOP: `sops/memory_sop.md`
- 场景：长期记忆、项目记忆、经验沉淀、final 前记忆判断、`start_long_term_update`、`memory_propose_update`、`memory_apply_update`、SOP candidate、Skill 晋级、能力等级。

## Testing

- SOP: `sops/testing_sop.md`
- 场景：Go/JS/插件/文档/后台服务改动后的验证命令和验收标准。

## Rules

- SOP 全文按需读取，不要凭印象执行。
- 读完 SOP 后，如果决定按它执行，必须用 `update_working_checkpoint` 保存关键约束和 `related_sop`。
- 多次失败、策略不清或任务超过多轮时，重读 `related_sop`。
- 有长期记忆信号时先按 `memory_sop.md` 判断是否值得沉淀；无可复用价值时明确 skip，不要硬写。
