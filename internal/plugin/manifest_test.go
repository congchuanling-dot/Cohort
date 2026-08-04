package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAndDoctorPluginManifest_BitsUT(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, ".cohort", "plugins", "local")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills", "demo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "demo", "SKILL.md"), []byte("# Demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "local",
  "version": "0.1.0",
  "skills": ["skills/demo/SKILL.md"],
  "commands": [{"name": "smoke", "command": ["go", "test", "./..."]}],
  "mcp": {"config": "mcp.json"},
  "dependencies": {"commands": ["go"]}
}`
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "mcp.json"), []byte(`{"mcpServers":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	plugins, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Manifest.Name != "local" {
		t.Fatalf("plugins = %#v", plugins)
	}
	result := Doctor(plugins[0])
	for _, check := range result.Checks {
		if check.Status == "error" {
			t.Fatalf("unexpected error check: %#v", check)
		}
	}
}

func TestLoadSimpleYAMLManifest_BitsUT(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.yaml")
	content := `name: yaml-plugin
version: 0.1.0
skills:
  - skills/demo/SKILL.md
permissions:
  allow_tools:
    - lsp_diagnostics
dependencies:
  commands:
    - go
  env:
    - HOME
mcp:
  config: mcp.json
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	item, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if item.Manifest.Name != "yaml-plugin" ||
		len(item.Manifest.Skills) != 1 ||
		len(item.Manifest.Permissions.AllowTools) != 1 ||
		len(item.Manifest.Dependencies.Commands) != 1 ||
		item.Manifest.MCP.Config != "mcp.json" {
		t.Fatalf("manifest = %#v", item.Manifest)
	}
}
