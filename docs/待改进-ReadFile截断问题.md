# 待改进：ReadFile 大文件截断问题

> 记录时间：2026-06-29
> 涉及文件：[internal/tool/builtin/read_file.go](../internal/tool/builtin/read_file.go)
> 相关文件：[internal/tool/tool.go](../internal/tool/tool.go)、[internal/action/action.go](../internal/action/action.go)

---

## 问题描述

`ReadFileTool.Run()` 在文件超过 10000 字节时直接硬截断，且截断后报错的原始文件大小是错误的：

```go
// read_file.go 第 44-46 行
if len(data) > 10000 {
    data = data[:10000]
    return fmt.Sprintf("...（文件共 %d 字节）", len(data)+10000), nil  // ← BUG
}
```

**Bug**：`data` 被截断后 `len(data)` 变成 10000，`len(data)+10000` 永远 = 20000，实际文件大小信息丢失。

## 更深层的问题

截断本身是"替 LLM 做决策"，理想做法应该是**让 LLM 判断该保留什么**。

但受框架分层约束：
- **Tool 不能调 LLM**（Tool 是纯函数，LLM 是 Action 的能力）
- **Action 不能调 Action**（Action 之间只能通过 Message history 间接通信）

### 框架分层回顾

```
Role（调度层）
  ├── Action（LLM 驱动决策）  ← 可以 AskLLM + CallTool
  └── Tool（纯函数）          ← 只能做确定性操作
```

| 层 | 能调 LLM？ | 能调 Tool？ | 能调 Action？ |
|---|---|---|---|
| Role | 间接（通过 Action） | 否 | 可以调度 |
| Action | ✅ AskLLM | ✅ CallTool | ❌ 不能 |
| Tool | ❌ | 否 | ❌ |

## 改进方案

### 短期方案（修 Bug + 改进信息）

在 Tool 层面，不替 LLM 做决策，而是给足信息让调用方（Action）判断：

```go
func (t *ReadFileTool) Run(ctx context.Context, args map[string]any) (string, error) {
    // ... 参数校验不变 ...
    
    data, err := os.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("ReadFile: 读取 %s 失败: %w", path, err)
    }

    origLen := len(data)  // ★ 先保存原始长度
    
    if origLen > 10000 {
        return fmt.Sprintf(
            "⚠️ 文件过大（%d 字节），仅返回前 10000 字符预览:\n\n%s\n\n"+
            "💡 如需完整内容，可分段读取或使用 Action 层做 LLM 摘要。",
            origLen, string(data[:10000]),  // ★ 报真实大小
        ), nil
    }

    return fmt.Sprintf("文件内容（%d 字节）:\n\n%s", origLen, string(data)), nil
}
```

### 长期方案（Action 层智能摘要）

新建一个 Action，在同一个 Action 内部：先用 Tool 拿数据 → 用 AskLLM 判断是否要摘要 → 输出结果：

```go
// Action 内部可以多次调用 AskLLM，不违反分层约束
func (a *ReadAndSummarize) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
    // Step 1: 用 Tool 读取文件（纯函数，不调 LLM）
    content, _ := a.CallTool(ctx, "ReadFile", map[string]any{"path": path})
    
    // Step 2: 如果内容太大，在同一个 Action 内部调 LLM 做摘要
    if len(content) > 10000 {
        summary, _ := a.AskLLM(ctx, 
            "以下文件内容过长，请保留对后续任务有用的部分，其余做摘要:\n\n"+content, 
            history)
        return &action.ActionOutput{Content: summary}, nil
    }
    
    return &action.ActionOutput{Content: content}, nil
}
```

或者拆成两个独立的 Action，由 Role 的 `ByOrder` 模式串联：

```go
role.NewRole("Reader",
    role.WithActions(readFileAction, summarizeAction),
    // ReadFile 产出 → history → Summarize 消费
)
```

## 结论

| 做法 | 可行？ | 理由 |
|------|--------|------|
| Tool 直接调 LLM | ❌ | 破坏 Tool 纯函数定义 |
| Action 调另一个 Action | ❌ | Action 彼此无感知，只能通过 history 解耦 |
| Tool 返回完整信息 + Action 层做摘要 | ✅ | 不破坏任何分层约束 |
| 同一 Action 内多次 AskLLM | ✅ | Action 本身就有 LLM 能力 |

**下一步**：先修 Tool 层 Bug（报正确的文件大小），再在 Action 层加 Summarize 能力。
