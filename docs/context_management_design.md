# Cohert 上下文管理层技术方案

本文档描述 Cohert 后续实现 Context Manager 的完整技术方案。

方案参考 Claude Code 上下文工程的四层压缩思路，并结合 Cohert 当前已经实现的 `history.jsonl`、`Runner.history`、工具调用协议和 session 恢复能力重新设计。

## 1. 背景

Cohert 当前已经具备基础会话能力：

- 用户输入会进入 `Runner.history`。
- 模型回复会进入 `Runner.history`。
- 模型发起的 `tool_calls` 会作为 `assistant` 消息保存。
- 工具执行结果会作为 `tool` 消息保存。
- 所有消息会追加写入 `temp/sessions/<session_id>/history.jsonl`。
- `session resume` 会读取 `history.jsonl` 并恢复成 `[]llm.Message`。

当前上下文是结构化的，但还没有真正的上下文管理层。

现在的请求链路是：

```text
history.jsonl
  -> Store.LoadHistory()
  -> Runner.history
  -> 直接发给模型
```

这个链路在短会话里可以工作，但长会话会出现明显问题：

- `history.jsonl` 越来越长。
- `Runner.history` 越来越长。
- `code_run`、`file_read`、未来 `browser_scan` 等工具输出可能很大。
- `session resume` 后会把历史全部发给模型。
- 一旦超过模型 context window，请求会失败。
- 即使没失败，也会变慢、变贵，并干扰模型判断。

所以 Cohert 需要把“完整历史”和“本轮模型可见上下文”拆开。

目标链路应该变成：

```text
history.jsonl 完整保存
  -> Store.LoadHistory()
  -> Runner.history 完整保留
  -> ContextManager.Build()
  -> 压缩后的 request messages
  -> 发给模型
```

核心原则：

```text
磁盘完整保存
内存完整保留
请求前压缩
压缩过程可测试
压缩失败可降级
```

## 2. 参考：Claude Code 四层压缩思路

参考文章把 Claude Code 的上下文压缩分成四层：

```text
第一层：Micro Compact
第二层：Session Memory Compact
第三层：Full Compact
第四层：Auto Compact
```

这四层的价值在于：不要把上下文管理做成一个单一裁剪函数，而是按成本从低到高分层处理。

### 2.1 Micro Compact

Micro Compact 是零成本压缩。

特点：

- 不调用模型。
- 只根据规则处理历史。
- 优先处理工具结果。
- 保留最近工具结果。
- 压缩较旧工具结果。
- 保证工具调用协议不被破坏。

它适合 Cohert 第一版落地，因为确定性强、容易测试、不会增加模型调用成本。

### 2.2 Session Memory Compact

Session Memory Compact 不在每轮对话后自动执行。它应由手动命令、上下文阈值、阶段完成或 Auto Compact 策略低频触发，只提取稳定事实，不记录临时执行细节。

Session Memory Compact 是低成本结构化记忆。

它不是简单总结，而是提取会话中的稳定事实，例如：

- 用户目标。
- 用户偏好。
- 项目约定。
- 已完成事项。
- 关键文件。
- 当前问题。
- 下一步。

这些内容可以保存到 session 目录下的 `memory.md`，用于替代大量旧对话。

### 2.3 Full Compact

Full Compact 是高成本模型摘要。

当规则压缩和结构化记忆还不够时，调用模型生成结构化摘要。

摘要不能只写“总结一下对话”，应该强制按维度输出，避免漏掉任务状态。

建议 Cohert 使用 9 个维度：

```text
1. Primary Request and Intent：用户主要请求和真实意图
2. Key Technical Concepts：关键技术概念和架构决策
3. Files and Code Sections：涉及的文件、函数、代码段
4. Errors and Fixes：遇到的错误和修复方式
5. Problem Solving：问题分析和解决过程
6. User Messages：关键用户消息
7. Pending Tasks：明确未完成任务
8. Current Work：压缩前正在进行的工作
9. Next Step：建议下一步
```

### 2.4 Auto Compact

Auto Compact 解决什么时候压缩。

它需要：

- 根据模型 context window 估算剩余预算。
- 在快爆之前触发压缩。
- 压缩失败后有熔断器。
- 避免压缩流程本身递归触发压缩。

