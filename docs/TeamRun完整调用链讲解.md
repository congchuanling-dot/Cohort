# Team.Run() 完整调用链讲解

> 以 `cmd/demo_duo/main.go` 为例：`t.Run(ctx, "写一个网页版 2048 游戏的需求文档，面向移动端用户")`

## 一句话概括

```
用户输入一句话 → Team 广播 → Environment 路由 → Role 收消息 → Action 调 LLM → 产出 Message → 再路由 → 下一个 Role 再执行 → 循环直到大家都干完
```

---

## 完整调用链（按执行顺序）

### Step 1：Team.Run() 发布用户需求

**文件**：[team.go:85-115](internal/team/team.go#L85-L115)

```go
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
    // ① 把用户输入包装成一条 framework.Message
    t.env.PublishMessage(foundation.NewUserMessage(idea))
    //     ↓ NewUserMessage 做了什么？
    //     ↓ [foundation/message.go:55]
    //     ↓ 创建 Message{Role:"user", CauseBy:"UserRequirement", SendTo:["<all>"]}
```

### Step 2：Environment.PublishMessage() 广播给所有人

**文件**：[environment.go:82-105](internal/env/environment.go#L82-L105)

```go
func (e *Environment) PublishMessage(msg *foundation.Message) {
    e.history.Add(msg)  // 存进全局历史

    // 遍历所有注册的 Role（Alice、Bob）
    for name, r := range e.roles {
        if msg.ShouldSendTo(name) {   // SendTo 包含 "<all>" → 所有人都匹配
            select {
            case r.MessageBuffer() <- msg:  // 投递到 Role 的 channel 邮箱
            default:
                // channel 满了就丢弃（防阻塞）
            }
        }
    }
}
```

此时 **Alice 和 Bob 的 channel 邮箱里各有一份消息**。

### Step 3：Team 主循环，每轮调用 Environment.Run()

**文件**：[team.go:92-106](internal/team/team.go#L92-L106)

```go
for round := 0; round < t.nRound; round++ {
    t.env.Run(ctx)       // ← 每轮：并发启动所有非空闲 Role
    if t.env.IsAllIdle() {   // 大家都干完了就提前结束
        break
    }
}
```

### Step 4：Environment.Run() 并发启动每个 Role

**文件**：[environment.go:117-161](internal/env/environment.go#L117-L161)

```go
func (e *Environment) Run(ctx context.Context) error {
    // 找出所有还活着的 Role（state != -1）
    active := ...

    // 每个 Role 启动一个 goroutine
    for _, r := range active {
        go r.Run(ctx)   // ← 并发！
    }

    // 轮询等待所有 Role 处理完当前消息（最长等 3 分钟）
    for time.Now().Before(deadline) {
        if e.IsAllIdle() { break }
    }

    cancel()  // 通知 goroutine 退出
    wg.Wait() // 等全部退出后返回
}
```

关键设计：**Environment 不知道 Role 在干什么，它只管"启动"和"等待空闲"**。

### Step 5：Role.Run() —— 整个框架的心脏

**文件**：[role.go:196-239](internal/role/role.go#L196-L239)

```go
func (r *Role) Run(ctx context.Context) error {
    for {
        select {
        case msg := <-r.msgBuffer:   // 阻塞等消息

            // === 第 1 步：Observe（观察） ===
            if !r.shouldObserve(msg) { continue }  // 不关心的消息跳过
            r.memory.Add(msg)    // 存到自己的记忆

            // === 第 2+3 步：Think + Act（思考+执行） ===
            rsp, err := r.react(ctx)

            // === 第 4 步：Publish（发布产出） ===
            if rsp != nil {
                r.env.PublishMessage(rsp)  // 又回到 Environment，路由给其他人
            }
        }
    }
}
```

核心循环 = **observe → react → publish**，模拟人类认知过程。

---

## 关键分支：Alice 和 Bob 的不同行为

### Alice（PM）收到消息后的执行路径

由于 Alice 设置了 `WithWatch("UserRequirement")`：
- **第 1 轮**收到的 msg 是 `UserRequirement` → `shouldObserve` 返回 true → 执行
- **第 2 轮**收到 Bob 的 review（`WriteCodeReview`）→ `shouldObserve` 返回 false → 跳过

`react()` → `reactByOrder()`（默认模式）：

```go
func (r *Role) reactByOrder(ctx context.Context) (*foundation.Message, error) {
    act := r.actions[r.state]     // 取第 state 个 action（Alice 只有 1 个：WritePRD）
    history := r.memory.Get(0)    // 取全部历史消息
    output, err := act.Run(ctx, history)  // ← 调用 WritePRD.Run()
    r.state++                     // 执行完 state 指向下一个

    if r.state >= len(r.actions) {
        r.state = -1              // 所有 action 执行完 → 标记为空闲
    }

    return &foundation.Message{
        Content:  output.Content,          // PRD 内容
        CauseBy:  "WritePRD",              // ← 这条消息的"来源"标记
        SentFrom: "Alice",                 // 谁写的
        SendTo:   []string{"<all>"},       // 广播给所有人
    }, nil
}
```

### WritePRD.Run() —— 实际调用 LLM

**文件**：[write_prd.go:39-54](internal/action/builtin/write_prd.go#L39-L54)

```go
func (a *WritePRD) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
    userReq := extractUserRequirement(history)   // 从历史里找到 UserRequirement 的内容
    prompt := "Please write a detailed PRD based on:\n\n" + userReq
    content, err := a.AskLLM(ctx, prompt, history)   // ← 调 LLM
    return &action.ActionOutput{Content: content}, nil
}
```

### AskLLM() —— 构建 prompt → 调 API

**文件**：[action.go:102-130](internal/action/action.go#L102-L130)

```go
func (a *BaseAction) AskLLM(ctx context.Context, prompt string, history []*foundation.Message) (string, error) {
    messages := []llm.ChatMessage{
        {Role: llm.RoleSystem, Content: a.prefix},   // System prompt（PRD 模板）
    }
    // 把历史消息也塞进去（给 LLM 上下文）
    for _, msg := range history {
        messages = append(messages, llm.ChatMessage{Role: msg.Role, Content: msg.Content})
    }
    // 追加用户当前问题
    messages = append(messages, llm.ChatMessage{Role: llm.RoleUser, Content: prompt})

    resp, err := a.client.Chat(ctx, messages)  // → DeepSeek API
    return resp.Content, nil
}
```

**发给 DeepSeek 的请求长这样：**

```json
{
  "model": "deepseek-chat",
  "messages": [
    {"role": "system", "content": "You are a professional Product Manager... (PRD模板)"},
    {"role": "user", "content": "写一个网页版 2048 游戏的需求文档，面向移动端用户"},
    {"role": "user", "content": "Please write a detailed PRD based on..."}
  ],
  "temperature": 0.3,
  "max_tokens": 1024
}
```

### Bob（Reviewer）收到消息后的执行路径

当 Alice 的 PRD 产出消息发布后（`CauseBy: "WritePRD"`）：
- Environment 再次广播 → Alice 和 Bob 都收到
- Alice：`shouldObserve("WritePRD")` → 她不 watch 这个 → 跳过
- Bob：`WithWatch("WritePRD")` → 他 watch 这个 → 执行！

Bob 的 `reactByOrder()` 调用 `WriteCodeReview.Run()`，流程一样。

---

## 完整时序图

```
用户输入: "写一个 2048 游戏需求文档"
    │
    ▼
Team.Run()
    │
    ├─► PublishMessage(NewUserMessage)           ← 包装成 Message{CauseBy:"UserRequirement"}
    │       │
    │       ▼
    │   Environment.PublishMessage()
    │       ├─► history.Add(msg)                 ← 存历史
    │       ├─► Alice.msgBuffer ← msg            ← 投递到 Alice 邮箱
    │       └─► Bob.msgBuffer ← msg              ← 投递到 Bob 邮箱
    │
    ├─► Round 1: env.Run()
    │       │
    │       ├─► goroutine: Alice.Run()
    │       │     ├─ msg = ← msgBuffer           ← 收到 UserRequirement
    │       │     ├─ shouldObserve? Yes (watch UserRequirement)
    │       │     ├─ react() → reactByOrder()
    │       │     │    └─ WritePRD.Run(history)
    │       │     │         └─ AskLLM() → client.Chat(messages) → DeepSeek API
    │       │     │              └─ 返回 PRD 内容
    │       │     ├─ r.state = -1                ← Alice 干完了，标记空闲
    │       │     └─ PublishMessage({CauseBy:"WritePRD", Content:PRD, SentFrom:"Alice"})
    │       │
    │       └─► goroutine: Bob.Run()
    │             ├─ msg = ← msgBuffer           ← 收到 UserRequirement
    │             ├─ shouldObserve? No (只 watch WritePRD)
    │             └─ 跳过，等待下一轮
    │
    │   等待 500ms... Alice idle ✓  Bob 还是 idle（没执行任何 action）
    │   IsAllIdle? → Yes（Bob state 还是 0 但因为没匹配消息所以...）
    │   
    │   实际上这里有个微妙点：Alice 产出 PRD 消息后，
    │   PublishMessage 会立即投递到 Bob 的 msgBuffer。
    │   Bob 在同一个 Run() 循环内就能收到这条消息。
    │
    ├─► Round 2: env.Run()
    │       │
    │       ├─► Alice: IsIdle()=true → 不启动
    │       │
    │       └─► goroutine: Bob.Run()
    │             ├─ msg = ← msgBuffer           ← 收到 Alice 的 PRD (WritePRD)
    │             ├─ shouldObserve? Yes (watch WritePRD)
    │             ├─ react() → reactByOrder()
    │             │    └─ WriteCodeReview.Run(history)
    │             │         └─ AskLLM() → client.Chat() → DeepSeek API
    │             │              └─ 返回评审意见
    │             ├─ r.state = -1                ← Bob 也干完了
    │             └─ PublishMessage({CauseBy:"WriteCodeReview", Content:评审, SentFrom:"Bob"})
    │
    ├─► Round 3: env.Run()
    │      IsAllIdle()? → Yes（Alice state=-1, Bob state=-1）→ break
    │
    └─► return env.History()                    ← 返回全部消息历史
```

---

## 关键设计理解

### 1. 三层抽象各管各的

| 层 | 类型 | 职责 |
|---|---|---|
| 编排层 | `Team` | 创建用户需求、控制轮次、返回结果 |
| 路由层 | `Environment` | 消息发布 → 投递到各 Role 的邮箱 |
| 执行层 | `Role` | observe → react → publish 循环 |
| 动作层 | `Action` | 构造 prompt → 调 LLM API |

### 2. Channel 邮箱模式

Role 之间**不直接通信**，全部通过 Environment 中转：

```
Role A → PublishMessage(msg) → Environment → Role B.msgBuffer ← Role B 收消息
```

### 3. 并发但同步

每轮内所有 Role 并发执行（各自 goroutine），但 Team 在**每轮结束**时会等所有 Role 空闲才进入下一轮。这就是为什么 `maxRound=3` 能控制总执行次数。

### 4. watch 机制实现分工

- Alice watch: `["UserRequirement"]` → 只响应用户输入
- Bob watch: `["WritePRD"]` → 只响应 PRD 产出

不需要硬编码"先执行 Alice 再执行 Bob"，而是**通过消息的 CauseBy 字段自动路由**。

---

## 相关文件索引

| 文件 | 内容 |
|---|---|
| [cmd/demo_duo/main.go](cmd/demo_duo/main.go) | Demo 入口，组装 Team |
| [internal/team/team.go](internal/team/team.go) | Team 编排器 |
| [internal/env/environment.go](internal/env/environment.go) | Environment 消息路由 |
| [internal/role/role.go](internal/role/role.go) | Role Agent 核心 |
| [internal/action/action.go](internal/action/action.go) | BaseAction + AskLLM |
| [internal/action/builtin/write_prd.go](internal/action/builtin/write_prd.go) | WritePRD Action |
| [internal/llm/client.go](internal/llm/client.go) | LLM Client 接口 |
| [internal/llm/provider_openai.go](internal/llm/provider_openai.go) | Chat() HTTP 实现 |
| [internal/llm/provider_deepseek.go](internal/llm/provider_deepseek.go) | DeepSeek 适配器 |
| [internal/foundation/message.go](internal/foundation/message.go) | Message 定义 + 工厂函数 |
