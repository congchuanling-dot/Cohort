package guardian

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const PolicyFile = "policy.json"

func DefaultPolicy() Policy {
	policy := Policy{
		SchemaVersion: SchemaVersion,
		Default: ToolContract{
			Role:                  ToolRoleSink,
			Effect:                EffectUnknown,
			OutputConfidentiality: ConfidentialityInternal,
			OutputIntegrity:       IntegrityUntrusted,
		},
		Contracts: map[string]ToolContract{},
	}
	register := func(contract ToolContract, tools ...string) {
		for _, tool := range tools {
			contract.Tool = tool
			policy.Contracts[tool] = contract
		}
	}
	register(ToolContract{
		Role: ToolRoleSource, Effect: EffectNone,
		OutputConfidentiality: ConfidentialityInternal, OutputIntegrity: IntegrityTrusted,
		Readers: []string{"local"},
	}, "file_read", "skill_read", "memory_read", "lsp_diagnostics", "lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols")
	register(ToolContract{
		Role: ToolRoleSource, Effect: EffectNone,
		OutputConfidentiality: ConfidentialityPublic, OutputIntegrity: IntegrityUntrusted,
		Readers: []string{"local"},
	}, "browser_scan", "browser_dom_summary", "browser_ocr", "browser_snapshot", "desktop_ocr", "desktop_ax_snapshot", "computer_see", "computer_visual_snapshot")
	register(ToolContract{
		Role: ToolRoleSink, Effect: EffectLocal,
		OutputConfidentiality: ConfidentialityInternal, OutputIntegrity: IntegrityTrusted,
		AllowUntrustedContext: true, AllowSecretContext: true,
		Readers: []string{"local"},
	}, "file_write", "file_patch", "update_working_checkpoint", "memory_propose_update", "memory_apply_update")
	register(ToolContract{
		Role: ToolRolePure, Effect: EffectNone,
		OutputConfidentiality: ConfidentialityInternal, OutputIntegrity: IntegrityTrusted,
		AllowUntrustedContext: true, AllowSecretContext: true,
		Readers: []string{"local"},
	}, "ask_user", "computer_find", "computer_check", "browser_tabs", "desktop_windows", "desktop_permissions")
	return NormalizePolicy(policy)
}

// LoadPolicy loads a project override. Missing files use the conservative
// built-in policy; configured contracts replace defaults by exact tool name.
func LoadPolicy(projectRoot string) (Policy, string, error) {
	policy := DefaultPolicy()
	path := filepath.Join(filepath.Clean(projectRoot), ".cohort", "guardian", PolicyFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return policy, HashPolicy(policy), nil
	}
	if err != nil {
		return Policy{}, "", err
	}
	var configured Policy
	if err := json.Unmarshal(data, &configured); err != nil {
		return Policy{}, "", fmt.Errorf("parse guardian policy: %w", err)
	}
	if configured.SchemaVersion != 0 && configured.SchemaVersion != SchemaVersion {
		return Policy{}, "", fmt.Errorf("unsupported guardian policy schema %d", configured.SchemaVersion)
	}
	if configured.Default.Role != "" {
		policy.Default = configured.Default
	}
	for tool, contract := range configured.Contracts {
		contract.Tool = tool
		policy.Contracts[tool] = contract
	}
	policy = NormalizePolicy(policy)
	if err := ValidatePolicy(policy); err != nil {
		return Policy{}, "", err
	}
	return policy, HashPolicy(policy), nil
}

func NormalizePolicy(policy Policy) Policy {
	policy.SchemaVersion = SchemaVersion
	policy.Default = normalizeContract(policy.Default)
	if policy.Contracts == nil {
		policy.Contracts = map[string]ToolContract{}
	}
	normalized := make(map[string]ToolContract, len(policy.Contracts))
	for name, contract := range policy.Contracts {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		contract.Tool = name
		normalized[name] = normalizeContract(contract)
	}
	policy.Contracts = normalized
	return policy
}

func normalizeContract(contract ToolContract) ToolContract {
	contract.Tool = strings.TrimSpace(contract.Tool)
	if contract.Role == "" {
		contract.Role = ToolRoleSink
	}
	if contract.Effect == "" {
		contract.Effect = EffectUnknown
	}
	if contract.OutputConfidentiality == "" {
		contract.OutputConfidentiality = ConfidentialityInternal
	}
	if contract.OutputIntegrity == "" {
		contract.OutputIntegrity = IntegrityUntrusted
	}
	contract.Readers = normalizeSet(contract.Readers)
	return contract
}

func ValidatePolicy(policy Policy) error {
	if policy.SchemaVersion != SchemaVersion {
		return fmt.Errorf("guardian policy schema must be %d", SchemaVersion)
	}
	for name, contract := range policy.Contracts {
		if name == "" || contract.Tool != name {
			return errors.New("guardian contract name mismatch")
		}
		if !validRole(contract.Role) || !validEffect(contract.Effect) ||
			!validConfidentiality(contract.OutputConfidentiality) || !validIntegrity(contract.OutputIntegrity) {
			return fmt.Errorf("guardian contract %q contains an invalid enum", name)
		}
	}
	return nil
}