对 Cohert 来说，Auto Compact 不建议第一版就做复杂。第一版先做请求前确定性裁剪；等稳定后再加入自动摘要和熔断器。

当前实现采用“每次请求前评估，超过阈值才压缩”的策略：

```text
estimated_input_tokens < 可用输入预算的 70%
  -> 不做 Micro Compact
  -> 不做 Group Trim

estimated_input_tokens >= 可用输入预算的 70%
  -> 先做 Micro Compact
  -> 如仍超过预算，再做 Group Trim
```

## 3. Cohert 设计目标

### 3.1 第一阶段目标

第一阶段实现确定性 Context Manager。

必须做到：

- 模型请求前统一经过 Context Manager。
- `history.jsonl` 不被裁剪。
- `Runner.history` 不被裁剪。
- 只裁剪发给模型的 `messages`。
- 对长工具结果做头尾保留。
- 对历史消息数量做限制。
- 保证 `assistant tool_calls` 和对应 `tool` 消息不拆散。
- 有单元测试覆盖核心边界。

### 3.2 第二阶段目标

第二阶段实现 session memory。

包括：

- 在 session 目录下保存 `memory.md`。
- 提取用户目标、项目约定、关键文件、已完成事项。
- 请求模型时把 `memory.md` 注入上下文。
- 历史过长时优先依赖 `memory.md + 最近消息`。

### 3.3 第三阶段目标

第三阶段实现 Full Compact。

包括：

- 提供手动命令 `/full-compact`。
- 调用模型生成结构化摘要。
- 保存到 `compact.md`。
- resume 后注入 `compact.md`。
- 对 compact 失败做重试和降级。

### 3.4 第四阶段目标

第四阶段实现 Auto Compact。

包括：

- 每次请求前估算上下文大小。
- 超过阈值时自动压缩。
- 连续失败超过阈值后停止自动压缩。
- 提示用户手动 compact 或调小配置。

## 4. 非目标

第一版不做：

- 精确 tokenizer。
- 复杂 LLM summary。
- 长期跨项目 memory。
- 自动删除 `history.jsonl`。
- 在工具层修改真实输出。
- 把 Context Manager 做成模型可调用工具。

特别注意：Context Manager 是 Agent 内部层，不是 Tool Registry 里的工具。

不应该暴露这种工具：

```text
context_trim
context_delete
context_compact
```

模型不应该自己决定删除哪些上下文。上下文预算和协议完整性属于运行时控制逻辑，应由 Cohert 保证。

## 5. 总体架构

建议新增包：

```text
internal/contextmgr/
  types.go          配置、统计、结果结构
  manager.go        上下文构造入口
  budget.go         字符和 token 估算
  trim.go           历史消息裁剪
  tool_result.go    工具结果压缩
  invariants.go     tool_calls/tool 关系保护
  memory.go         后续 session memory 注入
  compact.go        后续 full compact 摘要注入
```

调用链：

```text
Runner.Run()
  -> r.history 保留完整历史
  -> contextmgr.Manager.Build(r.history)
  -> request messages
  -> r.Client.Chat(...)
```

Runner 不应该自己写裁剪逻辑，只负责调用 Context Manager。

## 6. 当前消息结构

Cohert 当前模型消息结构是：

```go
type Message struct {
    Role       string
    Content    string
    ToolCallID string
    Name       string
    ToolCalls  []ToolCall
}
```

核心 role：

```text
system     系统提示词，当前单独放在 ChatRequest.System
user       用户输入
assistant  模型文本回复，或模型发起 tool_calls
tool       工具执行结果
```

工具调用协议示例：

```json
{
  "role": "assistant",
  "tool_calls": [
    {
      "id": "call_001",
      "type": "function",
      "function": {
        "name": "code_run",
        "arguments": "{\"script\":\"go test ./...\"}"
      }
    }
  ]
}
```

对应工具结果：

```json
{
  "role": "tool",
  "tool_call_id": "call_001",
  "name": "code_run",
  "content": "{\"status\":\"success\",\"stdout\":\"...\"}"
}
```

压缩时必须保证：

