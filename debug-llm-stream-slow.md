# Debug Session: llm-stream-slow

Status: [OPEN]

## Symptom

用户反馈：打开 Cohort 并对话时，LLM 转得明显变慢，流式输出也变慢，整体变卡。

## Hypotheses

1. 启动/首轮变慢来自 MCP Manager 同步加载或外部 MCP Server 冷启动。
2. 首 token 前变慢来自请求 payload 变大，例如工具 schema、系统提示词、Capability/Skill/Project/Plan 注入。
3. 流式输出变慢来自本地 consume/sink/观测日志逐 delta 阻塞。
4. Context Auto Compact 或 Context Build 在每轮请求前触发了重型处理。
5. 远端 LLM provider 变慢，本地请求构造正常但 SSE chunk 间隔变大。

## Evidence Plan

- 记录 Runner 启动装配耗时：LLM client、MCP manager、skill store、registry、context manager。
- 记录每轮请求构造耗时、system prompt 长度、message/tool schema 数量。
- 记录 LLM Chat 调用返回 stream 前耗时、首个 token 延迟、chunk 间隔和 consume 总耗时。
- 检查配置和本地 MCP/adapter 状态。

## Current Constraints

- 在收集运行时证据前不修改业务逻辑。
- 第一处代码改动只允许添加性能观测 instrumentation。

## Evidence

### Pre-fix reproduction

Command:

```bash
COHORT_DEBUG_PERF=1 /usr/bin/time -p go run . ask "只回复 OK，不要调用工具。"
```

Observed:

- Total wall time: 9.55s.
- `loadMCPManager`: 1867ms.
- Tool schemas exposed to model: 81.
- OpenAI request payload: 77174 bytes.
- First SSE payload: 293ms after request start.
- First visible text chunk: 2040ms after request start.

Additional finding:

- `.env` enables Langfuse:
  - `COHORT_LANGFUSE_ENABLED=true`
  - `LANGFUSE_HOST=https://us.cloud.langfuse.com`
- `run.log.jsonl` timestamps showed synchronous observation emission blocking the main loop:
  - `SessionStarted -> RunStarted`: ~1.8s.
  - Further observation events: ~300ms to >1s gaps.

### Root Causes

1. Project-local MCP config used two `npx -y` stdio servers. Cohort loaded them synchronously on Runner startup, even for ordinary chat.
2. The default tool registry exposed the full Browser/Desktop/Computer/MCP surface to every LLM call. A trivial prompt still sent 81 tool schemas and a 77KB payload.
3. Langfuse observation sink performed synchronous HTTP ingestion on the Agent loop path. Since project `.env` enabled Langfuse, each lifecycle event could block visible progress.
4. JSONL observation sink opened/wrote/closed synchronously per event. This was also on the hot path.

## Fixes Applied

- Added `tools.enabled_groups` configuration.
  - Empty/omitted remains backward compatible and registers the historical full tool surface.
  - Current project config explicitly uses lean groups: `[core, lsp, memory, skill, ask]`.
- Skipped MCP loading unless the `mcp` tool group is explicitly enabled.
- Converted local JSONL observation sink to asynchronous queued writing.
- Added a generic `observability.AsyncSink` and wrapped Langfuse with it so external HTTP ingestion cannot block the Agent loop.

## Post-fix Verification

Same command with Langfuse still enabled in `.env`:

```bash
COHORT_DEBUG_PERF=1 /usr/bin/time -p go run . ask "只回复 OK，不要调用工具。"
```

Observed:

- Total wall time: 2.44s.
- Runner startup: 7ms.
- MCP loading: disabled, 0-1ms, 0 registered MCP tools.
- Tool schemas exposed to model: 15.
- OpenAI request payload: 22397 bytes.
- `SessionStarted`, `RunStarted`, `UserPromptSubmitted`, `ContextBuilt`, `TurnStarted`, `LLMRequestStarted`: all emitted within ~10ms.
- LLM response duration in run log: 888ms.

## Verification Commands

```bash
go test ./...
COHORT_DEBUG_PERF=1 /usr/bin/time -p go run . ask "只回复 OK，不要调用工具。"
```

Status: [FIXED - pending user confirmation before removing debug instrumentation]
