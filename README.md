# Cohort

Go 语言多智能体协作框架 —— 从零构建的 MetaGPT 风格 AI Agent 框架。

**零外部依赖，纯 Go 标准库。**

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Dependencies](https://img.shields.io/badge/dependencies-0-green.svg)](go.mod)

---

## 为什么用 Go 做 Agent 框架

Agent 框架的核心是一个**消息驱动的并发系统**，Go 的并发模型恰好为此设计：

| Agent 框架需求 | Go 原语 |
|---------------|---------|
| Agent 并发运行 | `goroutine`（轻量，几 KB 栈） |
| Agent 消息邮箱 | `chan Message`（类型安全） |
| 多 Agent 同步 | `sync.WaitGroup` |
| 超时控制 | `context.Context`（一等公民） |

**核心优势**：Go 写 Agent 框架，并发部分的代码量大约是 Java 的 1/3。

---

## 快速开始

### 前置要求

- Go 1.21+
- DeepSeek API Key（[免费申请](https://platform.deepseek.com/)）

### 设置环境变量

```powershell
$env:DEEPSEEK_API_KEY = "sk-xxx"
```

### 跑起来

```powershell
# 体验双 Agent 协作：PM 写 PRD → Reviewer 评审
go run ./cmd/demo_duo/

# 测试 LLM 层
go run ./cmd/llmdemo/

# 演示基础设施层
go run ./cmd/demo/
```

### 30 秒看到结果

```
用户需求: "写一个网页版 2048 游戏的需求文档"
  │
  ├→ Alice (PM) 观察需求 → 执行 WritePRD → 调用 DeepSeek
  │     └→ 输出完整 PRD（3,458 字）
  │
  └→ Bob (Reviewer) 观察 PRD → 执行 WriteCodeReview → 调用 DeepSeek
        └→ 输出评审意见（3,070 字，评为 PASS_WITH_CHANGES）
```

---

## 架构

```
┌──────────────────────────────────────────────────────┐
│                    cmd/                               │
│              CLI 入口 / Demo                          │
├──────────────────────────────────────────────────────┤
│                    team                               │
│     Team.Hire(roles) → Team.Run(idea) → history       │
├──────────────────┬───────────────────────────────────┤
│       env        │              role                  │
│  消息路由中心     │  observe → think → act 循环        │
│  Publish/Subscribe│  ByOrder / ReAct / PlanAndAct     │
├──────────────────┴───────────────────────────────────┤
│                    action                             │
│    原子操作：WritePRD / WriteCode / WriteTest / ...   │
│    BaseAction.AskLLM(prompt, history)                │
├──────────────────────────────────────────────────────┤
│                     llm                               │
│  Client 接口 → OpenAI / DeepSeek / Anthropic / Custom │
│  双向翻译：框架类型 ↔ Provider API 格式                │
├──────────────────────────────────────────────────────┤
│                   foundation                          │
│  Config · Message · Memory · Logger                  │
└──────────────────────────────────────────────────────┘
```

**完整调用链**：

```
Team.Run(idea)
  └→ PublishMessage(用户需求)
  └→ env.Run()
       └→ go Role.Run()                    ← goroutine
            └→ for { msg := <-msgBuffer    ← channel
                   observe(msg)
                   rsp := react(ctx)       ← think + act
                     └→ Action.Run(history)
                          └→ AskLLM(prompt, history)
                               └→ Client.Chat(messages)  ← LLM
                   PublishMessage(rsp)
                 }
```

---

## 项目结构

```
internal/
├── foundation/             ← 基础设施层
│   ├── config.go           ← 全局配置 + 三级覆盖
│   ├── message.go          ← 通信单元 + 路由判断
│   ├── memory.go           ← 消息存储 + FIFO 淘汰
│   └── logger.go           ← 结构化日志
│
├── llm/                    ← LLM 调用层
│   ├── client.go           ← Client 接口 + 内部类型
│   ├── registry.go         ← 注册工厂 + 三级配置解析
│   ├── provider_openai.go  ← OpenAI
│   ├── provider_deepseek.go← DeepSeek（组合复用）
│   ├── provider_anthropic.go← Anthropic（适配器模式）
│   ├── provider_custom.go  ← Custom（万能兜底）
│   ├── provider_ollama.go  ← Ollama
│   └── mock.go             ← 测试桩
│
├── action/                 ← 动作层
│   ├── action.go           ← Action 接口 + BaseAction
│   ├── node.go             ← 结构化输出解析
│   └── builtin/            ← 内置 Action
│
├── role/                   ← 角色层（框架心脏）
│   ├── role.go             ← observe→think→act 循环
│   └── context.go          ← 状态管理
│
├── env/                    ← 环境层
│   └── environment.go      ← 消息路由中心
│
└── team/                   ← 编排层
    └── team.go             ← 多 Agent 编排器

cmd/
├── demo/main.go            ← Foundation 层演示
├── llmdemo/main.go         ← LLM 层演示
└── demo_duo/main.go        ← 双 Agent 协作演示
```

---

## 核心概念

### Role（角色）

每个 Role 是一个独立的 AI 智能体，运行在 goroutine 中，通过 channel 收发消息：

```go
alice := role.NewRole("Alice",
    role.WithProfile("Product Manager", "Write clear PRDs", "Be concise"),
    role.WithActions(writePRD),
    role.WithWatch("UserRequirement"),  // 只关注用户需求
    role.WithMemory(mem),
)
```

**三种 React 模式**：

| 模式 | 行为 | 场景 |
|------|------|------|
| `ReactByOrder` | 按 Actions 列表顺序执行 | SOP 流程（PM→Engineer→QA） |
| `ReactReAct` | LLM 动态选择下一步 | 开放域任务 |
| `ReactPlanAndAct` | 先规划再执行 | 复杂多步骤任务 |

### LLM 调用

切换 LLM 提供商**只改 YAML，Go 代码零改动**：

```go
// 5 行代码，支持 5 种 Provider
client, _ := llm.NewClient("deepseek", llm.ProviderConfig{
    Model: "deepseek-v4-pro", APIKey: os.Getenv("DEEPSEEK_API_KEY"),
})
resp, _ := client.Chat(ctx, messages)            // 同步
ch, _ := client.ChatStream(ctx, messages)        // 流式
```

**已支持 Provider**：OpenAI / DeepSeek / Anthropic Claude / Ollama / 自建代理

### 消息路由

消息通过 `CauseBy`（谁产生的）和 `SendTo`（发给谁）两个字段驱动协作：

```
Alice 发布消息 { CauseBy: "WritePRD", SendTo: ["<all>"] }
  → Bob (watch: "WritePRD") 接收并处理
  → Carol (watch: "WriteCode") 忽略
```

---

## 设计亮点

### 三级配置继承

```
actions.WriteCodeReview → Claude Haiku   ← 最高优先级
    ↑ 覆盖
roles.Alex              → Claude Opus    ← 中间优先级
    ↑ 覆盖
llm (全局默认)           → DeepSeek       ← 兜底
```

每个 Agent 可以有自己专属的 LLM。Alex 用 Claude 写代码，Edward 用 DeepSeek 写测试。

### 适配器模式

Anthropic API 格式与 OpenAI 完全不同，但上层代码完全无感：

```
框架 ChatMessage              Anthropic Messages API
system 在 messages 里     →  system 提升为顶层字段
content 是字符串           →  content 是 [{type:"text", text:"..."}]
Authorization: Bearer     →  x-api-key: xxx
```

### 组合复用

DeepSeek 和 Ollama 无需重写代码，嵌入 `*openaiClient` 即可：

```go
type deepseekClient struct {
    *openaiClient  // Chat / ChatStream / CountTokens 全部继承
}
// 只覆盖 Name() 和 BaseURL → 48 行完成一个完整 Provider
```

### 零外部依赖

`go.mod` 只有 `module cohort` + `go 1.21.10`。面试时说"用标准库从零实现"底气足。

---

## 路线图

| 阶段 | 内容 | 状态 |
|------|------|------|
| Phase 1 | Config + Message + Memory + Logger | ✅ |
| Phase 2 | LLM Client + 5 Providers + 三级配置 | ✅ |
| Phase 3 | Action + Role（observe→think→act） | ✅ |
| Phase 4 | Environment + Team 编排 | ✅ |
| Phase 5 | 内置 Action + 双 Agent 协作 | ✅ |
| Phase 6 | 软件公司完整场景 + YAML 配置 + CI/CD | 🚧 |
| Phase 7 | Web UI + 面试准备 | 📋 |

---

## 更多文档

- [开发过程总结](docs/开发过程总结.md) —— 每个 Batch 的详细设计和代码示例
- [Go 版本多智能体框架开发路线](Go版本多智能体框架开发路线.md) —— 完整开发路线图

---

## License

MIT
