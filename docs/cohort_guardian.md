# Cohort Guardian

Cohort Guardian is a deterministic security kernel for tool-using agents. It treats the LLM as a policy-untrusted guest and mediates every tool call outside the model.

```text
Untrusted LLM Guest
        |
        v
Guardian Tool Gateway
  - suitability
  - information-flow label
  - effect classification
  - authority decision
        |
        v
Tool / MCP / Browser / Desktop
```

## Threat Model

Guardian protects against:

- Instructions embedded in browser, file, MCP, OCR or other tool output.
- An untrusted observation causing a later external side effect.
- Secret-tainted context flowing to an external or unknown sink.
- Tool descriptions or model reasoning attempting to bypass runtime policy.
- A mitigation claim that cannot be reproduced against the same injected fault.

The model, generated plan and tool arguments are not trusted. The engine, policy file, tool contracts, append-only security log and explicit authority decisions are trusted.

Guardian does not claim to solve covert channels in arbitrary natural-language output, compromised tool binaries, kernel isolation or hardware attestation.

## Security Lattice

Every trajectory carries a folded label:

```text
Confidentiality: public < internal < secret
Integrity:       untrusted < user < trusted
Readers:         monotonically narrowing allow set
Sources:         append-only provenance set
```

Folding is monotonic:

- Confidentiality can only increase.
- Integrity can only decrease.
- Allowed readers can only narrow.
- Sources are never removed.

The primary trajectory therefore cannot silently regain authority after reading untrusted or secret data. A later phase adds isolated child trajectories and checked sanitization to avoid permanent label creep.

## Tool Contracts

Each tool has a deterministic contract:

```json
{
  "role": "source",
  "effect": "none",
  "output_confidentiality": "public",
  "output_integrity": "untrusted",
  "readers": ["local"]
}
```

Unknown tools fail conservatively as unknown-effect, untrusted-output sinks. Project overrides live at:

```text
.cohort/guardian/policy.json
```

The normalized policy and every resolved contract have stable SHA-256 hashes. Security proofs bind decisions to these hashes rather than mutable display text.

## Deterministic Decisions

Guardian emits one of:

- `allow`
- `ask`
- `deny`
- `fork_isolated`
- `sanitize`
- `require_declassification`

Initial mandatory rules:

- Untrusted source reads require an isolated trajectory.
- Untrusted context cannot authorize external or unknown effects.
- Secret context cannot reach external or unknown sinks without explicit declassification.
- External effects require an authority decision even when their context is clean.

Subsequent implementation phases connect these contracts to the Runner, append-only lineage, Time Machine fault injection and the Clean-Attacked-Mitigated proof loop.
