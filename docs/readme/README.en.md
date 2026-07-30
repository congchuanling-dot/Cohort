# Cohort

Local-first command-line Agent Runtime for tool calling, browser automation, desktop sensing, long-context work, SOPs, and verified memory.

**Languages:** [简体中文](../../README.md) · **English** · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## What Is Cohort

Cohort is a Go-based local Agent Runtime. It connects OpenAI-compatible and Anthropic LLM providers with controlled tools, persistent sessions, browser automation, macOS desktop Computer Use, context compaction, SOP routing, and verified long-term memory.

```text
User intent
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> Local tools / browser / desktop / shell
  -> Evidence ledger
  -> Session history and verified memory
```

The core rule is simple: the model reasons, but execution must be explicit, auditable, recoverable, and evidence-backed.

## Quick Start

```bash
npm install -g @cohort-ai/cohort@latest
export DEEPSEEK_API_KEY="sk-xxx"
cohort
```

Run one task:

```bash
cohort ask "read README.md and summarize the runtime capabilities"
```

Inspect the runtime:

```bash
cohort config
cohort doctor
cohort session list
```

Run from source for development:

```bash
git clone https://github.com/congchuanling-dot/Cohort.git
cd Cohort
go build -o cohort ./cmd/cohort
./cohort
```

The npm package is published on the public npm registry and downloads the verified macOS binary from GitHub Releases during installation. Default config lives in [`configs/config.yaml`](../../configs/config.yaml). Full usage is in [`docs/usage.md`](../usage.md).

## LLM Providers

Cohort natively supports two provider families:

- `provider: openai`: OpenAI-compatible Chat Completions endpoints such as DeepSeek, Ollama, LM Studio, and similar `/v1/chat/completions` gateways
- `provider: anthropic`: Anthropic Messages API

It also supports explicit `llm.profiles` and `fallback_profiles` for primary/backup chains.

This does not mean every API type works out of the box yet. Native adapters for Gemini, Bedrock, Vertex, and Azure OpenAI-specific auth/path variants are not implemented yet.

## Features

| Area | Capability |
| --- | --- |
| Agent Loop | Streaming chat with OpenAI-compatible / Anthropic providers, tool calling, max-turn control |
| Local Tools | File read/write/patch, shell execution, user questions, structured errors |
| Browser Automation | Chrome bridge, page scan, JS execution, element snapshot, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS permissions, windows, PID activation, screenshots, AX tree, desktop OCR, controlled `AXPress`, restricted key press, text drafting |
| Sessions | `history.jsonl`, metadata, session list, resume, local audit trail |
| Context Manager | Tool-result compaction, group-safe trimming, session memory, full compact |
| SOP Runtime | SOP index, task-based hints, working checkpoints |
| Evolution Memory | Evidence-backed entries, project memory, duplicate checks, read-back confirmation, audit records |

## CLI

```bash
cohort                         # interactive mode
cohort ask "task"              # run one task and exit
cohort tools                   # list mounted tools
cohort config                  # show effective config
cohort session list            # list local sessions
cohort session resume <id>     # resume a session
```

Interactive commands:

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/compact
/full-compact
/memory
/exit
```

## Browser Automation

Cohort controls Chrome through a local Browser Bridge:

```text
ws://127.0.0.1:18777/browser
```

Recommended flow:

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

Use `browser_ocr` only when DOM text and `browser_dom_summary` cannot read rendered text. OCR boxes are `screenshot-local` and must not be treated as system mouse coordinates.

Optional OCR dependencies:

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

If browser tools report `browser_not_connected`, load the Chrome extension from `assert/cohort_browser_bridge`.

## Desktop Computer Use

Cohort provides generic macOS desktop sensing and controlled AX semantic actions:

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
  -> desktop_press_key
  -> desktop_type_text
```

Accessibility / AX is preferred over OCR. `desktop_ax_press` requires a frontmost PID, fresh AX node metadata, pre-action revalidation, and post-action AX verification. `desktop_press_key` only supports a restricted key allowlist. Low-risk navigation keys can run directly; Enter, Cmd+Enter, Delete, Backspace, and similar submit/delete keys require confirmation. `desktop_type_text` only drafts text into the current focused editable field and never sends it.

Risk policy:

- R1 reversible actions can run directly.
- R2 external side effects require a one-time `ask_user` confirmation token.
- R3 high-risk actions such as payment, approval, authorization, login verification, or deletion are refused for manual completion.

There is still no desktop coordinate click tool.

macOS helper dependencies:

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Grant Accessibility and Screen Recording permissions to the terminal or IDE running Cohort.

## Memory And SOP

Long-term memory uses a strict three-step pipeline:

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Memory writes must reference verified tool evidence, reject sensitive or duplicate content, and read back the written entry before success.

SOPs are lightweight operational constraints. Cohort injects [`sops/index.md`](../../sops/index.md) as navigation and asks the model to read the relevant SOP before acting.

## Project Layout

```text
cmd/cohort/             CLI entrypoint
configs/                local configuration
docs/                   guides, designs, development records
internal/app/           app assembly and system prompt
internal/agent/         agent loop and evidence flow
internal/browser/       Chrome bridge protocol
internal/contextmgr/    request construction and compaction
internal/desktop/       desktop driver and helper runner
internal/evolution/     verified memory
internal/tools/         file, shell, browser, desktop, memory tools
sops/                   SOP index and playbooks
workspace/              default workspace
temp/                   sessions and runtime logs
```

## Development

```bash
go test ./...
go vet ./...
go run . tools
go run . config
```

## Principles

- Local first.
- Auditable tools.
- Immutable history.
- Layered context.
- Verified memory.
- Progressive evolution.