```text
保留 tool 结果时，必须保留对应 assistant tool_calls。
保留 assistant tool_calls 时，应该尽量保留对应 tool 结果。
不能让请求里出现孤立 tool result。
不能让请求里出现缺结果的 tool_calls，除非它是最新一轮正在等待工具执行的状态。
```

Cohert 当前 Runner 是同步执行工具，所以正常历史里不应该存在“正在等待工具执行”的 assistant tool_calls。

因此第一版规则可以更简单：

```text
assistant tool_calls 和后续对应 tool messages 作为一个 group 处理。
要么一起保留，要么一起压缩内容。
不要只保留其中一半。
```

## 7. 配置设计

建议在 `configs/config.yaml` 中保留可调的压缩细节，不暴露模型上下文窗口：

```yaml
context:
  max_history_messages: 40
  keep_recent_tool_results: 3
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  max_request_chars: 100000
  enable_micro_compact: true
  enable_session_memory: false
  enable_full_compact: false
  max_compact_failures: 3
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `max_history_messages` | 请求中最多保留的原始历史消息数量 |
| `keep_recent_tool_results` | 最近几个工具结果完整保留 |
| `max_tool_result_chars` | 单条工具结果超过该值就压缩 |
| `compacted_tool_head_chars` | 工具结果压缩后保留头部字符数 |
| `compacted_tool_tail_chars` | 工具结果压缩后保留尾部字符数 |
| `max_request_chars` | 第一版字符级硬上限 |
| `enable_micro_compact` | 是否启用规则压缩 |
| `enable_session_memory` | 是否启用 session memory |
| `enable_full_compact` | 是否启用模型摘要 |
| `max_compact_failures` | 自动压缩连续失败熔断阈值 |

第一版可以只接入这些字段：

```yaml
context:
  max_history_messages: 40
  keep_recent_tool_results: 3
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  max_request_chars: 100000
```

## 8. 模型上下文大小怎么获得

OpenAI-compatible API 通常不会稳定返回模型最大 context window。

因此 Cohert 不应该依赖自动获取，也不应该让用户手动配置模型窗口，而应该采用：

```text
根据 llm.model 查内置模型表
未知模型使用保守默认值
请求失败后提示用户调整
```

建议内置表：

```go
var ModelContextWindows = map[string]int{
    "deepseek-v4-pro":    1000000,
    "dsv4pro":            1000000,
    "deepseek-chat":      64000,
    "deepseek-reasoner":  64000,
    "gpt-4o":             128000,
    "gpt-4.1":            1000000,
    "claude-3-5-sonnet":  200000,
}
```

优先级：

```text
llm.model
  -> 模型名内置表
  -> 默认 1000000
```

第一版 token 估算可以用字符估算：

```text
estimated_tokens = len([]rune(text)) / 2
```

为了保守，也可以先直接用字符硬上限：

```text
max_request_chars = 100000
```

后续如果需要更精确，再引入 tokenizer。

如果 API 提供方后续仍不提供模型上下文窗口接口，可以继续采用两种策略：

```text
第一优先级：内置模型 map
后续增强：用探测请求做二分逼近，但默认不启用
```

## 9. 第一层：Micro Compact 设计

### 9.1 目标

Micro Compact 只处理请求前消息，不修改磁盘历史。

输入：

```go
[]llm.Message
```

输出：

```go
BuildResult{
    Messages: []llm.Message,
    Stats:    BuildStats,
}
```

### 9.2 可压缩内容

第一版只压缩 `role=tool` 的 `Content`。

原因：

- 工具结果通常最大。
- 工具结果时效性较短。
- 工具结果可以保留头尾，不必完整保留。
- 不会破坏用户原始输入。
- 不会破坏模型最终回答。

可压缩工具：

```text
code_run
file_read
file_write
file_patch
未来 browser_scan
未来 browser_execute_js
```

### 9.3 工具结果压缩格式

压缩后的 `tool.Content` 使用纯文本或 JSON 都可以。

推荐第一版保持字符串，格式如下：

```text
[tool result compacted]
tool: code_run
original_chars: 50000
kept_head_chars: 4000
kept_tail_chars: 4000

