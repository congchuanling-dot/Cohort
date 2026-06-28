# Go 版本多智能体框架——从零到一开发路线

> 目标：用 Go 从零实现一个 MetaGPT 风格的多智能体协作框架，作为秋招核心竞争力项目
>
> 读者画像：大三后端开发，有 Java/Go 基础，想在秋招中展示架构能力

---

## 目录

1. [为什么用 Go 做这个项目](#1-为什么用-go-做这个项目)
2. [整体架构设计](#2-整体架构设计)
3. [分阶段开发计划](#3-分阶段开发计划)
4. [第一阶段：基础设施层（Week 1-2）](#4-第一阶段基础设施层)
5. [第二阶段：LLM 调用层（Week 2-3）](#5-第二阶段-llm-调用层)
6. [第三阶段：Agent 核心层（Week 3-4）](#6-第三阶段-agent-核心层)
7. [第四阶段：环境与编排层（Week 4-5）](#7-第四阶段环境与编排层)
8. [第五阶段：内置角色与场景（Week 5-6）](#8-第五阶段内置角色与场景)
9. [第六阶段：工程化增强（Week 6-8）](#9-第六阶段工程化增强)
10. [关键设计决策与 Go 惯用法](#10-关键设计决策与-go-惯用法)
11. [测试策略](#11-测试策略)
12. [秋招面试话术指南](#12-秋招面试话术指南)

---

## 1. 为什么用 Go 做这个项目

### 1.1 技术层面的天然适配

Agent 框架的核心是一个 **消息驱动的并发系统**，Go 的并发模型恰好为此设计：

| Agent 框架需求 | Go 原语 | Java 等效方案 |
|---------------|---------|--------------|
| Agent 并发运行 | `goroutine`（轻量，几 KB 栈） | `VirtualThread`（JDK 21+）或 `ThreadPool` |
| Agent 消息邮箱 | `chan Message`（类型安全，阻塞/非阻塞可选） | `BlockingQueue` |
| 多 Agent 同步 | `sync.WaitGroup` + `errgroup.Group` | `CountDownLatch` + `CompletableFuture` |
| 消息广播 | `for range roles { ch <- msg }` | EventBus / 手动循环 |
| 超时控制 | `context.Context`（标准库一等公民） | `CompletableFuture.orTimeout()` |
| 优雅关闭 | `context.WithCancel` + `select` | `ExecutorService.shutdown()` |

**核心优势**：Go 写 Agent 框架，并发部分的代码量大约是 Java 的 1/3，且没有"该用哪种线程池"的决策负担。

### 1.2 秋招话术 —— 面试官问"为什么用 Go"

> "我对比了 Java 和 Go 两种方案。Agent 框架本质上是一个消息驱动的并发系统——每个 Agent 是一个独立执行单元，Agent 之间通过消息通信。
> Go 的 CSP 并发模型（goroutine + channel）和这个场景天然匹配：goroutine 作为 Agent 的执行体，channel 作为 Agent 的邮箱。
> 相比之下，Java 需要手动管理线程池、选择 BlockingQueue 的实现、处理线程安全——代码量至少是 Go 的 3 倍。
> 所以我选择了 Go，让架构的简洁性成为项目的亮点。"

这句话说完，面试官会有两个反应：① 他不是只会调包，有技术选型思考；② 他对并发模型有理解。

### 1.3 你已有的 Java 背景如何处理

```
简历策略：
┌─────────────────────────────────────────┐
│ Java 项目（1个）                          │
│ · 体现工程化能力：Spring Boot + MyBatis   │
│ · 体现团队协作：微服务拆分、接口设计       │
├─────────────────────────────────────────┤
│ Go 项目（就是这个 Agent 框架）             │
│ · 体现学习能力：自主选择技术栈             │
│ · 体现架构能力：从零设计并发框架           │
│ · 体现前沿视野：AI Agent 赛道             │
└─────────────────────────────────────────┘
```

---

## 2. 整体架构设计

### 2.1 架构全景图

```
┌──────────────────────────────────────────────────────────────┐
│                        cmd/myagent                           │
│                    CLI 入口 / HTTP API                       │
├──────────────────────────────────────────────────────────────┤
│                         orchestration                       │
│              Team（多 Agent 编排器，类似线程池）              │
├───────────────────────┬──────────────────────────────────────┤
│      environment      │              role                   │
│   消息路由 + 历史记录   │   observe → think → act 循环       │
│   Publish/Subscribe   │   三种模式：ByOrder / ReAct / Plan   │
├───────────────────────┴──────────────────────────────────────┤
│                       action                                 │
│         原子操作：WritePRD / WriteCode / RunTest             │
├──────────────────────────────────────────────────────────────┤
│                        llm                                   │
│     LLM 调用抽象：OpenAI / DeepSeek / Claude / Ollama        │
│     重试 · 压缩 · 流式 · 成本追踪                             │
├──────────────────────────────────────────────────────────────┤
│                      foundation                              │
│     Config · Message · Memory · Logger · Serializer          │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 依赖关系（自底向上构建）

```
foundation  ← 零外部依赖，纯标准库
    ↑
   llm      ← 依赖 foundation，需要 net/http
    ↑
  action    ← 依赖 llm + foundation
    ↑
   role     ← 依赖 action + memory + foundation
    ↑
environment ← 依赖 role + message + foundation
    ↑
orchestration ← 依赖 environment + role
    ↑
   cmd      ← 依赖所有层
```

### 2.3 完整项目结构

```
g:\beliveOnly\my-agent-framework\
├── cmd/
│   └── myagent/
│       └── main.go                 # CLI 入口（cobra）
├── internal/
│   ├── foundation/                 # === 基础设施层 ===
│   │   ├── config.go               # 配置定义 + YAML 加载
│   │   ├── config_test.go
│   │   ├── message.go              # Message 结构体定义
│   │   ├── message_test.go
│   │   ├── memory.go               # 消息存储（内存 + 索引）
│   │   ├── memory_test.go
│   │   ├── serializer.go           # JSON 序列化/反序列化
│   │   └── logger.go               # 结构化日志封装（slog）
│   │
│   ├── llm/                        # === LLM 调用层（可插拔多 Provider）===
│   │   ├── client.go               # Client 接口定义（上层只依赖此接口）
│   │   ├── registry.go             # Provider 注册工厂
│   │   ├── types.go                # 对内统一的请求/响应类型
│   │   ├── adapter.go              # 协议适配器接口（处理不同 API 格式差异）
│   │   ├── compressor.go           # Token 压缩策略
│   │   ├── tokenizer.go            # Token 计数
│   │   ├── retry.go                # 重试逻辑（指数退避）
│   │   ├── cost.go                 # 成本追踪器
│   │   ├── provider_openai.go      # OpenAI Provider（原生 OpenAI API）
│   │   ├── provider_deepseek.go    # DeepSeek Provider
│   │   ├── provider_anthropic.go   # Anthropic Claude Provider
│   │   ├── provider_ollama.go      # Ollama 本地模型 Provider
│   │   ├── provider_custom.go      # ★ 自定义 Provider：任意 OpenAI 兼容 API
│   │   └── mock.go                 # Mock 客户端（测试用）
│   │
│   ├── action/                     # === 动作层 ===
│   │   ├── action.go               # Action 接口 + BaseAction
│   │   ├── action_test.go
│   │   ├── node.go                 # 结构化输出解析（ActionNode 等效）
│   │   ├── output.go               # ActionOutput 定义
│   │   └── builtin/                # 内置动作
│   │       ├── write_prd.go
│   │       ├── write_design.go
│   │       ├── write_code.go
│   │       ├── write_test.go
│   │       ├── run_code.go
│   │       └── search.go
│   │
│   ├── role/                       # === 角色层 ===
│   │   ├── role.go                 # Role 核心：observe-think-act 循环
│   │   ├── role_test.go
│   │   ├── context.go              # RoleContext 状态管理
│   │   ├── react.go                # 三种 React 模式实现
│   │   ├── react_test.go
│   │   ├── planner.go              # Plan-and-Act 的规划器
│   │   └── builtin/                # 内置角色
│   │       ├── pm.go               # ProductManager
│   │       ├── architect.go        # Architect
│   │       ├── engineer.go         # Engineer
│   │       └── qa.go               # QAEngineer
│   │
│   ├── env/                        # === 环境层 ===
│   │   ├── environment.go          # 消息路由 + Role 注册
│   │   ├── environment_test.go
│   │   └── mgx.go                  # 增强版环境（支持人类介入）
│   │
│   └── team/                       # === 编排层 ===
│       ├── team.go                 # Team：多 Agent 生命周期管理
│       ├── team_test.go
│       └── software_company.go     # 软件公司场景的实现
│
├── configs/
│   └── config.yaml                 # 默认配置文件
├── examples/
│   ├── simple/                     # 最简示例：单 Agent
│   │   └── main.go
│   ├── duo/                        # 双 Agent 协作
│   │   └── main.go
│   └── software_company/           # 完整软件公司场景
│       └── main.go
├── docs/
│   ├── architecture.md             # 架构文档
│   └── api.md                      # API 文档
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
└── README.md
```

---

## 3. 分阶段开发计划

```
Week 1 ──── Week 2 ──── Week 3 ──── Week 4 ──── Week 5 ──── Week 6 ──── Week 7-8
  │            │            │            │            │            │            │
  ▼            ▼            ▼            ▼            ▼            ▼            ▼
┌────────┐┌────────┐┌────────┐┌────────┐┌────────┐┌────────┐┌──────────────┐
│ 基础设施 ││ LLM调用 ││ Agent  ││ Environment││ 内置   ││ 工程化 ││ 打磨 + 面试  │
│ 层      ││ 层      ││ 核心层  ││ + 编排层  ││ 角色场景││ 增强   ││ 准备        │
└────────┘└────────┘└────────┘└────────┘└────────┘└────────┘└──────────────┘
   6 文件     5 文件      4 文件      3 文件      4 文件     CI/CD+WebUI    README+面经
```

| 阶段 | 时间 | 目标 | 可演示成果 |
|------|------|------|-----------|
| 一 | Week 1-2 | Config + Message + Memory | 单元测试通过，数据模型跑通 |
| 二 | Week 2-3 | LLM Client + 重试 + 压缩 | 能调通 OpenAI/DeepSeek，支持流式输出 |
| 三 | Week 3-4 | Action + Role + React 循环 | 单个 Agent 能观察-思考-行动 |
| 四 | Week 4-5 | Environment + Team 编排 | 2+ Agent 协作完成简单任务 |
| 五 | Week 5-6 | 软件公司场景完整实现 | "写一个2048" → 生成完整代码仓库 |
| 六 | Week 6-8 | CI/CD + 文档 + 面试准备 | README 完善、架构图、面试问答 |

---

## 4. 第一阶段：基础设施层（Week 1-2）

> **原则**：纯标准库，零外部依赖。这是你面试时炫耀"基础扎实"的资本。

### 4.1 Config 模块

**文件**：`internal/foundation/config.go`

**设计要点**：
- 使用 `os` + `gopkg.in/yaml.v3`（唯一的第三方依赖）
- 支持环境变量覆盖（12-factor app 原则）
- 面试亮点：你实现了**配置优先级链**：环境变量 > 配置文件 > 默认值

```go
package foundation

import (
    "os"
    "gopkg.in/yaml.v3"
)

// Config 全局配置，聚合所有子配置
type Config struct {
    LLM       LLMConfig       `yaml:"llm"`       // 全局默认（最低优先级）
    Roles     RolesLLMConfig  `yaml:"roles"`     // ★ 按 Role（Agent）覆盖（中优先级）
    Actions   ActionsLLMConfig `yaml:"actions"`   // ★ 按 Action 覆盖（最高优先级）
    Workspace WorkspaceConfig `yaml:"workspace"`
    Agent     AgentConfig     `yaml:"agent"`
}

// LLMConfig 一个完整的 LLM 配置（Provider + 模型 + 参数）
// 这是"可继承的配置单元"——Role/Action 级别的覆盖只需填想改的字段
type LLMConfig struct {
    Provider    string            `yaml:"provider"`     // openai, deepseek, anthropic, ollama, custom
    Model       string            `yaml:"model"`        // gpt-4o, deepseek-chat, claude-sonnet-4-6
    APIKey      string            `yaml:"api_key"`      // 支持 ${ENV_VAR} 语法
    BaseURL     string            `yaml:"base_url"`     // 空 = 用 Provider 内置默认值
    Temperature float64           `yaml:"temperature"`
    MaxTokens   int               `yaml:"max_tokens"`
    TimeoutSec  int               `yaml:"timeout_seconds"`
    MaxRetries  int               `yaml:"max_retries"`
    Extra       map[string]string `yaml:"extra,omitempty"` // ★ Provider 专属配置（如 anthropic_version）
}

// RolesLLMConfig 按 Role 名称覆盖 LLM 配置
// key = Role 名称（如 "Alice", "Bob", "Alex", "Edward"）
// 只填想覆盖的字段，其余继承全局默认
type RolesLLMConfig map[string]*LLMConfig

// ActionsLLMConfig 按 Action 名称覆盖 LLM 配置（最高优先级）
// key = Action 名称（如 "WriteCode", "WritePRD"）
type ActionsLLMConfig map[string]*LLMConfig

type AgentConfig struct {
    MaxReactLoop  int     `yaml:"max_react_loop"`
    MaxBudgetUSD  float64 `yaml:"max_budget_usd"`
    MemoryMaxSize int     `yaml:"memory_max_size"`
}

type WorkspaceConfig struct {
    Path string `yaml:"path"` // 输出目录
}

// Load 从 YAML 文件加载配置，环境变量可覆盖
// 优先级：环境变量 > YAML 文件 > 默认值
func Load(path string) (*Config, error) {
    cfg := defaultConfig()

    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config: %w", err)
    }

    // 展开 ${VAR} 占位符
    data = []byte(os.ExpandEnv(string(data)))

    if err := yaml.Unmarshal(data, cfg); err != nil {
        return nil, fmt.Errorf("parse config: %w", err)
    }

    // 环境变量覆盖（最高优先级）
    cfg.applyEnvOverrides()

    return cfg, nil
}

func defaultConfig() *Config {
    return &Config{
        LLM: LLMConfig{
            Provider:    "deepseek",       // ★ 默认用 DeepSeek，便宜
            Model:       "deepseek-chat",
            Temperature: 0.3,
            MaxTokens:   4096,
            TimeoutSec:  120,
            MaxRetries:  3,
        },
        Actions: ActionsLLMConfig{},        // 默认无 Action 级别覆盖
        Roles:   RolesLLMConfig{},          // 默认无 Role 级别覆盖
        Agent: AgentConfig{
            MaxReactLoop:  10,
            MaxBudgetUSD:  5.0,
            MemoryMaxSize: 100,
        },
        Workspace: WorkspaceConfig{
            Path: "./workspace",
        },
    }
}

// applyEnvOverrides 环境变量覆盖配置文件的值（12-factor 原则）
func (c *Config) applyEnvOverrides() {
    if v := os.Getenv("MYAGENT_LLM_PROVIDER"); v != "" {
        c.LLM.Provider = v
    }
    if v := os.Getenv("MYAGENT_LLM_API_KEY"); v != "" {
        c.LLM.APIKey = v
    }
    if v := os.Getenv("MYAGENT_LLM_MODEL"); v != "" {
        c.LLM.Model = v
    }
    if v := os.Getenv("MYAGENT_LLM_BASE_URL"); v != "" {
        c.LLM.BaseURL = v
    }
}
```

**面试话术**：
> "配置模块我参考了 12-factor app 的设计原则，实现了三层优先级覆盖：环境变量 > 配置文件 > 默认值。API Key 这种敏感信息通过环境变量注入，不会写死在配置文件里。"

### 4.2 Message 模块

**文件**：`internal/foundation/message.go`

**设计要点**：
- `cause_by` 和 `send_to` 是消息路由的**核心字段**
- 常量定义广播/回环的特殊路由地址
- `ShouldSendTo` 方法封装路由匹配逻辑

```go
package foundation

import (
    "time"
    "github.com/google/uuid"
)

// 特殊路由地址
const (
    RouteToAll  = "<all>"   // 广播给所有角色
    RouteToSelf = "<self>"  // 回环给自己
)

// Role 标识
const (
    RoleUser    = "user"
    RoleSystem  = "system"
    RoleAssistant = "assistant"
)

// Message 智能体之间的通信单元
// 这是整个框架的数据载体，类比微服务中的消息体
type Message struct {
    ID              string         `json:"id"`
    Content         string         `json:"content"`           // 自然语言内容
    InstructContent any            `json:"instruct_content"`  // 结构化数据（PRD、代码等）
    Role            string         `json:"role"`              // user/system/assistant
    CauseBy         string         `json:"cause_by"`          // ★ 哪个 Action 产生（路由依据）
    SentFrom        string         `json:"sent_from"`         // ★ 哪个 Role 发送
    SendTo          []string       `json:"send_to"`           // ★ 接收者列表
    Metadata        map[string]any `json:"metadata"`          // 扩展元数据
    Timestamp       time.Time      `json:"timestamp"`
}

// NewUserMessage 创建用户消息（广播给所有角色）
func NewUserMessage(content string) *Message {
    return &Message{
        ID:        uuid.New().String(),
        Content:   content,
        Role:      RoleUser,
        CauseBy:   "UserRequirement",
        SentFrom:  "User",
        SendTo:    []string{RouteToAll},
        Timestamp: time.Now(),
    }
}

// NewSystemMessage 系统消息，由 Agent 产生
func NewSystemMessage(content string, causedBy, sentFrom string) *Message {
    return &Message{
        ID:        uuid.New().String(),
        Content:   content,
        Role:      RoleSystem,
        CauseBy:   causedBy,
        SentFrom:  sentFrom,
        SendTo:    []string{RouteToAll},
        Timestamp: time.Now(),
    }
}

// ShouldSendTo 判断消息是否应该发送给指定角色
func (m *Message) ShouldSendTo(roleName string) bool {
    for _, target := range m.SendTo {
        if target == RouteToAll || target == roleName || target == "*" {
            return true
        }
    }
    return false
}

// ShouldObserve 判断指定角色是否应该关注此消息
// watchKeys: 该角色关注的 cause_by 集合
func (m *Message) ShouldObserve(watchKeys map[string]bool) bool {
    if len(watchKeys) == 0 {
        return true // 空 = 关注所有
    }
    return watchKeys[m.CauseBy]
}
```

### 4.3 Memory 模块

**文件**：`internal/foundation/memory.go`

**设计要点**：
- 内存存储 + `cause_by` 索引（O(1) 查找）
- 线程安全（`sync.RWMutex`）
- 支持按多种维度查询

```go
package foundation

import "sync"

// Memory 消息存储器
// 既是存储层，也是角色获取历史上下文的接口
type Memory struct {
    mu      sync.RWMutex
    storage []*Message                    // 有序消息列表
    index   map[string][]*Message         // cause_by → 消息列表索引
    maxSize int                           // 容量上限
}

func NewMemory(maxSize int) *Memory {
    return &Memory{
        storage: make([]*Message, 0, maxSize),
        index:   make(map[string][]*Message),
        maxSize: maxSize,
    }
}

// Add 添加消息（并发安全）
func (m *Memory) Add(msg *Message) {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.storage = append(m.storage, msg)
    m.index[msg.CauseBy] = append(m.index[msg.CauseBy], msg)

    // 超出容量时淘汰最旧的消息（FIFO）
    if len(m.storage) > m.maxSize {
        removed := m.storage[0]
        m.storage = m.storage[1:]
        // 从索引中移除（简化实现，生产环境可优化）
        m.removeFromIndex(removed)
    }
}

// Get 获取最近 k 条消息，k=0 返回全部
func (m *Memory) Get(k int) []*Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if k == 0 || k > len(m.storage) {
        k = len(m.storage)
    }
    result := make([]*Message, k)
    copy(result, m.storage[len(m.storage)-k:])
    return result
}

// GetByAction 按 cause_by 查找消息
func (m *Memory) GetByAction(actionName string) []*Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    msgs, ok := m.index[actionName]
    if !ok {
        return nil
    }
    result := make([]*Message, len(msgs))
    copy(result, msgs)
    return result
}

// GetByActions 按多个 cause_by 查找（OR 逻辑）
func (m *Memory) GetByActions(actionNames ...string) []*Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    var result []*Message
    for _, name := range actionNames {
        if msgs, ok := m.index[name]; ok {
            result = append(result, msgs...)
        }
    }
    return result
}

// GetByRole 按发送者查找
func (m *Memory) GetByRole(roleName string) []*Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    var result []*Message
    for _, msg := range m.storage {
        if msg.SentFrom == roleName {
            result = append(result, msg)
        }
    }
    return result
}

// Last 获取最后一条消息
func (m *Memory) Last() *Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if len(m.storage) == 0 {
        return nil
    }
    return m.storage[len(m.storage)-1]
}

// FindNews 查找观察者尚未看到的消息
func (m *Memory) FindNews(observed []string, k int) []*Message {
    m.mu.RLock()
    defer m.mu.RUnlock()

    observedSet := make(map[string]bool, len(observed))
    for _, id := range observed {
        observedSet[id] = true
    }

    var news []*Message
    // 从后往前找最新的 k 条未读消息
    for i := len(m.storage) - 1; i >= 0 && len(news) < k; i-- {
        if !observedSet[m.storage[i].ID] {
            news = append(news, m.storage[i])
        }
    }
    return news
}

func (m *Memory) removeFromIndex(msg *Message) {
    msgs := m.index[msg.CauseBy]
    for i, indexed := range msgs {
        if indexed.ID == msg.ID {
            m.index[msg.CauseBy] = append(msgs[:i], msgs[i+1:]...)
            break
        }
    }
}

// Count 消息总数
func (m *Memory) Count() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return len(m.storage)
}

// Clear 清空所有消息
func (m *Memory) Clear() {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.storage = m.storage[:0]
    m.index = make(map[string][]*Message)
}
```

**面试话术**：
> "Memory 模块我设计成了并发安全的消息存储，支持按 cause_by、role、content 多维度索引。消息淘汰用的是 FIFO 策略，避免无限膨胀。RWMutex 保证了大量读场景下的性能——获取历史消息是读多写少的场景，读锁不互斥。"

---

## 5. 第二阶段：LLM 调用层（Week 2-3）

> **核心设计目标**：用户可以通过 YAML 配置自由切换任何 LLM 提供商，框架内部对上层完全透明。
>
> **设计原则**：面向接口编程 + 适配器模式。框架定义统一的 `Client` 接口和 `ChatMessage`/`ChatResponse` 内部类型，每个 Provider 各自负责把自己的 API 格式翻译成内部格式。

### 5.0 设计总览——多 Provider + 三级配置架构

```
                         config.yaml
                    ┌─────────────────────┐
                    │ llm: (全局默认)       │  ← 第 1 层：兜底
                    │ roles:               │  ← 第 2 层：Agent 级别
                    │   Alice: {...}       │
                    │   Alex: {...}        │
                    │ actions:             │  ← 第 3 层：Action 级别（最高）
                    │   WriteCode: {...}   │
                    └────────┬────────────┘
                             │
                    ┌────────▼────────────┐
                    │   LLMResolver       │
                    │   Resolve(roleName, │
                    │     actionName)      │
                    │   → Provider + Cfg  │
                    └────────┬────────────┘
                             │
                             ▼
上层（Action/Role）
    │
    │  只依赖 Client 接口 + ChatMessage/ChatResponse 内部类型
    │  完全不知道底层是 OpenAI 还是 DeepSeek 还是 Claude
    │
    ▼
┌──────────────────────────────────────────────────┐
│              Client 接口（client.go）              │
│  Chat(ctx, []ChatMessage) → (*ChatResponse, err) │
│  ChatStream(ctx, []ChatMessage) → (<-chan, err)  │
│  CountTokens([]ChatMessage) → int                │
└──────────────────────────────────────────────────┘
         ▲                ▲                ▲
         │                │                │
┌────────┴──────┐ ┌──────┴──────┐ ┌──────┴──────────┐
│ OpenAI 适配器  │ │Anthropic适配│ │ Custom 适配器    │
│ provider_     │ │provider_    │ │ provider_        │
│ openai.go     │ │anthropic.go │ │ custom.go        │
│               │ │             │ │                  │
│ 请求格式：     │ │ 请求格式：   │ │ 请求格式：        │
│ POST /v1/     │ │ POST /v1/   │ │ 用户配置的任意    │
│ chat/         │ │ messages    │ │ OpenAI 兼容端点   │
│ completions   │ │             │ │                  │
│               │ │ 认证头：     │ │ 认证头：          │
│ 认证头：       │ │ x-api-key   │ │ 用户配置          │
│ Bearer xxx    │ │             │ │                  │
└───────────────┘ └─────────────┘ └──────────────────┘
         ▲                ▲                ▲
         │                │                │
    ┌────┴────────────────┴────────────────┴────┐
    │       ProviderRegistry（registry.go）       │
    │   Register("openai", factory)              │
    │   Register("deepseek", factory)            │
    │   Register("anthropic", factory)           │
    │   Register("custom", factory)              │  ← ★ 万能兜底
    │   Register("ollama", factory)              │
    └────────────────────────────────────────────┘
```

**关键设计决策**：

| 问题 | 错误做法（上一版） | 正确做法（本版） |
|------|-------------------|-----------------|
| DeepSeek 怎么支持？ | 注册到 `openaiClient`，因为"兼容 OpenAI 格式" | 有自己的 `deepseekClient`，设置正确的默认 BaseURL |
| Anthropic 怎么支持？ | 写不了，API 格式完全不同 | 独立的 `anthropicClient`，内部做 Messages API 格式转换 |
| 用户自建的代理/小众 API？ | 改代码，在 init() 里加注册 | 用 `custom` Provider，YAML 里配 BaseURL + AuthHeader 即可 |
| 不同 Agent 用不同模型？ | 不支持 | `roles:` 配置按 Agent 覆盖，`actions:` 按 Action 覆盖 |
| 配置粒度？ | 全局一刀切 | 三级继承：Action > Role > 全局，字段级合并 |

### 5.1 配置解析——三级继承链

Config 类型定义见 [§4.1](#41-config-模块)，这里只补充 LLM 包内的枚举类型。

```go
// ========== Provider 类型枚举 ==========

// ProviderType 枚举所有内置 Provider 类型
type ProviderType string

const (
    ProviderOpenAI    ProviderType = "openai"
    ProviderDeepSeek  ProviderType = "deepseek"
    ProviderAnthropic ProviderType = "anthropic"
    ProviderOllama    ProviderType = "ollama"
    ProviderCustom    ProviderType = "custom"     // ★ 万能兜底
)

// （其余 Config 类型定义在 §4.1 — LLMConfig, RolesLLMConfig, ActionsLLMConfig）
```

**YAML 配置**（完整示例见 [§9.2](#92-配置文件)）：

```yaml
llm:
  provider: deepseek          # 第 1 层：全局默认
  model: deepseek-chat

roles:                        # 第 2 层：按 Agent（★ 核心）
  Alex:
    provider: anthropic       # Engineer 用 Claude 写代码
    model: claude-opus-4-8
    temperature: 0.1
  Edward:
    provider: deepseek        # QA 用 DeepSeek 写测试（便宜）
    model: deepseek-chat

actions:                      # 第 3 层：按 Action（最高优先级）
  WriteCodeReview:
    provider: anthropic
    model: claude-haiku-4-5   # 代码审查用 Haiku 更快更便宜
```

**面试话术**：
> "LLM 配置我设计成了三级继承：Action > Role > 全局。每个 Agent 可以有自己的 LLM——
> Alex 写代码用 Claude Opus 保证质量，Edward 写测试用 DeepSeek 控制成本。
> 只填想覆盖的字段，其余自动继承。切换只改 YAML，Go 代码零改动。"

### 5.2 第二步：Client 接口（上层唯一依赖）

**文件**：`internal/llm/client.go`

```go
package llm

import "context"

// ==========================================
// Client 接口 —— 上层只依赖这个接口
// ==========================================

// Client LLM 调用的统一接口
// 所有 Provider 必须实现此接口。
// 上层代码（Action/Role）只 import 这个接口，不依赖任何具体 Provider。
type Client interface {
    // Chat 同步对话
    Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error)

    // ChatStream 流式对话，返回 token 通道
    ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error)

    // CountTokens 估算消息的 token 数
    CountTokens(messages []ChatMessage) int

    // Name 返回 Provider 名称 + 模型名，用于日志
    Name() string
}

// ==========================================
// ★ 框架内部统一类型——所有 Provider 都翻译成这个格式
// ==========================================

// ChatMessage 框架内部的对话消息格式
// 注意：这不是 OpenAI 的格式！是框架自己的抽象。
// 每个 Provider 负责把自己的 API 格式翻译成这个。
type ChatMessage struct {
    Role    string `json:"role"`    // system / user / assistant
    Content string `json:"content"`
}

// ChatResponse 框架内部的 LLM 响应格式
type ChatResponse struct {
    Content      string      `json:"content"`
    FinishReason string      `json:"finish_reason"` // stop / length / tool_calls
    Usage        *TokenUsage `json:"usage,omitempty"`
}

// TokenUsage token 使用统计
type TokenUsage struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`
}

// StreamChunk 流式输出的一个 token 片段
type StreamChunk struct {
    Content string `json:"content"`
    Done    bool   `json:"done"`
    Error   error  `json:"-"`
}
```

### 5.3 第三步：Provider 注册 + 工厂（不改代码就能加新的）

**文件**：`internal/llm/registry.go`

```go
package llm

import (
    "fmt"
    "sync"
)

// ==========================================
// Provider 注册表 —— init() 自动注册
// ==========================================

// ProviderConfig 传给每个 Provider 工厂的配置
// 字段含义由各 Provider 自行解释，registry 不做假设
type ProviderConfig struct {
    Model       string
    APIKey      string
    BaseURL     string
    Temperature float64
    MaxTokens   int
    TimeoutSec  int
    Extra       map[string]string // ★ Provider 专属参数（如 anthropic_version）
}

// ProviderFactory 创建 Client 的工厂函数
type ProviderFactory func(cfg ProviderConfig) (Client, error)

var (
    mu        sync.RWMutex
    factories = make(map[string]ProviderFactory)
)

// Register 注册一个 Provider 工厂（各 provider_xxx.go 的 init() 里调用）
func Register(name string, factory ProviderFactory) {
    mu.Lock()
    defer mu.Unlock()
    factories[name] = factory
}

// NewClient 创建客户端——框架唯一的创建入口
func NewClient(provider string, cfg ProviderConfig) (Client, error) {
    mu.RLock()
    factory, ok := factories[provider]
    mu.RUnlock()

    if !ok {
        return nil, fmt.Errorf("unknown provider: %q (available: %v)", provider, AvailableProviders())
    }
    return factory(cfg)
}

// AvailableProviders 列出所有已注册的 Provider
func AvailableProviders() []string {
    mu.RLock()
    defer mu.RUnlock()
    names := make([]string, 0, len(factories))
    for name := range factories {
        names = append(names, name)
    }
    return names
}

// ==========================================
// LLMResolver —— 解析"这个 Role 的这个 Action 该用哪个 Client"
// ==========================================
// 三级优先级：Action 覆盖 > Role 覆盖 > 全局默认
// ==========================================

// LLMResolver 根据全局配置 + Roles 覆盖 + Actions 覆盖解析最终的 ProviderConfig
type LLMResolver struct {
    defaultCfg     ProviderConfig              // 全局默认
    roleOverrides  map[string]ProviderConfig    // key = Role 名称
    actionOverrides map[string]ProviderConfig   // key = Action 名称
}

// NewLLMResolver 从 Config 构造解析器
func NewLLMResolver(cfg *foundation.Config) *LLMResolver {
    r := &LLMResolver{
        roleOverrides:   make(map[string]ProviderConfig),
        actionOverrides: make(map[string]ProviderConfig),
    }
    r.defaultCfg = llmConfigToProviderConfig(cfg.LLM)

    for roleName, override := range cfg.Roles {
        r.roleOverrides[roleName] = llmConfigToProviderConfig(override)
    }
    for actionName, override := range cfg.Actions {
        r.actionOverrides[actionName] = llmConfigToProviderConfig(override)
    }
    return r
}

// Resolve 为指定 Role + Action 解析最终的 Provider + Config
//
// ★ 三级优先级（从高到低）：
//   1. Actions 覆盖（最精确）—— 如 WriteCode 强制用 Claude
//   2. Roles 覆盖（中粒度）  —— 如 Alex(Engineer) 这个角色整体用 Claude
//   3. 全局默认（兜底）       —— 其他情况用 deepseek-chat
//
// 每层只覆盖已设置的字段，其余继承下一层
func (r *LLMResolver) Resolve(roleName, actionName string) (string, ProviderConfig) {
    // 从默认值开始
    result := r.defaultCfg

    // 第 2 层：Role 覆盖
    if roleCfg, ok := r.roleOverrides[roleName]; ok {
        result = r.merge(result, roleCfg)
    }

    // 第 3 层：Action 覆盖（最高优先级）
    if actionCfg, ok := r.actionOverrides[actionName]; ok {
        result = r.merge(result, actionCfg)
    }

    return result.Provider, result
}

// merge 用 override 中非零值覆盖 base 中对应字段
func (r *LLMResolver) merge(base, override ProviderConfig) ProviderConfig {
    if override.Provider != "" {
        base.Provider = override.Provider
    }
    if override.Model != "" {
        base.Model = override.Model
    }
    if override.APIKey != "" {
        base.APIKey = override.APIKey
    }
    if override.BaseURL != "" {
        base.BaseURL = override.BaseURL
    }
    if override.Temperature != 0 {
        base.Temperature = override.Temperature
    }
    if override.MaxTokens != 0 {
        base.MaxTokens = override.MaxTokens
    }
    for k, v := range override.Extra {
        if base.Extra == nil {
            base.Extra = make(map[string]string)
        }
        base.Extra[k] = v
    }
    return base
}
```

### 5.4 第四步：各 Provider 独立实现

#### 5.4.1 OpenAI Provider

**文件**：`internal/llm/provider_openai.go`

```go
package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

func init() {
    // ★ 只注册 "openai"，不和 DeepSeek/Ollama 混在一起
    Register("openai", newOpenAI)
}

// ========== OpenAI 原生 API 的请求/响应格式 ==========

type openaiChatRequest struct {
    Model       string        `json:"model"`
    Messages    []openaiMessage `json:"messages"`
    Temperature float64       `json:"temperature,omitempty"`
    MaxTokens   int           `json:"max_tokens,omitempty"`
}

type openaiMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type openaiChatResponse struct {
    Choices []struct {
        Message      openaiMessage `json:"message"`
        FinishReason string        `json:"finish_reason"`
        Delta        struct {
            Content string `json:"content"`
        } `json:"delta"`
    } `json:"choices"`
    Usage struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
    } `json:"usage"`
}

// ========== 适配器实现 ==========

type openaiClient struct {
    cfg        ProviderConfig
    baseURL    string
    httpClient *http.Client
}

func newOpenAI(cfg ProviderConfig) (Client, error) {
    baseURL := cfg.BaseURL
    if baseURL == "" {
        baseURL = "https://api.openai.com/v1" // OpenAI 的默认地址
    }
    return &openaiClient{
        cfg:     cfg,
        baseURL: baseURL,
        httpClient: &http.Client{
            Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
        },
    }, nil
}

func (c *openaiClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
    // ★ 关键：把框架内部类型 → OpenAI API 格式
    oaiMsgs := make([]openaiMessage, len(messages))
    for i, m := range messages {
        oaiMsgs[i] = openaiMessage{Role: m.Role, Content: m.Content}
    }

    body := openaiChatRequest{
        Model:       c.cfg.Model,
        Messages:    oaiMsgs,
        Temperature: c.cfg.Temperature,
        MaxTokens:   c.cfg.MaxTokens,
    }

    reqBody, _ := json.Marshal(body)
    req, err := http.NewRequestWithContext(ctx, "POST",
        c.baseURL+"/chat/completions",
        bytes.NewReader(reqBody))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("openai: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(body))
    }

    var oaiResp openaiChatResponse
    if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
        return nil, fmt.Errorf("openai decode: %w", err)
    }

    // ★ 关键：把 OpenAI API 格式 → 框架内部类型
    if len(oaiResp.Choices) == 0 {
        return nil, fmt.Errorf("openai: empty response")
    }
    return &ChatResponse{
        Content:      oaiResp.Choices[0].Message.Content,
        FinishReason: oaiResp.Choices[0].FinishReason,
        Usage: &TokenUsage{
            PromptTokens:     oaiResp.Usage.PromptTokens,
            CompletionTokens: oaiResp.Usage.CompletionTokens,
            TotalTokens:      oaiResp.Usage.TotalTokens,
        },
    }, nil
}

func (c *openaiClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
    // ... 流式实现类似，省略，见上一版
    return nil, fmt.Errorf("not implemented yet")
}

func (c *openaiClient) CountTokens(messages []ChatMessage) int {
    return estimateTokens(messages) // 用 tiktoken 估算
}

func (c *openaiClient) Name() string {
    return "openai/" + c.cfg.Model
}
```

#### 5.4.2 Anthropic Provider（★ 展示真正的适配器模式）

**文件**：`internal/llm/provider_anthropic.go`

```go
package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

func init() {
    Register("anthropic", newAnthropic)
}

// ========== Anthropic Messages API 原生格式 ==========

type anthropicRequest struct {
    Model       string             `json:"model"`
    MaxTokens   int                `json:"max_tokens"`
    Temperature float64            `json:"temperature,omitempty"`
    System      string             `json:"system,omitempty"` // ★ 系统提示单独字段
    Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
    Role    string              `json:"role"`             // "user" | "assistant"
    Content []anthropicContent  `json:"content"`          // ★ 数组格式
}

type anthropicContent struct {
    Type string `json:"type"`   // "text"
    Text string `json:"text"`
}

type anthropicResponse struct {
    Content []anthropicContent `json:"content"`
    StopReason string          `json:"stop_reason"`
    Usage struct {
        InputTokens  int `json:"input_tokens"`
        OutputTokens int `json:"output_tokens"`
    } `json:"usage"`
}

// ========== 适配器实现 ==========

type anthropicClient struct {
    cfg        ProviderConfig
    baseURL    string
    apiVersion string
    httpClient *http.Client
}

func newAnthropic(cfg ProviderConfig) (Client, error) {
    baseURL := cfg.BaseURL
    if baseURL == "" {
        baseURL = "https://api.anthropic.com" // Anthropic 的默认地址
    }
    apiVersion := cfg.Extra["api_version"]
    if apiVersion == "" {
        apiVersion = "2023-06-01"
    }
    return &anthropicClient{
        cfg:        cfg,
        baseURL:    baseURL,
        apiVersion: apiVersion,
        httpClient: &http.Client{
            Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
        },
    }, nil
}

func (c *anthropicClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
    // ★ 关键适配：框架的 ChatMessage → Anthropic 的 Messages API 格式
    var systemPrompt string
    var anthropicMsgs []anthropicMessage

    for _, m := range messages {
        if m.Role == "system" {
            // Anthropic 的 system 是顶层字段，不在 messages 数组里
            systemPrompt += m.Content + "\n"
        } else {
            // user/assistant → anthropic role (直接对应)
            role := m.Role
            if role == "user" || role == "assistant" {
                anthropicMsgs = append(anthropicMsgs, anthropicMessage{
                    Role: role,
                    Content: []anthropicContent{
                        {Type: "text", Text: m.Content},
                    },
                })
            }
        }
    }

    body := anthropicRequest{
        Model:       c.cfg.Model,
        MaxTokens:   c.cfg.MaxTokens,
        Temperature: c.cfg.Temperature,
        System:      systemPrompt,
        Messages:    anthropicMsgs,
    }

    reqBody, _ := json.Marshal(body)
    req, err := http.NewRequestWithContext(ctx, "POST",
        c.baseURL+"/v1/messages",  // ★ Anthropic 的路径和 OpenAI 不同
        bytes.NewReader(reqBody))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    // ★ Anthropic 的认证头是 x-api-key，不是 Bearer token
    req.Header.Set("x-api-key", c.cfg.APIKey)
    req.Header.Set("anthropic-version", c.apiVersion)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("anthropic: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, string(body))
    }

    var antResp anthropicResponse
    if err := json.NewDecoder(resp.Body).Decode(&antResp); err != nil {
        return nil, fmt.Errorf("anthropic decode: %w", err)
    }

    // ★ 把 Anthropic 的响应格式 → 框架统一格式
    content := ""
    if len(antResp.Content) > 0 {
        content = antResp.Content[0].Text
    }

    return &ChatResponse{
        Content:      content,
        FinishReason: antResp.StopReason,
        Usage: &TokenUsage{
            PromptTokens:     antResp.Usage.InputTokens,
            CompletionTokens: antResp.Usage.OutputTokens,
            TotalTokens:      antResp.Usage.InputTokens + antResp.Usage.OutputTokens,
        },
    }, nil
}

func (c *anthropicClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
    // Anthropic 的 SSE 流式格式也是自己的，需要单独解析
    return nil, fmt.Errorf("streaming not implemented yet for anthropic")
}

func (c *anthropicClient) CountTokens(messages []ChatMessage) int {
    return estimateTokens(messages)
}

func (c *anthropicClient) Name() string {
    return "anthropic/" + c.cfg.Model
}
```

#### 5.4.3 Custom Provider（★ 用户的万能兜底）

**文件**：`internal/llm/provider_custom.go`

```go
package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

func init() {
    Register("custom", newCustom)
}

// ==========================================
// Custom Provider —— 适配任意 OpenAI 兼容 API
//
// 用户只需要在 YAML 里配置：
//   provider: custom
//   base_url: https://my-proxy.com/v1
//   extra:
//     auth_header: "X-API-Key"       ← 自定义认证头名称
//     auth_prefix: "Bearer"          ← 可选，默认 "Bearer"
// ==========================================

type customClient struct {
    cfg        ProviderConfig
    baseURL    string
    authHeader string // 从 extra 读取
    authPrefix string // 从 extra 读取
    httpClient *http.Client
}

func newCustom(cfg ProviderConfig) (Client, error) {
    if cfg.BaseURL == "" {
        return nil, fmt.Errorf("custom provider requires base_url in config")
    }
    authHeader := cfg.Extra["auth_header"]
    if authHeader == "" {
        authHeader = "Authorization" // 默认标准头
    }
    authPrefix := cfg.Extra["auth_prefix"]
    if authPrefix == "" {
        authPrefix = "Bearer" // 默认 Bearer token
    }
    return &customClient{
        cfg:        cfg,
        baseURL:    cfg.BaseURL,
        authHeader: authHeader,
        authPrefix: authPrefix,
        httpClient: &http.Client{
            Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
        },
    }, nil
}

// Chat 使用 OpenAI 兼容格式调用用户配置的端点
func (c *customClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
    // 复用 OpenAI 的请求格式（因为绝大多数自建 API 都是 OpenAI 兼容的）
    // 但路径和认证头由用户配置
    body := map[string]interface{}{
        "model":       c.cfg.Model,
        "messages":    messages,
        "temperature": c.cfg.Temperature,
        "max_tokens":  c.cfg.MaxTokens,
    }

    reqBody, _ := json.Marshal(body)
    req, err := http.NewRequestWithContext(ctx, "POST",
        c.baseURL+"/chat/completions",
        bytes.NewReader(reqBody))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    // ★ 使用用户配置的认证头格式
    if c.cfg.APIKey != "" {
        req.Header.Set(c.authHeader, c.authPrefix+" "+c.cfg.APIKey)
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("custom provider (%s): %w", c.baseURL, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("custom provider HTTP %d: %s", resp.StatusCode, string(body))
    }

    // 解析 OpenAI 兼容响应
    var result struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
            FinishReason string `json:"finish_reason"`
        } `json:"choices"`
        Usage *TokenUsage `json:"usage"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("custom provider decode: %w", err)
    }

    if len(result.Choices) == 0 {
        return nil, fmt.Errorf("custom provider: empty response")
    }

    return &ChatResponse{
        Content:      result.Choices[0].Message.Content,
        FinishReason: result.Choices[0].FinishReason,
        Usage:        result.Usage,
    }, nil
}

func (c *customClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
    // SSE 流式解析，和 OpenAI 格式一致
    return nil, fmt.Errorf("not implemented yet")
}

func (c *customClient) CountTokens(messages []ChatMessage) int {
    return estimateTokens(messages) // 保守估算
}

func (c *customClient) Name() string {
    return "custom/" + c.cfg.Model + " @ " + c.baseURL
}
```

#### 5.4.4 DeepSeek Provider

**文件**：`internal/llm/provider_deepseek.go`

```go
package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

func init() {
    Register("deepseek", newDeepSeek)
}

type deepseekClient struct {
    *openaiClient // ★ DeepSeek API 是 OpenAI 兼容的，直接复用
}

func newDeepSeek(cfg ProviderConfig) (Client, error) {
    if cfg.BaseURL == "" {
        cfg.BaseURL = "https://api.deepseek.com/v1" // DeepSeek 默认地址
    }
    return &deepseekClient{
        openaiClient: &openaiClient{
            cfg:     cfg,
            baseURL: cfg.BaseURL,
            httpClient: &http.Client{
                Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
            },
        },
    }, nil
}

// Chat/ChatStream/CountTokens 全部复用 openaiClient 的方法
// Name 覆盖为自己的标识
func (c *deepseekClient) Name() string {
    return "deepseek/" + c.cfg.Model
}
```

#### 5.4.5 Ollama Provider

**文件**：`internal/llm/provider_ollama.go`

```go
package llm

import "net/http"
import "time"

func init() {
    Register("ollama", newOllama)
}

type ollamaClient struct {
    *openaiClient // Ollama v0.5+ 也支持 OpenAI 兼容端点
}

func newOllama(cfg ProviderConfig) (Client, error) {
    if cfg.BaseURL == "" {
        cfg.BaseURL = "http://localhost:11434/v1" // Ollama 默认本地地址
    }
    // API Key 在 Ollama 场景下为空，跳过认证
    return &ollamaClient{
        openaiClient: &openaiClient{
            cfg:     cfg,
            baseURL: cfg.BaseURL,
            httpClient: &http.Client{
                Timeout: time.Duration(cfg.TimeoutSec) * time.Second,
            },
        },
    }, nil
}

func (c *ollamaClient) Name() string {
    return "ollama/" + c.cfg.Model
}
```

### 5.5 第五步：Token 压缩 + 重试（和 Provider 解耦）

Token 压缩器和重试机制都是对 `Client` 接口的装饰，不依赖具体 Provider。这部分代码和上一版基本一致，文件是：

- `internal/llm/compressor.go` —— Token 压缩（4 种策略，上一版 §5.4）
- `internal/llm/retry.go` —— 指数退避重试（上一版 §5.5）
- `internal/llm/cost.go` —— 成本追踪（根据 Provider + Model 查价格表）

### 5.6 使用示例——框架上层如何创建 LLM Client

```go
// ==========================================
// 创建 Client —— 框架里的使用方式
// ==========================================

func createLLMClient(cfg *foundation.Config, roleName, actionName string) (llm.Client, error) {
    // 1. 解析：Role + Action → 最终该用哪个 Provider + Model
    //    三级优先级：Action 覆盖 > Role 覆盖 > 全局默认
    resolver := llm.NewLLMResolver(cfg)
    providerName, providerCfg := resolver.Resolve(roleName, actionName)
    // 例如：roleName="Alex", actionName="WriteCode"
    //   → 检查 actions.WriteCode 有没有覆盖？有 → 用那个
    //   → 没有 → 检查 roles.Alex 有没有覆盖？有 → 用那个
    //   → 都没有 → 用 llm 全局默认

    // 2. 通过注册表创建客户端（不需要知道具体类型）
    rawClient, err := llm.NewClient(providerName, providerCfg)
    if err != nil {
        return nil, err
    }

    // 3. 包装重试能力
    retryClient := llm.NewRetryableClient(rawClient, llm.DefaultRetryConfig())

    return retryClient, nil
}

// 使用示例
// Alex（Engineer）写代码 → 用 Claude Opus（代码最强）
codeClient, _ := createLLMClient(cfg, "Alex", "WriteCode")
resp, _ := codeClient.Chat(ctx, []llm.ChatMessage{
    {Role: "system", Content: "You are a senior engineer."},
    {Role: "user", Content: "Write a function to sort a list."},
})
fmt.Println(resp.Content)

// Edward（QA）写测试 → 用 DeepSeek（便宜，测试不需要最强模型）
testClient, _ := createLLMClient(cfg, "Edward", "WriteTest")
```

---

### 5.7 架构对比总结

```
配置粒度进化：

v1（上一版）：
  llm: { provider: deepseek, model: deepseek-chat }
  └── 整个框架一个 LLM，所有 Agent 共用

v2（当前版）：
  llm: { provider: deepseek, model: deepseek-chat }        ← 全局默认
  roles:                                                    ← ★ Role 级别
    Alex:    { provider: anthropic, model: claude-opus-4-8 }
    Edward:  { provider: deepseek, model: deepseek-chat }
  actions:                                                  ← Action 级别（更高优先级）
    WriteCode: { provider: anthropic, model: claude-opus-4-8 }

  解析流程：
  Resolve("Alex", "WriteCode")
    → 1. 检查 actions.WriteCode → 有！→ 用 Claude Opus
  Resolve("Edward", "WriteTest")
    → 1. 检查 actions.WriteTest → 无
    → 2. 检查 roles.Edward → 有！→ 用 DeepSeek
    → 3. 全局默认也不看
  Resolve("Alice", "WritePRD")  [Alice 没有在 roles 里配置]
    → 1. 检查 actions.WritePRD → 无
    → 2. 检查 roles.Alice → 无
    → 3. 用全局默认 → deepseek-chat
```

> **一句话摘要**：每个 Agent 可以有自己专属的 LLM API（如 Alex 用 Claude Opus 写代码，Edward 用 DeepSeek 写测试）。配置继承链是 Action > Role > 全局。只填你想改的字段，其余自动继承。**不改一行 Go 代码，只改 YAML。**

---

## 6. 第三阶段：Agent 核心层（Week 3-4）

> **这是整个项目最核心的模块**。面试时 70% 的问题会集中在这里。

### 6.1 Action 接口与基础实现

**文件**：`internal/action/action.go`

```go
package action

import (
    "context"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/llm"
)

// Action 原子操作的接口
// 每个具体的 Action（写PRD、写代码...）都实现此接口
type Action interface {
    // Name 返回 Action 名称（也是 cause_by 的值）
    Name() string

    // Run 执行动作
    // history 是上下文消息（来自 Memory），返回执行结果
    Run(ctx context.Context, history []*foundation.Message) (*ActionOutput, error)
}

// ActionOutput Action 的执行结果
type ActionOutput struct {
    Content         string `json:"content"`          // 自然语言输出
    InstructContent any    `json:"instruct_content"` // 结构化输出（如解析后的代码）
}

// BaseAction 提供 LLM 调用的基础能力，所有具体 Action 嵌入此结构体
type BaseAction struct {
    name       string       // Action 名称
    prefix     string       // 系统提示词前缀
    client     llm.Client   // LLM 客户端
    compressor *llm.Compressor
    node       *ActionNode  // 可选：结构化输出解析器
}

func NewBaseAction(name, prefix string, client llm.Client) *BaseAction {
    return &BaseAction{
        name:   name,
        prefix: prefix,
        client: client,
        compressor: llm.NewCompressor(
            llm.CompressPostCutByToken,
            128000, // 默认 128K token 上下文
        ),
    }
}

// AskLLM 向 LLM 发送请求的便捷方法
// 自动附加前缀、压缩历史、构造 prompt
func (a *BaseAction) AskLLM(ctx context.Context, prompt string, history []*foundation.Message) (string, error) {
    // 1. 构建消息列表
    messages := []llm.ChatMessage{
        {Role: "system", Content: a.prefix},
    }

    // 2. 将框架内的 Message 转换为 LLM 的 ChatMessage
    historyMsgs := a.frameToLLMMessages(history)

    // 3. 压缩
    historyMsgs = a.compressor.Compress(historyMsgs)
    messages = append(messages, historyMsgs...)

    // 4. 附加当前 prompt
    messages = append(messages, llm.ChatMessage{
        Role:    "user",
        Content: prompt,
    })

    // 5. 调用 LLM
    resp, err := a.client.Chat(ctx, messages)
    if err != nil {
        return "", fmt.Errorf("ask llm: %w", err)
    }

    return resp.Content, nil
}

// AskLLMStream 流式版本
func (a *BaseAction) AskLLMStream(ctx context.Context, prompt string, history []*foundation.Message) (<-chan *llm.StreamChunk, error) {
    messages := []llm.ChatMessage{
        {Role: "system", Content: a.prefix},
    }
    historyMsgs := a.frameToLLMMessages(history)
    historyMsgs = a.compressor.Compress(historyMsgs)
    messages = append(messages, historyMsgs...)
    messages = append(messages, llm.ChatMessage{
        Role: "user", Content: prompt,
    })

    return a.client.ChatStream(ctx, messages)
}

// Name 返回 Action 名称
func (a *BaseAction) Name() string {
    return a.name
}

// SetPrefix 修改系统提示词（允许运行时调整）
func (a *BaseAction) SetPrefix(prefix string) {
    a.prefix = prefix
}

// SetNode 设置结构化输出解析器
func (a *BaseAction) SetNode(node *ActionNode) {
    a.node = node
}

// frameToLLMMessages 将框架 Message 转换为 LLM 的 ChatMessage
func (a *BaseAction) frameToLLMMessages(msgs []*foundation.Message) []llm.ChatMessage {
    result := make([]llm.ChatMessage, 0, len(msgs))
    for _, msg := range msgs {
        role := msg.Role
        if role == "" {
            role = "user"
        }
        result = append(result, llm.ChatMessage{
            Role:    role,
            Content: msg.Content,
            Name:    msg.SentFrom,
        })
    }
    return result
}
```

### 6.2 结构化输出解析（ActionNode）

**文件**：`internal/action/node.go`

```go
package action

import (
    "encoding/json"
    "fmt"
    "regexp"
    "strings"
)

// ActionNode 从 LLM 的非结构化输出中提取结构化数据
// 这是 MetaGPT ActionNode 的 Go 版本
//
// 核心思想：LLM 输出是自然语言 + 可能嵌有代码块/JSON，
// ActionNode 用模板 + 正则提取关键字段
type ActionNode struct {
    Schema map[string]string // 字段名 → 提取提示
}

func NewActionNode(schema map[string]string) *ActionNode {
    return &ActionNode{Schema: schema}
}

// Fill 从 LLM 输出中填充结构化字段
// 返回 map[string]string 或尝试解析到传入的 struct 指针
func (n *ActionNode) Fill(llmOutput string, target any) error {
    // 策略 1：尝试直接解析为 JSON
    if jsonStr := extractJSON(llmOutput); jsonStr != "" {
        if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
            return nil
        }
    }

    // 策略 2：按模板字段逐个提取
    result := make(map[string]string)
    for field, hint := range n.Schema {
        extracted := extractField(llmOutput, field, hint)
        result[field] = extracted
    }

    // 将 map 序列化为 JSON 再反序列化到 target
    data, _ := json.Marshal(result)
    return json.Unmarshal(data, target)
}

