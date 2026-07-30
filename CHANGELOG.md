# Changelog

All notable changes to Cohort will be documented in this file.

The format follows the spirit of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses semantic versioning for public releases.

## [0.1.0] - 2026-07-28

### Added

- First public macOS release of the `cohort` CLI.
- One-line installer via `scripts/install.sh`, with GitHub Release binary download and source-build fallback.
- OpenAI-compatible Chat Completions provider support for DeepSeek, Ollama, LM Studio, and compatible gateways.
- Anthropic Messages API provider support through `provider: anthropic`.
- Multiple LLM profile support with explicit `active_profile` and `fallback_profiles`.
- Local Agent loop with streaming responses, tool calling, max-turn control, and visible action narration.
- Local tools for file read/write/patch, shell command execution, user confirmation, and working checkpoints.
- Chrome Browser Bridge automation with tabs, open, DOM scan, JavaScript execution, click, type, key press, waits, screenshots, and OCR fallback.
- macOS desktop Computer Use based on Accessibility / AX, including permissions checks, window discovery, activation, screenshots, AX snapshots, controlled press/focus/click/type actions, visual snapshots, and multi-step execution plans.
- MCP runtime for stdio/HTTP server management, dynamic tool discovery, permissions, and project/user/local scoped configuration.
- Session store with `meta.json`, `history.jsonl`, session listing, resume, memory, and compact summaries.
- Context manager with tool-result compaction, group-safe history trimming, relevant memory injection, session memory, and full compact.
- Verified long-term memory flow with evidence requirements, duplicate checks, read-back confirmation, and audit records.
- Skill runtime with previewed installation, dry run, pinned git refs, manifest hash checks, update checks, doctor, and `skill_read`.
- Local observability with structured `run.log.jsonl`, token usage parsing, and optional Langfuse trace ingestion.
- Offline reflection command for session archives, tool-failure reports, SOP candidates, and memory quality reports.

### Security

- API keys are read from explicit configuration or environment variables and are not written by the installer.
- Browser and desktop actions are routed through controlled tools instead of unconstrained model-side execution.
- External side-effect actions require explicit confirmation; high-risk actions are refused for manual completion.
- Sensitive fields such as tokens, secrets, passwords, clipboard content, and screenshots are redacted or referenced instead of blindly logged.

### Known Limitations

- Desktop Computer Use is currently macOS-focused.
- Native LLM providers are limited to OpenAI-compatible Chat Completions and Anthropic Messages API.
- Gemini native API, Bedrock, Vertex, and Azure OpenAI-specific auth/path variants do not have native adapters yet.
- Browser automation requires loading the local Chrome Bridge extension.
- Desktop automation requires macOS Accessibility and Screen Recording permissions.
