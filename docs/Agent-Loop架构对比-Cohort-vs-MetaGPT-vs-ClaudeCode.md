# Agent Loop 架构对比：Cohort vs MetaGPT vs Claude Code

## 核心问题

Cohort 当前的执行模式是 **轮次驱动（Round-based）**：
- Team 循环 N 轮，每轮并发启动所有 Role 的 goroutine
- 每个 Role 处理一条消息后，用 `cancel()` 强行终止 goroutine
- 下一轮再重新启动 goroutine

这种"反复启动-停止"的模式是否有更好的替代方案？对比 MetaGPT 和 Claude Code 看看业界是怎么做的。

---

## 一、架构总览

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Claude Code（单 Agent）                             │
│                                                                          │
│  用户输入 → queryLoop() → while(true) {                                   │
│      API调用 → 有tool_use? → 执行工具 → 结果反馈 → 再调API                 │
│      API调用 → stop_reason="end_turn" → 退出                              │
│  }                                                                       │
│                                                                          │
│  特点：模型自己决定何时停止，框架只提供 max_turns 安全网                    │
└──────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────┐
│                   MetaGPT（多 Agent, Round-based）                         │
│                                                                          │
│  Team.run(n_round=5):                                                    │
│    for round in 1..n_round:                                              │
│      if env.is_idle: break        ← 所有 Role 空闲 → 提前退出              │
│      env.run() → asyncio.gather(*[role.run() for each 非空闲 Role])       │
│                                                                          │
│  特点：轮次循环 + 并发执行 + 空闲提前退出                                   │
└──────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────┐
│                   Cohort（多 Agent, Round-based）                          │
│                                                                          │
│  Team.Run(n_round=30):                                                   │
│    for round in 1..n_round:                                              │
│      env.Run() → go r.Run(ctx) 每个活跃 Role                              │
│      轮询等待 IsAllIdle() → cancel() → wg.Wait()                          │
│                                                                          │
│  特点：轮次循环 + 并发执行 + cancel 强行终止 + 空闲提前退出                 │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 二、执行循环对比

### 2.1 Claude Code：模型驱动循环