<head>
...
[omitted 42000 chars]
...
<tail>
```

如果原始工具结果本身是 JSON 字符串，可以不解析 JSON，直接按字符串压缩。

原因：

- 实现简单。
- 不依赖工具结果 schema。
- 所有工具都能通用。

后续如果要更细，可以对 `code_run.stdout`、`file_read.content`、`browser_scan.text` 做字段级压缩。

### 9.4 最近工具结果保留

配置：

```yaml
keep_recent_tool_results: 3
```

含义：

```text
最近 3 条 role=tool 消息完整保留。
更早的 role=tool 消息如果超过 max_tool_result_chars，则压缩。
```

这样能保证模型对最近操作仍有完整认知。

## 10. 第二层：历史消息裁剪

### 10.1 目标

当消息数量过多时，请求中只保留最近一段历史。

配置：

```yaml
max_history_messages: 40
```

含义：

```text
发给模型的原始消息最多保留最近 40 条。
```

### 10.2 不能简单截尾

不能直接写：

```go
messages = messages[len(messages)-40:]
```

因为这样可能把工具协议切断。

错误示例：

```text
保留了 tool result
但它前面的 assistant tool_calls 被裁掉
```

这种请求发给 OpenAI-compatible API 可能直接报错。

### 10.3 按 group 裁剪

建议把历史分组：

```text
普通消息 group:
  user
  assistant(content)

工具调用 group:
  assistant(tool_calls)
  tool(call_id=xxx)
  tool(call_id=yyy)
```

裁剪时按 group 从后往前保留，而不是按单条消息保留。

示例：

```text
Group 1: user
Group 2: assistant tool_calls + tool + tool
Group 3: assistant content
Group 4: user
```

保留最近 N 条消息时，实际可以保留超过 N 条一点点，以保证 group 完整。

### 10.4 裁剪提示消息

如果裁剪了旧历史，建议在最前面插入一条 `user` 或 `assistant` 摘要提示。

第一版可以插入简单提示：

```text
[earlier conversation omitted by Cohert Context Manager]
Some older messages were omitted from this request to fit the model context window.
The full history remains stored in history.jsonl.
```

更推荐用 `assistant` 消息：

```go
llm.Message{
    Role: llm.RoleAssistant,
    Content: "[Cohert context notice] Earlier conversation messages were omitted from this request. Full history is preserved in history.jsonl.",
}
```

注意：这个提示不写入 `history.jsonl`，只注入本次请求。

## 11. 第三层：Session Memory 设计

说明：这里的“第三层”是本文档工程阶段编号；对应 Claude Code 四层思路里的第二层 `Session Memory Compact`。

### 11.1 文件位置

建议保存到：

```text
temp/sessions/<session_id>/memory.md
```

### 11.2 内容结构

```md
# Session Memory

## 用户目标

- ...

## 用户偏好

- ...

## 项目约定

- ...

## 已完成事项

- ...

## 关键文件

- ...

## 错误和修复

- ...

## 当前状态

- ...

## 下一步

- ...
```

### 11.3 已实现：读取和注入

Context Manager 会读取并注入 `memory.md`。

行为：

```text
如果 session 目录存在 memory.md
  -> 读取
  -> 截断到 MaxSessionMemoryChars
  -> 作为受保护前缀注入 request messages 前部

如果 memory.md 不存在或为空
  -> 跳过，不影响正常请求
