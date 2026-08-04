package skill

import (
	"path/filepath"
	"sort"
)

const builtinPathPrefix = "builtin://"

var builtinSkillContents = map[string]string{
	"code-review": `---
name: code-review
description: Review local diffs or files for correctness, regressions, security risk, and missing tests.
user-invocable: true
argument-hint: "[path-or-diff]"
permissions:
  allow-tools: [file_read, code_run]
---

# Code Review

Use when the user asks for a code review or wants defects found before commit.

1. Inspect the relevant diff or files.
2. Prioritize correctness, data loss, security, concurrency, and missing verification.
3. Report findings first with file/line references.
4. Keep summaries secondary and concise.
`,
	"unit-test": `---
name: unit-test
description: Generate, repair, or run focused unit tests for changed code.
user-invocable: true
argument-hint: "[package-or-file]"
permissions:
  allow-tools: [file_read, file_write, file_patch, code_run]
---

# Unit Test

Use when the user asks to add, update, or fix unit tests.

1. Identify the smallest package or module that covers the changed behavior.
2. Add focused tests for behavior and edge cases.
3. Run the narrow test first, then broader tests when risk justifies it.
4. Do not hide failing tests; fix or report the blocker.
`,
	"browser-debug": `---
name: browser-debug
description: Debug browser UI behavior through DOM, wait, screenshot, and OCR fallbacks.
user-invocable: true
argument-hint: "[url-or-scenario]"
permissions:
  allow-tools: [browser_tabs, browser_open, browser_scan, browser_dom_summary, browser_snapshot, browser_wait_for_load, browser_wait_for_selector, browser_wait_for_text, browser_wait_for_url, browser_wait_for_stable, browser_click_element, browser_type_element, browser_press_key, browser_screenshot, browser_ocr, code_run]
---

# Browser Debug

Use for frontend verification, browser interaction, or UI bug diagnosis.

1. Open or select the target page.
2. Wait for load and stability before judging state.
3. Prefer DOM/snapshot tools before OCR.
4. Use real browser input tools for interactions and verify after each meaningful action.
`,
	"desktop-debug": `---
name: desktop-debug
description: Diagnose native desktop UI state with macOS permissions, AX snapshots, screenshots, and OCR.
user-invocable: true
argument-hint: "[app-or-window]"
permissions:
  allow-tools: [desktop_permissions, desktop_windows, desktop_activate, desktop_ax_snapshot, desktop_screenshot, desktop_ocr, computer_see, computer_find, computer_check, computer_visual_snapshot]
---

# Desktop Debug

Use for read-only diagnosis of native desktop application state.

1. Check permissions before desktop sensing.
2. Locate and activate the target window.
3. Prefer AX snapshots; use screenshot/OCR only when AX is insufficient.
4. Do not perform risky actions unless the user explicitly asks and the tool requires confirmation.
`,
	"release-check": `---
name: release-check
description: Run focused pre-release checks over git status, tests, docs, and obvious packaging issues.
user-invocable: true
argument-hint: "[scope]"
permissions:
  allow-tools: [file_read, code_run]
---

# Release Check

Use before handing off a completed change or release candidate.

1. Inspect git status and changed files.
2. Run the relevant tests or explain why they cannot run.
3. Check docs or user-facing command help if behavior changed.
4. Summarize residual risk and exact verification commands.
`,
}

func builtinSkills() []Skill {
	aliases := make([]string, 0, len(builtinSkillContents))
	for alias := range builtinSkillContents {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	out := make([]Skill, 0, len(aliases))
	for _, alias := range aliases {
		content := builtinSkillContents[alias]
		metadata := parseMetadata([]byte(content), alias)
		out = append(out, Skill{
			ID:            string(ScopeBuiltin) + "/" + alias,
			Alias:         alias,
			Name:          metadata.Name,
			Description:   metadata.Description,
			UserInvocable: metadata.UserInvocable,
			ArgumentHint:  metadata.ArgumentHint,
			Requires:      metadata.Requires,
			Permissions:   metadata.Permissions,
			Scope:         ScopeBuiltin,
			Path:          builtinPathPrefix + filepath.ToSlash(filepath.Join(alias, SkillFileName)),
		})
	}
	return out
}
