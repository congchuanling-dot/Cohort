# Cohert

टूल कॉलिंग, ब्राउज़र ऑटोमेशन, डेस्कटॉप सेंसिंग, लंबे context, SOP और verified memory के लिए local-first command-line Agent Runtime.

**भाषाएं:** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · **हिन्दी**

## Cohert क्या है

Cohert Go में लिखा गया local Agent Runtime है। यह OpenAI-compatible LLM, नियंत्रित tool layer, persistent sessions, browser automation, macOS desktop Computer Use, context compaction, SOP routing और verified long-term memory को जोड़ता है।

```text
User intent
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> Local tools / browser / desktop / shell
  -> Evidence ledger
  -> Session history और verified memory
```

मुख्य नियम सरल है: model reasoning करता है, लेकिन execution explicit, auditable, recoverable और evidence-backed होना चाहिए।

## Quick Start

```bash
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

एक task चलाएं:

```bash
go run . ask "README.md पढ़ो और runtime capabilities summarize करो"
```

Runtime inspect करें:

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

Default config [`configs/config.yaml`](../../configs/config.yaml) में है। पूरा usage guide [`docs/usage.md`](../usage.md) में है।

## Features

| Area | Capability |
| --- | --- |
| Agent Loop | Streaming OpenAI-compatible chat, tool calling, max-turn control |
| Local Tools | File read/write/patch, shell execution, user questions, structured errors |
| Browser Automation | Chrome bridge, page scan, JS execution, element snapshot, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS permissions, windows, PID activation, screenshots, AX tree, desktop OCR, controlled `AXPress` |
| Sessions | `history.jsonl`, metadata, session list, resume, local audit trail |
| Context Manager | Tool-result compaction, safe trimming, session memory, full compact |
| SOP Runtime | SOP index, task-based hints, working checkpoints |
| Evolution Memory | Evidence-backed entries, project memory, duplicate checks, read-back confirmation, audit records |

## CLI

```bash
cohert                         # interactive mode
cohert ask "task"              # one task run करके exit
cohert tools                   # mounted tools list
cohert config                  # effective config
cohert session list            # local sessions
cohert session resume <id>     # session resume
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

Cohert local Browser Bridge से Chrome control करता है:

```text
ws://127.0.0.1:18777/browser
```

Recommended flow:

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

`browser_ocr` केवल तब use करें जब DOM text और `browser_dom_summary` rendered text नहीं पढ़ पाते। OCR bbox `screenshot-local` होता है, system mouse coordinates नहीं।

Optional OCR dependencies:

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

अगर `browser_not_connected` मिले, तो Chrome extension `assert/cohert_browser_bridge` से load करें।

## Desktop Computer Use

Cohert generic macOS desktop sensing और controlled AX semantic actions देता है:

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
```

OCR से पहले Accessibility / AX को prefer किया जाता है। आज `desktop_ax_press` ही अकेला desktop input action है। इसके लिए frontmost PID, fresh AX node metadata, action से पहले revalidation और action के बाद AX verification जरूरी है।

Risk policy:

- R1 reversible actions direct run हो सकते हैं।
- R2 external side effects के लिए `ask_user` का one-time confirmation token चाहिए।
- R3 high-risk actions जैसे payment, approval, authorization, login verification या deletion automatic run नहीं होंगे।

Desktop coordinate click, keyboard और text-input tool अभी उपलब्ध नहीं हैं।

macOS helper dependencies:

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Cohert चलाने वाले terminal या IDE को Accessibility और Screen Recording permissions दें।

## Memory और SOP

Long-term memory strict three-step pipeline use करती है:

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Memory writes को verified tool evidence reference करना होगा, sensitive या duplicate content reject होगा, और success से पहले read-back होगा।

SOP lightweight operational constraints हैं। Cohert [`sops/index.md`](../../sops/index.md) को navigation की तरह inject करता है और relevant SOP पढ़कर action करता है।

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