```

注入格式：

```go
llm.Message{
    Role: llm.RoleAssistant,
    Content: "[Cohert session memory]\n\n" + memoryText,
}
```

关键约束：

```text
memory.md 不写入 Runner.history
memory.md 不写入 history.jsonl
group trim 不会把 memory.md 当作最旧历史裁掉
memory.md 内容会计入 70% 阈值预算
```

### 11.4 已实现：/compact 生成 memory.md

手动生成入口：

```text
/compact
```

行为：

```text
读取当前 Runner.history
构造 memory 生成 prompt
调用 LLM 提取稳定事实
如果已有 memory.md，先备份到 memory.bak.md
覆盖写入 temp/sessions/<session_id>/memory.md
后续请求由注入逻辑自动加载
```

生成要求：

```text
只提取稳定事实
不记录临时命令输出
不记录一次性中间过程
不每轮自动总结
生成失败不影响正常对话
```

关键约束：

```text
/compact 不调用工具
/compact 不把生成结果写入 history.jsonl
/compact 需要当前 Runner 已绑定 active session
生成 prompt 的历史输入有字符上限，过长时保留头尾并插入省略标记
```

查看入口：

```text
/memory
/session memory
```

输出当前 session 的 `memory.md` 路径、字符数和内容。没有 active session 时提示 `no active session`；当前 session 没有 `memory.md` 时提示 `no session memory`。

覆盖保护：

```text
temp/sessions/<session_id>/memory.md
temp/sessions/<session_id>/memory.bak.md
```

`/compact` 覆盖前如果发现旧 `memory.md` 非空，会先把旧内容复制到 `memory.bak.md`。

### 11.5 已实现：Context Stats 观测日志

Context Manager 每次构造 request messages 后，会把本轮决策写到：

```text
temp/model_responses/context.log
```

日志是 JSONL，一行对应一次 `ContextManager.Build()`。只记录统计和决策，不记录消息内容。

字段包括：

```text
session_id
original_messages
final_messages
original_tokens
final_tokens
usable_input_tokens
compact_trigger_tokens
trigger_reason
skipped_compact
compacted_tool_results
omitted_tool_result_chars
trimmed_messages
inserted_notice
injected_session_memory
session_memory_chars
session_memory_truncated
warnings
```

这份日志用于回答三个问题：

```text
本轮有没有触发 70% 阈值
本轮有没有注入 memory.md
本轮有没有压缩 tool result 或裁剪历史
```

后续仍需补充：

- `session memory <id>` 外部命令。
- memory 编辑命令。
- 自动触发策略和熔断器。

## 12. 第四层：Full Compact compact.md

### 12.1 文件位置

已实现保存到：

```text
temp/sessions/<session_id>/compact.md
temp/sessions/<session_id>/compact.bak.md
```

### 12.2 第一版触发策略

第一版只做手动触发：

```text
/full-compact
```

不做自动生成。原因是 Full Compact 会调用模型重新总结长历史，如果自动触发过早，容易带来高延迟、额外成本和摘要质量漂移。

只要当前 session 目录存在 `compact.md`，后续每次请求模型前都会自动读取并注入。

注入顺序固定为：

```text
memory.md
  -> compact.md
  -> 最近对话消息
```

### 12.3 生成流程

```text
/full-compact
  -> 读取当前 Runner.history
  -> 构造 compact prompt
  -> 调用模型生成摘要
  -> 从模型输出中提取 <summary> 内部内容
  -> 如果已有 compact.md，先备份到 compact.bak.md
  -> 覆盖写入 temp/sessions/<session_id>/compact.md
```

注意：

- `/full-compact` 不调用工具。
- `/full-compact` 不把生成结果写入 `history.jsonl`。
- `/full-compact` 需要当前 Runner 已绑定 active session。

### 12.4 摘要 prompt 结构

Compact prompt 应要求模型输出：

```xml
<analysis>
这里允许模型分析，但最终不会保存。
</analysis>

<summary>
1. Primary Request and Intent:
...

2. Key Technical Concepts:
...

3. Files and Code Sections:
...

4. Errors and Fixes:
...

5. Problem Solving:
...

6. User Messages:
...

7. Pending Tasks:
...

8. Current Work:
...

9. Next Step:
...
</summary>
```

保存时去掉 `<analysis>`，只保留 `<summary>` 里的结果。

### 12.5 请求前自动注入

如果当前 session 目录下存在：

```text
temp/sessions/<session_id>/compact.md
```

Context Manager 会读取并注入为一条 assistant 消息：

```text
[Cohert compact summary]

<compact.md 内容>
```

如果 `compact.md` 超过 `MaxCompactSummaryChars`，只截断本轮请求副本，不修改磁盘文件，并追加：

```text
[Cohert compact summary truncated]
```

### 12.6 压缩请求本身过长

如果 compact 请求本身也太长，需要降级：

```text
第一次：丢弃最早 20% 历史后重试
第二次：再丢弃最早 20% 历史后重试
第三次：失败并提示用户
```

不要无限重试。

## 13. 第五层：Auto Compact 和熔断器

虽然参考文章是四层，但 Cohert 实现时可以把自动触发和熔断器放在同一个模块。

### 13.1 触发条件

估算：

```text
input_budget = model_context_window_tokens - max_output_tokens - safety_tokens
```

当：

```text
estimated_input_tokens > input_budget
```

触发压缩。

第一版先不自动调用模型 compact，只做：

```text
Micro Compact
  -> history group trim
  -> 如果仍然超限，返回 warning
