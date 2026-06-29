# 待实现：React 三种模式完成度分析

> 记录时间：2026-06-29
> 涉及文件：[internal/role/role.go](../internal/role/role.go#L245-L364)
> 相关文件：[internal/action/action.go](../internal/action/action.go)、[internal/action/node.go](../internal/action/node.go)

---

## 现状总览

Role 定义了 3 种 React 模式，但只有 1 种真正可用：

| 模式 | 常量 | 完成度 | 实际行为 |
|------|------|:--:|------|
| `ReactByOrder` | 0 | ✅ 完成 | 按 actions 列表顺序依次执行，state 逐次递增 |
| `ReactReAct` | 1 | 🔸 壳 | `selectAction()` 直接返回 `r.state`，**没调 LLM** |
| `ReactPlanAndAct` | 2 | 🔴 空壳 | 一行 `return r.reactByOrder(ctx)`，完全回退 |

`react()` 入口分发（[role.go:249](internal/role/role.go#L249)）：

```go
func (r *Role) react(ctx context.Context) (*foundation.Message, error) {
    ctx = context.WithoutCancel(ctx)  // ★ 保护 LLM 调用不被 env 轮询超时打断
    switch r.reactMode {
    case ReactByOrder:
        return r.reactByOrder(ctx)
    case ReactReAct:
        return r.reactReAct(ctx)
    case ReactPlanAndAct:
        return r.reactPlanAndAct(ctx)
    }
}
```

`context.WithoutCancel` 是写好了的——说明作者考虑了 LLM 调用中途不能被 cancel，但实际 LLM 调用还没写。

---

## 一、ReactByOrder — 唯一可用的模式 ✅

[role.go:271](internal/role/role.go#L271)

```go
func (r *Role) reactByOrder(ctx context.Context) (*foundation.Message, error) {
    if r.state >= len(r.actions) {
        r.state = -1  // 所有 action 执行完毕 → idle
        return nil, nil
    }
    act := r.actions[r.state]
    r.injectToolsToAction(act)
    output, _ := act.Run(ctx, history)
    r.state++
    if r.state >= len(r.actions) { r.state = -1 }
    // 组装 Message（CauseBy=act.Name(), SendTo=<all>）
}
```

**逻辑完整**：state 从 0 开始，每次收到消息执行一个 Action，全部执行完 state=-1（idle）。所有 demo 都用这个模式。

---

## 二、ReactReAct — 壳写好了，LLM 选择逻辑是空的 🔸

[role.go:318](internal/role/role.go#L318)

```go
func (r *Role) reactReAct(ctx context.Context) (*foundation.Message, error) {
    if len(r.actions) == 0 {
        r.state = -1
        return nil, nil
    }
    history := r.memory.Get(10)
    selectedIdx := r.selectAction(ctx, history)  // ← 应该让 LLM 选，实际没调
    act := r.actions[selectedIdx]
    output, err := act.Run(ctx, r.memory.Get(0))
    // 组装 Message...
}
```

**问题在 `selectAction()`**（[role.go:351](internal/role/role.go#L351)）：

```go
func (r *Role) selectAction(ctx context.Context, history []*foundation.Message) int {
    // 如果有 LLM 客户端（第一个 action 的），尝试让 LLM 选择
    // 否则直接按索引顺序
    if r.state >= 0 && r.state < len(r.actions) {
        return r.state  // ← 直接返回当前索引，跟 ByOrder 没区别！
    }
    return 0
}
```

### 缺失的核心逻辑

```
1. 构建 prompt：
   - Role 身份（Name + Profile + Goal + Constraints）
   - 最近 N 条历史消息（already in history）
   - 可用 Action 列表（名称 + 描述）
   - 问 LLM："下一步应该执行哪个 Action？"

2. 调用 LLM（需要拿到第一个 Action 的 client，或 Role 自带一个 client）

3. 解析 LLM 返回的 action name → 匹配到 actions 数组的索引

4. 如果 LLM 认为任务完成 → state = -1（idle）
```

伪代码：

```go
func (r *Role) selectAction(ctx context.Context, history []*foundation.Message) int {
    // 1. 构建可用 Action 列表 prompt
    actionList := BuildActionListPrompt(r.actions)

    // 2. 构建思考 prompt
    prompt := fmt.Sprintf(`你是 %s（%s）。
目标：%s
历史对话：
%s
可用操作：
%s
请选择下一步应执行的操作名称，如果任务已完成请回复 "DONE"。`,
        r.Name, r.Profile, r.Goal,
        FormatHistory(history),
        actionList,
    )

    // 3. 调 LLM 选择（用第一个 Action 的 client）
    //    注意：这里需要拿到 LLM client，当前 Role 没有直接持有
    client := r.actions[0].(llmClientProvider).GetClient()  // 需要暴露接口
    resp, _ := client.Chat(ctx, ...)

    // 4. 解析 LLM 回复 → 匹配 action name → 返回索引
    for i, act := range r.actions {
        if strings.Contains(resp.Content, act.Name()) {
            return i
        }
    }
    return -1  // DONE 或无法匹配 → 标记完成
}
```

### 额外需要的改动

**Role 需要能拿到 LLM client**。当前 Role 不直接持有 client，client 藏在 `BaseAction.client` 里。方案：

- 方案 A：给 `Action` 接口加 `LLMClient() llm.Client` 方法，`BaseAction` 实现它
- 方案 B：给 `Role` 加一个 `thinkingClient llm.Client` 字段，通过 `WithThinkingClient()` 选项注入
- 方案 C：直接用 `r.actions[0]` 的 client（如果 Action 暴露了 `LLMClient()`）

**推荐方案 A**，改动最小：

```go
// action/action.go — Action 接口加一个可选方法
type LLMClientProvider interface {
    LLMClient() llm.Client
}

// BaseAction 实现
func (a *BaseAction) LLMClient() llm.Client { return a.client }
```

---

## 三、ReactPlanAndAct — 完全是空壳 🔴

[role.go:362](internal/role/role.go#L362)

```go
func (r *Role) reactPlanAndAct(ctx context.Context) (*foundation.Message, error) {
    return r.reactByOrder(ctx) // 暂时回退到 byOrder
}
```

### 缺失的完整流程

这是 MetaGPT 最核心的模式，完整流程应该是：

```
收到消息
  │
  ├─→ Step 1: 规划（Plan）
  │     - 构建 prompt：Role 身份 + 目标 + 历史
  │     - 让 LLM 生成步骤列表（JSON 格式）
  │     - 存入 r.todo（可用 r.actions 的动态副本）
  │
  ├─→ Step 2: 执行（Act）
  │     - 按计划逐步执行
  │     - 每步执行后记录结果
  │
  ├─→ Step 3: 反思（Reflect）
  │     - 每步后让 LLM 评估：是否按预期？需要调整计划吗？
  │     - 如果需要调整 → 回到 Step 1（重新规划剩余步骤）
  │
  └─→ Step 4: 完成
        - 所有步骤执行完毕 → state = -1
```

伪代码：

```go
func (r *Role) reactPlanAndAct(ctx context.Context) (*foundation.Message, error) {
    // 初始化：如果还没规划，先规划
    if r.todo == "" {
        plan, err := r.generatePlan(ctx)  // LLM 生成步骤列表
        if err != nil { return nil, err }
        r.todo = plan  // 存为 JSON：[{"action":"WritePRD","args":{...}}, ...]
    }

    // 解析计划，执行下一个步骤
    steps := parsePlan(r.todo)
    if r.state >= len(steps) {
        r.state = -1  // 全部完成
        return nil, nil
    }

    step := steps[r.state]
    act := r.findActionByName(step.ActionName)
    output, err := act.Run(ctx, r.memory.Get(0))

    // 反思：这一步符合预期吗？
    if r.shouldReflect(ctx, output) {
        r.todo = ""  // 清空计划，下次重新规划
        r.state = 0
    } else {
        r.state++
    }

    return &foundation.Message{...}, nil
}
```

### 需要新增的结构

```go
// PlanStep 计划中的一个步骤
type PlanStep struct {
    ActionName string         `json:"action"`
    Args       map[string]any `json:"args,omitempty"`
    Expect     string         `json:"expect"`  // 预期产出
}

// PlanResult 计划执行结果
type PlanResult struct {
    Steps   []PlanStep `json:"steps"`
    Reason  string     `json:"reason"`
}
```

以及 Role 需要新字段：

```go
type Role struct {
    // ... existing fields ...
    todo string  // ★ 已存在但未使用（见 RoleContext.Todo）
}
```

---

## 四、相关：同样未完成的其他模块

这些小模块代码写好了但没 demo、没接入流程：

| 模块 | 文件 | 状态 |
|------|------|:--:|
| `MockClient` | [llm/mock.go](../internal/llm/mock.go) | 写好了，零 demo。离线开发/测试的关键工具 |
| `EchoResponder` | [llm/mock.go:166](../internal/llm/mock.go#L166) | 同上，可用于验证 Action prompt 构建逻辑 |
| `ActionNode` | [action/node.go](../internal/action/node.go) | 结构化提取（JSON/正则），`BaseAction.SetNode()` 写了但没人调过 |
| `RoleContext` | [role/context.go](../internal/role/context.go) | 为序列化/断点恢复准备，`Role` 结构体没用它，仍用内联字段 |

---

## 五、实现优先级建议

| 优先级 | 内容 | 理由 |
|:--:|------|------|
| 1 | **ReactReAct 的 `selectAction` 真实现** | 代码骨架全在，只剩让 LLM 选 action name 这一步。先让 Role 能拿到 client（加接口方法），再写 prompt + 解析逻辑。预估 1-2 小时 |
| 2 | **MockClient 补 demo** | 有了它就能不花 API 钱验证 ReAct 逻辑。可以在现有 `cmd/demo/` 里加一小段。预估 15 分钟 |
| 3 | **ReactPlanAndAct 完整实现** | 工程量最大：需要规划 prompt、JSON 解析、反思循环。等同 MetaGPT 的 Planner 模块。预估 1-2 天 |
| 4 | **ActionNode 接入** | 让 WritePRD 等 Action 产出结构化 JSON，而不是纯 Markdown。依赖 PlanAndAct 的规划结果来驱动。预估 30 分钟 |

---

## 六、结论

三个 React 模式里，`ReactByOrder` 是唯一完整的。`ReactReAct` 的**调度骨架**写好了（react 分发 → selectAction → 执行 → publish），缺的只是 `selectAction` 里调 LLM + 解析结果这两步。`ReactPlanAndAct` 则完全是占位符，等同于还没开始做。

当前所有 demo 只用 `ReactByOrder`，不影响继续开发。如果需要 Agent 能动态决策"下一步做什么"，优先把 `ReactReAct` 补完。
