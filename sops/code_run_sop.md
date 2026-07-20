# Code Run SOP

## 默认原则

- 短命令可以直接用 `code_run`。
- 长生命周期服务不能直接前台运行。
- 后台服务必须脱离 stdout/stderr，否则工具会等待输出管道关闭，看起来像卡住。

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

## 停止

- 必须精确 PID。
- 不要无条件杀所有 `python` 或所有同名进程。
- 如果是当前工具启动的进程，优先记录 PID 后定点清理。
