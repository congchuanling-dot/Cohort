# Cohert

Local-first command-line Agent Runtime for tool calling, browser automation, desktop sensing, long-context work, SOPs, and verified memory.

**Languages:** [简体中文](../../README.md) · **English** · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## What Is Cohert

Cohert is a Go-based local Agent Runtime. It connects an OpenAI-compatible LLM with controlled tools, persistent sessions, browser automation, macOS desktop Computer Use, context compaction, SOP routing, and verified long-term memory.

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
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

Run one task:

```bash
go run . ask "read README.md and summarize the runtime capabilities"
```

Inspect the runtime:

```bash
go run . config
go run . tools
go run . session list
```

Build:

```bash
go build -o cohert ./cmd/cohert
./cohert
```

Default config lives in [`configs/config.yaml`](../../configs/config.yaml). Full usage is in [`docs/usage.md`](../usage.md).

## Features

| Area | Capability |
| --- | --- |
| Agent Loop | Streaming OpenAI-compatible chat, tool calling, max-turn control |
| Local Tools | File read/write/patch, shell execution, user questions, structured errors |
| Browser Automation | Chrome bridge, page scan, JS execution, element snapshot, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS permissions, windows, PID activation, screenshots, AX tree, desktop OCR, controlled `AXPress`, restricted key press, text drafting |
| Sessions | `history.jsonl`, metadata, session list, resume, local audit trail |
| Context Manager | Tool-result compaction, group-safe trimming, session memory, full compact |
| SOP Runtime | SOP index, task-based hints, working checkpoints |
| Evolution Memory | Evidence-backed entries, project memory, duplicate checks, read-back confirmation, audit records |

## CLI

```bash
cohert                         # interactive mode
cohert ask "task"              # run one task and exit
cohert tools                   # list mounted tools
cohert config                  # show effective config
cohert session list            # list local sessions
cohert session resume <id>     # resume a session
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

Cohert controls Chrome through a local Browser Bridge:

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

If browser tools report `browser_not_connected`, load the Chrome extension from `assert/cohert_browser_bridge`.

## Desktop Computer Use

Cohert provides generic macOS desktop sensing and controlled AX semantic actions:

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

Grant Accessibility and Screen Recording permissions to the terminal or IDE running Cohert.

## Memory And SOP

Long-term memory uses a strict three-step pipeline:

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Memory writes must reference verified tool evidence, reject sensitive or duplicate content, and read back the written entry before success.

SOPs are lightweight operational constraints. Cohert injects [`sops/index.md`](../../sops/index.md) as navigation and asks the model to read the relevant SOP before acting.

## Project Layout

```text
cmd/cohert/             CLI entrypoint
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