// extractJSON 从文本中提取 JSON 块
func extractJSON(text string) string {
    // 匹配 ```json ... ``` 或 ```...``` 或 裸 JSON
    patterns := []string{
        "(?s)```json\\s*\\n(.*?)\\n```",
        "(?s)```\\s*\\n(.*?)\\n```",
        "(?s)\\{.*\\}",
    }
    for _, p := range patterns {
        re := regexp.MustCompile(p)
        if matches := re.FindStringSubmatch(text); len(matches) > 1 {
            return strings.TrimSpace(matches[1])
        } else if len(matches) == 1 {
            return strings.TrimSpace(matches[0])
        }
    }
    return ""
}

// extractField 根据字段名和提示从文本中提取值
func extractField(text, field, hint string) string {
    // 查找 "field: value" 或 "field：value" 模式
    patterns := []string{
        fmt.Sprintf(`(?i)%s\s*[:：]\s*(.+?)(?:\n|$)`, regexp.QuoteMeta(field)),
        fmt.Sprintf(`(?i)\*\*%s\*\*\s*[:：]\s*(.+?)(?:\n|$)`, regexp.QuoteMeta(field)),
    }
    for _, p := range patterns {
        re := regexp.MustCompile(p)
        if matches := re.FindStringSubmatch(text); len(matches) > 1 {
            return strings.TrimSpace(matches[1])
        }
    }
    return ""
}
```

### 6.3 Role 核心——整个框架的心脏

**文件**：`internal/role/role.go`

这是最关键的实现，面试时可能会让你现场讲解代码逻辑。

```go
package role

