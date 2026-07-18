# Cohert 和 GA 功能差异清单

这份文档用于记录当前 Cohert MVP 和 GA 的功能差异，并按 P0/P1 给出后续补齐建议。

当前原则：

- Cohert 是独立项目，不做 GA 的逐行翻译。
- 第一阶段优先保证命令行 Agent 稳定可用。
- UI、浏览器、长期记忆等能力可以后置，但需要明确优先级。

## 1. 当前 Cohert 已具备的能力

| 能力 | Cohert 当前状态 |
| --- | --- |
| 命令行入口 | 支持 `go run .`、`go run . ask`、`go run . tools`、`go run . config` |
| Agent Loop | 支持用户输入、模型调用、tool_calls、工具执行、工具结果回灌、最大轮数保护 |
| LLM 协议 | 支持 OpenAI-compatible Chat Completions |
| 默认模型 | DeepSeek，默认 `deepseek-v4-pro` |
| 流式输出 | 支持 SSE 文本流 |
| 工具调用 | 支持原生 tool calling |
| 工具注册 | 支持 Tool Registry |
| 基础工具 | `file_read`、`file_write`、`file_patch`、`code_run`、`ask_user` |
| 日志 | 支持模型原始响应写入 `temp/model_responses/` |
| 配置 | 支持 `configs/config.yaml` 和 `DEEPSEEK_API_KEY` |

## 2. P0 差异清单

P0 表示：如果 Cohert 要成为一个可靠的命令行 Agent，这些能力应该优先补齐。

| 模块 | GA 能力 | Cohert 当前状态 | 差异 | 建议实现 |
| --- | --- | --- | --- | --- |
| 会话持久化 | GA 的 session/history 能保留历史上下文 | 只保存在内存 `Runner.history` | 进程退出后会话丢失 | 增加 `temp/sessions/<session_id>/history.jsonl` |
| 会话恢复 | GA 可围绕已有 session 继续工作 | 暂无恢复能力 | 无法 resume 上次任务 | 增加 `cohert session list`、`cohert session resume <id>` |
| 上下文裁剪 | GA 有 token 压缩、工具结果压缩、历史压缩逻辑 | 暂无上下文裁剪 | 长任务容易超过模型上下文 | 增加按消息数量和字符数的简单裁剪 |
| 工具错误处理 | GA 会把未知工具、坏 JSON、空响应等情况转成下一轮提示 | Cohert 只把工具错误转成 tool result | 模型修复能力较弱 | 增加 bad JSON、unknown tool、empty response 的明确错误消息 |
| 工具调用解析 | GA 同时支持原生工具和文本 `<tool_use>` 兜底解析 | Cohert 只支持原生 tool calling | 模型不稳定时可能无法调用工具 | 增加文本工具调用兜底解析 |
| 文件读取体验 | GA 的 `file_read` 有更复杂的行号、上下文、候选文件提示 | Cohert 只支持基础行号读取 | 文件路径错时反馈不足 | 增加路径不存在时的候选文件建议 |
| 文件修改安全 | GA 的 patch/write 行为更成熟 | Cohert 已有唯一块替换，但缺少变更摘要 | 用户不容易确认改了什么 | 工具结果返回 `old_bytes/new_bytes/changed_lines` |
| 命令执行安全 | GA 的命令执行输出有限制和上下文处理 | Cohert 有 timeout 和输出截断 | 缺少危险命令确认 | 对 `rm -rf`、`git reset`、`chmod -R` 等增加确认机制 |
| 配置检查 | GA 安装和配置文档更完整 | Cohert 只有 `config` | 不容易定位环境问题 | 增加 `go run . doctor` 检查 Go、API Key、模型连通性、workspace |
| 测试覆盖 | GA 经过较多场景验证 | Cohert 暂无单元测试 | 以后改 LLM 解析容易回归 | 给 `openai.go`、`file_patch`、`path` 增加单测 |
| 日志结构 | GA 有更丰富运行输出和历史 | Cohert 只有模型 raw log | 调试工具链路不够直观 | 增加 `run.log`，记录 turn、tool、args、result 摘要 |
| 退出状态 | GA 有 `CURRENT_TASK_DONE`、`EXITED`、`MAX_TURNS_EXCEEDED` 等状态 | Cohert 有 `done/exited/max_turns_exceeded` | 状态较少但够用 | 保留现状，补充状态常量 |

