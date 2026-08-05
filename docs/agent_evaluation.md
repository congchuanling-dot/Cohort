# Cohort Agent 评测系统

> 状态：`[完成第一版]`。本系统用于真实 Agent 回归，不是只比较字符串的演示脚本。

## 目标

评测链路覆盖：

```text
suite 定义
-> case 筛选
-> 独立 Runner/session 执行
-> 输出、工具、状态、耗时、轮数、失败数断言
-> 加权评分
-> 与同 suite 上一次结果比较
-> JSON / Markdown / HTML 持久化
-> 历史趋势与回归定位
```

评测不会把 session 写入日常 `temp/sessions`，而是隔离到：

```text
.cohort/evals/sessions/
```

评测结果位于：

```text
.cohort/evals/
  suites/
    core.json
    tool-routing.json
  runs/
    eval_<timestamp>/
      result.json
      report.md
      report.html
  latest
  sessions/
```

## 快速开始

```bash
cohort eval init
cohort eval list
cohort eval run core
cohort eval history
cohort eval report latest --open
```

筛选执行：

```bash
cohort eval run core --case instruction_exact
cohort eval run core --case read_go_version,locate_runner
cohort eval run core --tag codebase
cohort eval run tool-routing --tag browser
```

并行执行：

```bash
cohort eval run core --workers 4
cohort eval run stateful --workers 2 --repeat 3
```

默认 `workers=1`。涉及浏览器、桌面或共享外部状态的 case 不建议并行，避免不同 Agent 互相争抢窗口或页面。

## 内置套件

### core

稳定、只读的核心回归集，包含 8 个 case：

- 精确指令遵循。
- 不确定性与防幻觉。
- `go.mod` 读取。
- 显式模型配置读取。
- Agent 主循环代码定位。
- 生命周期事件总结。
- 信息充分时不滥用 `ask_user`。
- 只读边界遵循。

### tool-routing

环境相关工具路由集，包含 4 个 case：

- LSP symbols。
- macOS desktop permissions。
- Browser tabs bridge。
- 只读 shell 命令。

该套件会暴露真实环境问题。例如浏览器扩展未连接时，browser case 应失败，而不是被算作核心模型质量下降。

### stateful

真实状态任务集，每个 case 默认重复两次：

- 创建结构化 JSON 文件。
- 读取并精确 patch 已有文件。
- 运行测试、修复 Go 实现、再次验证测试。

该套件同时评分最终磁盘状态、工具顺序、工具调用数量、重复调用和稳定率。

## Suite 协议

Suite 使用显式 JSON：

```json
{
  "schema_version": 1,
  "id": "my-suite",
  "name": "My Agent Suite",
  "description": "项目回归集",
  "tool_groups": ["core", "lsp"],
  "default_repeat": 3,
  "cases": [
    {
      "id": "read_config",
      "name": "读取配置",
      "prompt": "读取 configs/config.yaml 并回答 model 值。",
      "tags": ["codebase", "config"],
      "timeout_seconds": 90,
      "repeat": 5,
      "fixture": {
        "mode": "temp",
        "files": {
          "input.txt": "status=old\n"
        }
      },
      "assertions": {
        "status": "done",
        "output_contains": ["deepseek-v4-pro"],
        "output_not_contains": ["无法读取"],
        "output_regex": ["deepseek-[a-z0-9-]+"],
        "min_output_chars": 4,
        "max_output_chars": 200,
        "required_tools": ["file_read"],
        "forbidden_tools": ["file_write", "file_patch"],
        "max_turns": 4,
        "max_duration_ms": 60000,
        "max_tool_failures": 0,
        "max_tool_calls": 5,
        "tool_sequence": ["file_read", "file_patch"],
        "no_consecutive_tool_repeat": true,
        "files_exist": ["output.json"],
        "files_not_exist": ["unexpected.txt"],
        "file_contains": {
          "output.json": ["\"status\"", "\"ready\""]
        },
        "file_not_contains": {
          "output.json": ["status=old"]
        }
      }
    }
  ]
}
```

支持的断言：

