# Cohort 项目评审报告（Code Review）

> 评审时间：2026-08-15 · 评审范围：`internal/`、`cmd/`、`main.go` 源码 + 工程化/文档/Git 卫生
> 排除项：`.cohort/`、`temp/`、`workspace/`、`internal/cli/temp/` 等运行时产物与 worktree 副本
> 定位：秋招展示项目，本报告同时关注「代码质量」和「面试展示专业度」

---

## 0. 一句话结论

**底子很硬，门面有瑕。** 核心工程能力（架构分层、测试覆盖、并发安全、极简依赖）达到了远超普通秋招项目的水准，`go build`/`go vet`/`go test ./internal/...` 全绿（31 个包全部 ok，0 失败）。但仓库里混进了大量**运行时垃圾、个人面试文档、灌水提交**，以及几个 1500~2000 行的**上帝文件**，这些会直接拉低面试官第一印象。**先花半天做「减法」，再谈重构。**

---

## 1. 做得好的地方（务必在面试里主动讲）

### 1.1 架构分层清晰，职责边界明确 ⭐
- `internal/` 下 39 个包，按能力域切分（`agent` / `llm` / `tools` / `session` / `replay` / `hermes` / `capability` / `observability` …），~67k 行生产代码 + ~19k 行测试代码。
- **依赖倒置用得地道**：
  - `agent.ToolRunner` 是个极小接口（[runner.go:41](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go#L41)），测试里能塞假工具，不用真拉起浏览器/桌面。
  - REPL 通过注入 `EvalCommand`/`TraceCommand` 等 `func` 字段避免 `repl → cli` 循环依赖，且有注释说明动机——这是**刻意的设计决策**，不是巧合。

### 1.2 测试覆盖真实且贯穿 ⭐
- `internal/` 下 85 个 `_test.go`，与源码同目录。LLM 层（`openai_test.go` / `anthropic_test.go` / `fallback_test.go` / `factory_test.go`）、回放、能力中心、eval 都有测试。
- 全量 `go test ./internal/...` **31 个包全部通过**（`cli` 20.7s、`lsp` 21.8s、`delivery` 20.5s，说明有真实的集成级用例，不是走过场）。

### 1.3 极简依赖，且是「正确的极简」⭐
- `go.mod` 只有 **4 个直接依赖**，全是终端 UI / 协议插件（`readline`、`promptui`、`x/term`、`gorilla/websocket`），**核心逻辑零第三方依赖**。
- 不是「不会用库」，而是**用标准库把该做的做对了**：
  - LLM HTTP 层（[openai.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/llm/openai.go)）用 `net/http` + 可配置超时 + 退避重试（`isRetryable`）+ `bufio` 解析 SSE 流为 channel，是地道 Go。
  - CLI 用手写 `switch` 分发而非 cobra，子命令场景下合理、依赖树扁平。

### 1.4 并发安全，几乎没有踩坑 ⭐
- **0 处 `panic(`**（源码），全靠 error 返回。
- 共享 map 都有锁保护：`mcp/manager.go`（RWMutex 护 `clients`/`tools`）、`browser/server.go`（RWMutex + 独立 `writeMu`）、`hermes/service.go`（`mu` 护 `running map`）。
- `observability/async_sink.go` 是范例级实现：有界 channel + 满则丢弃（`select{ case ch<-e: default: }`）+ `closeMu`/`closed` 防重复关闭 + `WaitGroup` + 关闭预算定时器。
- `controlplane/operation.go` 的嵌套锁**锁序一致**（从不反向持有），`publish()` 持锁时用非阻塞 `select+default`，慢订阅者无法拖死管理器——**审得越细越能看出功底**。

### 1.5 可观测性有真正的抽象层 ⭐
- **全树 0 处 `log.Printf`/`log.Fatal`/`slog`** —— 没有裸日志泄漏。
- 走 `observability` 的 `Sink`/`Bus` 结构化事件管线（`AsyncSink`/`jsonl_sink`/`langfuse_sink`/`memory_sink`），Agent 核心不直接 `fmt.Print`，靠注入的 `io.Writer` 输出，UI 与核心逻辑分离干净。

### 1.6 Context 贯穿 & 文档规范
- `ctx context.Context` 出现在 **425 个函数签名**里，长任务（agent 循环、MCP、eval、delivery、worktree）全程透传。
- `CHANGELOG.md` 遵循 Keep-a-Changelog + 语义化版本，`SECURITY.md` 有 R1/R2/R3 风险分级和敏感字段脱敏说明——**这两份文件很加分**。
- 代码有充分的中文 doc comment，且解释「为什么」而非「是什么」。

---

## 2. 做得不好的地方（按修复优先级排序）

### 🔴 P0 — 仓库卫生：直接影响面试印象，成本最低，务必先做

**问题 A：个人/内部文档混进了展示仓库**
- 仓库根目录 tracked：
  - [cohort_source_onboarding_feishu.xml](file:///Users/bytedance/Desktop/myOwnProject/Cohort/cohort_source_onboarding_feishu.xml)（92KB，飞书导出的「源码级架构全解…面试指南」）
  - [cohort_resume_interview_feishu.xml](file:///Users/bytedance/Desktop/myOwnProject/Cohort/cohort_resume_interview_feishu.xml)（48KB，简历/面试飞书导出）
  - [debug-llm-stream-slow.md](file:///Users/bytedance/Desktop/myOwnProject/Cohort/debug-llm-stream-slow.md)（一份 Status:[OPEN] 的调试日志）
- 这些是**个人面试准备资料**，出现在展示仓库里非常突兀，且暴露了「这是刷面试用的项目」。**建议直接从 git 移除**（可留本地）。

**问题 B：运行时产物被提交，且违反了自己写的 SECURITY.md**
- SECURITY.md 明确写「不要提交 memory 文件、session 日志」，但仓库却 tracked：
  - `memory/raw_sessions/all_histories.md`（**784KB** 的 agent 会话 dump）
  - `memory/audit.jsonl`、`memory/global.md`、`memory/reflection/*.md` 等
  - `.cohort/hermes/events.jsonl` / `runs.jsonl` / `alerts.jsonl`（`.gitignore` 漏掉了这几个 hermes jsonl）
- **建议**：从 git 移除这些运行时文件，并把 `.cohort/hermes/*.jsonl`、`memory/` 补进 `.gitignore`。

**问题 C：工作区磁盘臃肿（不影响 git，但影响本地体验）**
- `temp/` **331MB**、`workspace/` 56MB、`.cohort/` 60MB（含全量代码库 worktree 副本，`computer_actions.go` 等文件被复制了 4 份）。虽已 gitignore，但会拖慢本地搜索/构建，建议定期清理。

**问题 D：灌水提交信息**
- `git log` 里至少有 **8 条 `1`** + `修复一些bug` + `修改ui`，而且最新两条 HEAD 就是 `修复一些bug` 和 `1`。
- 与之对比，正经提交写得很好（`实现无副作用Exact Replay校验引擎`、`接入Control Center回放证明查询API`）——反差之下灌水提交更刺眼。**建议对灌水提交做交互式 rebase 改写信息**（若已 push 到展示远端需权衡）。

### 🟠 P1 — 上帝文件 / 巨型方法：重构价值最高

| 文件 | 行数 | 问题 |
|---|---|---|
| [computer_actions.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/tools/computer_actions.go) | 2075 | 审批/风险样板大量复制 |
| [repl.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/repl/repl.go) | 2012 | 混入 git 命令、MCP 配置、skill 安装等 |
| [computer_tools.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/tools/computer_tools.go) | 1757 | 观测/find/vision 混杂 |
| [cli.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/cli/cli.go) | 1699 | 命令分发膨胀 |
| [browser_tools.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/tools/browser_tools.go) | 1484 | 每个工具 Run 前置校验重复 |
| [runner.go](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go) | 1462 | `Run` 单方法 500 行 |

- **`agent.Run` 是最典型的巨型方法**（[runner.go:196-699](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go#L196-L699)）：一个 500 行方法里嵌套「turn 循环 → tool 循环」，中间穿插 24 处 `debugperf.Event`、32 处 observability/hook 发射、SOP/skill 提醒、finish-guard，业务逻辑被埋没。
- **按工具名硬编码特判**泄漏进通用 runner：`update_working_checkpoint`（[:592](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go#L592)）、`file_read`（[:595](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go#L595)）、`skill_read`（[:598](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/agent/runner.go#L598)）——每加一个「特殊工具」就要改 runner。
- **复制粘贴胜过抽取**：`tools/` 里有 6 个 `*ApprovalRequiredOutcome`、6 个 `classify*Risk`、6 种 `*ToolError` 包装函数，high-risk 拒绝块在 `computer_actions.go` 复制了 8 次、`desktop_actions.go` 6 次，硬编码的中文提示串重复出现。缺一个「校验必填参数 → Outcome」的公共装饰器。
- **`repl.go` 严重越界**：REPL 输入循环里直接 `exec.Command` 调 `git restore --staged --worktree`（[:804](file:///Users/bytedance/Desktop/myOwnProject/Cohort/internal/repl/repl.go#L804)），diff/rollback 逻辑应属于独立 VCS 包，而非前端循环。

> 结论：`Runner` 和 `repl.Options` 都是「吸积点」——新功能不断往老结构/老文件上焊，导致这几个文件越滚越大。**面试前不必全改，但至少把 `agent.Run` 拆出 3~4 个私有方法**，能显著提升可读性和可讲性。

### 🟡 P2 — 细节质量

- **error 包装比例偏低**：683 处 `fmt.Errorf` 里只有 151 处（~22%）用了 `%w`，其余会打断 `errors.Is`/`errors.As` 的 unwrap 链。建议统一「跨层错误一律 `%w`」。
- **静默吞错**：`traceview/traceview.go` 的 `:127/:141/:169` 在列 run 时 `continue` 跳过单项加载失败，既不记日志也不聚合，部分失败对用户不可见；`hermes/api.go` 多处 `_ = json.NewEncoder(w).Encode(...)` 忽略 HTTP 响应编码错误。
- **detached goroutine 不随关闭取消**：`hermes/api.go:279/336/348` 用 `context.Background()` 起后台任务，服务关闭时不会被 cancel。
- **工具注册顺序靠手维护**：`tools/registry.go` 用一个 ~65 项的 `order []string` 固定 schema 顺序，漏加就会掉到无序尾部，需与常量块手工同步，较脆弱。
- **无测试的包**：`consoleui`、`computeruse`、`version`、`replayexec`、`debugperf`、`guardian`、`timemachine` 等无 `_test.go`（部分是纯胶水/前端产物，可接受，但 `guardian`/`timemachine` 是核心特性，建议补）。

### 🟢 P3 — 文档与叙事（观感层）

- `docs/` 是 **34 个 md 的设计文档堆**，缺乏策展：含 140KB 的中文《开发记录文档.md》（像个人工作日志）、654KB 时间戳命名的 JPEG、两份近乎重复的 `cohort_vs_ga_gap.md`/`cohort_vs_ga_current_gap.md`。建议归档到 `docs/design/` 并删重复项。
- README 顶部徽章标 `stable 1.0` + npm 包徽章，对单人展示项目略有「过度声明」之嫌；叙事部分（`为什么是 Cohort`）偏宣言/散文化，get-started 干货占比可再提高。
- 命名漂移：飞书文档自己承认「旧文档偶尔写成 Cohert」，注意全库拼写统一。

### ⚪ 安全提醒（非 git 问题，但需处理）
- 真实 `.env` **未被 tracked**（`.gitignore` 生效，✅），但本地工作区的 `.env` 含**疑似有效的 Langfuse 凭据**（`sk-lf-...`）。虽未进 git，仍**建议立即轮换该密钥**，以防日后误提交。

---

## 3. 行动清单（建议顺序）

- [ ] **P0**：`git rm --cached` 移除 `*_feishu.xml`、`debug-llm-stream-slow.md`、`memory/`、`.cohort/hermes/*.jsonl`；补全 `.gitignore`
- [ ] **P0**：rebase 改写 `1` / `修复一些bug` / `修改ui` 等灌水提交信息
- [ ] **P0**：轮换本地 `.env` 里的 Langfuse 密钥
- [ ] **P1**：拆分 `agent.Run`（turn 循环 / tool 循环 / 观测发射分离）；抽取 tools 的审批/风险/错误公共 helper
- [ ] **P1**：把 `repl.go` 里的 git/VCS 逻辑挪到独立包
- [ ] **P2**：跨层错误统一 `%w`；补 `guardian`/`timemachine` 测试
- [ ] **P3**：整理 `docs/`，删重复与个人日志，README 徽章措辞降调

---

## 4. 给面试的一句话话术

> "这是一个 ~87k 行、零核心第三方依赖的 Go Agent 运行时，31 个包全测试通过。我刻意用标准库自己实现了带重试/超时/SSE 流的 LLM 客户端、有界丢弃的异步可观测性管线，和可精确回放/分叉的因果引擎。它最难的部分不是调模型，而是**运行时治理**——权限、恢复、记忆和可复现性。"

把 P0 清一遍，这个项目在秋招里是能打的。
