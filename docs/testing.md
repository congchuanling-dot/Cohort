# Cohort 测试功能文档

这份文档用于测试当前 Cohort 是否可用。当前先测试项目根目录内启动，不测试全局安装和任意路径启动。

日常使用教程见：[usage.md](./usage.md)。

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
./internal/tests/run.sh
```

这个脚本会读取 `internal/tests/packages.txt`，统一执行当前所有带测试的 internal 包。
源码旁的 `*_test.go` 保持在原包目录中，因为它们是 Go 同包白盒测试，搬到其他目录会失去对包内未导出符号的访问。

也可以直接运行全量包测试：

```bash
go test ./...
```

### 2.3 直接运行 CLI

```bash
go run . help
```

预期结果：看到命令说明。

### 2.4 构建本地二进制

```bash
go build -o cohort ./cmd/cohort
```

预期结果：在项目根目录生成本地二进制 `./cohort`。

### 2.5 查看帮助

```bash
go run . help
```

或者：

```bash
./cohort help
```

预期结果：看到命令说明：

```text
cohort
cohort ask "task"
cohort tools
cohort config
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

### 2.8 Control Center

先构建前端，再运行控制面测试：

```bash
cd web/console
npm ci
npm run typecheck
npm run build
cd ../..

go test -race ./internal/controlplane ./internal/controlactions ./internal/cli
go run . ui --no-open
```

浏览器验收：

1. 打开终端输出的 `control_center` URL。
2. 确认 URL 中的 token 被清除，页面显示当前分支、模型和运行数据。
3. 按 `⌘K` 搜索 `system.ping`，点击“执行动作”。
4. 确认 Operation 经 SSE 进入 `succeeded`，点击记录可查看结果。
5. 搜索 `delivery.cancel`，确认页面要求 Delivery ID 和精确文本 `CANCEL`；不要对真实
   Delivery 提交破坏性验收。

安全回归由 `internal/controlplane` 测试覆盖：非 loopback 拒绝、一次性 bootstrap、
HttpOnly/SameSite Cookie、Origin、CSRF、请求体限制、Secret 脱敏、Operation 中断恢复及
`0600/0700` 权限。

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
go run . ask "在 workspace/hello.txt 写入一行 Hello Cohort，然后读取它确认内容"
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
Hello Cohort
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

`code_run` 当前按 GA 的思路实现：

- Unix/macOS 下使用 `bash -c`，不使用 `bash -lc`，避免加载用户 `.bashrc` 或 `.bash_profile`。
- 默认 timeout 是 60 秒。
- 模型传入的 timeout 最大会被限制到 120 秒。
- 命令超时时会返回 `timeout: true` 和 `timeout_seconds`。
- Unix/macOS 下超时会尽量杀掉整组子进程，避免 `bash` 退出后 `grep/find/sleep` 继续残留。

如果需要本地只跑 `code_run` 相关测试：

```bash
go test ./internal/tools -run 'TestCodeRun|TestNormalize' -count=1
```

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

预期先看到启动欢迎页，包含：

- `Cohort`
- `Command-line Agent Runtime`
- 当前模型
- workspace
- session 状态
- 常用 slash 命令

真实终端里可以输入 `/`，预期出现 slash 命令选择菜单。

菜单里可以用上下键选择命令，按回车执行。

脚本或管道输入里可以直接输入：

```text
/
```

预期退化输出文本命令面板。

真实终端里输入 `/se` 后按 `Tab`，预期可以补全 `/session`。

进入后输入：

```text
/help
```

预期输出所有对话内命令。

继续输入：

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

## 6. 会话恢复测试

当前 Cohort 会把对话消息写入：

```text
temp/sessions/<session_id>/history.jsonl
```

### 6.1 查看本地 session 列表

推荐在交互模式里输入：

```text
/session list
```

也可以用兼容的外部 CLI：

```bash
go run . session list
```

预期结果：

- 如果还没有历史会话，输出 `no sessions`。
- 如果已经运行过任务，会看到 `ID`、`TITLE`、`MESSAGES`、`UPDATED`、`CWD`。

### 6.2 恢复一个 session

先从列表里复制一个 ID，然后在交互模式里执行：

```text
/resume <session_id>
```

