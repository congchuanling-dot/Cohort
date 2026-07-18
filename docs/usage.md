# Cohert 使用教程

这份文档说明当前 Cohert 怎么启动、有哪些命令、怎么恢复 session。

当前阶段只说明项目根目录内的本地启动方式，不说明全局安装。所有命令都默认在 Cohert 项目根目录执行：

```bash
cd /Users/bytedance/Desktop/myOwnProject/Cohort
```

## 1. 准备 API Key

如果只是查看配置、工具列表、session 列表，不需要 API Key。

如果要让模型真正回答问题或继续 session，需要先设置：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
```

检查配置：

```bash
go run . config
```

看到下面结果表示 Key 已被识别：

```text
api_key: set
```

## 2. 启动交互模式

最常用的启动方式：

```bash
go run .
```

启动后会看到：

```text
╭────────────────────────────────────────────────────────────╮
│ Cohert                                                     │
│ Command-line Agent Runtime                                │
├────────────────────────────────────────────────────────────┤
│ Model      deepseek-v4-pro                                 │
│ Workspace  workspace                                       │
│ Session    new session                                     │
│ Tools      5                                               │
├────────────────────────────────────────────────────────────┤
│ 直接输入任务开始执行                                      │
│ 输入 / 查看命令面板；输入 / 后按 Tab 选择命令             │
╰────────────────────────────────────────────────────────────╯

cohert ›
```

然后直接输入任务：

```text
读取 README.md 并总结
```

退出交互模式：

```text
/exit
```

清空当前内存上下文，并开启新 session：

```text
/clear
```

查看当前可用工具：

```text
/tools
```

查看所有对话内命令：

```text
/help
```

在真实终端里，可以输入 `/` 后按 `Tab` 选择命令。

如果直接输入 `/` 并回车，会显示命令面板：

```text
Slash commands

  /help                 显示命令帮助
  /model                查看当前模型
  /config               查看运行配置
  /tools                查看工具列表
  /session              查看当前 session
  /session list         列出历史 session
  /resume <id>          恢复 session
  /compact              预留上下文压缩入口
  /clear                清空当前内存上下文
  /exit                 退出
```

## 3. 执行单次任务

如果不想进入交互模式，可以用 `ask`：

```bash
go run . ask "读取 README.md 前 40 行，并用 5 条 bullet 总结"
```

`ask` 会执行一次任务，任务结束后进程退出。

## 4. 当前所有命令

### 4.1 查看帮助

```bash
go run . help
```

作用：查看当前支持的命令。

### 4.2 进入交互模式

```bash
go run .
```

等价于：

```bash
go run . run
```

作用：启动一个持续对话的本地 Agent。

### 4.3 执行单次任务

```bash
go run . ask "任务内容"
```

作用：执行一次任务，完成后退出。

### 4.4 查看工具列表

推荐在交互模式里输入：

```text
/tools
```

外部 CLI 也保留：

```bash
go run . tools
```

当前工具：

```text
file_read
file_write
file_patch
code_run
ask_user
```

这个命令不需要 API Key。

### 4.5 查看配置

推荐在交互模式里输入：

```text
/model
/config
```

外部 CLI 也保留：

```bash
go run . config
```

作用：查看模型、API 地址、工作区、API Key 是否已设置。

这个命令不需要 API Key。

### 4.6 查看 session 列表

推荐在交互模式里输入：

```text
/session list
```

外部 CLI 也保留：

```bash
go run . session list
```

作用：列出本地保存过的会话。

这个命令不需要 API Key。

输出示例：

```text
ID                        TITLE           MESSAGES  UPDATED              CWD
20260718-223408-8af91b03  你的session有什么效果  8         2026-07-18 22:36:49  /Users/bytedance/Desktop/myOwnProject/Cohort
```

字段含义：

- `ID`：session 的唯一标识，恢复时要用它。
- `TITLE`：会话标题，默认来自第一条用户输入。
- `MESSAGES`：`history.jsonl` 里已经保存的消息数，包括 user、assistant、tool。
- `UPDATED`：最后更新时间。
- `CWD`：创建 session 时所在目录。

### 4.7 恢复 session

推荐在交互模式里输入：

```text
/resume <session_id>
```

也可以写成：

```text
/session resume <session_id>
```

外部 CLI 兼容入口：

```bash
go run . session resume <session_id>
```

例如：

```bash
go run . session resume 20260718-223408-8af91b03
```

作用：

- 读取 `temp/sessions/<session_id>/history.jsonl`。
- 把历史消息恢复到 `Runner.history`。
- 进入交互模式，等待你继续输入新任务。
- 后续新消息继续追加到同一个 `history.jsonl`。

恢复成功后会看到类似：

```text
resumed session 20260718-223408-8af91b03 (8 messages): 你的session有什么效果
```

然后可以继续问：

```text
继续基于刚才的内容讲 session 是怎么落盘的
```
继续基于刚才的内容讲 session 是怎么落盘的
```