func HashPolicy(policy Policy) string {
	policy = NormalizePolicy(policy)
	data, _ := json.Marshal(policy)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func HashValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", value))
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func Contract(policy Policy, tool string) ToolContract {
	if contract, exists := policy.Contracts[strings.TrimSpace(tool)]; exists {
		return contract
	}
	contract := policy.Default
	contract.Tool = strings.TrimSpace(tool)
	return normalizeContract(contract)
}

func ContractHash(contract ToolContract) string {
	return HashValue(normalizeContract(contract))
}

func InitialLabel() Label {
	return Label{
		Confidentiality: ConfidentialityInternal,
		Integrity:       IntegrityUser,
		Readers:         []string{"local"},
		Sources:         []string{"user"},
	}
}

// Fold is monotonic: confidentiality can only increase, integrity can only
// decrease, and readers can only narrow.
func Fold(current, observed Label) Label {
	current = NormalizeLabel(current)
	observed = NormalizeLabel(observed)
	return Label{
		Confidentiality: maxConfidentiality(current.Confidentiality, observed.Confidentiality),
		Integrity:       minIntegrity(current.Integrity, observed.Integrity),
		Readers:         intersectReaders(current.Readers, observed.Readers),
		Sources:         union(current.Sources, observed.Sources),
	}
}

func NormalizeLabel(label Label) Label {
	if !validConfidentiality(label.Confidentiality) {
		label.Confidentiality = ConfidentialityPublic
	}
	if !validIntegrity(label.Integrity) {
		label.Integrity = IntegrityUntrusted
	}
	label.Readers = normalizeSet(label.Readers)
	label.Sources = normalizeSet(label.Sources)
	return label
}

func OutputLabel(contract ToolContract) Label {
	contract = normalizeContract(contract)
	return Label{
		Confidentiality: contract.OutputConfidentiality,
		Integrity:       contract.OutputIntegrity,
		Readers:         contract.Readers,
		Sources:         []string{"tool:" + contract.Tool},
	}
}

func Decide(policy Policy, label Label, tool string) Decision {
	policy = NormalizePolicy(policy)
	label = NormalizeLabel(label)
	contract := Contract(policy, tool)
	decision := Decision{
		Action:       ActionAllow,
		RuleID:       "G-ALLOW-DEFAULT",
		Reason:       "tool contract permits this information flow",
		Tool:         tool,
		InputLabel:   label,
		OutputLabel:  Fold(label, OutputLabel(contract)),
		ContractHash: ContractHash(contract),
		PolicyHash:   HashPolicy(policy),
	}
	if contract.Role == ToolRoleSource && contract.OutputIntegrity == IntegrityUntrusted {
		decision.Action = ActionForkIsolated
		decision.RuleID = "G-SOURCE-UNTRUSTED"
		decision.Reason = "untrusted source must be inspected in a confined trajectory"
		return decision
	}
	if contract.Effect == EffectExternal || contract.Effect == EffectUnknown {
		if label.Confidentiality == ConfidentialitySecret && !contract.AllowSecretContext {
			decision.Action = ActionRequireDeclassification
			decision.RuleID = "G-NO-SECRET-EGRESS"
			decision.Reason = "secret-tainted context cannot flow to an external or unknown sink"
			return decision
		}
		if label.Integrity == IntegrityUntrusted && !contract.AllowUntrustedContext {
			decision.Action = ActionDeny
			decision.RuleID = "G-NO-UNTRUSTED-EFFECT"
			decision.Reason = "untrusted observations cannot authorize external or unknown side effects"
			return decision
		}
		decision.Action = ActionAsk
		decision.RuleID = "G-EXTERNAL-EFFECT"
		decision.Reason = "external or unknown side effect requires an authority decision"
	}
	return decision
}

func normalizeSet(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func union(left, right []string) []string {
	return normalizeSet(append(append([]string(nil), left...), right...))
}

func intersectReaders(left, right []string) []string {
	left = normalizeSet(left)
	right = normalizeSet(right)
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(left))
	for _, value := range left {
		allowed[value] = true
	}
	var result []string
	for _, value := range right {
		if allowed[value] {
			result = append(result, value)
		}
	}
	return normalizeSet(result)
}

func maxConfidentiality(left, right Confidentiality) Confidentiality {
	if confidentialityRank(right) > confidentialityRank(left) {
		return right
	}
	return left
}

func minIntegrity(left, right Integrity) Integrity {
	if integrityRank(right) < integrityRank(left) {
		return right
	}
	return left
}

func confidentialityRank(value Confidentiality) int {
	switch value {
	case ConfidentialitySecret:
		return 2
	case ConfidentialityInternal:
		return 1
	default:
		return 0
	}
}

func integrityRank(value Integrity) int {
	switch value {
	case IntegrityTrusted:
		return 2
	case IntegrityUser:
		return 1
	default:
		return 0
	}
}

func validConfidentiality(value Confidentiality) bool {
	return value == ConfidentialityPublic || value == ConfidentialityInternal || value == ConfidentialitySecret
}

func validIntegrity(value Integrity) bool {
	return value == IntegrityUntrusted || value == IntegrityUser || value == IntegrityTrusted
}

func validRole(value ToolRole) bool {
	return value == ToolRolePure || value == ToolRoleSource || value == ToolRoleSink || value == ToolRoleTransform
}

func validEffect(value Effect) bool {
	return value == EffectNone || value == EffectLocal || value == EffectExternal || value == EffectUnknown
}
