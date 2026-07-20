# Context SOP

## 触发场景

- 上下文压缩、token 预算、历史裁剪。
- 工具结果很长、日志很大、模型开始丢状态。
- 涉及 assistant tool_calls 和 tool result 协议配对。

## 核心规则

- assistant 的 `tool_calls` 和后续 `tool` result 必须成组保留。
- 不要留下孤立 tool result。
- 工具结果过长先做 micro compact，不要直接塞完整日志。
- 预算达到阈值时优先压缩旧工具结果，再裁剪旧消息组。

## 处理长日志

```text
1. 不把完整 log 塞进回答
2. 用 rg 定位关键词
3. 读取局部上下文
4. 总结关键错误和证据
```

## 失败处理

- 如果请求模型报协议错误，优先检查 tool_calls/tool result 配对。
- 如果模型开始遗忘任务约束，更新 `update_working_checkpoint`。
- 如果上下文污染严重，先整理 checkpoint，再继续。

## 验收标准

- 压缩后请求仍符合 OpenAI-compatible 工具协议。
- 最新用户请求、最近工具结果、checkpoint 没丢。
- 不把大日志、大 JSONL 原样灌回模型。
