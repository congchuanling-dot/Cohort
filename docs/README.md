# Cohort 文档导航与实现状态

> 状态基线：2026-07-26。本文档是阅读项目文档时判断“已经可用”与“仅为设计”的唯一入口。
> 代码和 `go test ./...` 是最终事实来源；设计文档不因保留方案文字而代表功能已经上线。

## 状态定义

| 标记 | 含义 |
| --- | --- |
| `[完成]` | 已有实现和对应测试或手工验收路径。 |
| `[部分完成]` | 主链路可用，但文档列出的扩展能力、真实服务验收或可靠性工作尚未全部完成。 |
| `[规划]` | 已确认方向或技术设计，尚未进入当前实现。 |
| `[历史]` | 调研、旧差距快照或开发记录；保留其决策背景，不作为当前开发状态。 |
| `[维护]` | 面向当前用户或开发者的操作文档，应随命令和行为同步更新。 |

## 当前开发路径

当前优先级以 [开发任务拆解表](development_task_breakdown.md) 顶部的“当前开发路径”为准：

1. `[下一步]` 实现 `FinishGuard` / `NoToolPolicy`，保持无工具默认结束，只处理空回复、截断回复、大代码块误输出和 plan 未验证完成等强异常。
2. `[规划]` 增加严格文本 `<tool_use>` 兜底，降低不同模型或中转服务的 tool calling 波动。
3. `[部分完成]` 将现有工具级 `run.log` 扩展为完整 Runner 生命周期事件流，再抽取内部 Hook 接口。
4. `[规划]` 实现 `cohort doctor` 总入口，统一检查配置、模型、MCP、Skill、浏览器和桌面环境。
5. `[规划]` 实现交互式 diff、变更审阅与回滚边界。
6. `[规划]` 再推进 Project/Plan Mode、内置 Skill 包、Plugin、LSP、多模型和 daemon。
7. `[验收支线]` 用官方飞书 MCP 完成 OAuth、只读和受控写操作的真实端到端验收。

## 运行与使用

| 状态 | 文档 | 用途 |
| --- | --- | --- |
| `[维护]` | [usage.md](usage.md) | 安装、CLI、REPL、MCP、session 和 context 的实际操作方式。 |
| `[维护]` | [testing.md](testing.md) | 单元测试、端到端和手工验收步骤。 |
| `[维护]` | [learning.md](learning.md) | 当前代码结构和核心数据流的入门阅读路径。 |
| `[历史]` | [开发记录文档.md](开发记录文档.md) | 已完成开发的原因、取舍、验证和演进过程。 |

## 当前路线与设计

| 状态 | 文档 | 当前判断 |
| --- | --- | --- |
| `[维护]` | [development_task_breakdown.md](development_task_breakdown.md) | 当前优先级和历史任务档案。 |
| `[部分完成]` | [cohort_mcp_integration_design.md](cohort_mcp_integration_design.md) | add/list/status/tools/probe/remove 与 P1 权限审计基础已完成；导入导出、飞书真实验收、OAuth 体验和 Plugin 尚待完成。 |
| `[规划]` | [cohort_future_development_opportunities.md](cohort_future_development_opportunities.md) | Claude Code/OpenClaw 对标能力池；以顶部状态表判断实际进度。 |
| `[部分完成]` | [cohort_vs_ga_current_gap.md](cohort_vs_ga_current_gap.md) | 当前差距分析；MCP 和渐进式 Skill Runtime 基础已补齐，NoToolPolicy、Hook、Project/Plan Mode、长期自治和前端生态仍未完成。 |
| `[规划]` | [agent_observability_technical_design.md](agent_observability_technical_design.md) | 提炼 GA hook、Langfuse、checkpoint 和 L4 反射经验；规划 Cohort lifecycle events、run.log JSONL、usage/cost、tracing sink 和调优报告。 |
| `[历史]` | [cohort_vs_ga_gap.md](cohort_vs_ga_gap.md) | 早期 MVP 差距快照，已由 `cohort_vs_ga_current_gap.md` 取代。 |

## 核心能力设计

| 状态 | 文档 | 当前判断 |
| --- | --- | --- |
| `[部分完成]` | [context_management_design.md](context_management_design.md) | 工具裁剪、group trim、session memory、full compact 已落地；自动 compact 熔断仍是后续项。 |
| `[完成]` | [tool_context_trim_iteration.md](tool_context_trim_iteration.md) | 当前工具结果裁剪实现和取舍记录。 |
| `[部分完成]` | [browser_operation_design.md](browser_operation_design.md) | Chrome bridge、DOM、点击、输入、等待、截图已落地；扩展桥与持续监控仍为后续。 |
| `[部分完成]` | [browser_ocr_real_input_fallback_design.md](browser_ocr_real_input_fallback_design.md) | DOM 摘要、OCR 和 macOS 受控输入已落地；视觉候选和 Windows 支持未完成。 |
| `[部分完成]` | [desktop_computer_use_technical_design.md](desktop_computer_use_technical_design.md) | M1、M2 受控桌面链路已完成；M3 视觉候选和更强后验验证待完成。 |
| `[部分完成]` | [computer_control_roadmap.md](computer_control_roadmap.md) | “操控电脑的所有操作”的能力缺口、风险边界和开发顺序；P1 核心原语、`computer_visual_snapshot`、`computer_execute_step` 与 `computer_execute_plan` 第一版已完成；下一步是强化 recover、UI detector 和真实 App E2E。 |
| `[规划]` | [human_os_operation_technical_design.md](human_os_operation_technical_design.md) | Computer Use 跨 OS 操作层方案；对模型暴露 `computer_see/find/click/type/press/check/wait`，底层键鼠输入绑定窗口、风险等级、验证和审计。 |
| `[部分完成]` | [self_evolution_technical_design.md](self_evolution_technical_design.md) | 受控长期记忆、证据和 SOP candidate 已完成；后台反射、L4 归档和质量闭环未完成。 |
| `[部分完成]` | [capability_evolution_technical_design.md](capability_evolution_technical_design.md) | 能力边界拓展闭环方案；本地 registry、手动 gap/proposal CLI 和 Runner no-tool gap 记录已落地，确认、Skill/Tool scaffold、smoke test 和 registry promote 待实现。 |
| `[部分完成]` | [cohort_self_evolution_research.md](cohort_self_evolution_research.md) | 自进化调研及后续路线，P1-P3 仍为规划。 |

## 调研与问题记录

| 状态 | 文档 | 使用方式 |
| --- | --- | --- |
| `[历史]` | [genericagent_borrowing_research.md](genericagent_borrowing_research.md) | GenericAgent 能力调研；不能直接当作 Cohort 当前状态。 |
| `[历史]` | [ga_browser_technical_route.md](ga_browser_technical_route.md) | GA 浏览器技术路线与对比依据。 |
| `[历史]` | [code_run_background_server_issue.md](code_run_background_server_issue.md) | 一次 code_run 长服务问题的根因和规避方案。 |

## README 翻译

`docs/readme/` 下的多语言 README 是根目录 `README.md` 的派生翻译。修改根 README 的命令、能力或安全语义时，翻译需要单独同步；它们不承担路线图状态。

## 维护规则

1. 开始实现前，先在相关设计文档写明目标范围和验收条件。
2. 实现完成后，更新本文档、任务拆解表、用户使用文档和开发记录。
3. 真实外部服务未验收时，不能把“单元测试通过”标记为 `[完成]`。
4. 新旧方案冲突时保留旧文档，但在顶部标为 `[历史]` 并链接到替代文档。