import (
    "context"
    "fmt"
    "log"
    "sync"

    "my-agent-framework/internal/action"
    "my-agent-framework/internal/foundation"
)

// ReactMode 反应模式
type ReactMode int

const (
    ReactByOrder    ReactMode = iota // 按 Actions 列表顺序执行（SOP 模式）
    ReactReAct                       // LLM 动态选择下一个 Action
    ReactPlanAndAct                  // 先规划，后执行
)

// Role 智能体的核心抽象
//
// 设计思想：
// - 每个 Role 是一个独立的执行单元，运行在自己的 goroutine 中
// - msgBuffer（channel）是 Role 的"邮箱"，由 Environment 投递消息
// - observe → think → act 是核心循环，模拟人类的认知过程
type Role struct {
    // === 身份信息 ===
    Name        string `json:"name"`
    Profile     string `json:"profile"`
    Goal        string `json:"goal"`
    Constraints string `json:"constraints"`
    Desc        string `json:"desc"`

    // === 行为定义 ===
    actions   []action.Action // 可执行的动作列表
    reactMode ReactMode        // 反应模式
    watch     map[string]bool  // 关注的 cause_by 集合（为空则关注所有）

    // === 运行时状态 ===
    state     int    // 当前动作索引，-1 表示 idle/terminated
    msgBuffer chan *foundation.Message    // 消息邮箱（channel）
    memory    *foundation.Memory          // 持久化消息历史
    env       *Environment                // 所在环境
    observed  []string                    // 已观察过的消息 ID

    // === 生命周期 ===
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// RoleOption 函数式选项模式
type RoleOption func(*Role)

func WithActions(actions ...action.Action) RoleOption {
    return func(r *Role) { r.actions = actions }
}

func WithReactMode(mode ReactMode) RoleOption {
    return func(r *Role) { r.reactMode = mode }
}

func WithWatch(causeByList ...string) RoleOption {
    return func(r *Role) {
        for _, c := range causeByList {
            r.watch[c] = true
        }
    }
}

func WithMemory(m *foundation.Memory) RoleOption {
    return func(r *Role) { r.memory = m }
}

// NewRole 创建一个新的 Role
func NewRole(name string, opts ...RoleOption) *Role {
    r := &Role{
        Name:      name,
        reactMode: ReactByOrder, // 默认 SOP 模式
        watch:     make(map[string]bool),
        state:     0,
        msgBuffer: make(chan *foundation.Message, 100), // 缓冲 100 条消息
    }
    for _, opt := range opts {
        opt(r)
    }
    return r
}

// Run 启动 Role 的主循环（在独立的 goroutine 中运行）
// 这是 Role 的入口点，由 Environment.Run() 调用
func (r *Role) Run(ctx context.Context) error {
    r.ctx, r.cancel = context.WithCancel(ctx)
    defer r.cancel()

    log.Printf("[%s] started, reactMode=%v, actions=%d, watch=%v",
        r.Name, r.reactMode, len(r.actions), r.watch)

    for {
        select {
        case <-r.ctx.Done():
            log.Printf("[%s] context cancelled, exiting", r.Name)
            return r.ctx.Err()

        case msg, ok := <-r.msgBuffer:
            if !ok {
                log.Printf("[%s] msgBuffer closed, exiting", r.Name)
                return nil
            }

            // === Step 1: Observe ===
            if !r.shouldObserve(msg) {
                continue
            }
            r.memory.Add(msg)
            r.markObserved(msg)

            log.Printf("[%s] observed message: cause_by=%s, content_len=%d",
                r.Name, msg.CauseBy, len(msg.Content))

            // === Step 2 & 3: Think + Act (合并为 React) ===
            rsp, err := r.react(r.ctx)
            if err != nil {
                log.Printf("[%s] react error: %v", r.Name, err)
                continue
            }

            // === Step 4: Publish ===
            if rsp != nil {
                r.env.PublishMessage(rsp)
                log.Printf("[%s] published response: cause_by=%s", r.Name, rsp.CauseBy)
            }
        }
    }
}

// react 核心反应逻辑：分发到不同的模式实现
func (r *Role) react(ctx context.Context) (*foundation.Message, error) {
    switch r.reactMode {
    case ReactByOrder:
        return r.reactByOrder(ctx)
    case ReactReAct:
        return r.reactReAct(ctx)
    case ReactPlanAndAct:
        return r.reactPlanAndAct(ctx)
    default:
        return nil, fmt.Errorf("unknown react mode: %v", r.reactMode)
    }
}

// reactByOrder 按 Action 列表顺序依次执行（SOP 模式）
// 这是软件公司场景使用的主要模式
func (r *Role) reactByOrder(ctx context.Context) (*foundation.Message, error) {
    if r.state >= len(r.actions) {
        r.state = -1 // 所有 action 执行完毕，标记为 idle
        return nil, nil
    }

    act := r.actions[r.state]
    history := r.memory.Get(0) // 获取全部历史

    log.Printf("[%s] executing action [%d/%d]: %s",
        r.Name, r.state+1, len(r.actions), act.Name())

    output, err := act.Run(ctx, history)
    if err != nil {
        return nil, fmt.Errorf("action %s failed: %w", act.Name(), err)
    }

    r.state++
    if r.state >= len(r.actions) {
        r.state = -1
    }

    return &foundation.Message{
        Content:    output.Content,
        CauseBy:    act.Name(),
        SentFrom:   r.Name,
        SendTo:     []string{foundation.RouteToAll},
        Role:       foundation.RoleSystem,
        InstructContent: output.InstructContent,
    }, nil
}

// reactReAct LLM 动态选择 Action 的模式
// 让 LLM 根据当前状态，从 actions 列表中选择最合适的下一步
func (r *Role) reactReAct(ctx context.Context) (*foundation.Message, error) {
    // 构建选择 prompt
    actionList := r.buildActionListPrompt()
    history := r.memory.Get(10) // 最近 10 条消息

    // 使用第一个 action 的 LLM 客户端做"思考"（选择下一个 action）
    // 实际上这里需要一个没有具体任务的 "think" 方法
    // 简化实现：遍历 actions，用索引作为选择结果

    selectPrompt := fmt.Sprintf(
        `You are %s. %s
Your goal: %s
Constraints: %s

Recent context:
%s

Available actions:
%s

Which action should you take next? Reply with the action name only.`,
        r.Name, r.Profile,
        r.Goal,
        r.Constraints,
        formatHistory(history),
        actionList,
    )

    // 向 LLM 询问下一步（使用第一个 action 的 LLM）
    // 注意：这是一个简化实现，实际应该有独立的 ThinkAction
    if len(r.actions) == 0 {
        r.state = -1
        return nil, nil
    }

    // 选择 action（简化：按索引）
    selectedIdx := 0 // TODO: 解析 LLM 返回的 action name
    if selectedIdx >= len(r.actions) {
        selectedIdx = 0
    }

    act := r.actions[selectedIdx]
    output, err := act.Run(ctx, r.memory.Get(0))
    if err != nil {
        return nil, err
    }

    return &foundation.Message{
        Content:    output.Content,
        CauseBy:    act.Name(),
        SentFrom:   r.Name,
        SendTo:     []string{foundation.RouteToAll},
        Role:       foundation.RoleSystem,
    }, nil
}

// reactPlanAndAct 先规划后执行
func (r *Role) reactPlanAndAct(ctx context.Context) (*foundation.Message, error) {
    // TODO: Phase 4 实现
    return r.reactByOrder(ctx) // 暂时回退到 byOrder
}

// IsIdle 判断角色是否已空闲（所有 action 执行完毕）
func (r *Role) IsIdle() bool {
    return r.state == -1
}

// shouldObserve 判断是否应该关注此消息
func (r *Role) shouldObserve(msg *foundation.Message) bool {
    if len(r.watch) == 0 {
        return true // 空 watch = 关注所有消息
    }
    return r.watch[msg.CauseBy]
}

// markObserved 记录已观察的消息 ID
func (r *Role) markObserved(msg *foundation.Message) {
    r.observed = append(r.observed, msg.ID)
    // 限制记录数量，防止内存泄露
    if len(r.observed) > 1000 {
        r.observed = r.observed[100:]
    }
}

// MessageBuffer 返回消息邮箱（供 Environment 投递消息）
func (r *Role) MessageBuffer() chan<- *foundation.Message {
    return r.msgBuffer
}

// SetEnvironment 设置所属环境（由 Environment.RegisterRole 调用）
func (r *Role) SetEnvironment(env *Environment) {
    r.env = env
}

// buildActionListPrompt 构建动作列表的描述文本
func (r *Role) buildActionListPrompt() string {
    var sb fmt.Stringer
    for i, a := range r.actions {
        fmt.Fprintf(&sb, "%d. %s\n", i+1, a.Name())
    }
    return sb.String()
}

// formatHistory 将历史消息格式化为文本
func formatHistory(msgs []*foundation.Message) string {
    var sb strings.Builder
    for _, msg := range msgs {
        fmt.Fprintf(&sb, "[%s] %s\n", msg.SentFrom, truncate(msg.Content, 500))
    }
    return sb.String()
}

func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}
```

### 6.4 RoleContext 状态管理

**文件**：`internal/role/context.go`

```go
package role

