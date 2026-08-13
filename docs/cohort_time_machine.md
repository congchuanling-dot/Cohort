# Cohort Time Machine

Cohort Time Machine turns an Agent run into a verifiable, executable experiment:

```text
Record -> Verify -> Fork -> Intervene -> Replay -> Compare -> Prove
```

It is designed for debugging non-deterministic Agent behavior without repeating historical side effects.

## Replay Bundle

Every normal `Runner.Run` writes a bundle under:

```text
temp/sessions/<session_id>/replay/<run_id>/
├── manifest.json
├── runtime.json
├── frames.jsonl
├── workspace.patch
└── workspace-files/
```

The bundle records:

- Provider, model, Git commit/tree and working-tree fingerprint.
- Base system prompt and complete tool schemas in a `0600` runtime snapshot.
- Every LLM request and response.
- Every tool call, normalized arguments, observation and control metadata.
- Per-frame, aggregate and runtime SHA-256 hashes.
- A bounded dirty-worktree snapshot for isolated reconstruction.

Dirty snapshots are limited to 20 MiB total and 5 MiB per untracked file. Symlinks, special files, escaping paths and oversized snapshots make the run `exact_only`.

## Exact Replay

Exact Replay is offline and has no model, tool or network side effects:

```bash
cohort trace replay exact <session_id> --run <run_id>
```

It verifies:

- Manifest and runtime schema compatibility.
- Runtime, frame and aggregate content hashes.
- Monotonic request/response state transitions.
- Tool-call/result identity and ordering.
- Final status and proof hash.

The command fails at the first divergence and reports its sequence, turn and reason.

## Fork Replay

Fork Replay reuses recorded responses and observations before a decision point, then switches to the configured live model and tools:

```bash
cohort trace replay fork <session_id> \
  --run <run_id> \
  --fork-turn 7 \
  --model candidate-model \
  --system-prompt candidate.md \
  --repeat 5
```

Each trial:

1. Creates an isolated Git worktree at the recorded commit.
2. Restores the recorded dirty workspace snapshot.
3. Seeds the historical conversation prefix into a new Session.
4. Replays LLM and tool results before `fork-turn`.
5. Rejects the trial if the replayed request prefix has drifted.
6. Calls the live model and tools at and after `fork-turn`.
7. Archives the trial bundle before deleting the worktree.

`--keep-worktree` preserves trial worktrees for manual inspection. The source Session is never mutated.

## Proof Report

Fork experiments are stored under:

```text
temp/sessions/<session_id>/replay/<run_id>/experiments/<experiment_id>/
├── report.json
└── trials/<n>/
    ├── manifest.json
    ├── runtime.json
    └── frames.jsonl
```

The report contains baseline/trial status, turns, token usage, latency, first behavioral divergence, success rate, means across repeated trials and a proof hash.

The Control Center exposes the redacted index through:

```text
GET /api/v1/replays/{session_id}/{run_id}
```

This endpoint requires the existing HttpOnly control-session cookie. It returns hashes and summaries, not raw prompts or tool observations.

## Determinism Boundary

- Prefix LLM responses are replayed from the bundle.
- Prefix tool observations are replayed; tools are not executed.
- The live suffix is statistically evaluated and is not claimed to be deterministic.
- Browser, desktop and external MCP side effects only execute in the live suffix.
- A forkable proof requires a valid Git baseline and a complete workspace snapshot.

Use `--repeat` for claims about model or prompt quality. A single successful suffix is evidence of one outcome, not proof of a stable improvement.