也可以用兼容的外部 CLI：

```bash
go run . session resume <session_id>
```

预期现象：

- 控制台输出 `resumed session ...`。
- 后续输入的新问题会接在旧的 `history.jsonl` 后面。
- 模型请求会带上恢复出来的历史上下文。

### 6.3 本地测试 session 读写

```bash
go test ./internal/session ./internal/agent -run 'TestStoreListAndLoadHistory|TestRunnerResumeSessionContinuesExistingHistory' -count=1
```

## 7. 日志检查

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

## 8. 回归测试清单

每次改代码后至少执行：

```bash
gofmt -w ./cmd ./internal
go test ./...
go build -o cohort ./cmd/cohort
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
- 改 `code_run`：跑 4.5，并执行 `go test ./internal/tools -run 'TestCodeRun|TestNormalize' -count=1`。
- 改 `ask_user`：跑 4.6。
- 改 `browser_ocr`：执行 `python3 -m py_compile scripts/browser_ocr.py`，再执行 `go test ./internal/vision ./internal/tools -run 'Test.*OCR' -count=1`。
- 改桌面 M1/M2：执行 `python3 -m py_compile scripts/desktop_darwin.py`，再执行 `go test ./internal/desktop ./internal/tools -run 'Test.*Desktop|TestConfirmation' -count=1`。macOS 手工验收先调用 `desktop_permissions`，确认权限后再枚举窗口、激活和 AX 快照；`desktop_screenshot` 必须生成 sidecar manifest；`desktop_ax_press` 只能选取无副作用的 R1 节点，确认动作前后 AX 快照存在可观察变化；`desktop_ax_focus` 只测临时输入框聚焦；`desktop_click` 只测 AX 节点中心点击，不得使用 OCR bbox 或任意坐标；`desktop_visual_click` 只测截图 manifest + OCR bbox 的受控转换，不得输入裸屏幕坐标，并确认输入类 bbox 会返回一次性 `visual_focus_token`；`desktop_press_key` 实机只测 `Escape`、`Tab` 等低风险键，或在搜索/下拉候选已选中时测试 `Enter + intent=open_selected_result` 不触发确认；`desktop_type_text` 只在已聚焦的临时输入框中输入无敏感测试文本，或在 WebView 验收中消费 `visual_focus_token` 进行一次起草。
- 改 session：跑 6.1、6.2，并执行 `go test ./internal/session ./internal/agent -run 'TestStoreListAndLoadHistory|TestRunnerResumeSessionContinuesExistingHistory' -count=1`。

## 9. 常见问题

### 9.1 `api_key: missing`

说明环境变量没有设置。

执行：

```bash
export DEEPSEEK_API_KEY="sk-xxx"
go run . config
```

### 9.2 `llm http status 401`

说明 Key 无效、过期或没有权限。

处理：

- 检查 `DEEPSEEK_API_KEY` 是否正确。
- 检查 DeepSeek 控制台余额或权限。

### 9.3 模型没有调用工具

可能原因：

- 模型认为可以直接回答。
- 任务描述不够明确。
- 模型服务商 tool calling 兼容性异常。

可以换成更明确的任务：

```bash
go run . ask "必须调用 file_read 读取 README.md 前 20 行，然后总结"
```

### 9.4 `bash: ./cohort: No such file or directory`

说明还没构建本地二进制。当前推荐直接用：

```bash
go run . tools
```

如果需要本地二进制，再执行：


```bash
go build -o cohort ./cmd/cohort
```

### 9.5 文件写到了意料之外的位置

默认工作区是：

```text
workspace
```

相对路径会基于 `workspace` 解析。检查配置：

```bash
go run . config
```

## 10. 当前 MVP 验收标准

满足以下条件即可认为 MVP 可用：

- `go test ./...` 通过。
- `go build -o cohort ./cmd/cohort` 通过。
- `go run . tools` 输出文件、命令、浏览器、工作记忆和长期记忆工具。
- `go run . config` 能识别 API Key。
- `go run . ask "用一句话介绍你自己"` 能得到模型回答。
- `go run . ask "读取 README.md 前 40 行并总结"` 能触发 `file_read`。
- `go run . ask "在 workspace/hello.txt 写入 Hello Cohort"` 能生成文件。