import (
    "my-agent-framework/internal/foundation"
)

// RoleContext 角色的运行时上下文
// 将可变状态集中管理，便于序列化和恢复
type RoleContext struct {
    Env       *Environment        `json:"-"`
    MsgBuffer chan *foundation.Message `json:"-"`
    Memory    *foundation.Memory  `json:"-"`
    State     int                 `json:"state"`
    Todo      string              `json:"todo"`      // 待执行的 action 名称
    Watch     map[string]bool     `json:"watch"`
    Observed  []string            `json:"observed"`
}

func NewRoleContext(memory *foundation.Memory) *RoleContext {
    return &RoleContext{
        MsgBuffer: make(chan *foundation.Message, 100),
        Memory:    memory,
        State:     0,
        Watch:     make(map[string]bool),
    }
}
```

---

## 7. 第四阶段：环境与编排层（Week 4-5）

### 7.1 Environment——消息路由中心

**文件**：`internal/env/environment.go`

```go
package env

import (
    "context"
    "log"
    "sync"

    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/role"
)

// Environment 消息路由中心 + Agent 生命周期管理器
//
// 设计思想：
// - 类比消息中间件（Kafka/RabbitMQ），Environment 是 Topic 路由器
// - 每个 Role 订阅一组 cause_by（Topic），Environment 负责投递匹配的消息
// - 所有 Role 独立并发运行，Environment 不控制执行顺序
type Environment struct {
    mu          sync.RWMutex
    roles       map[string]*role.Role       // 角色注册表
    memberAddrs map[string]map[string]bool  // roleName → 它订阅的地址
    history     *foundation.Memory          // 全局消息历史（用于调试）
    context     *foundation.Config          // 全局配置
}

