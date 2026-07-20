# Meta SOP

## 触发场景

- 不确定该不该读 SOP。
- 一个任务同时命中多个 SOP。
- 刚读取 SOP，需要提炼执行约束。
- 多次失败、策略不清、任务跑了很多轮。

## SOP 使用流程

```text
1. 先看 sops/index.md 判断相关 SOP
2. 命中场景后 file_read 对应 SOP
3. 提取和当前任务直接相关的关键约束
4. 调用 update_working_checkpoint
5. 按 checkpoint 执行
6. 多次失败或策略变化时重读 related_sop
```

## Checkpoint 固定格式

`key_info` 建议使用这个结构：

```text
[任务] 一句话说明当前目标
[关键约束] 必须遵守的 SOP 规则
[禁止事项] 明确不能做什么
[当前进度] 已完成/已验证内容
[下一步] 立刻要执行的动作
```

`related_sop` 写相关 SOP 路径，可以多个，用逗号分隔。

## 多个 SOP 同时命中

- 先读最直接决定行动顺序的 SOP。
- 如果涉及文件修改和测试，通常还要读 `file_edit_sop` 和 `testing_sop`。
- 不要把多个 SOP 全文复制进回答；只把关键约束压缩进 checkpoint。

## 什么时候不要读 SOP

- 简单问答、无需工具、无需项目上下文。
- 用户只要当前状态或一个很小的命令结果。
- SOP 与任务没有实际决策关系。

## 失败处理

- 第一次失败：读错误，检查参数。
- 第二次失败：补环境探测。
- 第三次失败：重读 `related_sop`，更新 checkpoint，换策略或问用户。
