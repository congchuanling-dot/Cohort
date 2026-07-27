package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDefaultConfigCreatesClaudeProfile_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteDefaultConfig(InitConfigOptions{Path: path, ActiveProfile: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"active_profile: claude",
		"provider: anthropic",
		"api_key: ${ANTHROPIC_API_KEY}",
		`workspace: "`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config content does not contain %q:\n%s", want, content)
		}
	}

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Active().Provider != "anthropic" || cfg.LLM.Active().ID != "claude" {
		t.Fatalf("active profile = %#v, want claude anthropic", cfg.LLM.Active())
	}
}

func TestWriteDefaultConfigRefusesOverwriteWithoutForce_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := WriteDefaultConfig(InitConfigOptions{Path: path})
	if err == nil {
		t.Fatal("WriteDefaultConfig error = nil, want overwrite refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want already exists", err)
	}
}