func NewEnvironment(cfg *foundation.Config) *Environment {
    return &Environment{
        roles:       make(map[string]*role.Role),
        memberAddrs: make(map[string]map[string]bool),
        history:     foundation.NewMemory(1000),
        context:     cfg,
    }
}

// RegisterRole 注册一个角色到环境中
// 同时建立角色的消息邮箱与 Environment 的连接
func (e *Environment) RegisterRole(r *role.Role, addresses ...string) {
    e.mu.Lock()
    defer e.mu.Unlock()

    e.roles[r.Name] = r
    r.SetEnvironment(e)

    addrSet := make(map[string]bool)
    for _, addr := range addresses {
        addrSet[addr] = true
    }
    e.memberAddrs[r.Name] = addrSet

    log.Printf("[Env] registered role: %s (addresses: %v)", r.Name, addresses)
}

// PublishMessage 发布消息到所有匹配的角色
//
// 路由逻辑：
// 1. 存储消息到全局历史
// 2. 遍历所有角色，检查 send_to 匹配
// 3. 匹配的角色，将消息推入其 msgBuffer（非阻塞，buffer 满则丢弃并告警）
func (e *Environment) PublishMessage(msg *foundation.Message) {
    // 1. 存储到全局历史
    e.history.Add(msg)
    log.Printf("[Env] published: cause_by=%s, sent_from=%s, content_len=%d",
        msg.CauseBy, msg.SentFrom, len(msg.Content))

    // 2. 路由到匹配的角色
    e.mu.RLock()
    defer e.mu.RUnlock()

    for name, r := range e.roles {
        if !msg.ShouldSendTo(name) {
            continue
        }

        // 非阻塞投递：如果 buffer 满了，记录警告但不阻塞
        select {
        case r.MessageBuffer() <- msg:
            log.Printf("[Env] routed to: %s", name)
        default:
            log.Printf("[Env] WARNING: %s msgBuffer full, dropping message %s",
                name, msg.ID)
        }
    }
}

