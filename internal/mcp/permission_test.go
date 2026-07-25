package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultPermissionConfigTreatsUnknownToolsAsR2Ask(t *testing.T) {
	rule := DefaultPermissionConfig().Resolve("custom", "read_everything")
	if rule.Risk != RiskR2 || rule.Decision != PermissionAsk {
		t.Fatalf("unknown tool rule = %#v, want R2 + ask", rule)
	}
}

func TestDefaultPermissionConfigRefusesObviousIrreversibleTools(t *testing.T) {
	rule := DefaultPermissionConfig().Resolve("custom", "delete_everything")
	if rule.Risk != RiskR3 || rule.Decision != PermissionDeny {
		t.Fatalf("irreversible tool rule = %#v, want R3 + deny", rule)
	}
}

func TestPermissionConfigRequiresGrantForExactR2Allow(t *testing.T) {
	config := DefaultPermissionConfig()
	config.Rules["custom/send_message"] = ToolPermissionRule{
		Risk:       RiskR2,
		Decision:   PermissionAllow,
		ArgsPolicy: ArgsPolicyExact,
	}
	rule := config.Resolve("custom", "send_message")
	if rule.ArgsPolicy != ArgsPolicyExact || rule.Risk != RiskR2 {
		t.Fatalf("rule = %#v", rule)
	}
	argsHash := ArgsHash(map[string]any{"text": "hello"})
	if config.HasExactGrant("custom", "send_message", argsHash) {
		t.Fatal("empty config must not authorize exact R2 arguments")
	}
}

func TestPermissionStorePersistsExactProjectGrantWithoutServerDefinition(t *testing.T) {
	store := NewStore(t.TempDir())
	argsHash := ArgsHash(map[string]any{"recipient": "self", "text": "test"})
	config, err := store.AddExactProjectGrant("lark", "send_message", argsHash)
	if err != nil {
		t.Fatal(err)
	}
	if !config.HasExactGrant("lark", "send_message", argsHash) {
		t.Fatalf("config = %#v, expected exact grant", config)
	}
	otherArgsHash := ArgsHash(map[string]any{"recipient": "self", "text": "different"})
	if config.HasExactGrant("lark", "send_message", otherArgsHash) {
		t.Fatal("project grant must not authorize different arguments")
	}

	content, err := os.ReadFile(store.PermissionPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "mcpServers") || strings.Contains(string(content), `"command"`) {
		t.Fatalf("permission file must not define MCP servers: %s", content)
	}
	projectPath, err := store.Path(ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(projectPath); !os.IsNotExist(err) {
		t.Fatalf("project .mcp.json should not be created by permission grant, err=%v", err)
	}
}
