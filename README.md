# Cohert

Cohert 是一个用 Go 编写的命令行智能体运行时。

目标是先提供稳定、清晰、可扩展的本地命令行能力，再逐步扩展 UI、浏览器控制、记忆系统和插件能力。

## 当前能力

- `cohert`：进入交互式命令行 Agent。
- `cohert ask "任务"`：执行单次任务。
- OpenAI-compatible Chat Completions。
- 默认 DeepSeek：`deepseek-v4-pro`。
- 流式输出。
- 原生 tool calling。
- 基础工具：
  - `file_read`
  - `file_write`
  - `file_patch`
  - `code_run`
  - `ask_user`
- 模型响应日志：`temp/model_responses/`。

## 暂不支持

- UI / TUI / Web / Tauri。
- 浏览器控制。
- Claude 原生协议。
- 多模型 fallback。
- 长期记忆系统。
- scheduler / goal mode。
- 插件系统。
- 多 Agent Team/Role/Action 旧架构。

## 配置

默认配置在：

```bash
configs/config.yaml
```

推荐使用环境变量提供 Key：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
```

配置检查：

```bash
go run . config
```

## 启动

交互式：

```bash
go run .
```

单次任务：

```bash
go run . ask "读取 README.md 并总结"
```

查看工具：

```bash
go run . tools
```

## 构建

```bash
go build -o cohert ./cmd/cohert
./cohert
```

## 目录结构

```text
cmd/cohert/             CLI 入口
configs/           本地配置
internal/app/      应用装配和配置加载
internal/agent/    Agent Loop
internal/llm/      OpenAI-compatible LLM Client
internal/tools/    工具注册和基础工具
workspace/         默认工作区
temp/              日志和临时文件
```

## 设计原则

- 先保证命令行 Agent 闭环。
- 工具、LLM、Agent Loop 分层清晰。
- 不把 UI 和浏览器能力作为第一阶段阻塞项。