// Run 并发运行所有非空闲角色
// 使用 errgroup 确保所有角色完成或任一失败时退出
func (e *Environment) Run(ctx context.Context) error {
    e.mu.RLock()
    roles := make([]*role.Role, 0, len(e.roles))
    for _, r := range e.roles {
        if !r.IsIdle() {
            roles = append(roles, r)
        }
    }
    e.mu.RUnlock()

    if len(roles) == 0 {
        log.Println("[Env] no active roles to run")
        return nil
    }

    log.Printf("[Env] running %d roles concurrently", len(roles))

    // 使用 errgroup 管理并发
    g, ctx := errgroup.WithContext(ctx)
    for _, r := range roles {
        r := r // 捕获循环变量（Go 1.22+ 不需要此行）
        g.Go(func() error {
            return r.Run(ctx)
        })
    }

    return g.Wait()
}

// History 返回全局消息历史
func (e *Environment) History() *foundation.Memory {
    return e.history
}

// IsAllIdle 检查是否所有角色都已空闲
func (e *Environment) IsAllIdle() bool {
    e.mu.RLock()
    defer e.mu.RUnlock()

    for _, r := range e.roles {
        if !r.IsIdle() {
            return false
        }
    }
    return true
}

// Archive 归档所有生成的文件（git commit）
func (e *Environment) Archive() error {
    // Phase 5 实现：对工作区执行 git init + git add + git commit
    return nil
}
```

### 7.2 Team——多 Agent 编排器

**文件**：`internal/team/team.go`

```go
package team

import (
    "context"
    "log"

    "my-agent-framework/internal/env"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/role"
)

// Team 多智能体团队
// 负责：创建环境、注册角色、管理预算、运行循环
type Team struct {
    env        *env.Environment
    cfg        *foundation.Config
    roles      []*role.Role
    budget     float64 // USD
    nRound     int     // 最大运行轮次
}

func NewTeam(cfg *foundation.Config) *Team {
    return &Team{
        env:    env.NewEnvironment(cfg),
        cfg:    cfg,
        nRound: 5,
    }
}

// Hire 雇佣一个角色加入团队
func (t *Team) Hire(r *role.Role) {
    t.roles = append(t.roles, r)
    t.env.RegisterRole(r, r.Name)
    log.Printf("[Team] hired: %s (%s)", r.Name, r.Profile)
}

// Invest 设置预算上限（美元）
func (t *Team) Invest(budget float64) {
    t.budget = budget
}

// SetMaxRound 设置最大运行轮次
func (t *Team) SetMaxRound(n int) {
    t.nRound = n
}

// Run 启动团队协作
//
// 整体流程：
// 1. 接收用户需求，发布给所有成员
// 2. 循环运行环境，直到达到最大轮次或所有角色空闲
// 3. 归档结果
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
    log.Printf("[Team] starting: idea=%q, budget=$%.2f, rounds=%d, members=%d",
        idea, t.budget, t.nRound, len(t.roles))

    // 1. 发布用户需求
    t.env.PublishMessage(foundation.NewUserMessage(idea))

    // 2. 循环运行
    for round := 0; round < t.nRound; round++ {
        log.Printf("[Team] === Round %d/%d ===", round+1, t.nRound)

        // 检查预算（TODO: Phase 4）
        if t.budget > 0 {
            // 检查成本是否超预算
        }

        // 运行环境
        if err := t.env.Run(ctx); err != nil {
            return nil, fmt.Errorf("round %d failed: %w", round+1, err)
        }

        // 检查是否所有角色都空闲
        if t.env.IsAllIdle() {
            log.Printf("[Team] all roles idle, finishing at round %d", round+1)
            break
        }
    }

    // 3. 归档
    if err := t.env.Archive(); err != nil {
        log.Printf("[Team] archive warning: %v", err)
    }

    log.Printf("[Team] completed: %d messages in history", t.env.History().Count())
    return t.env.History(), nil
}
```

---

## 8. 第五阶段：内置角色与场景（Week 5-6）

### 8.1 内置 Action 示例——WritePRD

**文件**：`internal/action/builtin/write_prd.go`

```go
package builtin

import (
    "context"

    "my-agent-framework/internal/action"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/llm"
)

// WritePRD 撰写产品需求文档
type WritePRD struct {
    *action.BaseAction
}

const prdPrompt = `You are a professional Product Manager with 10 years of experience at top tech companies.

Your task is to write a comprehensive Product Requirement Document (PRD) based on the user's requirements.

The PRD should include:
1. **Product Overview**: What is this product? What problem does it solve?
2. **Target Users**: Who will use this product?
3. **Core Features**: List all features with priority (P0, P1, P2)
4. **User Stories**: Write user stories in the format "As a [user], I want [feature] so that [benefit]"
5. **Non-Functional Requirements**: Performance, security, accessibility requirements
6. **Success Metrics**: How to measure success (KPIs)

Output in clear, professional markdown format.`

func NewWritePRD(client llm.Client) *WritePRD {
    return &WritePRD{
        BaseAction: action.NewBaseAction("WritePRD", prdPrompt, client),
    }
}

// Run 执行 PRD 撰写
func (a *WritePRD) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
    // 从历史中提取用户需求
    userReq := extractUserRequirement(history)

    prompt := fmt.Sprintf(
        "Please write a detailed PRD based on the following requirement:\n\n%s",
        userReq,
    )

    content, err := a.AskLLM(ctx, prompt, history)
    if err != nil {
        return nil, err
    }

    return &action.ActionOutput{
        Content: content,
    }, nil
}

// extractUserRequirement 从历史消息中提取用户需求
// 查找 cause_by="UserRequirement" 的消息
func extractUserRequirement(history []*foundation.Message) string {
    for i := len(history) - 1; i >= 0; i-- {
        if history[i].CauseBy == "UserRequirement" {
            return history[i].Content
        }
    }
    return ""
}
```

### 8.2 内置 Action 示例——WriteCode

**文件**：`internal/action/builtin/write_code.go`

```go
package builtin

import (
    "context"
    "fmt"
    "strings"

    "my-agent-framework/internal/action"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/llm"
)

// WriteCode 根据设计文档和任务生成代码
type WriteCode struct {
    *action.BaseAction
}

const codePrompt = `You are a senior software engineer with 15 years of experience.

Your task is to write production-quality code based on the design document and task description.

Requirements:
- Write clean, readable, well-documented code
- Follow best practices and design patterns
- Include proper error handling
- Add inline comments for complex logic
- Output code in proper markdown code blocks with language specification

Available context:
%s

Please write the code for the following task:
%s`

func NewWriteCode(client llm.Client) *WriteCode {
    return &WriteCode{
        BaseAction: action.NewBaseAction("WriteCode", codePrompt, client),
    }
}

func (a *WriteCode) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
    // 从历史中提取设计文档和任务描述
    designDoc := extractByCause(history, "WriteDesign")
    taskDoc := extractByCause(history, "WriteTasks")

    context := fmt.Sprintf(
        "## Design Document\n%s\n\n## Task Description\n%s",
        designDoc, taskDoc,
    )

    prompt := fmt.Sprintf(codePrompt, context, taskDoc)

    content, err := a.AskLLM(ctx, prompt, history)
    if err != nil {
        return nil, err
    }

    // 提取代码块
    code := extractCodeBlock(content)
    filename := extractFilename(content)

    return &action.ActionOutput{
        Content:         content,
        InstructContent: map[string]string{
            "code":     code,
            "filename": filename,
        },
    }, nil
}

// extractByCause 按 cause_by 提取消息内容
func extractByCause(history []*foundation.Message, causeBy string) string {
    for i := len(history) - 1; i >= 0; i-- {
        if history[i].CauseBy == causeBy {
            return history[i].Content
        }
    }
    return ""
}

// extractCodeBlock 从 markdown 中提取代码块
func extractCodeBlock(content string) string {
    // 匹配 ```lang\n...\n```
    re := regexp.MustCompile("(?s)```\\w*\\n(.*?)```")
    matches := re.FindStringSubmatch(content)
    if len(matches) > 1 {
        return strings.TrimSpace(matches[1])
    }
    return ""
}

// extractFilename 从内容中提取文件名
func extractFilename(content string) string {
    // 匹配 "Filename: xxx" 或 "# xxx.py" 等模式
    re := regexp.MustCompile(`(?i)(?:filename|file)\s*[:：]\s*([^\s\n]+)`)
    matches := re.FindStringSubmatch(content)
    if len(matches) > 1 {
        return matches[1]
    }
    return "output.txt"
}
```

### 8.3 软件公司——完整场景实现

**文件**：`internal/team/software_company.go`

```go
package team

import (
    "context"

    "my-agent-framework/internal/action/builtin"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/llm"
    "my-agent-framework/internal/role"
)

// NewSoftwareCompany 创建一个软件公司团队
// 包含：ProductManager, Architect, Engineer, QAEngineer
func NewSoftwareCompany(cfg *foundation.Config) (*Team, error) {
    // 1. 创建 LLM 解析器（支持三级优先级：Action > Role > 全局）
    resolver := llm.NewLLMResolver(cfg)

    // 2. 辅助函数：为指定 Role 的指定 Action 创建 Client
    //    Resolve 内部走三级优先级：
    //    actions.WriteCode > roles.Alex > llm 全局
    newClient := func(roleName, actionName string) (llm.Client, error) {
        providerName, providerCfg := resolver.Resolve(roleName, actionName)
        raw, err := llm.NewClient(providerName, providerCfg)
        if err != nil {
            return nil, fmt.Errorf("create llm for %s.%s: %w", roleName, actionName, err)
        }
        return llm.NewRetryableClient(raw, llm.DefaultRetryConfig()), nil
    }

    // 3. 创建共享 Memory
    memory := foundation.NewMemory(cfg.Agent.MemoryMaxSize)

    // 4. 为每个 Role + Action 创建独立的 Client
    //    实际用什么 Provider/Model，由 YAML 的 roles 配置决定，代码完全不关心
    alicePRD, err := newClient("Alice", "WritePRD")
    if err != nil { return nil, err }
    bobDesign, err := newClient("Bob", "WriteDesign")
    if err != nil { return nil, err }
    alexCode, err := newClient("Alex", "WriteCode")
    if err != nil { return nil, err }
    alexReview, err := newClient("Alex", "WriteCodeReview")
    if err != nil { return nil, err }
    edwardTest, err := newClient("Edward", "WriteTest")
    if err != nil { return nil, err }

    // 5. 创建角色（每个 Agent 有自己的 LLM Client + 自己的 SOP 职责）
    // ProductManager (Alice): 只看用户需求 → 输出 PRD
    pm := role.NewRole("Alice",
        role.WithActions(builtin.NewWritePRD(alicePRD)),
        role.WithWatch("UserRequirement"),       // ★ 只看用户需求
        role.WithReactMode(role.ReactByOrder),
        role.WithMemory(memory),
    )
    pm.Profile = "Senior Product Manager with 10 years of experience"
    pm.Goal = "Write comprehensive and clear PRD documents"
    // Alice 用什么 LLM？YAML 里 roles.Alice 决定（或继承全局默认）

    // Architect (Bob): 只看 PRD → 输出设计文档
    architect := role.NewRole("Bob",
        role.WithActions(builtin.NewWriteDesign(bobDesign)),
        role.WithWatch("WritePRD"),              // ★ 只看 PRD
        role.WithReactMode(role.ReactByOrder),
        role.WithMemory(memory),
    )
    architect.Profile = "Senior System Architect with 15 years of experience"
    architect.Goal = "Design scalable and maintainable system architectures"

    // Engineer (Alex): 只看设计文档 → 输出代码 + 代码审查
    // ★ Alex 的 WriteCode 和 WriteCodeReview 可以分别用不同模型
    engineer := role.NewRole("Alex",
        role.WithActions(
            builtin.NewWriteCode(alexCode),
            builtin.NewWriteCodeReview(alexReview),
        ),
        role.WithWatch("WriteDesign", "WriteTasks"),
        role.WithReactMode(role.ReactByOrder),
        role.WithMemory(memory),
    )
    engineer.Profile = "Senior Software Engineer with 10 years of experience"
    engineer.Goal = "Write elegant, readable, extensible, efficient code"

    // QAEngineer (Edward): 只看代码 → 输出测试
    qa := role.NewRole("Edward",
        role.WithActions(
            builtin.NewWriteTest(edwardTest),
            builtin.NewRunCode(edwardTest),
        ),
        role.WithWatch("WriteCode"),             // ★ 只看代码
        role.WithReactMode(role.ReactByOrder),
        role.WithMemory(memory),
    )
    qa.Profile = "Senior QA Engineer with 12 years of testing experience"
    qa.Goal = "Ensure code quality through thorough testing"

    // 6. 组建团队
    team := NewTeam(cfg)
    team.Hire(pm)
    team.Hire(architect)
    team.Hire(engineer)
    team.Hire(qa)
    team.SetMaxRound(5)

    return team, nil
}
```

**面试重点**：软件公司场景中 `_watch` 的 SOP 流水线设计是核心亮点：

```
UserRequirement ──→ Alice(PM) ──WritePRD──→ Bob(Architect)
                                               │
                                          WriteDesign
                                               │
                    ┌──────────────────────────┘
                    ▼
              Alex(Engineer) ──WriteCode──→ Edward(QA)
                                              │
                                         WriteTest
                                          RunCode
