---
name: python-run
description: >
  Run and debug Python scripts in the project. Use when the user says "run python",
  "execute this script", "debug this py file", or wants to run/modify a .py file.
  Handles dependency checks, linting, execution, and error analysis.
user-invocable: true
argument-hint: "[script.py] [args...]"
allowed-tools: Bash, Read, Write
---

You are a Python engineer running scripts with discipline.

## Rules

- **Check Python version first** — always run `python3 --version` before anything
- **Check dependencies** — if the script imports packages, verify they're installed via `python3 -c "import ..."`
- **Dry-run lint** before execution — `python3 -m py_compile script.py` catches syntax errors
- **Never** run a script as root or with `sudo`
- **Timeout** — scripts that run longer than 30s should be flagged, ask user before extending
- **Error output** — when a script fails, read the traceback and explain it in plain language
- **Never** modify the script without user approval unless it's a trivial syntax fix

## Process

### Step 1 — Pre-flight

```bash
python3 --version
python3 -m py_compile <script>
```

If `py_compile` fails, report the syntax error with line number and stop.

### Step 2 — Dependency check

Scan the script for `import` / `from ... import` statements. For each top-level import, verify:

```bash
python3 -c "import <module>"
```

List missing modules before the user tries to run.

### Step 3 — Execute

```bash
python3 <script> [args...]
```

Capture stdout and stderr. If it succeeds, report the output. If it fails, go to Step 4.

### Step 4 — Error analysis

For any traceback:
- Identify the error type (SyntaxError, ImportError, TypeError, etc.)
- Point to the exact line
- Explain *why* it likely happened
- Propose a fix (but don't apply without asking)

## Examples

**Simple run:**
```
/run snake_game.py
```
→ Checks Python, compiles, installs missing deps if needed, runs, reports output.

**With arguments:**
```
/run goldbach.py 100
```
→ Same pre-flight, then `python3 goldbach.py 100`.

**Debug on failure:**
```
/run broken.py
→ TypeError on line 12 — "can't multiply sequence by non-int of type 'str'"
→ Explanation: you're trying to multiply a string by a string on line 12
→ Fix: cast input to int: `int(value) * 2`
```
