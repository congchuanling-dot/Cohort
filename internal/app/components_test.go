package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/evaluation"
	"cohort/internal/project"
)

func TestBuildComponentInventorySummarizesCoreSubsystems_BitsUT(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if _, err := project.NewStore(root).Init("Component Test", false); err != nil {
		t.Fatal(err)
	}
	evalStore := evaluation.NewStore(root)
	if err := os.MkdirAll(evalStore.SuitesDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evalStore.SuitesDir(), "core.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	inventory := BuildComponentInventory(Config{
		Language:  "zh",
		Workspace: workspace,
		Tools: ToolConfig{
			EnabledGroups: []string{"core", "lsp", "skill"},
		},
	}, root, nil)
	byID := map[string]ComponentStatus{}
	for _, component := range inventory.Components {
		byID[component.ID] = component
	}
	for _, id := range []string{"tools.core", "tools.lsp", "tools.browser", "project.mode", "skill.index", "eval.suites", "hermes.daemon"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("component %s missing from inventory: %#v", id, inventory.Components)
		}
	}
	if byID["tools.core"].Status != "enabled" || byID["tools.browser"].Status != "disabled" {
		t.Fatalf("tool group statuses = core:%s browser:%s", byID["tools.core"].Status, byID["tools.browser"].Status)
	}
	if byID["project.mode"].Status != "ready" {
		t.Fatalf("project mode status = %s", byID["project.mode"].Status)
	}
	if byID["eval.suites"].Status != "ready" || !strings.Contains(byID["eval.suites"].Detail, "1 suites") {
		t.Fatalf("eval status = %#v", byID["eval.suites"])
	}
	if _, err := os.Stat(filepath.Join(root, ".cohort", "hermes", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("component inventory should not create Hermes config, stat err=%v", err)
	}
}

func TestBuildSystemPromptInjectsComponentMap_BitsUT(t *testing.T) {
	root := t.TempDir()
	prompt := BuildSystemPromptForProject(Config{
		Language: "zh",
		Tools: ToolConfig{
			EnabledGroups: []string{"core", "skill"},
		},
	}, nil, root)
	for _, want := range []string{"[Component Map]", "tools.core", "cohort components", "disabled/missing"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