```

### 13.2 熔断器

后续 Full Compact 自动化后，必须记录连续失败次数。

可以保存到：

```text
temp/sessions/<session_id>/context_state.json
```

结构：

```json
{
  "auto_compact_failures": 0,
  "last_compact_error": "",
  "last_compact_at": ""
}
```

规则：

```text
连续失败 >= 3
  -> 本 session 停止自动 compact
  -> 提示用户手动处理
```

这是为了避免压缩失败后每一轮都继续浪费模型调用。

## 14. 类型设计

建议新增：

```go
package contextmgr

type Config struct {
    MaxHistoryMessages      int
    KeepRecentToolResults   int
    MaxToolResultChars      int
    CompactedToolHeadChars  int
    CompactedToolTailChars  int
    MaxRequestChars         int
    ContextWindowTokens     int
    MaxOutputTokens         int
    SafetyTokens            int
    EnableMicroCompact      bool
    EnableSessionMemory     bool
    EnableFullCompact       bool
}

type Manager struct {
    Config Config
}

type BuildInput struct {
    Messages      []llm.Message
    SessionID     string
    SessionDir    string
    SystemPrompt  string
}

type BuildResult struct {
    Messages []llm.Message
    Stats    Stats
}

type Stats struct {
    OriginalMessages        int
    FinalMessages           int
    OriginalChars           int
    FinalChars              int
    TrimmedMessages         int
    CompactedToolResults    int
    OmittedToolResultChars  int
    InsertedMemory          bool
    InsertedCompactSummary  bool
    Warnings                []string
}
```

入口方法：

```go
func (m Manager) Build(input BuildInput) BuildResult
```

Runner 使用：

```go
requestMessages := append([]llm.Message(nil), r.history...)
if r.ContextManager != nil {
    result := r.ContextManager.Build(contextmgr.BuildInput{
        Messages: requestMessages,
        SessionID: r.sessionID,
        SessionDir: r.SessionStore.SessionDir(r.sessionID),
        SystemPrompt: r.SystemPrompt,
    })
    requestMessages = result.Messages
}
```

第一版也可以更简单：

```go
requestMessages := r.ContextManager.BuildMessages(r.history)
```

但建议保留 `BuildResult.Stats`，方便后续日志和测试。

## 15. Runner 接入方案

当前 Runner 里有两处构造 `messages`：

```go
messages := append([]llm.Message(nil), r.history...)
```

以及工具执行后：

```go
messages = append([]llm.Message(nil), r.history...)
```

接入后改成：

```go
messages := r.buildRequestMessages()
```

新增方法：

```go
func (r *Runner) buildRequestMessages() []llm.Message {
    messages := append([]llm.Message(nil), r.history...)
    if r.ContextManager == nil {
        return messages
    }
    result := r.ContextManager.Build(contextmgr.BuildInput{
        Messages: messages,
        SessionID: r.sessionID,
        SessionDir: r.sessionDir(),
        SystemPrompt: r.SystemPrompt,
    })
    return result.Messages
}
```

注意：

- `appendMessage` 仍然写完整历史。
- `SessionStore.AppendHistory` 仍然写完整消息。
- 只有 `Client.Chat` 看到的是裁剪后消息。

## 16. 工具调用协议保护

### 16.1 为什么要保护

OpenAI-compatible tool calling 对消息顺序有要求：

```text
assistant(tool_calls)
tool(result for call_id)
assistant(next answer)
```

如果裁剪成：

```text
tool(result for call_id)
assistant(next answer)
```

模型 API 可能认为 tool result 没有对应 tool call。

### 16.2 分组算法

伪代码：

```go
func groupMessages(messages []llm.Message) []MessageGroup {
    var groups []MessageGroup
    for i := 0; i < len(messages); i++ {
        msg := messages[i]
        if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
            group := MessageGroup{Messages: []llm.Message{msg}}
            needed := toolCallIDs(msg.ToolCalls)
            for i+1 < len(messages) && messages[i+1].Role == llm.RoleTool {
                next := messages[i+1]
                if needed[next.ToolCallID] {
                    group.Messages = append(group.Messages, next)
                    delete(needed, next.ToolCallID)
                    i++
                    continue
                }
                break
            }
            groups = append(groups, group)
            continue
        }
        groups = append(groups, MessageGroup{Messages: []llm.Message{msg}})
    }
    return groups
}
```

裁剪时：

```text
从后往前保留 group
直到消息数或字符数达到预算
```

### 16.3 异常历史处理

如果历史本身已经不完整，例如：

```text
tool result 找不到对应 assistant tool_calls
```

第一版处理：

- 保守删除这个孤立 tool result。
- 在 `Stats.Warnings` 记录。
- 不修改磁盘。

## 17. 请求预算策略

第一版使用双阈值：

```text
消息数量阈值
字符数量阈值
```

例如：

```yaml
max_history_messages: 40
max_request_chars: 100000
```

压缩顺序：

```text
1. 复制完整 messages
2. 压缩旧工具结果
3. 按 group 裁剪旧消息
4. 如果仍超过 max_request_chars，继续减少 group
5. 插入 context notice
6. 返回 BuildResult
```

不要一开始就追求精确 token，因为：

- tokenizer 引入成本高。
- 不同模型 tokenizer 不同。
- Cohert 当前优先是稳定 MVP。

## 18. 与 session 的关系

session 目录后续会变成：

```text
temp/sessions/<session_id>/
  meta.json
  history.jsonl
  memory.md
  memory.bak.md
  compact.md
  context_state.json
  run.log
