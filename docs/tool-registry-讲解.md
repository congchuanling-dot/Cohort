# Tool Registry 代码讲解

> 文件: `internal/foundation/tool/registry.go`

## 整体设计思想

这个文件是 **Tool 层** 的核心基础设施——它定义了一套**不依赖 LLM 的纯函数式工具系统**。

### 核心区分：Tool vs Action

```
Tool（纯函数）                  Action（需要 LLM）
┌─────────────────┐            ┌─────────────────┐
│ ReadFile         │            │ AnalyzeCode      │
│ WriteFile        │            │ GenerateReport   │
│ RunCommand       │            │ AskLLM           │
│ GoFmt            │            │                  │
│                  │            │                  │
│ Run() 直接执行    │            │ 核心方法是 AskLLM  │
│ 不调 LLM         │            │ 必须调 LLM        │
└─────────────────┘            └─────────────────┘
```

Tool 是 **给 LLM 用的手和脚**，Action 是 **LLM 的大脑**。

### 架构分层

```
Environment (全局)
  ├── ToolRegistry (公有)     ← 所有 Role 共享的工具
  │     ├── ReadFile
  │     ├── WriteFile
  │     └── RunCommand
  │
  └── Role A                     Role B
        ├── ToolRegistry (私有)    ├── ToolRegistry (私有)
        │     └── 自己的工具         │     └── 自己的工具
        └── Actions               └── Actions
```

关键设计：**公有工具注册在 Environment，私有工具注册在 Role**。通过 `Merge()` 方法可以把公有工具注入到 Role 的私有表中，实现"基类工具 + 特化工具"的组合。

---

## 逐段讲解

### 1. Tool 接口

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]string
    Run(ctx context.Context, args map[string]any) (string, error)
}
```

设计要点：
- **`Description()` 和 `Parameters()` 是给 LLM 看的**——这些信息会被注入到 system prompt 中，让 LLM 知道"我有哪些工具、怎么用"
- **`Run()` 接收 `map[string]any`**——用通用 map 而非结构体，因为不同工具参数个数和类型各不相同。返回值是 `string`，这也是直接给 LLM 看的文本
- **接收 `context.Context`**——支持超时控制和取消传播

### 2. ToolRegistry 结构

```go
type ToolRegistry struct {
    mu    sync.RWMutex    // 读写锁，保证并发安全
    tools map[string]Tool // name → Tool 的映射
}
```

**`sync.RWMutex`**：读写互斥锁。
- 多个 goroutine 可以同时读（`RLock`）
- 写操作独占（`Lock`）
- 适合 **读多写少** 的场景——工具通常在初始化阶段注册，运行时大量查询

### 3. Register —— 重名 panic

```go
func (r *ToolRegistry) Register(t Tool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, exists := r.tools[t.Name()]; exists {
        panic(fmt.Sprintf("tool: %q already registered", t.Name()))
    }
    r.tools[t.Name()] = t
}
```

**为什么重名要 panic 而不是静默覆盖？**
- 工具名是 LLM 调用的唯一标识，重名意味着配置错误
- **fail-fast**：在启动阶段就暴露问题，而不是运行时静默覆盖导致不可预测的行为
- 这是 Go 标准库的惯用法（`http.HandleFunc` 重名也 panic）

### 4. Get —— 返回 nil

```go
func (r *ToolRegistry) Get(name string) Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.tools[name]
}
```

返回 nil 而不是 `(Tool, bool)`。调用方通过 `Call()` 统一处理不存在的情况。

### 5. Call —— 统一调用入口

```go
func (r *ToolRegistry) Call(ctx context.Context, name string, args map[string]any) (string, error) {
    t := r.Get(name)
    if t == nil {
        return "", fmt.Errorf("tool %q not found", name)
    }
    return t.Run(ctx, args)
}
```

这是 LLM 调用工具的**唯一入口**——LLM 只需知道工具名和参数，由 Registry 负责查找和转发。

### 6. Merge —— 组合而非继承

```go
func (r *ToolRegistry) Merge(other *ToolRegistry) {
    // ...
    for name, t := range other.tools {
        if _, exists := r.tools[name]; !exists {
            r.tools[name] = t
        }
    }
}
```

**"重名不覆盖"** 的设计原因：私有工具的优先级 > 公有工具。Role 可以先注册自己的特化版本，再用 `Merge` 把公有工具补进来，自己的不会被覆盖。

### 7. ToolsInfo —— 生成 System Prompt

```go
func (r *ToolRegistry) ToolsInfo() string {
    // 输出格式：
    // ## Available Tools
    // 1. **ReadFile**: 读取文件内容
    //    Parameters: path (文件路径)
```

这个方法把注册表中的所有工具格式化成 LLM 能理解的 Markdown 文本。**按名称排序保证输出稳定**——这对 LLM 很重要，因为 stable prompt → stable behavior。

---

## 关键 Go 语法

| 语法 | 用途 |
|------|------|
| `sync.RWMutex` | 读写锁，读多写少场景用 |
| `defer r.mu.Unlock()` | 函数返回前自动解锁，防止忘记 |
| `map[string]Tool` | name → 接口实例映射 |
| `fmt.Sprintf("%q", name)` | `%q` 给字符串加引号，输出如 `"ReadFile"` |
| `strings.Builder` | 高效拼接字符串，避免 `+=` 产生大量临时字符串 |
| `make([]string, 0, len(r.tools))` | 预分配 slice 容量，避免动态扩容 |
| `sort.Strings(names)` | 原地排序字符串 slice |

---

## 设计模式总结

1. **注册表模式**（Registry Pattern）—— 集中管理、按名查找
2. **策略模式**（Strategy Pattern）—— Tool 接口，每个具体工具是一种策略
3. **模板方法**—— `ToolsInfo()` 是所有工具的"自描述"模板，具体内容由各工具实现提供
