package guardian

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFoldIsMonotonic(t *testing.T) {
	initial := InitialLabel()
	untrusted := Label{
		Confidentiality: ConfidentialityPublic,
		Integrity:       IntegrityUntrusted,
		Readers:         []string{"local"},
		Sources:         []string{"tool:browser_scan"},
	}
	secret := Label{
		Confidentiality: ConfidentialitySecret,
		Integrity:       IntegrityTrusted,
		Readers:         []string{"local"},
		Sources:         []string{"tool:file_read"},
	}
	folded := Fold(Fold(initial, untrusted), secret)
	if folded.Confidentiality != ConfidentialitySecret || folded.Integrity != IntegrityUntrusted {
		t.Fatalf("folded label = %#v", folded)
	}
	if len(folded.Readers) != 1 || folded.Readers[0] != "local" || len(folded.Sources) != 3 {
		t.Fatalf("folded sets = %#v", folded)
	}
}

func TestDecisionBlocksUntrustedExternalEffect(t *testing.T) {
	policy := DefaultPolicy()
	label := Fold(InitialLabel(), Label{
		Confidentiality: ConfidentialityPublic,
		Integrity:       IntegrityUntrusted,
		Readers:         []string{"local"},
		Sources:         []string{"tool:browser_scan"},
	})
	decision := Decide(policy, label, "mcp_lark_send_message")
	if decision.Action != ActionDeny || decision.RuleID != "G-NO-UNTRUSTED-EFFECT" {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.PolicyHash == "" || decision.ContractHash == "" {
		t.Fatalf("decision is missing proof hashes: %#v", decision)
	}
}

func TestDecisionConfinesUntrustedSource(t *testing.T) {
	decision := Decide(DefaultPolicy(), InitialLabel(), "browser_scan")
	if decision.Action != ActionForkIsolated || decision.RuleID != "G-SOURCE-UNTRUSTED" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestLoadPolicyMergesProjectOverrideDeterministically(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cohort", "guardian")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := `{
  "schema_version": 1,
  "contracts": {
    "mcp_lark_send_message": {
      "role": "sink",
      "effect": "external",
      "output_confidentiality": "internal",
      "output_integrity": "trusted",
      "readers": ["lark"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, PolicyFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	policy, hash, err := LoadPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	contract := Contract(policy, "mcp_lark_send_message")
	if contract.Effect != EffectExternal || len(contract.Readers) != 1 || contract.Readers[0] != "lark" {
		t.Fatalf("contract = %#v", contract)
	}
	if hash != HashPolicy(policy) || hash != HashPolicy(NormalizePolicy(policy)) {
		t.Fatalf("policy hash is unstable: %s", hash)
	}
}

func TestSecretContextRequiresDeclassification(t *testing.T) {
	policy := DefaultPolicy()
	policy.Contracts["send"] = ToolContract{
		Tool: "send", Role: ToolRoleSink, Effect: EffectExternal,
		OutputConfidentiality: ConfidentialityPublic, OutputIntegrity: IntegrityTrusted,
		Readers: []string{"external"},
	}
	decision := Decide(policy, Label{
		Confidentiality: ConfidentialitySecret,
		Integrity:       IntegrityTrusted,
		Readers:         []string{"local"},
	}, "send")
	if decision.Action != ActionRequireDeclassification || decision.RuleID != "G-NO-SECRET-EGRESS" {
		t.Fatalf("decision = %#v", decision)
	}
}