**核心**：[单个 `while(true)`](https://github.com/anthropics/claude-code/blob/main/src/query.ts)，模型自己决定何时退出。

```
用户输入
  │
  ▼
while True:
  │
  ├─► API 调用（带上全部 messages + tools）
  │     │
  │     ├─ stop_reason == "end_turn"  → 模型说"我做完了"，退出
  │     │
  │     └─ stop_reason == "tool_use"  → 模型要调工具
  │           │
  │           ├─ 执行工具（最多 10 个并发）
  │           ├─ 结果 append 到 messages
  │           └─ 回到 while 顶部，再调 API
  │
  ▼
返回最终结果给用户
```

**关键设计：**
- **没有 planner/executor 分离**，模型既是规划者又是执行者
- **max_turns=10~30** 只是安全网，**模型自主决定停止才是主要退出方式**
- 工具调用本质是"模型输出了一段 JSON，框架执行它，把结果喂回去"
- 不是轮次模式，是**连续流模式**——模型不停调用工具直到自己觉得够了

### 2.2 MetaGPT：轮次驱动 + 并发执行

**核心**：[`Team.run()`](g:\beliveOnly\MetaGPT\metagpt\team.py#L123-L138)，固定轮次循环。

```python
# metagpt/team.py
async def run(self, n_round=3, idea="", send_to="", auto_archive=True):
    if idea:
        self.run_project(idea)         # 发布初始需求消息

    while n_round > 0:
        if self.env.is_idle:           # 所有 Role 空闲 → 退出
            break
        n_round -= 1
        self._check_balance()          # 预算检查
        await self.env.run()           # ★ 一轮：所有 Role 并发执行

    self.env.archive()
    return self.env.history
```

```python
# metagpt/environment/base_env.py
async def run(self, k=1):
    for _ in range(k):
        futures = []
        for role in self.roles.values():
            if role.is_idle:
                continue              # 空闲的跳过
            future = role.run()       # 创建协程
            futures.append(future)

        if futures:
            await asyncio.gather(*futures)  # ★ 并发执行所有 Role
```

对比 Cohort 的 `Environment.Run()`：

```go
// Cohort: internal/env/environment.go
func (e *Environment) Run(ctx context.Context) error {
    // 为每轮创建新 ctx + cancel
    ctx, cancel := context.WithCancel(ctx)

    for _, r := range active {
        go r.Run(ctx)   // 启动 goroutine
    }

    // 轮询等待
    for time.Now().Before(deadline) {
        if e.IsAllIdle() { break }
    }

    cancel()   // ★ 强行终止 goroutine
    wg.Wait()
}
```

**关键区别：MetaGPT 不需要 cancel。**

| | MetaGPT | Cohort |
|---|---|---|
| Role.run() 返回 | 处理一条消息后 **主动返回** | **死循环**，靠 ctx.Done() 退出 |
| 终止方式 | `role.run()` 自己 return | `cancel()` 强行打断 |
| 协程模型 | `asyncio.gather` 等所有协程自然结束 | goroutine + poll + cancel |
| 状态持久 | Role 对象一直存活，下次 run() 继续用 | Role 对象也存活，但启动/停止开销大 |

### 2.3 MetaGPT 的 Role.run() 为什么能主动返回？

```python
# metagpt/roles/role.py
async def run(self, with_message=None) -> Message | None:
    # ① 观察：看看有没有新消息
    if not await self._observe():
        # 没有新消息 → 直接返回 None，不执行任何 Action
        return

    # ② 思考+执行：react
    rsp = await self.react()

    # ③ 发布产出
    self.publish_message(rsp)
    return rsp    # ★ 处理完一条消息就返回，让 env 决定下一轮
```

**关键：`run()` 处理一条消息就返回**，不是死循环。`asyncio.gather` 等所有 Role 的 `run()` 自然返回后，一轮结束。

---

## 三、停止机制对比

### 3.1 Claude Code

| 停止方式 | 说明 |
|---|---|
| **模型自主决定**（主要） | `stop_reason: "end_turn"` → 模型认为任务完成 |
| max_turns（安全网） | 防止无限循环，默认 10-30 轮 |
| 预算耗尽 | 达到 `max_budget_usd` |
| 用户中断 | Ctrl+C |
| 上下文溢出 | 下一轮会超 context window |

### 3.2 MetaGPT

```python
# 停止条件（优先级从高到低）
while n_round > 0:
    if self.env.is_idle:     # 条件 1：所有 Role 空闲
        break
    n_round -= 1             # 条件 2：达到最大轮次
    self._check_balance()    # 条件 3：预算超支（抛异常）
```

`Role.is_idle` 的判断：
```python
@property
def is_idle(self) -> bool:
    return (
        not self.rc.news and          # 没有新消息关注
        not self.rc.todo and          # 没有待执行的 Action
        self.rc.msg_buffer.empty()    # 消息缓冲为空
    )
```

### 3.3 Cohort

```go
// 停止条件
for round := 0; round < t.nRound; round++ {
    t.env.Run(ctx)
    if t.env.IsAllIdle() {        // 条件 1：所有 Role 空闲
        break
    }
}
// 没有预算检查
```

`IsAllIdle` 的判断：
```go
func (r *Role) IsIdle() bool {
    return r.state == -1    // 所有 Action 执行完毕
}
```

**Cohort 的问题：** `IsIdle() = state == -1` 过于简单。一个 Role 可能只是"当前没有可关注的消息"而暂时空闲，但它的 Action 还没全部执行完。这导致：
- 第 1 轮：Alice 执行 WritePRD，state → -1；Bob 没收到关注的消息，state 还是 0 但被 cancel 了
- 第 2 轮：Bob 收到 Alice 的 PRD 消息，执行 WriteCodeReview

这种"强行打断又重启"造成了不必要的开销。

---

## 四、消息路由对比

三者都是 **发布-订阅模型**，但细节不同：

### MetaGPT

```
Role A → publish_message(msg) → Environment
                                    │
                                    ├─ 遍历所有 Role
                                    ├─ is_send_to(msg, role.addresses)?
                                    └─ role.put_message(msg) → msg_buffer (asyncio.Queue)
```

**两个过滤层：**
1. **SendTo 地址匹配**（Environment 层）：`msg.send_to` 是否包含 role 的地址
2. **cause_by watch 过滤**（Role 层）：`msg.cause_by` 是否在 `self.rc.watch` 中

### Cohort

```
Role A → PublishMessage(msg) → Environment
                                    │
                                    ├─ 遍历所有 Role
                                    ├─ msg.ShouldSendTo(roleName)?
                                    └─ role.msgBuffer ← msg (channel)
```

**两个过滤层：**
1. **SendTo 匹配**（Environment 层）：`msg.send_to` 包含 roleName 或 `<all>`
2. **cause_by watch 过滤**（Role 层）：`msg.cause_by` 是否在 `r.watch` 中

> **Cohort 和 MetaGPT 在消息路由上几乎一样**，这是两者最相似的设计。

---

## 五、Action 选择模式对比

| 模式 | MetaGPT | Cohort | Claude Code |
|---|---|---|---|
| **顺序执行 (ByOrder)** | ✅ 按 state 顺序推进 | ✅ reactByOrder() | ❌ 不适用（单 Agent） |
| **LLM 动态选择 (ReAct)** | ✅ LLM 选 state 编号 | ✅ reactReAct()（简化版） | ✅ 核心机制 |
| **先规划后执行 (PlanAndAct)** | ✅ Planner + Task 循环 | 🚧 TODO | ❌ 模型隐式规划 |

### MetaGPT 的 ReAct 模式

```python
async def _react(self) -> Message:
    actions_taken = 0
    while actions_taken < self.rc.max_react_loop:  # 默认 max_react_loop=1
        has_todo = await self._think()   # LLM 选择下一个 Action
        if not has_todo:
            break                        # LLM 说"做完了"
        rsp = await self._act()          # 执行选中的 Action
        actions_taken += 1
    return rsp
```

`_think()` 里 LLM 的选择 prompt：
```python
prompt = f"""
历史对话: {history}
可用步骤: {states}     # [0. WritePRD, 1. WriteDesign, 2. WriteCode, ...]
你的上一步: {previous_state}
请选择下一步的编号（-1 表示结束）
"""
```

### Cohort 的 reactByOrder

```go
func (r *Role) reactByOrder(ctx context.Context) (*foundation.Message, error) {
    act := r.actions[r.state]    // 按固定顺序取
    output, err := act.Run(ctx, history)
    r.state++                    // 下一次取下一个
    if r.state >= len(r.actions) {
        r.state = -1            // 全部执行完
    }
    return &Message{...}, nil
}
```

---

## 六、Cohort 当前的核心问题

### 问题 1：强行 cancel 的"启动-停止"循环

```
每轮的流程：
  创建 ctx → 启动 goroutine → 轮询等待 → cancel() 强杀 → wg.Wait()

相当于：
  让厨师进厨房 → 等菜做好 → 拉闸断电赶人 → 下轮再开门通电请回来
```

**对比 MetaGPT：** Role.run() 处理完一条消息后自然返回，不需要 cancel。下次 env.run() 再调用同一个 Role 的 run() 方法即可。

### 问题 2：Role 的 Run() 是死循环，但实际只用一次

`Role.Run()` 设计成死循环（持续 `select { case msg := <-msgBuffer }`），但实际每轮只处理一条消息就被 cancel 了。死循环的价值没发挥出来。

### 问题 3：IsIdle 判断过窄

`state == -1` 只反映"所有 Action 执行完了"，但一个 Role 可能是因为"还没收到匹配的消息"而暂时跳过，它并不真的 idle。

### 问题 4：没有利用 channel 的天然阻塞特性

Go 的 channel 天然支持"没消息就阻塞"，但 Cohort 现在每轮都要 poll + cancel，把 channel 的优势消解了。

---

## 七、改进建议

### 方案 A：对齐 MetaGPT 模式（推荐）

改为 **Role.run() 处理一条消息后返回**，去掉死循环和 cancel：

```go
// 新的 Role.RunOnce()
func (r *Role) RunOnce(ctx context.Context) (*foundation.Message, error) {
    // 从 msgBuffer 取一条消息（带超时）
    select {
    case msg := <-r.msgBuffer:
        if !r.shouldObserve(msg) {
            return nil, nil   // 不关注的消息，返回 nil
        }
        r.memory.Add(msg)
        rsp, err := r.react(ctx)
        if rsp != nil && r.env != nil {
            r.env.PublishMessage(rsp)
        }
        return rsp, err
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

```go
// 新的 Environment.Run()
func (e *Environment) Run(ctx context.Context) error {
    var wg sync.WaitGroup
    for _, r := range e.roles {
        if r.IsIdle() { continue }
        wg.Add(1)
        go func() {
            defer wg.Done()
            r.RunOnce(ctx)   // ★ 处理一条消息就返回
        }()
    }
    wg.Wait()                // ★ 自然等待，不需要 cancel
    return nil
}
```

**收益：**
- 去掉 poll + cancel 的复杂逻辑
- Role 生命周期更清晰
- 代码量减少 ~30%

### 方案 B：纯事件驱动（更大改动）

每个 Role 启动后持续运行（死循环），当所有 Role 都阻塞在 channel 上时，Team 检测到"全员空闲"后发送 poison pill 消息来优雅关闭：

```go
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
    t.env.PublishMessage(foundation.NewUserMessage(idea))

    // 启动所有 Role（持续运行）
    t.env.StartAll(ctx)

    // 等待空闲超时
    ticker := time.NewTicker(3 * time.Second)
    idleCount := 0
    for {
        <-ticker.C
        if t.env.IsAllIdle() {
            idleCount++
            if idleCount >= 2 {  // 连续 2 次检查都空闲 → 真干完了
                break
            }
        } else {
            idleCount = 0
        }
    }

    t.env.Shutdown()  // 优雅关闭
    return t.env.History(), nil
}
```

**收益：** 更接近真实的消息驱动模型。**代价：** 改动大，容易出现 Role 假死检测等问题。

### 方案 C：借鉴 Claude Code 的模型自主停止（长远）

在 ReAct 模式下，让 LLM 决定"任务完成"：

```go
func (r *Role) reactReAct(ctx context.Context) (*foundation.Message, error) {
    for {
        hasMore := r.askLLMToChoose(ctx)
        if !hasMore {           // LLM 返回 -1 → 不干了
            r.state = -1
            return nil, nil
        }
        act := r.actions[r.state]
        output, _ := act.Run(ctx, history)
        r.publish(output)
    }
}
```

这把停止权从框架交给模型，和 Claude Code 的理念一致。

---

## 八、总结

| 维度 | Claude Code | MetaGPT | Cohort（当前） |
|---|---|---|---|
| Agent 数量 | 1 | N | N |
| 执行模型 | 模型驱动连续流 | 轮次 + 并发 | 轮次 + 并发 + cancel |
| Role.run() 返回 | （单Agent） | 处理一条消息后返回 | 死循环，靠 cancel 退出 |
| 主要停止方式 | 模型 stop_reason | 全员 idle + 轮次上限 | 全员 idle + 轮次上限 |
| Action 选择 | 模型自主 | ByOrder / ReAct / PlanAndAct | ByOrder / ReAct(TODO) |
| 并发方式 | 工具并发(≤10) | asyncio.gather | goroutine |
| 上下文管理 | 5阶段压缩流水线 | Memory+历史截断 | Memory.Get(N) |
| 成熟度 | 生产级 | 学术+开源 | 开发中 |

**MetaGPT 是 Cohort 最直接的参考对象**——两者架构几乎一致（轮次驱动、发布-订阅、cause_by 路由），但 MetaGPT 的 Role.run() 返回模型更自然，不需要 cancel 操作。

**Claude Code 提供了一个根本不同的思路**——放弃框架编排，让模型自己决定何时调用工具、何时停止。Cohort 的 ReAct 模式可以朝这个方向演进。
