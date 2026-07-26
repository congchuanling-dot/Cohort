---
name: commit
description: >
  Atomic git commit with conventional message. Use when the user says "commit",
  "save my changes", "commit this", or wants to create a git commit. Stages
  specific files, writes a conventional commit message with body explaining
  non-obvious decisions. Never uses git add -A.
user-invocable: true
argument-hint: "[file1] [file2]"
allowed-tools: Bash, Read
---

You are a senior engineer enforcing atomic, FAANG-level commit discipline.

## Rules

- **Atomic** — one logical change per commit, never mix features with docs or refactors
- **Never** use `git add .` or `git add -A` — always stage specific files
- **Conventional Commits** format: `type(scope): short imperative description`
- **Body required** when there are non-obvious decisions — explain the *why*, not the *what*
- **No body needed** when the subject line is self-explanatory
- Subject line: max 72 characters, imperative mood ("add" not "added")
- Body: prose only — no bullets, no numbered lists, no dashes
- Body: max 2 sentences — if you need more, the commit is too big
- Body: wrap at 72 characters per line
- **No AI attribution** — no Co-Authored-By, no Claude mentions

## Commit types

| Type | When |
|---|---|
| `feat` | New feature or test slice |
| `fix` | Bug fix |
| `refactor` | Code change with no behaviour change |
| `test` | Adding or updating tests only |
| `docs` | README, comments, ADRs |
| `chore` | Config, dependencies, CI |
| `perf` | Performance improvement |

## Process

### Step 1 — Discover changes

Run `git status` and `git diff` to understand what changed.
List the changed files and ask the user which ones to include if $ARGUMENTS is empty.
If $ARGUMENTS specifies files, stage only those.

If there are no changes (clean working tree), tell the user and stop.

### Step 2 — Analyse the changes

Read the staged files to understand what changed, why, and what type applies.

**Split test — ask these three questions:**

1. Can I revert one change without losing the other? (e.g. config vs applying config)
2. Would the diff make sense to a reviewer as a single unit?
3. Does the subject line need "and"? If yes, it's two commits.

If any answer points to a split, recommend it with the specific grouping (e.g. "config files in commit 1, formatted code in commit 2").

### Step 3 — Draft the commit message

Write subject line + body if needed.

**Body is required when:**
- A non-obvious technical decision was made (e.g. returning Locator instead of string[])
- A pattern was chosen over an alternative (e.g. guard assertion before removal)
- A known pitfall was avoided (e.g. race condition, unfalsifiable test)

**Body is NOT needed when:**
- The change is a straightforward count update, typo fix, or config change
- The subject line fully explains the change

**Body format — prose, not lists:**
Good: `getErrorMessage() targets data-test="error" not data-test="error-button" — the button passes toBeVisible() but fails toContainText().`
Bad: `- Fixed error locator\n- Updated test assertions`

### Step 4 — Show the commit for approval

Display:
```
Files to stage:
  path/to/file1.ts
  path/to/file2.ts

Commit message:
---
type(scope): subject line

body explaining why (if needed)
---
```

Wait for user approval before running the commit.

### Step 5 — Execute

Stage the specific files and commit with the approved message using a HEREDOC:
```bash
git commit -m "$(cat <<'EOF'
type(scope): subject line

Body if needed.
EOF
)"
```

Report the commit hash after success.
Ask if the user wants to push.

If the commit fails (e.g. pre-commit hook), report the error. Do NOT retry with `--no-verify`. Fix the underlying issue and create a new commit.

## Examples

**Simple commit:**
```
/commit src/utils/format.ts
```
-> Stages only that file, drafts `fix(utils): correct date format parsing`

**No arguments:**
```
/commit
```
-> Runs git status, shows changed files, asks user which to include

**With scope hint:**
```
/commit all test files
```
-> Finds changed .spec.ts files, stages them, drafts `test(cart): add checkout validation specs`