```

---

## 9. 第六阶段：工程化增强（Week 6-8）

### 9.1 CLI 入口

**文件**：`cmd/myagent/main.go`

```go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/spf13/cobra"

    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/team"
)

func main() {
    rootCmd := &cobra.Command{
        Use:   "myagent",
        Short: "A multi-agent collaboration framework in Go",
    }

    // 子命令：生成项目
    generateCmd := &cobra.Command{
        Use:   "generate [idea]",
        Short: "Generate a software project from an idea",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            return runGenerate(args[0])
        },
    }
    generateCmd.Flags().StringP("config", "c", "config.yaml", "config file path")
    generateCmd.Flags().Float64P("budget", "b", 5.0, "max budget in USD")
    generateCmd.Flags().IntP("rounds", "r", 5, "max rounds")

    // 子命令：交互模式
    chatCmd := &cobra.Command{
        Use:   "chat",
        Short: "Start interactive chat with an agent",
        RunE: func(cmd *cobra.Command, args []string) error {
            return runChat()
        },
    }

    rootCmd.AddCommand(generateCmd, chatCmd)
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func runGenerate(idea string) error {
    // 1. 加载配置
    cfg, err := foundation.Load("config.yaml")
    if err != nil {
        return fmt.Errorf("load config: %w", err)
    }

    // 2. 创建团队
    t, err := team.NewSoftwareCompany(cfg)
    if err != nil {
        return fmt.Errorf("create team: %w", err)
    }
    t.Invest(5.0)

    // 3. 优雅关闭
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go func() {
        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
        <-sigCh
        fmt.Println("\nShutting down gracefully...")
        cancel()
    }()

    // 4. 运行
    history, err := t.Run(ctx, idea)
    if err != nil {
        return fmt.Errorf("run failed: %w", err)
    }

    // 5. 输出结果
    fmt.Printf("\n=== Completed ===\n")
    fmt.Printf("Total messages: %d\n", history.Count())
    if last := history.Last(); last != nil {
        fmt.Printf("Last output by: %s\n", last.SentFrom)
    }

    return nil
}

func runChat() error {
    fmt.Println("Interactive chat mode - coming soon!")
    return nil
}
```

### 9.2 配置文件

**文件**：`configs/config.yaml`

```yaml
# ============================================
# 配置文件 —— 所有 LLM 在这里统一管理
# 切换模型只需要改 YAML，不需要改一行 Go 代码
# ============================================

# ── 第一层：全局默认（所有 Agent 的兜底配置）──
llm:
  provider: deepseek              # openai / deepseek / anthropic / ollama / custom
  model: deepseek-chat
  api_key: ${DEEPSEEK_API_KEY}    # 从环境变量读取，不写死
  base_url: ""                    # 空 = 用 Provider 内置默认值
  temperature: 0.3
  max_tokens: 4096
  timeout_seconds: 120
  max_retries: 3

# ── 第二层：按 Agent（Role）覆盖 ★ 核心设计 ★ ──
# 每个 Agent 可以有自己专属的 LLM，发挥不同模型的优势
roles:
  Alice:                          # ProductManager —— 需求文档写得好
    provider: openai
    model: gpt-4o                 # GPT-4o 文档能力强
    api_key: ${OPENAI_API_KEY}
    temperature: 0.5              # 创造性任务可以高一点

  Bob:                            # Architect —— 架构设计
    provider: anthropic
    model: claude-sonnet-4-6      # Claude 逻辑推理强
    api_key: ${ANTHROPIC_API_KEY}
    temperature: 0.2

  Alex:                           # ★ Engineer —— 代码生成，用最强模型
    provider: anthropic
    model: claude-opus-4-8        # Opus 代码能力最强
    api_key: ${ANTHROPIC_API_KEY}
    temperature: 0.1              # 代码生成用低温度，确保确定性
    max_tokens: 8192              # 代码通常更长

  Edward:                         # QA Engineer —— 测试用便宜的就行
    provider: deepseek
    model: deepseek-chat          # DeepSeek 便宜，测试量大
    temperature: 0.1

# ── 第三层：按 Action 覆盖（最高优先级，精确控制）──
actions:
  # 如果 Alex 的 WriteCodeReview 想用和 WriteCode 不同的模型：
  # WriteCodeReview:
  #   provider: anthropic
  #   model: claude-haiku-4-5    # 代码审查用 Haiku 就够了，更快更便宜

  # 如果某个 Action 不想用 Agent 默认的，可以强制覆盖：
  # RunCode:
  #   provider: ollama
  #   model: codellama:13b
  #   base_url: http://localhost:11434/v1

# ── 其他场景的配置参考 ──
  # ★ 全 DeepSeek（最省钱方案）：
  #   roles 全部删掉或注释掉，只保留 llm.provider: deepseek

  # ★ 全 Claude（最强代码质量）：
  #   llm.provider: anthropic + llm.model: claude-sonnet-4-6
  #   Alex 单独配 claude-opus-4-8

  # ★ 混合方案（推荐）：
  #   如上配置：Alice 用 GPT-4o，Bob/Alex 用 Claude，Edward 用 DeepSeek

  # ★ 公司自建代理：
  #   roles:
  #     Alex:
  #       provider: custom
  #       model: internal-model
  #       api_key: ${INTERNAL_KEY}
  #       base_url: https://llm-proxy.mycompany.com/v1
  #       extra:
  #         auth_header: "X-API-Key"
  #         auth_prefix: "Bearer"

workspace:
  path: ./workspace

agent:
  max_react_loop: 10
  max_budget_usd: 5.0
  memory_max_size: 200
```

### 9.3 Makefile

```makefile
.PHONY: build run test lint clean

# 构建
build:
	go build -o bin/myagent ./cmd/myagent

# 运行生成
generate: build
	./bin/myagent generate "写一个网页版2048游戏"

# 运行所有测试
test:
	go test -v -race -cover ./...

# 运行测试 + 生成覆盖率报告
test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# 代码检查
lint:
	golangci-lint run ./...

# 格式化
fmt:
	go fmt ./...
	goimports -w .

# 依赖管理
deps:
	go mod tidy
	go mod verify

# 清理
clean:
	rm -rf bin/ workspace/ coverage.out coverage.html

# 运行基准测试
bench:
	go test -bench=. -benchmem ./...

# 完整 CI 流程
ci: deps fmt lint test build
	@echo "CI passed!"
```

---

## 10. 关键设计决策与 Go 惯用法

### 10.0 为什么用适配器模式做多 Provider，而不是写一个统一的 HTTP 调用？

```
这是整个项目最重要的架构决策。

方案 A（错误）：写一个 God HTTP Client，用 if/switch 处理不同 API
  伪代码：
    if provider == "openai" || provider == "deepseek" {
        body = {model, messages, temperature}  // OpenAI 格式
        url = baseURL + "/chat/completions"
        auth = "Bearer " + key
    } else if provider == "anthropic" {
        body = {model, messages, system, max_tokens}  // Anthropic 格式
        url = baseURL + "/v1/messages"
        auth = key  // x-api-key 头
    }
  问题：加一个 Provider 要改核心逻辑，违反开闭原则。

方案 B（正确）：适配器模式 —— 每个 Provider 一个文件
  Client 接口 ← 上层只认这个
      ↑
  ┌───┴───────────────────────────┐
  │ openaiClient  翻译 OpenAI API │
  │ anthropicClient 翻译 Anthropic│
  │ customClient   翻译任意兼容API│
  └───────────────────────────────┘

  新增 Provider：写一个新文件 + 实现 Client 接口 + init() 注册。
  不影响任何已有代码。这是标准的开闭原则（对扩展开放，对修改关闭）。
```

**面试话术**：
> "我在 LLM 层用了适配器模式。框架定义统一的 Client 接口和 ChatMessage/ChatResponse 内部类型，每个 Provider 各自把自己的 API 格式翻译成内部格式。
> 这样 Anthropic 的 Messages API 和 OpenAI 的 Chat Completions API 虽然请求/响应格式完全不同，但上层 Action 代码完全不用关心——它只调用 `client.Chat(messages)`，得到一个统一的 `ChatResponse`。
> 还设计了一个 `custom` Provider，用户只需要在 YAML 里填 URL 和认证头格式就能接入任意 OpenAI 兼容的 API。新增 Provider 只要写一个新文件加 `init()` 注册，零侵入。"

### 10.1 为什么用 channel 做消息邮箱而不是 slice + mutex？

```
方案 A：chan Message（推荐）
  r.msgBuffer <- msg   ← 零分配，Go runtime 优化
  优点：阻塞语义天然适合"等待消息"的场景
  缺点：buffer 大小需要调优

方案 B：[]Message + sync.Mutex + sync.Cond
  优点：无容量限制
  缺点：代码更复杂，需要手动条件变量

选 A 的原因：
  "Agent 的生产-消费速率基本匹配，100 条 buffer 足够。
   且 select { case msg := <-buffer: ... } 是 Go 最惯用的模式。"
```

### 10.2 为什么用 errgroup 而不是裸 sync.WaitGroup？

```go
// 方案 A：errgroup（推荐）
g, ctx := errgroup.WithContext(ctx)
for _, r := range roles {
    r := r
    g.Go(func() error { return r.Run(ctx) })
}
return g.Wait()
// 优点：任一角色出错，自动取消其他角色

// 方案 B：sync.WaitGroup
var wg sync.WaitGroup
for _, r := range roles {
    wg.Add(1)
    go func(r *Role) {
        defer wg.Done()
        r.Run(ctx)  // 错误丢失了！
    }(r)
}
wg.Wait()
// 缺点：错误无法向上传播
```

### 10.3 为什么用函数式选项模式而不是构造函数参数？

```go
// 方案 A：函数式选项（推荐）
pm := role.NewRole("Alice",
    role.WithActions(writePRD),
    role.WithWatch("UserRequirement"),
    role.WithReactMode(role.ReactByOrder),
)

// 方案 B：构造函数
pm := role.NewRole("Alice", writePRD, "UserRequirement", role.ReactByOrder)
// 参数多了以后可读性差，且扩展需要改接口
```

### 10.4 并发安全原则

| 共享数据 | 保护方式 | 理由 |
|---------|---------|------|
| `Memory.storage` | `sync.RWMutex` | 读多写少，读锁不互斥 |
| `Environment.roles` | `sync.RWMutex` | 注册后不变，运行时只读 |
| `Role.state` | 单 goroutine 访问 | 每个 Role 只有一个 goroutine，天然线程安全 |
| `msgBuffer` | channel | Go runtime 保证并发安全 |

> **面试话术**：
> "我在设计时遵循了 Go 的并发哲学：Don't communicate by sharing memory; share memory by communicating.
> Agent 之间的消息传递完全通过 channel，每个 Agent 的内部状态只在自己的 goroutine 中修改——避免了锁竞争。"

### 10.5 为什么每个 Agent 可以有自己的 LLM？不是浪费吗？

```
这个问题的本质是：不同 Agent 的任务对 LLM 能力的要求不同。

举个软件公司场景的例子：

  Alice (PM)     → 写需求文档   → 需要：语言表达好，不需要推理
  Bob (Architect) → 写架构设计   → 需要：逻辑推理强，结构化思维
  Alex (Engineer) → 写代码       → 需要：代码能力最强，贵也要用
  Edward (QA)     → 写测试/运行  → 需要：量大便宜，不要求最强

如果全部用同一个模型：
  ┌──────────────────────────────────────────┐
  │ 全用 DeepSeek → Alex 的代码质量上不去     │
  │ 全用 Claude   → Edward 的测试用例太贵     │
  │ 全用 GPT-4o   → 总体成本不可控           │
  └──────────────────────────────────────────┘