## 3. P1 差异清单

P1 表示：命令行核心稳定后，再补这些能力。它们重要，但不应该阻塞 MVP。

| 模块 | GA 能力 | Cohert 当前状态 | 差异 | 建议实现 |
| --- | --- | --- | --- | --- |
| 多模型支持 | GA 支持多供应商、多协议和 fallback | Cohert 只支持 OpenAI-compatible | 模型选择不灵活 | 增加 provider 接口，先支持 OpenAI-compatible 多 base_url |
| Claude 协议 | GA 对 Claude/Anthropic 兼容较多 | 暂不支持 | 不能直接走 Claude 原生工具协议 | 后续新增 Anthropic Client |
| 浏览器能力 | GA 有 `web_scan`、`web_execute_js`、TMWebDriver | 暂无浏览器工具 | 不能做网页观察和操作 | 增加 browser 工具包，先只做页面读取 |
| 长期记忆 | GA 有 `memory/`、global memory、SOP、访问统计 | 暂无长期记忆 | 长任务和跨任务经验不能沉淀 | 增加 `memory/` 目录和手动读写工具 |
| 工作记忆 | GA 有 `update_working_checkpoint` | 暂无工作检查点 | 长任务缺少阶段性摘要 | 增加 `update_checkpoint` 工具 |
| done hook | GA 支持 `_done_hooks` 和 turn_end_callback | 暂无 hook | 无法在任务完成时自动追加检查 | 增加简单 after-run hook |
| 插件 hook | GA 有 `plugins/hooks.py` | 暂无插件系统 | 无法外部监听 agent/tool/llm 生命周期 | 增加轻量事件接口，不急着做动态插件 |
| 前端形态 | GA 有 Streamlit、TUI、桌面、IM 等多前端 | Cohert 只有 CLI | 使用场景有限 | 命令行稳定后再做 TUI，最后做 Web/UI |
| 安装脚本 | GA 有安装脚本和启动封装 | Cohert 暂不做全局启动 | 当前必须在项目根目录启动 | 后续再做 `scripts/install.sh` 和用户配置目录 |
| 计划模式 | GA 有 plan/checklist/goal 相关 SOP | 暂无计划模式 | 复杂任务缺少显式阶段管理 | 增加 `plan` 工具或内置计划状态 |
| 调度任务 | GA 有 scheduler 相关能力 | 暂无定时任务 | 不能做延迟/周期任务 | 等核心稳定后再做 scheduler |
| 多 Agent | GA 有 reflect/autonomous/team 等方向 | 暂无多 Agent | 不能拆分角色协作 | 后续再考虑，不进入近期范围 |
| IM/机器人集成 | GA 有飞书、钉钉、微信、Telegram 等前端 | 暂无 | Cohert 仅本地命令行 | 不建议近期做，除非 CLI 内核稳定 |
| 富输出 | GA 前端支持更丰富展示 | Cohert 终端纯文本 | 工具结果展示简单 | 增加 Markdown 友好输出和结果折叠 |
| 成本统计 | GA 有 cost tracker 相关模块 | 暂无 token/cost 统计 | 不知道每次请求消耗 | LLM 响应里记录 usage 后再统计 |

## 4. 建议开发顺序

### P0 第一批

1. 给 role、run status、tool name 增加常量。
2. 增加 `history.jsonl` 会话落盘。
3. 增加 `go run . doctor`。
4. 给 `openai.go` 的 SSE tool_calls 拼接加单测。
5. 给 `file_patch` 加单测。

### P0 第二批

1. 增加 session list/resume。
2. 增加上下文裁剪。
3. 增加 unknown tool、bad JSON、empty response 的自修复提示。
4. 增加危险命令确认。
5. 增加工具运行日志 `run.log`。

### P1 第一批

1. 增加多模型配置。
2. 增加简单 memory 目录和 checkpoint 工具。
3. 增加 TUI 或更好的终端输出。
4. 增加浏览器只读工具。

## 5. 当前结论

Cohert 当前已经具备 Agent 的最小闭环，但和 GA 相比仍然是命令行 MVP。

最关键差异不是 UI，而是这些基础工程能力：

- 会话落盘和恢复
- 上下文裁剪
- 工具调用兜底解析
- 工具错误自修复
- 单元测试
- 安全确认

这些补齐后，Cohert 才适合继续扩展 UI、浏览器、记忆和多模型能力。
