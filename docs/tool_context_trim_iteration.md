# Tool 上下文剪切迭代记录

本文档记录当前 Context Manager 对 tool 上下文的处理现状、已知问题，以及下一版按模型上下文阈值触发压缩的技术方案。

## 当前实现形状

当前链路是：

```text
Runner.history 完整消息
  -> Runner.buildRequestMessages()
  -> contextmgr.Manager.Build()
  -> request messages
  -> LLM Client
```

`Runner.history` 和 `history.jsonl` 都保留完整原始消息。Context Manager 只处理本轮发给模型的临时副本。

当前 `Manager.Build()` 每次被调用都会按固定顺序执行：

```text
clone messages
  -> Micro Compact
  -> group trim
  -> return request messages
```

### Micro Compact 当前行为

位置：

```text
internal/contextmgr/tool_result.go
```

行为：

```text
从后往前扫描 role=tool 消息
保留最近 KeepRecentToolResults 条 tool result 完整内容
更旧的 tool result 如果超过 MaxToolResultChars
  -> 压缩成 head + tail 格式
```

压缩后的格式：

```text
[tool result compacted]
original_chars: <原始字符数>
kept_head_chars: <保留头部字符数>
kept_tail_chars: <保留尾部字符数>

--- head ---
<头部内容>

--- tail ---
<尾部内容>
```

注意：

- 这一步只压缩 `tool.Content`。
- 不删除 `tool` 消息。
- 不删除 `assistant tool_calls`。
- 不修改 `Runner.history`。
- 不修改 `history.jsonl`。

### Group Trim 当前行为

位置：

```text
internal/contextmgr/trim.go
```

行为：

```text
把 messages 分成 group
从最新 group 往前保留
直到满足 MaxHistoryMessages 和 MaxRequestChars
如果裁掉了旧消息，在最前面插入 context notice
```

group 规则：

```text
普通 user/assistant
  -> 单消息 group

assistant(tool_calls) + 后续匹配 tool results
  -> tool-call group

孤立 tool result
  -> 不进入请求，记录 warning
```

这样做是为了避免发给 OpenAI-compatible API 的消息破坏 tool calling 协议。

## 当前问题

当前实现能保证基本安全，但策略还不够好。

### 问题一：每次请求前都会尝试压缩和裁剪

当前 `Runner.Run()` 中两处会构造 request messages：

```text
用户输入进入 history 后
工具执行结果进入 history 后
```

代码形状：

```go
messages := r.buildRequestMessages()
...
messages = r.buildRequestMessages()
```

这个调用时机本身是合理的，因为每次调用模型前都需要重新构造本轮 request messages。

真正的问题是：

```text
ContextManager.Build() 当前没有“是否需要压缩”的判断。
```

也就是说，只要调用 `Build()`，它就会尝试 Micro Compact 和 group trim。

### 问题二：触发条件和模型上下文窗口脱节

当前触发主要由这两个配置控制：

```text
MaxHistoryMessages
MaxRequestChars
```

问题是：

- `MaxHistoryMessages` 只看消息数量，不知道模型 context window。
- `MaxRequestChars` 是字符预算，不是 token 预算。
- 当前配置没有表达“达到模型最大上下文 70% 才开始压缩”。
- 即使实际上下文只占模型窗口很小一部分，也可能因为消息数超过阈值被裁剪。

### 问题三：Micro Compact 和 Group Trim 没有分级触发

更理想的策略应该是：

```text
低于阈值
  -> 不压缩、不裁剪，只做必要的协议清理或直接返回原始副本

达到 70% 阈值
  -> 先 Micro Compact 旧工具结果

Micro Compact 后仍超过预算
  -> 再 group trim 旧历史
```

当前实现没有这个分级决策。

## 下一版目标

下一版目标不是移除 `buildRequestMessages()` 调用。

正确方向是：

```text
Runner 每次请求模型前仍然调用 buildRequestMessages()
Context Manager 内部根据上下文占用决定是否压缩
```

也就是：

```text
buildRequestMessages()
  -> ContextManager.Build()
  -> estimate usage
  -> below threshold: return cloned messages
  -> over threshold: compact / trim
```

## 70% 阈值方案

新增配置：

```go
type Config struct {
    ContextWindowTokens int
    MaxOutputTokens     int
    SafetyTokens        int
    CompactTriggerRatio float64
}
```

默认建议：

```text
context_window_tokens: 64000
max_output_tokens: 4096
safety_tokens: 4000
compact_trigger_ratio: 0.70
```

