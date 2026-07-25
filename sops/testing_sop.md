# Testing SOP

## 触发场景

- 完成代码改动后需要验证。
- 用户问功能是否可用。
- 修改 Go、JS、浏览器插件、文档路径、后台服务相关逻辑。

## 默认验证矩阵

Go 改动：

```bash
./internal/tests/run.sh
```

`internal/tests/packages.txt` 是 internal 包测试的统一清单。Go 同包白盒测试文件仍放在源码包旁边，不要直接搬进 `internal/tests`，否则会改变包路径并破坏未导出符号访问。

Chrome 插件 JS 改动：

```bash
node --check assert/cohert_browser_bridge/background.js
```

文档路径或引用改动：

```bash
rg -n "旧路径或旧名字" .
```

后台服务：

```bash
lsof -i :端口
curl -I http://127.0.0.1:端口
```

## 验证原则

- 验证范围随风险扩大。
- 能跑自动测试就不要只做静态检查。
- PASS 必须有工具证据；只读代码或口头判断不算验证。
- 实现者写的 happy path 测试只是上下文，关键链路要至少做一次独立检查。
- 如果不能验证，必须说明原因和剩余风险。
- 用户看不到终端输出，最终回答要说清楚验证结论。

## 验证动作选择

| 产物类型 | 最小动作 |
| --- | --- |
| Go 代码 | `go test ./...`；范围大时补相关包定向测试 |
| CLI / 脚本 | 运行命令，检查 stdout/stderr/exit code，必要时跑边界输入 |
| 浏览器工具 | 工具注册检查、mock 测试；真实页面任务还要 wait 后 scan/screenshot 取证 |
| Chrome 插件 JS | `node --check`，必要时配合浏览器 bridge 手测 |
| 文档 / SOP | 读取修改后全文或相关段落，检查路径、索引和触发词一致 |
| 记忆 / SOP 晋级 | 检查目标文件、`memory/audit.jsonl` 和 `/sop candidates` 或索引更新 |

## 对抗性检查

至少选择一个和改动相关的非 happy path：

- 空输入、缺字段、坏 JSON。
- 重复执行是否幂等。
- 目标不存在或路径错误。
- 旧数据、旧 tool result 或 orphan tool result。
- 浏览器页面未加载、元素 disabled、selector 不唯一。

## 验收标准

- 测试通过：给出命令和结论。
- 测试失败：列出失败点、原因判断、下一步。
- 未能测试：明确阻塞条件。
- 文档类改动：说明已检查涉及的索引、路径或引用；无须为纯文档改动强行跑全量 Go 测试，除非改了代码或 schema。