## 5. session 怎么用

推荐流程：

1. 先启动交互模式。

```bash
go run .
```

2. 输入你的任务。

```text
帮我看一下这个项目的 session 设计
```

3. 退出。

```text
/exit
```

4. 下次回来先列出 session。

```text
/session list
```

5. 复制要恢复的 ID。

```text
/resume 20260718-223408-8af91b03
```

6. 继续提问。

```text
基于刚才的上下文，下一步应该开发什么
```

## 6. session 文件保存在哪里

默认目录：

```text
temp/sessions/
```

每个 session 一个子目录：

```text
temp/sessions/<session_id>/
```

目录里主要有两个文件：

```text
meta.json
history.jsonl
```

`meta.json` 保存轻量信息：

- session ID
- 标题
- 工作目录
- 模型
- 创建时间
- 更新时间

`history.jsonl` 保存真正的上下文：

- 用户消息
- 模型回复
- 模型工具调用
- 工具执行结果

## 7. 什么时候用 ask，什么时候用 resume

用 `ask` 的场景：

- 一次性问题。
- 不需要保留上下文。
- 例如总结一个文件、跑一次命令、问一个独立问题。

```bash
go run . ask "总结 README.md"
```

用交互模式的场景：

- 连续开发。
- 多轮追问。
- 希望自动保存 session。

```bash
go run .
```

用 `/resume` 的场景：

- 上次聊到一半退出了。
- 想让模型继续看到之前上下文。
- 想继续往同一个 `history.jsonl` 追加消息。

```text
/resume <session_id>
```

## 8. 常见问题

### 8.1 `session list` 没有内容

说明还没有产生过本地 session。

先执行一次：

```bash
go run . ask "用一句话介绍 Cohert"
```

或者进入交互模式问一个问题：

```bash
go run .
```

然后在交互模式里执行：

```text
/session list
```

### 8.2 `session resume` 后会不会新建 session

不会。

恢复后会继续使用原来的 session ID。新产生的 user、assistant、tool 消息会继续追加到原来的：

```text
temp/sessions/<session_id>/history.jsonl
```

### 8.3 `/clear` 和 `session resume` 有什么关系

`/clear` 只在当前交互进程里生效。

它会清空当前 Runner 的内存上下文，并重置当前 session。之后你再输入新任务，会创建新的 session。

它不会删除磁盘上的旧 session 文件。

### 8.4 恢复很久以前的 session 会有什么问题

当前版本会把 `history.jsonl` 里的历史恢复进 Runner。

如果历史太长，后续可能导致：

- 模型请求变慢。
- 上下文太长。
- 工具结果过大。

所以下一步需要开发上下文裁剪，把过长历史和工具输出控制住。

### 8.5 可以直接修改 `history.jsonl` 吗

不建议。

`history.jsonl` 是一行一个 JSON。如果手动改坏其中一行，`session resume` 读取时会失败。

需要排查时可以只读文件：

```bash
sed -n '1,20p' temp/sessions/<session_id>/history.jsonl
```

## 9. 命令速查

外部 CLI：

```bash
# 查看帮助
go run . help

# 进入交互模式
go run .

# 执行单次任务
go run . ask "任务内容"

# 查看工具
go run . tools

# 查看配置
go run . config

# 查看 session 列表，兼容入口
go run . session list

# 恢复 session，兼容入口
go run . session resume <session_id>

# 构建本地二进制
go build -o cohert ./cmd/cohert

# 使用本地二进制
./cohert
./cohert ask "任务内容"
./cohert session list
./cohert session resume <session_id>
```

交互模式内：

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/session resume <session_id>
/compact
/clear
/exit
```
