# code_run 启动本地长期服务卡住问题记录

## 问题现象

使用 `code_run` 启动本地 HTTP 服务时，页面文件已经创建成功，端口也已经监听，但 Agent 停在 `code_run` 结果处，迟迟不进入下一步。

典型命令：

```bash
cd /Users/bytedance/Desktop/myOwnProject/Cohort/workspace && python3 -m http.server 8899 &
sleep 1 && echo "Server started on port 8899"
```

表面看这条命令已经把 `python3 -m http.server 8899` 放到后台，但在 `code_run` 中仍可能表现为卡住。

## 根因

`python3 -m http.server 8899` 是长期运行服务，不会主动退出。

即使 shell 里用了 `&` 把它放到后台，后台进程仍可能继承当前命令的 `stdout` 和 `stderr`。`code_run` 会等待命令输出流关闭后再返回结果，因此只要后台服务还占着输出句柄，工具就可能认为命令还没有彻底结束。

这不是 `file_write` 失败，也不是浏览器能力失败，而是长期运行进程和一次性命令执行工具之间的生命周期不匹配。

## 如何确认服务其实已经启动

检查端口：

```bash
lsof -nP -iTCP:8899 -sTCP:LISTEN
```

检查页面是否可访问：

```bash
curl -I --max-time 3 http://127.0.0.1:8899/demo.html
```

如果返回 `HTTP/1.0 200 OK` 或类似成功响应，说明本地服务已经正常工作。

## 推荐写法

启动长期服务时，应让服务进程彻底脱离当前 `code_run` 的输出流：

```bash
cd /Users/bytedance/Desktop/myOwnProject/Cohort/workspace
nohup python3 -m http.server 8899 > /tmp/cohort-demo-server.log 2>&1 &
echo "Server started on port 8899"
```

关键点：

- `nohup`：让服务脱离当前 shell 生命周期。
- `> /tmp/cohort-demo-server.log 2>&1`：把标准输出和错误输出重定向到日志文件。
- `&`：后台运行。
- `echo`：给 `code_run` 一个明确的短输出，便于模型判断服务已经启动。

## 停止服务

先找到监听端口的进程：

```bash
lsof -nP -iTCP:8899 -sTCP:LISTEN
```

再停止对应 PID：

```bash
kill <PID>
```

如果服务无响应，再考虑：

```bash
kill -9 <PID>
```

## 后续改进方向

可以考虑新增专门的长期服务工具，例如 `server_start` / `server_stop` / `server_status`，不要让模型直接用 `code_run` 管理常驻进程。

工具层可以做这些事情：

- 自动重定向 stdout 和 stderr。
- 记录 PID、端口、工作目录和日志路径。
- 启动后主动探活。
- 提供统一停止能力。
- 避免长期服务占住一次性工具调用。
