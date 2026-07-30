# Security Policy

Cohort is a local-first Agent Runtime that can read files, run shell commands, automate Chrome, inspect macOS desktop state, and call configured MCP servers. These capabilities are powerful by design, so the security boundary is part of the product surface.

## Supported Versions

| Version | Supported |
| --- | --- |
| `0.1.x` | Security fixes are accepted on a best-effort basis during active development. |

## Reporting a Vulnerability

Please do not open a public issue for a suspected vulnerability.

Report privately through GitHub Security Advisories if available for this repository, or contact the maintainers through the GitHub repository owner profile.

Include:

- Affected version or commit.
- Operating system and architecture.
- Minimal reproduction steps.
- Expected impact.
- Whether secrets, credentials, local files, screenshots, browser data, or desktop permissions are involved.

## Security Model

Cohort assumes the user explicitly chooses to run a local agent with access to a configured workspace and configured tools. The runtime is designed to make that access auditable and bounded, not invisible.

### Local-first Defaults

- Sessions, logs, screenshots, memory, and runtime artifacts are stored locally by default.
- External observability, such as Langfuse, is optional and must be explicitly configured.
- The installer initializes configuration but does not write API keys.

### Secrets and Credentials

- API keys should be provided through environment variables or explicit local config.
- Do not commit `.env`, private config files, screenshots, session logs, or memory files that may contain sensitive context.
- Tool-call redaction covers common sensitive keys such as `token`, `secret`, and `password`.
- Token usage fields such as `input_tokens`, `output_tokens`, and `total_tokens` are preserved because they are operational metrics, not credentials.

### Tool Execution

- File and shell tools operate inside the configured workspace unless a tool explicitly documents a broader scope.
- MCP servers are only available after explicit configuration.
- Third-party Skills are not installed silently: Cohort previews the source, target path, manifest metadata, dependency declarations, and content hash before installation.

### Browser and Desktop Automation

- Browser automation uses the local Chrome Bridge and prefers DOM/state-based actions over screenshots.
- Desktop automation on macOS prefers Accessibility / AX metadata over raw visual clicking.
- Screenshots, OCR output, and AX snapshots may reveal sensitive local state. Treat runtime artifacts as private.

### Risk Controls

- `R1` reversible actions can run directly.
- `R2` external side effects require an explicit one-time confirmation token.
- `R3` high-risk actions are refused for automatic execution, including payment, approval, authorization, login verification, and destructive deletion.

## Hardening Recommendations

- Run Cohort in a dedicated workspace.
- Keep API keys in environment variables or a local `.env` file excluded from git.
- Review `.mcp.json`, `.cohort/local.mcp.json`, and installed Skills before running untrusted tasks.
- Run `cohort doctor` and `cohort doctor computer` before demos.
- Avoid enabling external tracing when working with sensitive local data.
- Before publishing logs or screenshots, manually review them for secrets and private information.