如果每个 Agent 用自己的模型：
  ┌──────────────────────────────────────────┐
  │ Alice  → GPT-4o       ($5/1M tokens)     │
  │ Bob    → Claude Sonnet ($3/1M tokens)    │
  │ Alex   → Claude Opus   ($15/1M tokens)   │  ← 只在关键任务上花大钱
  │ Edward → DeepSeek      ($0.5/1M tokens)  │  ← 量大便宜的活
  └──────────────────────────────────────────┘
  总体成本 ≈ 把 Claude Opus 全量的 40%，但代码质量没降

类比后端微服务：
  你不会用 128 核的机器跑一个简单的健康检查接口
  也不会用 2 核的机器跑一个视频转码服务
  → Agent 的 LLM 配置就是"算力配置"，按需分配
```

**面试话术**：
> "LLM 配置我设计成了三级继承：Action > Role > 全局。这解决了'不同任务需要不同模型'的问题——
> 比如写代码用 Claude Opus 保证质量，写测试用 DeepSeek 控制成本。
> 类比微服务里不同服务配不同规格的容器，你不会用 128 核跑健康检查。
> 关键是：切换只改 YAML，Go 代码零改动——因为 Resolver 把配置解析和框架逻辑完全解耦了。"

---

## 11. 测试策略

### 11.1 测试分层

```
┌────────────────────────────────────┐
│         集成测试 (examples/)        │  ← 完整场景：2+ Agent 协作
├────────────────────────────────────┤
│         单元测试 (*_test.go)         │  ← 每个模块的独立测试
├────────────────────────────────────┤
│         Mock LLM (test helper)      │  ← 不调用真实 API 的测试基础设施
└────────────────────────────────────┘
```

### 11.2 Mock LLM 客户端

**文件**：`internal/llm/mock.go`（放在测试中）

```go
package llm

import "context"

// MockClient 用于单元测试的 LLM 客户端
type MockClient struct {
    responses map[string]string // prompt 关键词 → 预定义响应
    tokens    int
}

func NewMockClient() *MockClient {
    return &MockClient{
        responses: make(map[string]string),
    }
}

// When 注册 mock 响应
func (m *MockClient) When(promptContains, response string) *MockClient {
    m.responses[promptContains] = response
    return m
}

func (m *MockClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
    // 查找匹配的 mock 响应
    lastContent := messages[len(messages)-1].Content
    for keyword, resp := range m.responses {
        if strings.Contains(lastContent, keyword) {
            return &ChatResponse{
                Content:      resp,
                FinishReason: "stop",
                Usage:        &TokenUsage{TotalTokens: m.tokens},
            }, nil
        }
    }
    return &ChatResponse{
        Content:      "Mock response: " + truncate(lastContent, 50),
        FinishReason: "stop",
    }, nil
}

// ... 其他方法
```

### 11.3 关键测试用例

```go
// internal/role/role_test.go

func TestRoleByOrder_ExecutesActionsInSequence(t *testing.T) {
    // 创建一个有 2 个 action 的 Role
    // 记录执行顺序
    // 断言：action[0] 先于 action[1] 执行
}

func TestRoleByOrder_IdleAfterAllActions(t *testing.T) {
    // 执行 n 次 react
    // 断言：state == -1, IsIdle() == true
}

func TestRole_ObservesOnlyWatchedMessages(t *testing.T) {
    // 发送 watched 和 non-watched 消息
    // 断言：只处理 watched 消息
}

// internal/env/environment_test.go

func TestEnvironment_RoutesMessageToMatchingRoles(t *testing.T) {
    // 注册 3 个 Role，其中 2 个订阅 "WritePRD"
    // 发布 cause_by="WritePRD" 的消息
    // 断言：只有 2 个 Role 收到消息
}

func TestEnvironment_RunsRolesConcurrently(t *testing.T) {
    // 注册 2 个 Role，各自有不同的执行时间
    // 运行环境，测量总耗时
    // 断言：总耗时 < max(role1, role2)，不是 sum
}

// internal/llm/compressor_test.go

func TestCompressor_KeepsSystemMessages(t *testing.T) {
    // 输入：system + 100 user messages
    // 压缩预算：只够 system + 10 user
    // 断言：system 消息始终保留
}

func TestCompressor_CutByTokenRear_PreservesRecent(t *testing.T) {
    // 输入：10 条消息
    // 预算：最后 3 条
    // 断言：返回的是第 8-10 条
}
```

---

## 12. 秋招面试话术指南

### 12.1 项目介绍（30 秒电梯演讲）

> "我做了一个 Go 语言的多智能体协作框架，核心思想是让多个 AI Agent 扮演不同角色通过协作来完成复杂任务。
> 技术上实现了三个核心抽象：Role（智能体）、Action（原子操作）、Environment（消息路由），
> 多个 Agent 通过 goroutine 并发运行，通过 channel 进行消息通信。
> 目前完成了软件公司场景——输入一个需求，4 个 AI 角色（产品、架构、开发、测试）协作生成完整代码。"

### 12.2 常见追问及回答

**Q1：为什么不用 Python，要用 Go？**

> "我评估了两个方案。Agent 框架本质是一个消息驱动的并发系统——每个 Agent 独立执行，Agent 之间通过消息通信。
> Go 的 goroutine + channel 和这个场景天然匹配：goroutine 作为执行体，channel 作为邮箱，代码量和复杂度都比线程池方案低很多。
> 而且 Go 的并发安全哲学——'Don't communicate by sharing memory; share memory by communicating'——
> 恰好就是 Agent 通信的最佳实践。所以 Go 不是我的选择偏好，而是技术上的合理决策。"

**Q2：你的 Role 的 observe-think-act 循环怎么设计的？**

> "核心循环是：从 channel 接收消息 → 按 watch 集合过滤 → 根据 reactMode 选择策略 → 执行 Action → 发布结果。
> 我实现了三种模式：ByOrder（SOP 流水线）、ReAct（LLM 动态决策）、PlanAndAct（先规划后执行）。
> ByOrder 是软件公司场景的核心——通过设置每个角色的 watch 集合，形成了一条需求→PRD→设计→代码→测试的 SOP 流水线。"

**Q3：消息路由怎么实现的？**

> "用的是发布-订阅模式。每条消息有两个关键字段：cause_by（谁产生的）和 send_to（发给谁）。
> 角色通过 watch 声明自己关注哪些 cause_by，Environment 遍历所有角色进行匹配，投递到角色的 channel。
> 这是一个 topic-based 的消息路由，类比 Kafka 的 consumer group。"

**Q4：LLM Provider 怎么设计的？如何支持多个不同的模型 API？**

> "我用的是适配器模式。框架定义了统一的 Client 接口和 ChatMessage/ChatResponse 内部类型——
> 注意这不是 OpenAI 的格式，是框架自己的抽象。每个 Provider 各有一个适配器文件，负责把自己的 API 格式翻译成内部格式。
> 新增 Provider 只要写一个新文件 + `init()` 注册，零侵入。"

**Q5：为什么每个 Agent 能配置自己的 LLM？有什么好处？**

> "我设计了一个三级继承的配置链：Action > Role > 全局。每个 Agent 只填想覆盖的字段，其余自动继承。
> 这解决了一个实际问题：不同 Agent 的任务对 LLM 能力要求不同——Alex 写代码用 Claude Opus（代码能力最强，贵但值得），
> Edward 写测试用 DeepSeek（便宜，测试量大），Alice 写 PRD 用 GPT-4o（文档表达能力好）。
> 类比微服务：你不会用 128 核跑健康检查，也不会用 2 核跑视频转码。Agent 的 LLM 配置本质就是'算力配置'，按需分配。
> 而且切换只改 YAML，Go 代码零改动。"

**Q6：如果 Agent 数量很多（比如 100 个），性能怎么样？**

> "我的设计是每个 Agent 一个 goroutine，goroutine 的栈初始只有几 KB，100 个 Agent 的开销是 MB 级别。
> 真正的瓶颈不在 Agent 数量，而在 LLM API 的并发限制和 token 消耗。如果要优化，
> 可以用 semaphore 限制并发 LLM 调用数，Agent 的 goroutine 在调用 LLM 前获取信号量。"

**Q7：你怎么做测试的？**

> "我设计了一个 Mock LLM Client，可以预设 prompt 关键词对应的响应。单元测试不需要调用真实 API。
> Role 的测试验证了执行顺序、空闲状态、消息过滤等核心逻辑。
> 集成测试用两个 Agent 协作完成一个简单任务来验证消息路由的正确性。"

**Q8：项目中最大的挑战是什么？**

> "两个挑战。一是如何避免 Agent 陷入死循环——我给每个 Role 设置了 max_react_loop 上限，
> 达到上限自动标记为 idle。二是消息压缩——LLM 有 token 上限，我需要在不丢失关键上下文的前提下截断历史。
> 我实现了 4 种策略：从前往后/从后往前 × 按 token/按条数，系统消息始终保留。"

**Q9：如果重新做，你会怎么改进？**

> "三方面。一是引入流式输出的端到端传递——LLM 返回 token 流可以边生成边路由给下游 Agent，
> 减少等待时间。二是增加长期记忆模块——用向量数据库存储历史经验，做到真正的知识积累。
> 三是支持分布式部署——每个 Agent 跑在不同节点上，Environment 变成真正的消息中间件。"

### 12.3 简历项目描述模板

```markdown
## Go 多智能体协作框架 | 个人项目 | 2026.06 - 2026.08

**项目描述**：
用 Go 从零实现一个 MetaGPT 风格的多智能体协作框架，让多个 AI Agent 扮演不同
角色通过消息通信协作完成复杂任务。

**技术栈**：Go 1.22+, goroutine/channel, OpenAI API, YAML

**核心成果**：
- 实现了 3 个核心抽象：Role（智能体）、Action（原子操作）、Environment（消息路由）
- 每个 Agent 独立 goroutine 运行，通过 channel 通信，支持 100+ Agent 并发
- 设计了 3 种协作模式：SOP 流水线、ReAct 动态决策、Plan-and-Act 先规划后执行
- 实现了 Token 压缩（4 种策略）、指数退避重试、结构化输出解析
- 完成了软件公司场景：4 个 AI 角色协作，从需求生成完整代码仓库

**技术亮点**：
- 全并发安全设计：RWMutex + channel，零锁竞争
- 可插拔 LLM Provider：接口抽象 + 工厂注册，支持 OpenAI/DeepSeek/Ollama
- 面向接口编程 + 函数式选项模式，代码高内聚低耦合

**项目地址**：github.com/xxx/my-agent-framework
```

---

## 附录 A：依赖清单

```
# go.mod 核心依赖（最小化原则）

module github.com/xxx/my-agent-framework

go 1.22

require (
    github.com/spf13/cobra v1.8.0        // CLI
    gopkg.in/yaml.v3 v3.0.1              // YAML 配置
    golang.org/x/sync v0.7.0             // errgroup
    github.com/google/uuid v1.6.0        // UUID
    github.com/pkoukk/tiktoken-go v0.1.7 // Token 计数（可选）
)
```

## 附录 B：最小可运行 Demo

```go
// examples/simple/main.go
// 最简示例：单个 Agent 根据用户需求写 PRD
package main

import (
    "context"
    "fmt"
    "os"

    "my-agent-framework/internal/action/builtin"
    "my-agent-framework/internal/foundation"
    "my-agent-framework/internal/llm"
    "my-agent-framework/internal/role"
)

func main() {
    // 1. 创建 LLM 客户端（使用 DeepSeek，便宜）
    client, _ := llm.NewClient("deepseek", llm.ProviderConfig{
        Model:       "deepseek-chat",
        APIKey:      os.Getenv("DEEPSEEK_API_KEY"),
        Temperature: 0.3,
    })

    // 2. 创建 Action
    writePRD := builtin.NewWritePRD(client)

    // 3. 创建 Role
    pm := role.NewRole("Alice",
        role.WithActions(writePRD),
        role.WithReactMode(role.ReactByOrder),
    )
    pm.Profile = "Senior Product Manager"

    // 4. 创建 Environment 并注册 Role
    env := env.NewEnvironment(nil)
    env.RegisterRole(pm, "Alice")

    // 5. 发送需求
    env.PublishMessage(foundation.NewUserMessage("设计一个网页版番茄钟应用"))

    // 6. 运行
    ctx := context.Background()
    _ = env.Run(ctx)

    // 7. 获取结果
    last := env.History().Last()
    fmt.Println("=== PRD Output ===")
    fmt.Println(last.Content)
}
```

---

## 附录 C：时间安排建议（8 周暑期冲刺）

```
第 1 周（基础设施）：Config + Message + Memory + 单元测试
第 2 周（LLM 层）：Client 接口 + OpenAI 实现 + 重试 + 压缩
第 3 周（核心循环）：Action + Role（ByOrder 模式）+ 能跑通单 Agent
第 4 周（环境）：Environment 消息路由 + 2 Agent 协作
第 5 周（场景）：WritePRD + WriteDesign + WriteCode + SoftwareCompany
第 6 周（打磨）：README + 架构图 + 示例代码 + 异常处理
第 7 周（增强）：流式输出 + PlanAndAct + CLI 完善
第 8 周（面试准备）：面经话术演练 + 代码走读 + 扩展规划
```

> **建议节奏**：每天 3-4 小时，周末 6-8 小时。重点是第 3-5 周的核心代码，这是面试时最会被问到的部分。

---

开始写代码吧！记住：**先跑通，再优化。第一版的目标是能跑通"单 Agent 根据需求写 PRD"，不要一开始就追求完美架构。**