```

文件职责：

| 文件 | 职责 |
| --- | --- |
| `meta.json` | 会话元信息 |
| `history.jsonl` | 完整消息历史 |
| `memory.md` | 结构化会话记忆 |
| `memory.bak.md` | 上一次 `/compact` 覆盖前的记忆备份 |
| `compact.md` | 模型生成的压缩摘要 |
| `context_state.json` | 自动 compact 状态和熔断信息 |
| `run.log` | 后续结构化运行日志 |

第一版只依赖：

```text
history.jsonl
```

第二版开始读取：

```text
memory.md
compact.md
```

## 19. 与开发记录文档的关系

`docs/开发记录文档.md` 是人看的开发过程记录。

它不应该进入模型上下文，除非用户明确让 Cohert 读取它。

Context Manager 管理的是 session 内部对话上下文，不管理项目文档。

后续如果实现项目规则文件，可以新增：

```text
COHERT.md
```

类似 Claude Code 的 `CLAUDE.md`，用于保存项目规则、命令、约定。

但这属于后续能力，不属于第一版 Context Manager。

## 20. 与浏览器工具的关系

浏览器工具会强依赖 Context Manager。

原因：

- 网页正文很长。
- DOM 很长。
- JS 执行结果可能很长。
- 页面变化摘要可能很长。

没有 Context Manager 就做浏览器工具，会很容易把模型上下文撑爆。

浏览器工具第一版必须遵守：

```text
browser_scan 返回内容先在工具层截断
Context Manager 再做请求前二次压缩
```

也就是两层保护：

```text
工具返回前限长
请求模型前压缩
```

## 21. 测试方案

### 21.1 工具结果压缩测试

覆盖：

- 短 tool result 不压缩。
- 超长 tool result 被压缩。
- 压缩后保留头尾。
- 最近 N 个 tool result 完整保留。
- 旧 tool result 被压缩。

### 21.2 group 裁剪测试

覆盖：

- 普通 user/assistant 消息裁剪。
- assistant tool_calls + tool result 成组保留。
- 多 tool_calls 对应多个 tool result。
- 孤立 tool result 被移除或告警。

### 21.3 请求预算测试

覆盖：

- 消息数超过 `max_history_messages`。
- 字符数超过 `max_request_chars`。
- 插入 context notice。
- `Stats` 统计正确。

### 21.4 Runner 接入测试

使用 fake LLM 验证：

- `history.jsonl` 写入完整消息。
- `Client.Chat` 收到的是裁剪后消息。
- 工具执行后下一轮仍经过 Context Manager。

### 21.5 resume 测试

覆盖：

- 从长 `history.jsonl` 恢复。
- 恢复后请求模型时被裁剪。
- 磁盘文件不被修改。

## 22. 开发阶段拆分

### 阶段一：确定性请求前压缩

任务：

```text
P0-020：定义 Context Manager 配置和类型
P0-021：实现工具结果 Micro Compact
P0-022：实现 group 裁剪
P0-023：Runner 请求前接入 Context Manager
P0-024：补充单元测试
P0-025：更新文档和开发记录
```

验收：

- `go test ./...` 通过。
- 长工具结果不会完整进入下一轮模型请求。
- session resume 长历史时不会直接把全部历史发给模型。
- `history.jsonl` 仍完整保存。

### 阶段二：Session Memory

任务：

```text
P0-026：定义 memory.md 格式
P0-027：Context Manager 注入 memory.md
P0-028：接入 /compact 生成 memory.md
P0-029：增加文档和测试
P0-030：增加 session memory 查看命令
```

验收：

- 有 `memory.md` 时会被注入模型请求。
- 没有 `memory.md` 时不影响正常运行。
- 注入内容不会写回 `history.jsonl`。
- `/compact` 会生成或更新当前 session 的 `memory.md`。

### 阶段三：手动 Full Compact

任务：

```text
P1-030：定义 compact prompt
P1-031：实现 /full-compact
P1-032：保存 compact.md
P1-033：resume 后注入 compact.md
P1-034：处理 compact 请求过长
```

验收：

- 用户可以手动压缩长 session。
- compact.md 是结构化 9 维摘要。
- compact 后继续对话时模型能理解旧任务状态。

### 阶段四：Auto Compact

任务：

```text
P1-040：实现上下文 token/字符预算估算
P1-041：超过阈值自动触发 compact
P1-042：实现 context_state.json
P1-043：实现连续失败熔断器
P1-044：补充自动 compact 测试
```

验收：

- 快爆上下文前自动处理。
- compact 连续失败不会无限重试。
- 用户能看到清晰错误提示。

## 23. 风险和取舍

### 23.1 精确 token 估算不做

第一版不用 tokenizer，会有估算误差。

取舍：

- 用保守字符预算规避风险。
- 后续再按模型适配 tokenizer。

### 23.2 压缩可能丢细节

工具结果压缩会丢中间内容。

取舍：

- `history.jsonl` 完整保留。
- 模型需要时可以再次调用 `file_read` 或 `code_run`。
- 最近工具结果完整保留。

### 23.3 Full Compact 可能摘要错误

模型摘要可能漏信息。

取舍：

- 第一版不自动摘要。
- 手动 compact 时保留完整 history。
- 摘要按 9 维度强约束，降低遗漏概率。

### 23.4 自动压缩可能死循环

如果 compact 自己也失败，可能重复浪费请求。

取舍：

- 必须有 `max_compact_failures`。
- compact 流程本身禁止再次触发 auto compact。

## 24. 最终效果

实现后，Cohert 的上下文行为会变成：

```text
完整历史：
  temp/sessions/<session_id>/history.jsonl

本地内存：
  Runner.history 完整消息

模型请求：
  Context Manager 压缩后的 messages
```

长会话示例：

```text
history.jsonl：300 条消息
其中包含大量 code_run/file_read/browser_scan 输出
```

发给模型时：

```text
system prompt
+ context notice
+ memory.md
+ compact.md
+ 最近若干组完整消息
+ 压缩后的旧工具结果
```

用户效果：

- 可以长期 resume。
- 不容易上下文爆掉。
- 工具输出不会无限撑大请求。
- 磁盘历史不丢。
- 后续浏览器工具可以更安全接入。

## 25. 推荐下一步

下一步建议直接实现阶段一：

```text
P0-020：定义 Context Manager 配置和类型
P0-021：实现工具结果 Micro Compact
P0-022：实现 group 裁剪
P0-023：Runner 请求前接入 Context Manager
P0-024：补充单元测试
```

不要先做 Full Compact，也不要先做浏览器工具。

原因：

- Micro Compact 成本最低。
- 当前 `session resume` 已经完成，长历史问题马上会出现。
- 浏览器工具会产生更长上下文，必须先有压缩层兜底。
- 第一阶段不调用模型，稳定、可测、风险低。