输入预算：

```text
usable_input_tokens = context_window_tokens - max_output_tokens - safety_tokens
```

压缩触发阈值：

```text
compact_trigger_tokens = usable_input_tokens * compact_trigger_ratio
```

第一版可以继续使用字符估算 token：

```text
estimated_tokens = len([]rune(messages_text)) / 2
```

后续再替换成精确 tokenizer。

## Build 流程改造

建议把 `Manager.Build()` 改成：

```text
clone messages
estimate original tokens

if original tokens < compact_trigger_tokens:
  return cloned messages with Stats.SkippedCompact = true

Micro Compact
estimate compacted tokens

if compacted tokens <= usable_input_tokens:
  return compacted messages

Group Trim
return trimmed messages
```

伪代码：

```go
func (m Manager) Build(input BuildInput) BuildResult {
    cfg := m.Config.Normalize()
    messages := cloneMessages(input.Messages)
    stats := newStats(input.Messages)

    budget := NewBudget(cfg)
    originalTokens := EstimateTokens(messages)
    stats.OriginalTokens = originalTokens

    if originalTokens < budget.CompactTriggerTokens {
        stats.SkippedCompact = true
        stats.FinalMessages = len(messages)
        stats.FinalTokens = originalTokens
        return BuildResult{Messages: messages, Stats: stats}
    }

    if cfg.EnableMicroCompact {
        compactToolResults(messages, cfg, &stats)
    }

    if EstimateTokens(messages) <= budget.UsableInputTokens {
        return finish(messages, stats)
    }

    messages = trimMessages(messages, cfg, &stats)
    return finish(messages, stats)
}
```

## Runner 接入方案

`runner.go` 里这段不建议删除：

```go
messages = r.buildRequestMessages()
```

原因：

- 工具执行后，`Runner.history` 新增了 `assistant tool_calls` 和 `tool result`。
- 下一轮模型请求必须看到工具结果。
- 因此每次再次调用模型前都要重新构造 request messages。

需要调整的是 `ContextManager.Build()` 的内部策略，而不是 Runner 是否调用它。

更准确的注释应该改成：

```go
// 工具结果已经进入完整 history；下一轮模型请求前重新构造可见上下文。
// Context Manager 会根据预算决定是否压缩，而不是每轮固定裁剪。
messages = r.buildRequestMessages()
```

## 配置文件改造

`configs/config.yaml` 建议改成：

```yaml
context:
  context_window_tokens: 64000
  max_output_tokens: 4096
  safety_tokens: 4000
  compact_trigger_ratio: 0.70

  max_history_messages: 40
  keep_recent_tool_results: 2
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  enable_micro_compact: true
```

`max_request_chars` 后续可以保留为兜底，但主触发条件应该转为 token budget。

## Stats 改造

建议增加统计字段：

```go
type Stats struct {
    OriginalTokens       int
    FinalTokens          int
    UsableInputTokens    int
    CompactTriggerTokens int
    SkippedCompact       bool
    TriggerReason        string
}
```

典型 `TriggerReason`：

```text
below_trigger_threshold
over_compact_trigger_threshold
over_usable_input_budget
```

这样后续日志里可以看清楚：

```text
这轮为什么没有压缩
这轮为什么只做 Micro Compact
这轮为什么进入 Group Trim
```

## 测试补充

下一版需要补以下测试：

```text
低于 70% 阈值
  -> 不压缩旧 tool result
  -> 不插入 context notice
  -> Stats.SkippedCompact = true

超过 70% 但 Micro Compact 后低于可用预算
  -> 压缩旧 tool result
  -> 不 group trim

超过可用预算
  -> Micro Compact 后继续 group trim
  -> tool-call group 不拆散

工具执行后下一轮请求
  -> Runner 仍调用 buildRequestMessages
  -> Build 根据预算决定是否压缩
```

## 推荐改造顺序

```text
1. 扩展 Config：context window、输出预留、安全余量、70% 阈值。
2. 新增 budget.go 的 token 估算和预算计算。
3. 改造 Manager.Build：低于阈值时 no-op 返回。
4. 保留 Runner.buildRequestMessages 调用，只更新注释。
5. 更新 configs/config.yaml 和 docs/usage.md。
6. 补充 contextmgr 和 runner 接入测试。
```

核心原则：

```text
不是每轮固定压缩。
是每次请求前评估，超过阈值才压缩。
```
