package project

import (
	"os"
	"strings"
	"testing"
)

func TestProjectInitWritesExplicitPointers_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	status, err := store.Init("Demo", false)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Exists {
		t.Fatalf("project should exist: %#v", status)
	}
	data, err := os.ReadFile(status.ProjectPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"# Demo", "project_memory_pointer", "plan_state_pointer", ".cohort/plan.json"} {
		if !strings.Contains(content, want) {
			t.Fatalf("project.md missing %q:\n%s", want, content)
		}
	}
	if prompt := store.Prompt(); !strings.Contains(prompt, "[Project Mode]") || !strings.Contains(prompt, "# Demo") {
		t.Fatalf("prompt = %q", prompt)
	}
}
