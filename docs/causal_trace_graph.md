# Agent 因果追踪图

状态：`[完成]`

## 1. 解决的问题

普通时间线只能回答“发生了什么”，很难直接回答：

- 哪次 LLM 决策触发了哪个工具？
- 哪个工具修改了哪个文件？
- 权限决策、工具失败和能力扩容如何影响后续步骤？
- 端到端耗时真正卡在哪条依赖链上？

Cohort 会从脱敏后的 `run.log.jsonl` 重建运行级 DAG，并输出离线交互式 HTML。
整个过程不调用 LLM，不依赖云服务，也不读取 prompt、工具结果或文件正文。

## 2. 使用

```bash
# 为最近一次运行生成 HTML
cohort trace graph last

# 生成后直接打开
cohort trace graph last --open

# 指定 session、run 和输出位置
cohort trace graph show <session_id> --run <run_id> --out ./trace.html

# 输出机器可读 DAG
cohort trace graph last --json
```

HTML 支持：

- 点击节点聚焦一跳因果邻居。
- 一键只显示关键路径。
- LLM、Tool、File、Route、Permission、Decision 节点分层展示。
- 展示 Top 5 延迟瓶颈和工具失败、路由扩容等异常。

## 3. 因果模型

核心节点：

```text
Run -> UserTask -> Turn -> Context -> ToolRoute -> LLM
                                                |
                                                v
                                      Tool -> FileChanged
                                        |
                                        v
                                  Permission / Decision
```

`ToolStarted` 与 `ToolFinished` 按 `tool_call_id` 合并为一个节点。
`LLMRequestStarted` 与 `LLMResponseFinished` 按 turn 合并。
文件变更只记录工具名、`tool_call_id` 和路径，不记录文件内容。

## 4. 关键路径

构图后对 DAG 执行最长加权路径计算：

- LLM 节点权重：模型调用耗时。
- Tool 节点权重：工具执行耗时。
- Compact 节点权重：上下文压缩耗时。
- 其余控制节点权重为零，但保留在依赖链中。

结果会同时写入 JSON 和 HTML：

```json
{
  "critical_path_ms": 2447,
  "critical_path": ["run", "prompt-1", "turn-1", "route-1", "llm-1"]
}
```

## 5. 安全边界

- 只消费已经经过 observability redaction 的事件。
- Graph Node 使用字段白名单，不透传原始 `event.data`。
- HTML 使用 `html/template` 自动转义。
- 页面没有 CDN、远端脚本或网络请求。
- Graph 生成失败不影响 Agent 主运行。

## 6. 面试表达

可以准确描述为：

> 我为 Agent Runtime 实现了本地 Flight Recorder。系统从脱敏事件流重建
> LLM、工具、权限和文件副作用的因果 DAG，并用最长路径算法定位端到端关键路径。
> 它同时支持离线交互式 HTML 和机器可读 JSON，不依赖模型二次分析，也不会泄露
> prompt 或工具结果正文。

这不是把日志换一种样式展示。核心工程点是事件关联、因果边构建、运行副作用归因、
DAG 关键路径计算和观测数据安全边界。
