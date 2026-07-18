# Cohert Go MVP 测试功能文档

这份文档用于测试当前 Cohert Go MVP 是否可用。当前先测试项目根目录内启动，不测试全局安装和任意路径启动。

测试分两类：

- 本地测试：不调用模型，不需要 API Key。
- 端到端测试：调用 DeepSeek，需要 `DEEPSEEK_API_KEY`。

## 1. 测试前准备

进入项目根目录：

```bash
cd /Users/bytedance/Desktop/myOwnProject/Cohort
```

确认 Go 可用：

```bash
go version
```

建议 Go 版本：

```text
go1.21+
```

## 2. 本地基础测试

这些命令不需要 API Key。

### 2.1 格式化

```bash
gofmt -w ./cmd ./internal
```

预期结果：无输出。

### 2.2 编译和测试

```bash
go test ./...
```

预期结果类似：

```text
?    cohert/cmd/cohert              [no test files]
?    cohert/internal/agent      [no test files]
?    cohert/internal/app        [no test files]
?    cohert/internal/llm        [no test files]
?    cohert/internal/tools      [no test files]
```

### 2.3 直接运行 CLI

```bash
go run . help
```

预期结果：看到命令说明。

### 2.4 构建本地二进制

```bash
go build -o cohert ./cmd/cohert
```

预期结果：在项目根目录生成本地二进制 `./cohert`。

### 2.5 查看帮助

```bash
go run . help
```

或者：

```bash
./cohert help
```

预期结果：看到命令说明：

```text
cohert
cohert ask "task"
cohert tools
cohert config
```

### 2.6 查看工具列表

```bash
go run . tools
```

预期结果：

```text
file_read
file_write
file_patch
code_run
ask_user
```

### 2.7 查看配置

```bash
go run . config
```

如果没有设置 Key，预期结果：

```text
model: deepseek-v4-pro
api_base: https://api.deepseek.com
workspace: workspace
api_key: missing
```

如果已经设置 Key，最后一行应该是：

```text
api_key: set
```

## 3. API Key 测试

当前默认使用 DeepSeek。

### 3.1 临时设置 Key

只对当前终端有效：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
```

### 3.2 验证 Key 是否被项目识别

```bash
go run . config
```

预期结果：

```text
api_key: set
```

### 3.3 如果使用 `go run`

当前推荐不依赖二进制，直接运行：

```bash
go run . config
```

## 4. 端到端模型测试

以下命令会调用模型。

### 4.1 最小问答测试

```bash
go run . ask "用一句话介绍你自己"
```

预期现象：

- 控制台出现 `LLM Running (Turn 1) ...`
- 模型流式输出文本。
- 没有工具调用时任务结束。

### 4.2 文件读取工具测试

```bash
go run . ask "读取 README.md 前 40 行，并用 5 条 bullet 总结"
```

预期现象：

- 第一轮模型应该调用 `file_read`。
- 控制台出现：

```text
Tool: file_read
Args: ...
Result(file_read): ...
```

- 后续轮次模型基于 README 内容输出总结。

### 4.3 文件写入工具测试

```bash
go run . ask "在 workspace/hello.txt 写入一行 Hello Cohert，然后读取它确认内容"
```

预期现象：

- 模型调用 `file_write`。
- 模型调用 `file_read`。
- 文件生成在：

```bash
workspace/hello.txt
```

手动确认：

```bash
cat workspace/hello.txt
```

预期包含：

```text
Hello Cohert
```

### 4.4 文件补丁工具测试

先准备文件：

```bash
mkdir -p workspace
printf 'name=old\n' > workspace/patch-demo.txt
```

运行：

```bash
go run . ask "把 workspace/patch-demo.txt 里的 name=old 改成 name=new，然后读取文件确认"
```

预期现象：

- 模型调用 `file_patch`。
- 模型调用 `file_read`。
- 文件内容变为：

```text
name=new
```

手动确认：

```bash
cat workspace/patch-demo.txt
```

### 4.5 命令执行工具测试

```bash
go run . ask "执行 pwd 和 ls，告诉我当前工作区里有哪些文件"
```

预期现象：

- 模型调用 `code_run`。
- 工具结果包含命令输出和 `exit_code`。

### 4.6 ask_user 工具测试

```bash
go run . ask "问我想创建什么文件名，然后用我的回答在 workspace 下创建这个文件"
```

预期现象：

- 模型调用 `ask_user`。
- 命令行出现问题并等待输入。
- 输入文件名后，模型继续调用 `file_write`。

## 5. 交互模式测试

启动：

```bash
go run .
```

进入后输入：

```text
/tools
```

预期输出工具列表。

继续输入：

```text
读取 README.md 并总结
```

预期模型开始执行任务。

清空会话：

```text
/clear
```

退出：

```text
/exit
```

## 6. 日志检查

模型响应日志写入：

```bash
temp/model_responses/
```

查看：

```bash
find temp/model_responses -type f -maxdepth 1 -print
```

预期至少有一个 `.log` 文件。

注意：日志里可能包含模型原始响应，不要提交 `temp/`。

## 7. 回归测试清单

每次改代码后至少执行：

```bash
gofmt -w ./cmd ./internal
go test ./...
go build -o cohert ./cmd/cohert
go run . tools
go run . config
```

如果改了 LLM 或 Agent Loop，再执行：

```bash
go run . ask "读取 README.md 前 20 行并总结"
```

如果改了工具，再执行对应工具测试：

- 改 `file_read`：跑 4.2。
- 改 `file_write`：跑 4.3。
- 改 `file_patch`：跑 4.4。
- 改 `code_run`：跑 4.5。
- 改 `ask_user`：跑 4.6。

## 8. 常见问题

### 8.1 `api_key: missing`

说明环境变量没有设置。

执行：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
go run . config
```

### 8.2 `llm http status 401`

说明 Key 无效、过期或没有权限。

处理：

- 检查 `DEEPSEEK_API_KEY` 是否正确。
- 检查 DeepSeek 控制台余额或权限。

### 8.3 模型没有调用工具

可能原因：

- 模型认为可以直接回答。
- 任务描述不够明确。
- 模型服务商 tool calling 兼容性异常。

可以换成更明确的任务：

```bash
go run . ask "必须调用 file_read 读取 README.md 前 20 行，然后总结"
```

### 8.4 `bash: ./cohert: No such file or directory`

说明还没构建本地二进制。当前推荐直接用：

```bash
go run . tools
```

如果需要本地二进制，再执行：


```bash
go build -o cohert ./cmd/cohert
```

### 8.5 文件写到了意料之外的位置

默认工作区是：

```text
workspace
```

相对路径会基于 `workspace` 解析。检查配置：

```bash
go run . config
```

## 9. 当前 MVP 验收标准

满足以下条件即可认为 MVP 可用：

- `go test ./...` 通过。
- `go build -o cohert ./cmd/cohert` 通过。
- `go run . tools` 输出 5 个工具。
- `go run . config` 能识别 API Key。
- `go run . ask "用一句话介绍你自己"` 能得到模型回答。
- `go run . ask "读取 README.md 前 40 行并总结"` 能触发 `file_read`。
- `go run . ask "在 workspace/hello.txt 写入 Hello Cohert"` 能生成文件。
