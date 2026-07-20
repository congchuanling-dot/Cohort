# Code Run SOP

## 触发场景

- 需要运行 shell、Go test、node check、Python 脚本。
- 需要启动本地 HTTP 服务、调试进程、检查端口。
- 命令可能长时间运行、持续输出或需要清理进程。

## 默认原则

- 短命令可以直接用 `code_run`。
- 长生命周期服务不能直接前台运行。
- 后台服务必须脱离 stdout/stderr，否则工具会等待输出管道关闭，看起来像卡住。
- 命令要有明确工作目录。
- 不要用无限运行命令占住 Agent。

## 短命令

适合直接运行：

```bash
go test ./...
node --check assert/cohert_browser_bridge/background.js
rg -n "keyword" .
```

要求：

- 需要用户看到的关键结果必须总结。
- 失败后先读错误，不要无信息重试。

## 启动后台服务

推荐：

```bash
nohup python3 -m http.server 8899 > /dev/null 2>&1 &
```

禁止：

```bash
python3 -m http.server 8899 &
```

原因：

```text
code_run 会等待 stdout/stderr 管道关闭。
普通后台符号 & 不一定关闭这些管道，服务继续运行时工具可能一直等。
```

## 验证

启动后台服务后必须检查：

- 端口是否监听。
- HTTP 请求是否可访问。
- PID 是否存在。

示例：

```bash
lsof -i :8899
curl -I http://127.0.0.1:8899
```

## 停止

- 必须精确 PID。
- 不要无条件杀所有 `python` 或所有同名进程。
- 如果是当前工具启动的进程，优先记录 PID 后定点清理。

## 验收标准

- 测试命令：明确通过或列出失败测试。
- 服务命令：端口、HTTP、PID 三者至少验证两个。
- 清理命令：确认目标进程已退出，不误杀无关进程。
