# Cohort 执行流程深度讲解：从 Team.Run() 到 Role.Run()

> 以 `cmd/demo_duo/main.go` 为例，逐层拆解 `t.Run(ctx, "写一个 2048 游戏...")` 到底发生了什么。

---

## 一、先搞清楚三层架构

```
┌─────────────────────────────────────────┐
│  Team        ← 总指挥                    │
│  "循环 N 轮，每轮让 Environment 跑一次"   │
│                                          │
│  ┌─────────────────────────────────┐    │
│  │  Environment  ← 消息中心         │    │
│  │  两个能力：                      │    │
│  │    ① PublishMessage  投递消息    │    │
│  │    ② Run() 启动所有 Role         │    │
│  │                                  │    │
│  │  ┌──────────┐  ┌──────────┐     │    │
│  │  │  Alice   │  │   Bob    │     │    │
│  │  │  (PM)    │  │ (Review) │     │    │
│  │  │          │  │          │     │    │
│  │  │ Role.Run() 死循环         │    │    │
│  │  │ "等消息→干活→发布→再等"    │    │    │
│  │  └──────────┘  └──────────┘     │    │
│  └─────────────────────────────────┘    │
└─────────────────────────────────────────┘
```

**类比：**
- Team = 项目经理，说"我们开 3 轮会，每轮大家同时干活"
- Environment = 办公室 + 公告板，消息贴上去，通知相关人
- Role = 员工，盯着公告板（channel），看到自己的任务就干活

---

## 二、Team.Run() —— 总指挥

