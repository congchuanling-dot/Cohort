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

1. `[完成]` `FinishGuard` / `NoToolPolicy` 保守早停守卫：无工具默认结束，只对空回复、截断回复、大代码块误输出和疑似未验证完成等强异常做一次性重试。
2. `[完成]` 严格文本 `<tool_use>` 兜底：降低不同模型或中转服务的 tool calling 波动。
3. `[部分完成]` Runner 生命周期 `run.log.jsonl` 事件流：Runner、LLM、tool、permission、compact、session start/end、FinishGuard 和 TextToolUse 已落地；`cohort trace last/show` 与 `cohort perf last/show` 已能读取本地事件流并定位 LLM/tool/context 瓶颈；`cohort tuning report` 已能跨 run 输出慢请求、失败工具、schema/request/context 膨胀和调优建议；`internal/hooks` 已提供可注册 Hook 接口并接入 SessionStart/SessionEnd/PreToolUse/PostToolUse/FileChanged/PreCompact/PostCompact，外部插件化执行器、更完整 policy sink、eval 和 A/B 仍待补。
4. `[部分完成]` `cohort doctor` 总入口：已检查配置、模型、MCP、Skill、浏览器扩展、桌面/OCR helper、workspace/session/log；真实桌面权限深检仍走 `cohort doctor computer`。
5. `[完成]` 交互式 diff、变更审阅与受限回滚边界：`/diff`、`/diff show`、`/diff accept`、`/diff rollback <file> --confirm` 已落地。
6. `[部分完成]` Project / Plan Mode：`cohort project init/status`、`.cohort/project.md`、`.cohort/config.json`、`cohort plan create/status/start/verify/block` 和 `.cohort/plan.json` 已落地；更完整 Project bootstrap 向导仍待补。
7. `[完成]` Skill Runtime 补强和候选挖掘：内置高频 Skill 包、`SKILL.md permissions`、`/skill run` active policy、Skill 候选离线报告已落地。
8. `[部分完成]` MCP 补全、usage/cost 和离线反思增强：MCP import/export、旧 SSE 兼容、per-tool policy CLI、Runner usage/cost 汇总、`mine-skill-candidates` 已落地；OAuth 体验深优化和 L4 闭环仍待补。
9. `[部分完成]` LSP / Plugin / Adapter / Explorer / TUI 底座：`lsp_diagnostics` Agent tool 和 `cohort lsp doctor/diagnostics` 已支持 Go/TypeScript/Python 只读诊断（Go=`gopls`、TS=`tsc --noEmit`、Python=`pyright`）；Go 已支持 `definition/references/hover/symbols` 的 gopls 查询，TS/Python 已支持 `definition/references/hover/symbols` 的只读 `symbol_scan` fallback；`cohort lsp doctor --install` 可用 npm 显式补装缺失的 `typescript`/`pyright`；`cohort plugin list/show/doctor` 已能发现和校验 `.cohort/plugins/*/plugin.json`；`cohort capability adapter <proposal_id> --type tool|mcp` 可生成待审查 adapter scaffold，并支持 `verify/promote/enable` 显式启用流，Tool adapter 启用后会进入运行时注册，MCP adapter 仍输出明确 import 命令；`cohort explorer create/list/show/run/run-batch` 可生成并通过隔离子进程运行只读验证任务，支持并行 lane 和 aggregate report；`cohort tui status|plan|diff|logs|explorers|watch` 可查看 plan、diff、日志、explorer 面板和周期刷新的实时面板。真正长驻 TS/Python language server、更强 subagent 结果合并和全屏 TUI 仍待补。
10. `[下一步]` 推进 OAuth 体验深优化、多模型体验、daemon、真实并行 subagent 和本地 API。
11. `[验收支线]` 用官方飞书 MCP 完成 OAuth、只读和受控写操作的真实端到端验收。
12. `[部分完成]` Agent Eval：`cohort eval init/list/run/history/report`、确定性多维断言、独立评测 session、并发执行、同 profile/model 自动基线比较、临时 Fixture、最终文件状态断言、JSON 等价、文件 diff、后验命令、Git 状态断言、工具轨迹评分、启发式 Judge、真实 LLM Judge、Judge 校准样例、重复运行稳定率、profile A/B matrix、CI gate、trace path 打通、失败 case 内嵌 trace timeline、flaky/regression Action Items、Hermes Action Queue、JSON/Markdown/离线 HTML Dashboard，以及 `cohort eval stability` 历史稳定性平台已落地；Langfuse dataset 接入、更完整 Hermes eval run 调度、外部通知 sink 与 SQLite 化待补。

## 运行与使用

