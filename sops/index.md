# SOP Index

这个文件只做导航，不写完整操作步骤。任务命中某个场景时，先读取对应 SOP，再把关键约束写入 `update_working_checkpoint`。

## L0 Meta

- SOP: `sops/meta_sop.md`
- 场景：不知道如何使用 SOP、多个 SOP 同时命中、读完 SOP 后如何提炼 checkpoint、失败后何时重读 SOP。

## Browser

- SOP: `sops/browser_sop.md`
- 场景：浏览器打开网页、等待加载、读取页面、点击、输入、CDP JSON 路由、页面变化判断、截图/OCR 兜底。

## Code Run

- SOP: `sops/code_run_sop.md`
- 场景：执行 shell/Python、启动后台服务、长生命周期进程、超时、端口监听、进程清理。

## File Edit

- SOP: `sops/file_edit_sop.md`
- 场景：读取、修改、新增、删除项目文件；处理用户未提交改动；应用 patch；改后验证。

## Context

- SOP: `sops/context_sop.md`
- 场景：上下文压缩、tool result 过长、历史裁剪、assistant tool_calls 与 tool result 配对、token 预算。

## Testing

- SOP: `sops/testing_sop.md`
- 场景：Go/JS/插件/文档/后台服务改动后的验证命令和验收标准。

## Rules

- SOP 全文按需读取，不要凭印象执行。
- 读完 SOP 后，如果决定按它执行，必须用 `update_working_checkpoint` 保存关键约束和 `related_sop`。
- 多次失败、策略不清或任务超过多轮时，重读 `related_sop`。
