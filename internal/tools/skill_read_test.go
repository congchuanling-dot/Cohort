package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/agent"
	"cohort/internal/skill"
)

func TestSkillReadReturnsDiscoveredSkill_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".cohort", "skills", "go-test", skill.SkillFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Go Test\n\nRun focused tests.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}

	tool := NewSkillRead(store)
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"skill_id": "go-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := outcome.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", outcome.Data)
	}
	if data["status"] != agent.ToolStatusSuccess || !strings.Contains(data["content"].(string), "Run focused tests.") {
		t.Fatalf("unexpected data: %#v", data)
	}
	if !strings.Contains(outcome.NextPrompt, "update_working_checkpoint") ||
		!strings.Contains(outcome.NextPrompt, "related_skill") {
		t.Fatalf("next prompt = %q", outcome.NextPrompt)
	}
}

func TestSkillReadReportsMissingSkill_BitsUT(t *testing.T) {
	store := skill.NewStore(t.TempDir(), t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	tool := NewSkillRead(store)
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"skill_id": "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr, ok := outcome.Data.(agent.ToolErrorData)
	if !ok {
		t.Fatalf("data type = %T, want ToolErrorData", outcome.Data)
	}
	if toolErr.Code != "skill_read_failed" {
		t.Fatalf("code = %q", toolErr.Code)
	}
}