| 字段 | 含义 |
| --- | --- |
| `status` | Runner 最终状态，例如 `done` |
| `output_contains` | 输出必须包含，大小写不敏感 |
| `output_not_contains` | 输出禁止包含 |
| `output_regex` | Go 正则表达式 |
| `min_output_chars` | 最小 Unicode 字符数 |
| `max_output_chars` | 最大 Unicode 字符数 |
| `required_tools` | 必须真实调用的工具 |
| `forbidden_tools` | 禁止调用的工具 |
| `max_turns` | 最大 Agent turn 数 |
| `max_duration_ms` | 最大执行时间 |
| `max_tool_failures` | 最大工具失败数 |
| `max_tool_calls` | 最大工具调用总数 |
| `tool_sequence` | 必须按顺序出现的工具子序列 |
| `no_consecutive_tool_repeat` | 禁止连续重复调用同一工具 |
| `files_exist` / `files_not_exist` | 最终文件存在性 |
| `file_contains` / `file_not_contains` | 最终文件内容断言 |

`tool_groups` 用于固定评测时暴露给模型的工具面。省略时继承普通配置；正式 suite 建议显式声明，避免 MCP、桌面或其他无关工具造成授权等待、schema 膨胀和不可复现路由。

`default_repeat` 设置 suite 默认重复次数，case 的 `repeat` 可覆盖它，CLI 的 `--repeat N` 优先级最高。Case 只有所有 attempt 全部通过才算通过；报告同时保留平均分和稳定率。

`fixture.mode=temp` 会为每次 attempt 创建独立工作区，`files` 用于声明初始文件。Fixture 路径和状态断言路径必须是工作区内相对路径，拒绝绝对路径和 `..` 逃逸。

## 评分语义

- 状态、工具和执行错误权重高于普通文本包含断言。
- 每个断言产生独立 `AssertionResult`。
- 分数表示满足断言的加权比例。
- 任意断言失败时 case 判定为失败，不能用其他低价值断言的得分掩盖硬失败。
- Suite 分数是所有 case 分数的算术平均。
- Pass Rate 是完全通过 case 的比例。

工具调用和轮数不是从模型文本猜测，而是读取该评测 session 的 `run.log.jsonl`。

## 基线与回归

每次运行会自动查找同 suite 的上一次结果并比较：

- Score delta。
- Pass Rate delta。
- Duration delta。
- Token delta。
- Regressed cases。
- Improved cases。

这使 prompt、tool schema、模型或 Agent Loop 修改可以进行真实回归，而不是只看一次主观体验。

## Dashboard

`report.html` 是单文件离线 Dashboard，不依赖 CDN 或前端构建工具。包括：

- Pass Rate、Score、Cases、Duration、Tokens KPI。
- 基线变化与回归数。
- 最近 20 次运行趋势。
- 标签通过率。
- case 搜索和 pass/fail 筛选。
- 每个 case 的断言、工具、轮数、token、session 和输出详情。

打开最近结果：

```bash
cohort eval report --open
```

## CI 建议

核心回归：

```bash
cohort eval run core --workers 2
```

命令在任意 case 失败时返回非零退出码，适合 CI gate。Dashboard 和 `result.json` 应作为 CI artifact 保存。

环境相关 suite 建议单独运行：

```bash
cohort eval run tool-routing --tag lsp
cohort eval run tool-routing --tag browser
cohort eval run tool-routing --tag desktop
```

## 安全边界

- 内置 suite 默认不写文件。
- core suite 明确禁止 `file_write` / `file_patch`。
- core suite 只暴露 `core`、`lsp` 工具组；tool-routing suite 显式暴露其需要的环境工具，但不启用 MCP。
- Eval Runner 关闭长期记忆 final review，避免评测样本写入记忆或产生额外模型轮次。
- Eval Runner 关闭 capability-gap 自动记录；评测失败只进入 eval result，不污染项目真实能力缺口。
- tool-routing 只做读取和权限检查。
- 自定义 suite 是可执行 Agent 输入，评审后再在真实环境运行。
- Dashboard 对模型输出使用 HTML escaping；嵌入 JSON 会转义 `<` 等危险字符。
- 并行执行不适用于共享 GUI 状态。

## 后续方向

下一阶段：

1. Judge 模型评分，作为确定性断言的补充，而不是替代。
2. Golden artifact/diff 断言。
3. 按 provider/profile 做 A/B matrix。
4. Eval result 接入 Langfuse dataset。
5. Hermes daemon 调度周期评测并推送回归告警。