[team.go:85-115](internal/team/team.go#L85-L115)

```go
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
    // ❶ 把用户输入打包成一条 Message，广播出去
    t.env.PublishMessage(foundation.NewUserMessage(idea))

    // ❷ 循环 N 轮
    for round := 0; round < t.nRound; round++ {
        t.env.Run(ctx)           // ← 让 Environment 跑一轮
        if t.env.IsAllIdle() {   // ← 大家都干完了？提前下班
            break
        }
    }

    // ❸ 返回历史消息
    return t.env.History(), nil
}
```

### 每一步在干什么？

**❶ `PublishMessage(NewUserMessage(idea))`**

把用户输入 `"写一个 2048 游戏..."` 包成：

```go
Message{
    ID:       "msg_xxxxx",
    Content:  "写一个网页版 2048 游戏的需求文档，面向移动端用户",
    Role:     "user",
    CauseBy:  "UserRequirement",    // ← 这个标记很重要！Role 靠它判断是否关注
    SentFrom: "User",
    SendTo:   []string{"<all>"},    // ← 发给所有人
}
```

然后 Environment 把这个消息投递到**所有 Role 的 channel 邮箱**里。

**❷ for 循环**

```
Round 1: env.Run() → 启动 Alice 和 Bob 的 goroutine → 等他们干活 → cancel 终止
Round 2: env.Run() → 再次启动 → cancel 终止
Round 3: env.Run() → 大家都 idle → 不启动了 → break
```

**❸ 返回历史**

所有消息（用户需求、PRD、评审意见）都在 `env.History()` 里，上层遍历输出。

---

## 三、Environment —— 消息中心 + 协程管理器

[environment.go](internal/env/environment.go)

Environment 做了两件事：

### 3.1 PublishMessage() —— 投递消息

```go
func (e *Environment) PublishMessage(msg *foundation.Message) {
    e.history.Add(msg)           // ① 存档

    for name, r := range e.roles {
        if msg.ShouldSendTo(name) {     // ② 判断：这条消息应该发给这个 Role 吗？
            select {
            case r.MessageBuffer() <- msg:   // ③ 非阻塞投递到 channel
            default:
                // channel 满了，丢弃（防止慢消费者拖死整个系统）
            }
        }
    }
}
```

**ShouldSendTo 的逻辑**（[foundation/message.go:81](internal/foundation/message.go#L81)）：
- `SendTo` 包含 `"<all>"` → 发给所有人
- `SendTo` 包含这个 Role 的名字 → 发给它
- `SendTo` 包含 `"*"` → 发给它

**关键设计：非阻塞投递。** channel 满了就丢弃，防止一个 Role 卡住导致发布者阻塞。

### 3.2 Run() —— 启动所有活跃 Role 跑一轮

这是最复杂、也是问题最多的地方。

```go
func (e *Environment) Run(ctx context.Context) error {
    // ① 找出还没干完活的 Role
    active := ...
    for _, r := range e.roles {
        if !r.IsIdle() {          // state != -1
            active = append(active, r)
        }
    }

    if len(active) == 0 { return nil }  // 都干完了，啥也不做

    // ② 创建新的 ctx（可手动取消）
    ctx, cancel := context.WithCancel(ctx)

    // ③ 每个活跃 Role 启动一个 goroutine
    for _, r := range active {
        go r.Run(ctx)   // ← Role.Run() 是死循环！
    }

    // ④ 轮询等待，每隔 500ms 看一次大家都干完没
    for time.Now().Before(deadline) {
        time.Sleep(500 * time.Millisecond)
        if e.IsAllIdle() { break }
    }

    // ⑤ 强行终止所有 goroutine
    cancel()    // 触发 ctx.Done()，Role.Run() 里的 select 收到信号退出
    wg.Wait()   // 等 goroutine 真正退出
    return nil
}
```

**问题就在这里：`cancel()` 强行终止。**

因为 `Role.Run()` 是死循环（永远不会自己返回），Environment 只能通过 `cancel()` 强制终止它。这就是"反复启动-停止"的根源。

---

## 四、Role.Run() —— 为什么是个死循环？

[role.go:196-239](internal/role/role.go#L196-L239)

```go
func (r *Role) Run(ctx context.Context) error {
    for {                                  // ← 死循环！
        select {
        case <-ctx.Done():                 // ← 唯一的出口：被 cancel 了
            return ctx.Err()

        case msg := <-r.msgBuffer:         // ← 阻塞等消息
            // ① Observe：这条消息我关注吗？
            if !r.shouldObserve(msg) {
                continue                   // 不关注 → 跳过，继续等下一条
            }
            r.memory.Add(msg)              // 存到记忆

            // ② Think + Act：执行 Action
            rsp, _ := r.react(ctx)

            // ③ Publish：产出发布出去
            if rsp != nil {
                r.env.PublishMessage(rsp)
            }
        }
    }
}
```

### 设计意图 vs 实际执行

**设计意图（为什么是死循环）：**

Role 要**持续存活**，多轮处理消息。第 1 轮收到 `UserRequirement` → 执行 WritePRD，第 2 轮收到 `WriteCodeReview` → 但因为 watch 不匹配跳过，第 3 轮收到别的消息 → 再干活。这是一个**长期存活的 Agent**。

**实际执行（问题所在）：**

但 Environment.Run() 每轮都给它 `cancel()`，所以实际是：

```
Round 1:
  → ctx 创建 → go Role.Run(ctx)
  → Alice 从 channel 收到 UserRequirement → 执行 WritePRD → state=-1
  → cancel()  ❌ 强制终止！

Round 2:
  → 新 ctx 创建 → go Role.Run(ctx)
  → Alice state=-1（idle），不启动
  → Bob 从 channel 收到 WritePRD → 执行 WriteCodeReview → state=-1
  → cancel()  ❌ 又强制终止！
```

**死循环的白写了：** Role.Run() 设计成可以持续处理多条消息，但实际每轮只处理一条就被杀了。

---

## 五、完整时序（demo_duo 的真实过程）

```
用户: "写一个 2048 游戏需求文档"
│
▼
Team.Run()
│
├─ PublishMessage( NewUserMessage("写一个 2048...") )
│      │
│      ├─ Alice.msgBuffer ← msg    (CauseBy: UserRequirement)
│      └─ Bob.msgBuffer   ← msg    (CauseBy: UserRequirement)
│
├─ Round 1: env.Run()
│   │
│   │  Alice.state=0, Bob.state=0  → 两个都启动
│   │
│   ├─ goroutine: Alice.Run(ctx1)
│   │   │  msg ← msgBuffer (UserRequirement)
│   │   │  shouldObserve?  watch=[UserRequirement] → Yes!
│   │   │  react() → WritePRD.Run()
│   │   │    └─ AskLLM → DeepSeek API → 返回 PRD 内容
│   │   │  PublishMessage({CauseBy:"WritePRD", Content:"# 2048游戏PRD..."})
│   │   │       │
│   │   │       ├─ Alice.msgBuffer ← msg  (她自己不关注 WritePRD，跳过)
│   │   │       └─ Bob.msgBuffer   ← msg  (Bob 关注 WritePRD！)
│   │   │  state = -1（干完了）
│   │   │  ← 继续 select，等 Bob 产出新消息
│   │
│   ├─ goroutine: Bob.Run(ctx1)
│   │   │  msg ← msgBuffer (UserRequirement)
│   │   │  shouldObserve?  watch=[WritePRD] → No! skip
│   │   │  ← 继续 select，等下一条
│   │   │  msg ← msgBuffer (WritePRD，Alice 刚才发的)
│   │   │  shouldObserve?  watch=[WritePRD] → Yes!
│   │   │  react() → WriteCodeReview.Run()
│   │   │    └─ AskLLM → DeepSeek API → 返回评审意见
│   │   │  PublishMessage({CauseBy:"WriteCodeReview", Content:"评审..."})
│   │   │  state = -1（干完了）
│   │
│   │  ← 轮询发现 IsAllIdle → cancel()！
│   │  ← Alice 和 Bob 的 ctx.Done() 触发 → 退出死循环
│   │  ← wg.Wait()
│
├─ Round 2: env.Run()
│   │  Alice.state=-1, Bob.state=-1 → active=[] → return nil
│   │  IsAllIdle=true → break
│
└─ return env.History()
```

**关键观察：** 实际上 Round 1 里面 Bob 已经收到了 Alice 的 PRD 并执行了评审！这是因为：
1. Alice 执行 WritePRD → PublishMessage → Bob 的 msgBuffer 立即收到
2. Bob 的 goroutine 还在运行（还没被 cancel），`select` 立刻收到这条新消息
3. Bob 执行 WriteCodeReview

**同一条 goroutine 里完成了两次消息处理。** cancel 发生在两人都干完之后。

---

## 六、问题诊断

### 问题 1：cancel() 是痛苦的根源

```
MetaGPT:                           Cohort:
role.run() → 处理一条 → return      ctx → goroutine → poll → cancel → wait
下次再调 role.run() 就行             每轮重新创建 ctx、重启 goroutine
```

Role.Run() 的死循环只在一个场景有价值：**同一轮里处理多条消息**（比如上面时序中 Bob 先跳过 UserRequirement，又在同一轮收到 WritePRD 并处理）。但这个价值用"处理一条就返回"也能实现——下一轮再处理就是了。

### 问题 2：轮询等待 500ms 间隔

```go
pollInterval := 500 * time.Millisecond
for time.Now().Before(deadline) {
    time.Sleep(pollInterval)    // ← 即使 Role 已经干完了，也要等最多 500ms 才发现
    if e.IsAllIdle() { break }
}
```

如果 Role 100ms 就执行完了，也要等最多 500ms。用 `sync.WaitGroup` 或 channel 通知可以消除这个延迟。

### 问题 3：实际 Round 1 就干完了，但总是空跑最后一轮

```
Round 1: Alice 干了 → Bob 干了 → IsAllIdle → cancel
Round 2: IsAllIdle=true → 没启动任何 Role → 立刻返回 → break
```

第 2 轮是多余的，因为第 1 轮结束时两人都 state=-1 了。Team 总是多跑一轮来发现"大家都空闲"。

### 问题 4：Role.state 的语义模糊

```go
func (r *Role) IsIdle() bool {
    return r.state == -1
}
```

`state` 同时表示两个东西：
- "下一个要执行的 Action 的索引"（0, 1, 2...）
- "Role 是否空闲"（-1）

如果 Role 有 0 个 Action（纯监听角色），它永远是 idle，永远不会被启动。这其实也合理，但语义不够清晰。

---

## 七、改进方案

### 方案 A（推荐，改动最小）：RunOnce 模式

**核心改动：Role.Run() 从死循环变成处理一条消息后返回。**

```go
// 新方法：处理一条消息，然后返回
func (r *Role) RunOnce(ctx context.Context) (bool, error) {
    select {
    case <-ctx.Done():
        return false, ctx.Err()
    case msg, ok := <-r.msgBuffer:
        if !ok {
            return false, nil
        }
        if !r.shouldObserve(msg) {
            return true, nil  // 不关注，但还有活力
        }
        r.memory.Add(msg)
        rsp, err := r.react(ctx)
        if rsp != nil && r.env != nil {
            r.env.PublishMessage(rsp)
        }
        return !r.IsIdle(), err  // 返回是否还需要继续
    }
}
```

```go
// Environment.Run() 大幅简化
func (e *Environment) Run(ctx context.Context) error {
    var wg sync.WaitGroup
    for _, r := range e.roles {
        if r.IsIdle() { continue }
        wg.Add(1)
        go func(role *role.Role) {
            defer wg.Done()
            hasMore, _ := role.RunOnce(ctx)
            if hasMore {
                // 如果还有 Action 没执行完，可以立即再调度
            }
        }(r)
    }
    wg.Wait()  // 自然等所有 goroutine 结束
    return nil
}
```

**收益：**
- ❌ 删除 `cancel()`、删除 poll 循环
- ❌ 删除 `wg` 在 Environment 层的手动管理（WaitGroup 只在 Run 内部用）
- ✅ 代码量减少 ~40%
- ✅ 和 MetaGPT 的行为完全一致

### 方案 B（中期）：纯事件驱动

取消轮次概念，Team 只负责"启动 → 等待收敛 → 关闭"：

```go
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
    // ① 发布初始消息
    t.env.PublishMessage(foundation.NewUserMessage(idea))

    // ② 启动所有 Role（后台持续运行）
    t.env.StartAll(ctx)

    // ③ 等收敛：连续 N 秒全员空闲 → 真干完了
    idleDeadline := 30 * time.Second
    ticker := time.NewTicker(1 * time.Second)
    idleSince := time.Time{}
    for {
        <-ticker.C
        if t.env.IsAllIdle() {
            if idleSince.IsZero() {
                idleSince = time.Now()
            } else if time.Since(idleSince) > idleDeadline {
                break
            }
        } else {
            idleSince = time.Time{}
        }
    }

    // ④ 优雅关闭
    t.env.Shutdown()
    return t.env.History(), nil
}
```

**收益：** 真正的事件驱动，Role 持续存活，不需要反复启动/停止。
**代价：** 需要处理"假空闲"（Role 在等 LLM 响应时看起来 idle）、"永久阻塞"等问题。

### 方案 C（长远）：Role 内部用 Claude Code 的模型驱动循环

Role 的 react 模式不再仅仅是 ByOrder，而是让 LLM 自主决策：

```go
func (r *Role) reactCC(ctx context.Context, history []*foundation.Message) (*foundation.Message, error) {
    for {
        // 构建 prompt：身份 + 历史 + 上下文
        choice := r.askLLMToChoose(ctx, history)

        if choice.Done {
            return choice.FinalMessage, nil  // LLM 说"我做完了"
        }
        if choice.ActionIndex == -1 {
            continue  // LLM 不想执行任何 Action，等待更多消息
        }

        act := r.actions[choice.ActionIndex]
        output, _ := act.Run(ctx, history)
        history = append(history, &Message{
            Content:  output.Content,
            CauseBy:  act.Name(),
            SentFrom: r.Name,
        })
        // 继续循环，LLM 看到新产出后决定下一步
    }
}
```

这和 Claude Code 的 `while(true) → API → tool_use → 执行 → 反馈 → 再调 API` 完全一致。

---

## 八、建议的实施顺序

```
现在                    →  Phase 1（方案 A）       →  Phase 2（方案 B+C）
─────────────────────────────────────────────────────────────────────

Role.Run() 死循环          Role.RunOnce()            纯事件驱动
Environment 轮询+cancel    直接 WaitGroup             连续空闲检测关闭
                           与 MetaGPT 对齐            模型自主停止

改动量：0                  改动量：~100行              改动量：~300行
风险：-                    风险：低（只改 env+role）    风险：中（并发模型变更）
```

**先做方案 A**，因为：
1. 改动最小，只涉及 `env/environment.go` 和 `role/role.go` 两个文件
2. 和 MetaGPT 行为对齐，可以直接参考他们的成熟设计
3. 给方案 B/C 铺路——RunOnce 是事件驱动的基础单元

---

## 九、相关文件索引

| 文件 | 内容 |
|---|---|
| [cmd/demo_duo/main.go](cmd/demo_duo/main.go) | Demo 入口，看怎么组装 Team |
| [internal/team/team.go](internal/team/team.go) | Team 编排，73 行，入口是 Run() |
| [internal/env/environment.go](internal/env/environment.go) | Environment 消息路由 + Role 管理 |
| [internal/role/role.go](internal/role/role.go) | Role 核心，Run() + reactByOrder/ReAct/PlanAndAct |
| [internal/foundation/message.go](internal/foundation/message.go) | Message 定义 + NewUserMessage + ShouldSendTo |
| [internal/action/action.go](internal/action/action.go) | BaseAction + AskLLM |
| [internal/action/builtin/write_prd.go](internal/action/builtin/write_prd.go) | WritePRD 示例 Action |
| [docs/TeamRun完整调用链讲解.md](docs/TeamRun完整调用链讲解.md) | 之前写的调用链文档 |
| [docs/Agent-Loop架构对比-Cohort-vs-MetaGPT-vs-ClaudeCode.md](docs/Agent-Loop架构对比-Cohort-vs-MetaGPT-vs-ClaudeCode.md) | 三方对比文档 |
