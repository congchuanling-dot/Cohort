# SOP Index

这个文件只做导航，不写完整操作步骤。任务命中某个场景时，先读取对应 SOP，再把关键约束写入 `update_working_checkpoint`。

## Browser

- SOP: `sops/browser_sop.md`
- 场景：浏览器打开网页、等待加载、读取页面、点击、输入、CDP JSON 路由、页面变化判断、截图/OCR 兜底。

## Code Run

- SOP: `sops/code_run_sop.md`
- 场景：执行 shell/Python、启动后台服务、长生命周期进程、超时、端口监听、进程清理。

## Rules

- SOP 全文按需读取，不要凭印象执行。
- 读完 SOP 后，如果决定按它执行，必须用 `update_working_checkpoint` 保存关键约束和 `related_sop`。
- 多次失败、策略不清或任务超过多轮时，重读 `related_sop`。
