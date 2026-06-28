# MetaGPT 技术架构深度分析 & 自建多智能体项目指南

> 写给有 Java/Go 后端背景、想入门 AI Agent 开发的你

---

## 目录

1. [项目概览](#1-项目概览)
2. [技术栈一览](#2-技术栈一览)
3. [核心架构设计](#3-核心架构设计)
4. [关键抽象与数据流](#4-关键抽象与数据流)
5. [设计模式剖析](#5-设计模式剖析)
6. [LLM 调用层设计](#6-llm-调用层设计)
7. [多智能体协作机制](#7-多智能体协作机制)
8. [为什么选择 Python？Java/Go 能做什么？](#8-为什么选择-pythonjava-go-能做什么)
9. [自建项目实战指南](#9-自建项目实战指南)
10. [推荐路线图](#10-推荐路线图)

---

## 1. 项目概览

**MetaGPT** 是一个 **多智能体协作框架**，核心思想是：**让多个 AI Agent 扮演软件公司中的不同角色（产品经理、架构师、工程师、测试等），通过协作自动生成完整的软件项目**。

你给它一个自然语言需求（如"写一个 2048 游戏"），它输出一个完整的代码仓库——包括需求文档、系统设计、代码实现。

```
┌─────────────────────────────────────────────────────┐
│                     MetaGPT                         │
│                                                     │
│  "写一个2048游戏"  ──→   Team（软件公司）              │
│                         │                           │
│    ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐  │
│    │Product │─→│Architect│─→│Engineer│─→│   QA   │  │
│    │Manager │  │        │  │        │  │Engineer│  │
│    │ (Alice)│  │ (Bob)  │  │ (Alex) │  │(Edward)│  │
│    └────────┘  └────────┘  └────────┘  └────────┘  │
│         ↓           ↓           ↓           ↓      │
│       PRD        Design       Code       Tests     │
│                                                     │
│    输出: 完整的项目仓库 (文档 + 代码 + 测试)            │
└─────────────────────────────────────────────────────┘
```

### 核心亮点

| 特性 | 说明 |
|------|------|
| **SOP 驱动** | 模拟真实软件公司的标准作业流程（PRD → 设计 → 任务 → 代码 → 测试） |
| **角色扮演** | 每个 Agent 有独立的 name/profile/goal/constraints，产生差异化行为 |
| **消息驱动** | 基于发布-订阅的环境消息路由，Agent 间松耦合通信 |
| **多 LLM 支持** | 20+ LLM 提供商：OpenAI、Claude、Gemini、DeepSeek、国产模型等 |
| **可恢复性** | 序列化/反序列化支持断点续跑 |
| **成本控制** | 内置 token 计数和成本追踪，支持预算上限 |

---

## 2. 技术栈一览

### 语言与核心依赖

| 类别 | 技术 | 用途 |
|------|------|------|
| 语言 | Python 3.9+ | 整个框架 |
| 类型系统 | **Pydantic v2** | 数据模型定义、序列化、验证——**整个框架的基石** |
| 异步框架 | **asyncio** | 并发执行多个 Agent |
| LLM 客户端 | `openai` SDK、`anthropic` SDK | 统一的 LLM 调用接口 |
| 重试机制 | `tenacity` | LLM 调用失败自动重试（指数退避） |
| 日志 | `loguru` | 结构化日志 |
| CLI | `typer` | 命令行入口 |
| Token 计数 | `tiktoken` | 估算 token 消耗 |
| 图计算 | `networkx` | 任务依赖拓扑排序 |

### 架构分层

```
┌──────────────────────────────────────┐
│         应用层 (examples/)            │  ← 具体场景：软件公司、狼人杀、数据分析...
├──────────────────────────────────────┤
│         角色层 (roles/)               │  ← Agent 定义：角色、行为、交互
├──────────────────────────────────────┤
│         动作层 (actions/)             │  ← 原子操作：写PRD、写代码、运行测试...
├──────────────────────────────────────┤
│         环境层 (environment/)         │  ← 消息路由：发布-订阅机制
├──────────────────────────────────────┤
│         基础设施层                     │  ← LLM调用、Memory、Tool、Config...
└──────────────────────────────────────┘
```

---

## 3. 核心架构设计

MetaGPT 的核心架构可以用一句话概括：

> **由 Environment 进行消息路由，Role 通过 observe-think-act 循环处理消息，每个 act 调用 LLM 完成具体工作。**

### 3.1 三大核心抽象

```
┌──────────┐      ┌──────────┐      ┌──────────┐
│Environment│ ←──→ │   Role   │ ←──→ │  Action  │
│  环境     │      │  角色    │      │  动作    │
├──────────┤      ├──────────┤      ├──────────┤
│· 消息路由 │      │· 观察消息 │      │· 调用LLM │
│· 角色注册 │      │· 思考决策 │      │· 生成结果 │
│· 历史记录 │      │· 执行动作 │      │· 结构化输出│
└──────────┘      └──────────┘      └──────────┘
     ↑                 ↑                 ↑
     │                 │                 │
 publish_message()  run() loop      _aask(prompt)
```

### 3.2 Role 的运行循环

这是整个框架最核心的循环：

```
async def run(self):
    1. _observe()           # 从 msg_buffer 拉取新消息，按 watch 过滤
       ↓
    2. react()              # 模式分发：
       ├── REACT 模式      # LLM 动态选择下一个 Action
       ├── BY_ORDER 模式   # 按预定义顺序执行 Actions
       └── PLAN_AND_ACT    # LLM 生成计划，逐步执行
       ↓
    3. _think()            # 从 state 和 todo 中选择下一个要执行的 Action
       ↓
    4. _act()              # action.run(history_messages)
                              ↓
                          action._aask(prompt)
                              ↓
                          LLM 调用
                              ↓
                          ActionOutput (content + instruct_content)
       ↓
    5. publish_message()   # 将结果发回 Environment
       ↓
    6. 循环直到 state == -1 (idle)，或达到 max_react_loop
```

### 3.3 三种 React 模式对比

| 模式 | 行为 | 适用场景 |
|------|------|----------|
| **REACT** | LLM 动态决策"下一步该做什么"，最多循环 N 次 | 通用 Agent、需要灵活决策 |
| **BY_ORDER** | 按 Actions 列表顺序依次执行（0→1→2→...） | SOP 场景：先写 PRD、再写设计、再写代码... |
| **PLAN_AND_ACT** | 先由 LLM 生成一个 Plan（任务列表，含依赖），再逐个执行 | 复杂任务、需要分步规划 |

### 3.4 具体角色实现——以 Engineer 为例

```python
class Engineer(Role):
    name = "Alex"
    profile = "An engineer with 10 years of Python experience"
    goal = "Write elegant, readable, extensible, efficient code"
    
    def __init__(self):
        super().__init__()
        # 设置要观察哪些 Action 产生的消息
        self._watch([WriteTasks])          # 只看任务分解的结果
        # 按顺序执行这些 Action
        self.set_actions([WriteCode, WriteCodeReview])
        self._set_react_mode(react_mode="byOrder")
```

### 3.5 环境——所有 Agent 共享的房间

```python
class Environment:
    roles: dict[str, Role]              # 所有角色
    member_addrs: Dict[Role, Set[str]]  # 角色→地址映射（用于路由）
    history: Memory                     # 全局消息历史（用于调试）
    
    def publish_message(self, message: Message):
        """将消息路由到所有匹配的 Role"""
        for role, addrs in self.member_addrs.items():
            if message.send_to & addrs:      # 检查发送目标
                role.put_message(message)     # 放入角色的消息缓冲区
    
    async def run(self, k=1):
        """并发执行所有非 idle 的 Role"""
        await asyncio.gather(*[
            role.run() for role in self.roles.values()
            if not role.is_idle
        ])
```

---

## 4. 关键抽象与数据流

### 4.1 Message——智能体之间的通信单元

```python
class Message(BaseModel):
    id: str                              # UUID
    content: str                         # 自然语言文本
    instruct_content: Optional[BaseModel] # 结构化数据（如 PRD、Design、Task 列表）
    role: str                            # "user" | "system" | "assistant"
    cause_by: str                        # 由哪个 Action 触发（路由依据！）
    sent_from: str                       # 由哪个 Role 发送
    send_to: Set[str]                    # 发送目标；"<all>"=广播, "<self>"=回环
```

**关键设计**：`cause_by` 和 `sent_from` 是消息路由的核心依据。

- `cause_by`：告诉 Role "这条消息是什么动作产生的" → Role 通过 `_watch` 决定是否关注
- `send_to`：告诉 Environment "这条消息应该发给谁" → 支持广播、单播、回环

### 4.2 完整数据流示例

以 "写一个 2048 游戏" 为例：

```
Step 1: 用户输入
  Message(content="写一个2048游戏", cause_by="UserRequirement", send_to={"<all>"})
    ↓ Environment.publish_message() → 广播给所有 Role
  
Step 2: ProductManager (Alice) 收到消息
  _observe() → 看到 cause_by="UserRequirement" ✓ (watch中包含)
  react(BY_ORDER) → 执行 WritePRD Action
    WritePRD._aask("请分析需求并撰写PRD...") → LLM 返回 PRD 文档
  publish_message(Message(content=PRD内容, cause_by="WritePRD", send_to={"<all>"}))
    ↓

Step 3: Architect (Bob) 收到 PRD 消息
  _observe() → 看到 cause_by="WritePRD" ✓
  react(BY_ORDER) → 执行 WriteDesign Action
    WriteDesign._aask("请根据PRD设计系统架构...") → LLM 返回设计文档
  publish_message(Message(content=设计文档, cause_by="WriteDesign"))
    ↓

Step 4: Engineer (Alex) 收到设计消息
  _observe() → 看到 cause_by="WriteDesign" ✓ (或 WriteTasks)
  react(BY_ORDER) → WriteCode → WriteCodeReview
    WriteCode._aask(design_doc + task_list + 已有代码) → LLM 生成代码
  publish_message(Message(cause_by="WriteCode"))
    ↓

Step 5: QA Engineer (Edward) 收到代码消息
  _observe() → 看到 cause_by="WriteCode" ✓
  react(BY_ORDER) → WriteTest → RunCode
    WriteTest._aask(code + design) → LLM 生成测试
  publish_message(...)
    ↓

Step 6: 所有 Role idle，流程结束
  Environment.archive() → git commit 生成的项目
```

### 4.3 这就是 SOP 的力量

MetaGPT 的精髓在于**用代码固化了软件开发的 SOP**：

```
ProductManager._watch([UserRequirement])     # 只看用户需求
Architect._watch([WritePRD])                 # 只看 PRD 输出
Engineer._watch([WriteDesign, WriteTasks])   # 只看设计和任务
QaEngineer._watch([WriteCode])               # 只看代码输出
```

每个角色**只关心自己需要的信息**，形成了一条清晰的**流水线**。

---

## 5. 设计模式剖析

如果用 Java/Go 重写，以下设计模式的映射非常重要：

### 5.1 Pydantic 替代方案

Python 中 Pydantic 承担了**类型定义 + 序列化 + 验证**三重职责：

```python
class Message(BaseModel):
    content: str
    cause_by: str = ""
# 自动提供: .model_dump(), .model_validate(), JSON Schema
```

| 语言 | 替代方案 |
|------|----------|
| **Java** | `record`（JDK 16+）或 `@Data` Lombok + Jackson annotations + `@Valid` (Jakarta Validation) |
| **Go** | `struct` + `json` tags + `go-playground/validator` |

```go
// Go 示例
type Message struct {
    Content  string   `json:"content" validate:"required"`
    CauseBy  string   `json:"cause_by"`
    SentFrom string   `json:"sent_from"`
    SendTo   []string `json:"send_to"`
}
```

### 5.2 Mixin / 组合模式 → Context 传递

Python 中 `ContextMixin` 让 Role/Action/Environment 都能访问 `self.llm`、`self.config`、`self.context`，带**私有/公有回退链**：

```python
class ContextMixin:
    @property
    def llm(self) -> BaseLLM:
        return self.private_llm or self.context.llm  # 私有优先，回退到全局
```

在 Go 中建议用**显式依赖注入**（更 Go 风格）：

```go
type Role struct {
    config  *Config
    llm     LLM
    context *Context
}

func NewRole(config *Config, llm LLM, ctx *Context) *Role {
    return &Role{config: config, llm: llm, context: ctx}
}
```

在 Java 中可以用 **Spring 的依赖注入** 或手动构造函数注入：

```java
@Component
public class Engineer extends Role {
    public Engineer(LLMConfig config, LLMClient llmClient, Context context) {
        super(config, llmClient, context);
    }
}
```

### 5.3 发布-订阅模式

Python 版：Environment 直接遍历所有 Role，按 `send_to` 匹配后放入 `msg_buffer`（async Queue）。

```python
def publish_message(self, message):
    for role, addrs in self.member_addrs.items():
        if message.send_to & addrs:
            role.put_message(message)
```

Java 实现建议：利用 **Guava EventBus** 或 **Reactor (Spring WebFlux)**：

```java
// 使用 Guava EventBus
@Subscribe
public void onMessage(Message msg) {
    if (shouldObserve(msg)) {
        msgBuffer.add(msg);
    }
}
```

Go 实现建议：**channel** 是天然的消息队列：

```go
type Role struct {
    msgBuffer chan Message  // buffered channel
}

func (e *Environment) PublishMessage(msg Message) {
    for _, role := range e.roles {
        if role.shouldReceive(msg) {
            select {
            case role.msgBuffer <- msg:
            default:
                // buffer full, drop or log
            }
        }
    }
}
```

### 5.4 模板方法模式

`Action.run()` 是模板方法，子类只需实现具体的 LLM prompt 逻辑：

```python
class Action:
    async def run(self, *args, **kwargs):
        if self.node:
            return await self._run_action_node(*args, **kwargs)
        raise NotImplementedError

class WritePRD(Action):
    PROMPT_TEMPLATE = """
    You are a product manager. Based on the requirements, write a PRD.
    Requirements: {requirements}
    """
    
    async def run(self, requirements: str):
        prompt = self.PROMPT_TEMPLATE.format(requirements=requirements)
        return await self._aask(prompt)
```

Java 实现：

```java
public abstract class Action {
    protected abstract ActionOutput execute(Message context);
    
    protected String askLLM(String prompt) {
        return llmClient.chat(prompt);
    }
}

public class WritePRD extends Action {
    @Override
    public ActionOutput execute(Message context) {
        String prompt = "You are a PM... " + context.getContent();
        String result = askLLM(prompt);
        return ActionOutput.of(result);
    }
}
```

### 5.5 注册表模式（Registry/Provider）

LLM Provider 的注册机制：

```python
# 注册
@register_provider([LLMType.OPENAI, LLMType.DEEPSEEK, ...])
class OpenAILLM(BaseLLM):
    ...

# 工厂创建
llm = LLM_REGISTRY.create_llm_instance(config.llm)
```

Java 实现（Spring 风格）：

```java
@Component
@Qualifier("openai")
public class OpenAILLMClient implements LLMClient { ... }

// 工厂
@Service
public class LLMClientFactory {
    private final Map<LLMType, LLMClient> clients;
    
    public LLMClient getClient(LLMType type) {
        return clients.get(type);
    }
}
```

Go 实现（最简单的 map + init 注册）：

```go
var llmRegistry = map[LLMType]func(Config) LLMClient{}

func RegisterProvider(types []LLMType, factory func(Config) LLMClient) {
    for _, t := range types {
        llmRegistry[t] = factory
    }
}

func init() {
    RegisterProvider([]LLMType{OpenAI, DeepSeek}, NewOpenAILLM)
}
```

---

## 6. LLM 调用层设计

### 6.1 抽象层次

```
Action._aask(prompt)
    ↓
BaseLLM.aask(msg, system_msgs)
    ↓
BaseLLM.compress_messages()   ← Token 压缩
    ↓
BaseLLM.acompletion_text()
    ↓ (retry with tenacity)
BaseLLM._achat_completion()   ← 每个 Provider 各自实现
    ↓
HTTP Request → LLM API
```

### 6.2 关键设计点

**1. 统一接口，多种实现**

所有 Provider 实现相同的 `BaseLLM` 接口：

```go
// Go 风格接口
type LLMClient interface {
    Chat(messages []Message) (string, error)
    ChatStream(messages []Message) (<-chan string, error)
    CountTokens(messages []Message) int
}
```

```java
// Java 风格接口
public interface LLMClient {
    String chat(List<Message> messages);
    Flux<String> chatStream(List<Message> messages);
    int countTokens(List<Message> messages);
}
```

**2. Token 压缩策略**

当消息超过 token 限制时，MetaGPT 支持 4 种压缩策略：
- `POST_CUT_BY_TOKEN`：保留系统消息，从后往前按 token 数截断用户/助手消息
- `POST_CUT_BY_MSG`：同上，按消息条数截断
- `PRE_CUT_BY_TOKEN`：保留最新消息，从前往后截断
- `PRE_CUT_BY_MSG`：同上，按消息条数截断

**3. 重试机制**

```python
@retry(stop=stop_after_attempt(3), wait=wait_exponential(multiplier=1, min=4, max=10))
async def acompletion_text(self, messages):
    ...
```

Java 可以用 Resilience4j：
```java
@Retry(name = "llmRetry")
public String chat(List<Message> messages) { ... }
```

Go 实现：
```go
func (c *Client) ChatWithRetry(messages []Message) (string, error) {
    b := backoff.NewExponentialBackOff()
    b.InitialInterval = 4 * time.Second
    b.MaxInterval = 10 * time.Second
    
    var result string
    err := backoff.Retry(func() error {
        var err error
        result, err = c.Chat(messages)
        return err
    }, backoff.WithMaxRetries(b, 3))
    return result, err
}
```

---

## 7. 多智能体协作机制

### 7.1 三种协作模式

MetaGPT 支持三种智能体协作模式：

| 模式 | Python 实现 | 工作原理 |
|------|-------------|----------|
| **顺序（byOrder）** | `for action in self.actions: action.run()` | 固定流水线，像一个装配线 |
| **ReAct** | LLM 从 action 列表中动态选择下一个 | 灵活决策，像人类"想一步做一步" |
| **Plan-and-Act** | LLM 首先生成 Plan，再逐步执行 | 先规划后执行，适合复杂任务 |

### 7.2 关键特性

**1. 状态管理**

```python
class RoleContext:
    env: BaseEnvironment        # 环境引用
    msg_buffer: MessageQueue    # 消息队列（异步）
    memory: Memory              # 持久化消息历史
    state: int                  # 当前 action 索引 (-1 = idle, 0-N = 第N个action)
    todo: Action                # 下一步要执行的 action
    watch: set[str]             # 关注哪些 cause_by
```

对应到 Go 的并发安全版本：

```go
type RoleContext struct {
    mu        sync.RWMutex
    Env       *Environment
    MsgBuffer chan Message
    Memory    *Memory
    State     int
    Watch     map[string]bool
}
```

**2. 发布-订阅路由**

```
publish_message(msg)
    │
    ├── msg.send_to == "<all>"  → 发给所有 Role
    ├── msg.send_to == "<self>" → 发回给自己
    └── msg.send_to == {"Alice"} → 发给指定 Role
        │
        └── Role._watch 过滤：只有当 msg.cause_by 在 watch 集合中才处理
```

**3. 人类介入（Human-in-the-Loop）**

MGXEnv 支持人类参与模式：当某个角色需要人类确认时，消息会路由到人类交互界面。

---

## 8. 为什么选择 Python？Java/Go 能做什么？

### 8.1 Python 的优势

| 优势 | 说明 |
|------|------|
| **AI/ML 生态** | OpenAI SDK、LangChain、Transformers 等都以 Python 为第一语言 |
| **Pydantic** | Python 独有的声明式数据建模，在 Java/Go 中没有完全等价物 |
| **快速原型** | 不需要编译，修改即运行；动态类型适合实验 |
| **社区** | Agent 框架（LangChain、AutoGen、CrewAI）几乎全是 Python |

### 8.2 你作为 Java/Go 后端开发者的优势

| 你的优势 | 在 Agent 开发中的应用 |
|----------|----------------------|
| **系统设计能力** | Agent 框架本质是一个分布式消息系统，你在微服务中学到的架构思维直接适用 |
| **并发编程** | Go 的 goroutine/channel、Java 的虚拟线程，比 Python asyncio 更适合高并发 |
| **类型安全** | 大型 Agent 系统需要强类型约束，Java/Go 在这方面碾压 Python |
| **工程化能力** | CI/CD、容器化、监控、日志——这些后端基本功让 Agent 项目更可维护 |
| **性能** | 当 Agent 数量很大时，Go 的性能优势非常明显 |

### 8.3 我的建议：双语言策略

```
┌─────────────────────────────────────┐
│       Agent 核心框架 (Go/Java)        │
│  · 消息路由 (高性能并发)               │
│  · 角色管理 (强类型)                  │
│  · 状态持久化                         │
│  · API 服务                          │
└──────────────┬──────────────────────┘
               │ HTTP/gRPC 调用
┌──────────────▼──────────────────────┐
│      LLM 调用层 (Python 微服务)       │
│  · Prompt 模板管理                   │
│  · LLM API 调用 (复用现有 SDK)       │
│  · Token 计数                        │
│  · 输出解析                          │
└─────────────────────────────────────┘
```

或者**纯 Go/Java 方案**也可以——OpenAI 兼容的 API 用 HTTP 调用很简单，Go 的 `net/http` 或 Java 的 `HttpClient` 就能搞定。

---

## 9. 自建项目实战指南

### 9.1 最小可行产品 (MVP) 设计

**第一阶段目标**：实现一个能对话的 Agent + 消息路由

```
核心模块（按实现优先级）：
1. Config      —— 配置管理 (YAML 加载)
2. LLMClient   —— LLM 调用封装 (OpenAI 兼容 API)
3. Message     —— 消息定义
4. Memory      —— 消息存储
5. Action      —— 原子操作（调用 LLM 完成具体任务）
6. Role        —— 智能体（observe → think → act）
7. Environment —— 消息路由 (发布-订阅)
8. Team        —— 编排多个 Role
```

### 9.2 Go 版本核心代码骨架

#### 项目结构

```
my-agent-framework/
├── cmd/
│   └── main.go              # 入口
├── internal/
│   ├── config/
│   │   └── config.go        # 配置定义和加载
│   ├── llm/
│   │   ├── client.go        # LLM 接口定义
│   │   ├── openai.go        # OpenAI 实现
│   │   └── registry.go      # Provider 注册工厂
│   ├── message/
│   │   └── message.go       # Message 结构体
│   ├── memory/
│   │   └── memory.go        # Memory 存储
│   ├── action/
│   │   ├── action.go        # Action 基类
│   │   └── builtin/         # 内置 Action
│   │       ├── write_prd.go
│   │       └── write_code.go
│   ├── role/
│   │   ├── role.go          # Role 核心循环
│   │   └── builtin/         # 内置 Role
│   │       ├── pm.go         # ProductManager
│   │       └── engineer.go   # Engineer
│   └── env/
│       └── environment.go   # 消息路由
├── go.mod
└── go.sum
```

#### 核心接口定义

```go
// internal/llm/client.go

package llm

import "context"

// Message 对 LLM 友好的消息格式
type ChatMessage struct {
    Role    string `json:"role"`    // system, user, assistant
    Content string `json:"content"`
}

// Client LLM 调用统一接口
type Client interface {
    // Chat 同步对话
    Chat(ctx context.Context, messages []ChatMessage) (string, error)
    // ChatStream 流式对话
    ChatStream(ctx context.Context, messages []ChatMessage) (<-chan string, <-chan error)
    // CountTokens 估算 token 数量
    CountTokens(messages []ChatMessage) int
}
```

```go
// internal/action/action.go

package action

import (
    "context"
    "my-agent-framework/internal/llm"
    "my-agent-framework/internal/message"
)

// Action 原子操作的抽象
type Action interface {
    Name() string
    Run(ctx context.Context, history []message.Message) (*ActionOutput, error)
}

// BaseAction 提供 LLM 调用的基础能力
type BaseAction struct {
    Name   string
    Prefix string          // 系统提示词前缀
    LLM    llm.Client
}

func (a *BaseAction) AskLLM(ctx context.Context, prompt string, history []*ChatMessage) (string, error) {
    messages := []llm.ChatMessage{
        {Role: "system", Content: a.Prefix},
    }
    // 将历史消息转换为 LLM 格式并压缩
    messages = append(messages, compressMessages(history, maxTokens)...)
    messages = append(messages, llm.ChatMessage{Role: "user", Content: prompt})
    
    return a.LLM.Chat(ctx, messages)
}
```

```go
// internal/role/role.go

package role

import (
    "context"
    "my-agent-framework/internal/action"
    "my-agent-framework/internal/message"
)

// ReactMode 定义角色的决策模式
type ReactMode int

const (
    ReactModeByOrder  ReactMode = iota // 按顺序执行
    ReactModeReAct                     // LLM 动态选择
    ReactModePlanAndAct                // 先规划后执行
)

// Role 智能体的核心抽象
type Role struct {
    Name        string
    Profile     string
    Goal        string
    Constraints string
    
    actions     []action.Action   // 可执行的动作列表
    reactMode   ReactMode
    watch       map[string]bool   // 关注哪些 Action 产生的消息
    state       int               // 当前动作索引 (-1 = idle)
    msgBuffer   chan *message.Message
    memory      *memory.Memory
    env         *Environment
    
    cancel      context.CancelFunc
}

// Run 核心循环
func (r *Role) Run(ctx context.Context) {
    ctx, r.cancel = context.WithCancel(ctx)
    defer r.cancel()
    
    for {
        select {
        case <-ctx.Done():
            return
        case msg := <-r.msgBuffer:
            if !r.shouldObserve(msg) {
                continue
            }
            r.memory.Add(msg)
            rsp, err := r.react(ctx)
            if err != nil {
                log.Printf("[%s] react error: %v", r.Name, err)
                continue
            }
            if rsp != nil {
                r.env.PublishMessage(rsp)
            }
        }
    }
}

func (r *Role) react(ctx context.Context) (*message.Message, error) {
    switch r.reactMode {
    case ReactModeByOrder:
        return r.reactByOrder(ctx)
    case ReactModeReAct:
        return r.reactReAct(ctx)
    case ReactModePlanAndAct:
        return r.reactPlanAndAct(ctx)
    default:
        return nil, fmt.Errorf("unknown react mode: %v", r.reactMode)
    }
}

func (r *Role) reactByOrder(ctx context.Context) (*message.Message, error) {
    if r.state >= len(r.actions) {
        r.state = -1  // 所有 action 执行完毕
        return nil, nil
    }
    
    act := r.actions[r.state]
    history := r.memory.Get(0) // 获取全部历史
    
    output, err := act.Run(ctx, history)
    if err != nil {
        return nil, err
    }
    
    r.state++
    return &message.Message{
        Content:    output.Content,
        CauseBy:    act.Name(),
        SentFrom:   r.Name,
        SendTo:     []string{message.RouteToAll},
    }, nil
}
```

```go
// internal/env/environment.go

package env

import (
    "context"
    "sync"
)

// Environment 消息路由中心
type Environment struct {
    mu          sync.RWMutex
    roles       map[string]*Role
    memberAddrs map[string]map[string]bool  // roleName -> addresses
    history     *Memory
}

// PublishMessage 发布消息到匹配的角色
func (e *Environment) PublishMessage(msg *Message) {
    e.history.Add(msg)
    e.mu.RLock()
    defer e.mu.RUnlock()
    
    for name, role := range e.roles {
        if msg.ShouldSendTo(name) {
            select {
            case role.msgBuffer <- msg:
            default:
                // buffer full, log warning
            }
        }
    }
}

// Run 并发运行所有非空闲角色
func (e *Environment) Run(ctx context.Context) error {
    var wg sync.WaitGroup
    
    e.mu.RLock()
    for _, role := range e.roles {
        if !role.IsIdle() {
            wg.Add(1)
            go func(r *Role) {
                defer wg.Done()
                r.Run(ctx)
            }(role)
        }
    }
    e.mu.RUnlock()
    
    wg.Wait()
    return nil
}
```

### 9.3 Java 版核心架构

如果用 Java（Spring Boot），建议这样组织：

```java
// 核心抽象
public interface Action {
    String getName();
    ActionOutput run(List<Message> history);
}

// Role 的 observe-think-act 循环
@Component
public abstract class Role implements Runnable {
    private final BlockingQueue<Message> msgBuffer = new LinkedBlockingQueue<>();
    private final List<Action> actions;
    private final Set<String> watch;
    private int state = 0;
    
    @Override
    public void run() {
        while (!Thread.currentThread().isInterrupted()) {
            Message msg = msgBuffer.poll(1, TimeUnit.SECONDS);
            if (msg != null && shouldObserve(msg)) {
                memory.add(msg);
                Message response = react();
                if (response != null) {
                    environment.publishMessage(response);
                }
            }
        }
    }
    
    private Message react() {
        return switch (reactMode) {
            case BY_ORDER -> reactByOrder();
            case REACT -> reactReAct();
            case PLAN_AND_ACT -> reactPlanAndAct();
        };
    }
}

// 使用 Virtual Threads 实现高并发（JDK 21+）
public class Environment {
    private final Map<String, Role> roles = new ConcurrentHashMap<>();
    
    public void run() {
        try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            var futures = roles.values().stream()
                .filter(r -> !r.isIdle())
                .map(r -> executor.submit(r))
                .toList();
            
            for (var future : futures) {
                future.get(); // 等待所有完成
            }
        }
    }
}
```

### 9.4 MVP 的第一步：最小可运行 Demo

```go
// 最简示例：单 Agent 对话
func main() {
    // 1. 加载配置
    cfg := config.Load("config.yaml")
    
    // 2. 创建 LLM 客户端
    llmClient := llm.NewOpenAIClient(cfg.LLM)
    
    // 3. 创建一个 Action
    writePRD := actions.NewWritePRD(llmClient)
    
    // 4. 创建一个 Role
    pm := role.NewProductManager(writePRD)
    
    // 5. 创建环境
    env := env.NewEnvironment()
    env.RegisterRole(pm)
    
    // 6. 发送需求
    ctx := context.Background()
    env.PublishMessage(message.NewUserMessage("写一个网页版2048游戏"))
    
    // 7. 运行
    env.Run(ctx)
    
    // 8. 获取结果
    fmt.Println(env.History().Last().Content)
}
```

---

## 10. 推荐路线图

### 第一阶段：理解核心（1-2 周）

```
□ 阅读 MetaGPT 核心源码（本文档已帮你做了大部分工作）
□ 重点阅读文件：
  - metagpt/roles/role.py         ← observe-think-act 循环
  - metagpt/environment/base_env.py ← 消息路由
  - metagpt/actions/action.py     ← Action 基类
  - metagpt/provider/base_llm.py  ← LLM 调用抽象
  - metagpt/schema.py             ← Message 设计
  - metagpt/software_company.py   ← 整体编排
□ 用 Python 跑通一次 `metagpt "写一个计算器"`
□ 用 debug 模式观察每个 Role 的输入输出
```

### 第二阶段：MVP 实现（2-4 周）

```
□ 选择语言：Go（推荐，并发模型更好）或 Java
□ 实现 Config 模块（YAML 加载）
□ 实现 LLMClient（对接 OpenAI 兼容 API）
□ 实现 Message + Memory
□ 实现 BaseAction + 一个具体 Action（如 WritePRD）
□ 实现 Role（支持 BY_ORDER 模式）
□ 实现 Environment（消息路由）
□ 实现 Team（编排多个 Role）
□ 编写集成测试：用 2 个 Role 完成一个简单任务
```

### 第三阶段：增强特性（4-8 周）

```
□ 添加 ReAct 模式（LLM 动态选择 Action）
□ 添加 Plan-and-Act 模式
□ 添加 Token 压缩
□ 添加流式输出
□ 添加多 Provider 支持（至少 OpenAI + DeepSeek）
□ 添加结构化输出解析（ActionNode 等效物）
□ 添加断点续跑（序列化/反序列化）
□ 添加成本追踪
□ 添加 Web UI（展示 Agent 运行过程）
```

### 第四阶段：打造特色（持续）

```
□ 添加你自己的 Agent 场景（不限于软件开发）
□ 添加 MCP (Model Context Protocol) 支持
□ 添加知识库/RAG 集成
□ 添加长期记忆
□ 性能优化：Agent 池化、消息批处理
□ 容器化部署（Docker + K8s）
□ 编写文档和教程
```

---

## 总结

MetaGPT 的本质是一个 **消息驱动的多智能体编排框架**，核心思想并不复杂：

```
┌─────────────────────────────────────────────┐
│  3 个核心抽象    │  Role → Action → Environment  │
│  1 个核心循环    │  observe → think → act       │
│  1 种通信机制    │  publish → subscribe          │
│  1 种编排模式    │  SOP（流水线）                 │
└─────────────────────────────────────────────┘
```

作为 Java/Go 后端开发者，你已经掌握了构建这类系统所需的**所有基础技能**：并发编程、消息系统、接口设计、工程化部署。你缺的只是对 AI Agent 范式的理解——而这份文档就是为你准备的。

**建议选择 Go** 作为实现语言：
- goroutine + channel 天然适配 Agent 并发模型
- 编译型语言，部署简单
- 性能优势明显（当 Agent 数量多时）
- 语法简洁，适合个人项目快速迭代

开始写代码吧！一个能用的 MVP 不需要超过 1000 行代码。

---

> **推荐阅读源码的切入点**（按此顺序阅读）：
> 1. `metagpt/schema.py` → 理解 Message
> 2. `metagpt/environment/base_env.py` → 理解消息路由
> 3. `metagpt/roles/role.py` → 理解核心循环
> 4. `metagpt/actions/action.py` → 理解 LLM 调用封装
> 5. `metagpt/software_company.py` → 理解整体编排
