// Package guardian implements Cohort's deterministic information-flow and
// tool-mediation security kernel. The model is never part of the trusted
// computing base: it proposes calls, while Guardian decides whether the calls
// may execute.
package guardian

import "time"

const SchemaVersion = 1

type Confidentiality string

const (
	ConfidentialityPublic   Confidentiality = "public"
	ConfidentialityInternal Confidentiality = "internal"
	ConfidentialitySecret   Confidentiality = "secret"
)

func (c Confidentiality) String() string {
	if c == "" {
		return string(ConfidentialityPublic)
	}
	return string(c)
}

type Integrity string

const (
	IntegrityUntrusted Integrity = "untrusted"
	IntegrityUser      Integrity = "user"
	IntegrityTrusted   Integrity = "trusted"
)

func (i Integrity) String() string {
	if i == "" {
		return string(IntegrityUntrusted)
	}
	return string(i)
}

// Label is the folded security state of the visible trajectory.
// Readers is an allow set. An empty set means local-only, never unrestricted.
type Label struct {
	Confidentiality Confidentiality `json:"confidentiality"`
	Integrity       Integrity       `json:"integrity"`
	Readers         []string        `json:"readers,omitempty"`
	Sources         []string        `json:"sources,omitempty"`
}

type ToolRole string

const (
	ToolRolePure      ToolRole = "pure"
	ToolRoleSource    ToolRole = "source"
	ToolRoleSink      ToolRole = "sink"
	ToolRoleTransform ToolRole = "transform"
)

type Effect string

const (
	EffectNone     Effect = "none"
	EffectLocal    Effect = "local"
	EffectExternal Effect = "external"
	EffectUnknown  Effect = "unknown"
)

type ToolContract struct {
	Tool                  string          `json:"tool"`
	Role                  ToolRole        `json:"role"`
	Effect                Effect          `json:"effect"`
	OutputConfidentiality Confidentiality `json:"output_confidentiality"`
	OutputIntegrity       Integrity       `json:"output_integrity"`
	Readers               []string        `json:"readers,omitempty"`
	AllowUntrustedContext bool            `json:"allow_untrusted_context,omitempty"`
	AllowSecretContext    bool            `json:"allow_secret_context,omitempty"`
	Sanitizer             bool            `json:"sanitizer,omitempty"`
}

type Action string

const (
	ActionAllow                   Action = "allow"
	ActionAsk                     Action = "ask"
	ActionDeny                    Action = "deny"
	ActionForkIsolated            Action = "fork_isolated"
	ActionSanitize                Action = "sanitize"
	ActionRequireDeclassification Action = "require_declassification"
)

type Decision struct {
	Action       Action `json:"action"`
	RuleID       string `json:"rule_id"`
	Reason       string `json:"reason"`
	Tool         string `json:"tool"`
	InputLabel   Label  `json:"input_label"`
	OutputLabel  Label  `json:"output_label"`
	ContractHash string `json:"contract_hash"`
	PolicyHash   string `json:"policy_hash"`
}

type ProvenanceEvent struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Time          time.Time `json:"time"`
	RunID         string    `json:"run_id"`
	SessionID     string    `json:"session_id"`
	Turn          int       `json:"turn"`
	Index         int       `json:"index"`
	Tool          string    `json:"tool"`
	ToolCallID    string    `json:"tool_call_id,omitempty"`
	Phase         string    `json:"phase"`
	Decision      Decision  `json:"decision"`
	ArgsHash      string    `json:"args_hash,omitempty"`
	ResultHash    string    `json:"result_hash,omitempty"`
	ParentIDs     []string  `json:"parent_ids,omitempty"`
}

type Policy struct {
	SchemaVersion int                     `json:"schema_version"`
	Default       ToolContract            `json:"default"`
	Contracts     map[string]ToolContract `json:"contracts,omitempty"`
}