| 状态 | 文档 | 用途 |
| --- | --- | --- |
| `[维护]` | [usage.md](usage.md) | 安装、CLI、REPL、MCP、session 和 context 的实际操作方式。 |
| `[维护]` | [testing.md](testing.md) | 单元测试、端到端和手工验收步骤。 |
| `[维护]` | [agent_evaluation.md](agent_evaluation.md) | Agent 评测协议、内置 suite、评分、基线、Dashboard 和 CI 使用方式。 |
| `[维护]` | [learning.md](learning.md) | 当前代码结构和核心数据流的入门阅读路径。 |
| `[历史]` | [开发记录文档.md](开发记录文档.md) | 已完成开发的原因、取舍、验证和演进过程。 |

## 当前路线与设计

| 状态 | 文档 | 当前判断 |
| --- | --- | --- |
| `[维护]` | [development_task_breakdown.md](development_task_breakdown.md) | 当前优先级和历史任务档案。 |
| `[部分完成]` | [cohort_mcp_integration_design.md](cohort_mcp_integration_design.md) | add/list/status/tools/probe/remove、import/export、旧 SSE 兼容、per-tool policy CLI 与 P1 权限审计基础已完成；飞书真实验收、OAuth 体验和 Plugin 尚待完成。 |
| `[规划]` | [cohort_future_development_opportunities.md](cohort_future_development_opportunities.md) | Claude Code/OpenClaw 对标能力池；以顶部状态表判断实际进度。 |
| `[部分完成]` | [cohort_vs_ga_current_gap.md](cohort_vs_ga_current_gap.md) | 当前差距分析；MCP、渐进式 Skill Runtime、保守 NoToolPolicy、文本 tool_use 兜底、diff、Project/Plan Mode 基础已补齐，Hook、长期自治和前端生态仍未完成。 |
| `[部分完成]` | [agent_observability_technical_design.md](agent_observability_technical_design.md) | 提炼 GA hook、Langfuse、checkpoint 和 L4 反射经验；Runner lifecycle events、usage/cost 汇总、Langfuse 基础、trace/perf CLI 和 tuning report 已落地，完整 tracing sink、TUI trace panel、eval 与 A/B 仍待补。 |
| `[历史]` | [cohort_vs_ga_gap.md](cohort_vs_ga_gap.md) | 早期 MVP 差距快照，已由 `cohort_vs_ga_current_gap.md` 取代。 |

## 核心能力设计

| 状态 | 文档 | 当前判断 |
| --- | --- | --- |
| `[部分完成]` | [context_management_design.md](context_management_design.md) | 工具裁剪、group trim、session memory、full compact 已落地；Auto Compact 第一版已支持显式配置、`context_state.json` 和连续失败熔断，默认关闭。 |
| `[完成]` | [tool_context_trim_iteration.md](tool_context_trim_iteration.md) | 当前工具结果裁剪实现和取舍记录。 |
| `[部分完成]` | [browser_operation_design.md](browser_operation_design.md) | Chrome bridge、DOM、点击、输入、等待、截图已落地；扩展桥与持续监控仍为后续。 |
| `[部分完成]` | [browser_ocr_real_input_fallback_design.md](browser_ocr_real_input_fallback_design.md) | DOM 摘要、OCR 和 macOS 受控输入已落地；视觉候选和 Windows 支持未完成。 |
| `[部分完成]` | [desktop_computer_use_technical_design.md](desktop_computer_use_technical_design.md) | M1、M2 受控桌面链路已完成；M3 已有 detector 协议和结构化 recover policy，模型/SDK 级 UI detector 与更强后验验证待完成。 |
| `[部分完成]` | [computer_control_roadmap.md](computer_control_roadmap.md) | “操控电脑的所有操作”的能力缺口、风险边界和开发顺序；P1 核心原语、`computer_visual_snapshot`、`computer_execute_step`、结构化 recover policy、detector 协议和 `doctor computer --smoke-app` 已完成；下一步是模型/SDK 级 UI detector、多显示器和更多真实 App 回归样例。 |
| `[规划]` | [human_os_operation_technical_design.md](human_os_operation_technical_design.md) | Computer Use 跨 OS 操作层方案；对模型暴露 `computer_see/find/click/type/press/check/wait`，底层键鼠输入绑定窗口、风险等级、验证和审计。 |
| `[部分完成]` | [self_evolution_technical_design.md](self_evolution_technical_design.md) | 受控长期记忆、证据、SOP candidate、离线 session archive、failure pattern、memory quality 和 Skill candidate 报告已完成；后台反射和更强 L4 质量闭环未完成。 |
| `[部分完成]` | [capability_evolution_technical_design.md](capability_evolution_technical_design.md) | 能力边界拓展闭环方案；本地 registry、手动 gap/proposal CLI、Runner no-tool gap 记录、项目级 Skill scaffold、doctor 诊断、依赖安装审核与审计、smoke test、registry promote、available capability 索引注入、重复 gap CLI 建议、Skill 候选离线挖掘和 Tool/MCP adapter scaffold + verify/promote/enable 已落地；更强自动路由和离线反思汇总仍待补。 |
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
