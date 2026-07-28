package tools

import (
	"context"
	"path/filepath"
	"testing"

	"cohort/internal/agent"
	"cohort/internal/evolution"
)

func TestMemoryApplyUpdateReturnsReadBackConfirmation_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := evolution.NewManager(workspace)
	tool := NewMemoryApplyUpdate(workspace)
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		SessionID: "session-1",
		Args: map[string]any{
			"candidate": map[string]any{
				"type":              "project_lesson",
				"target":            manager.ProjectMemoryPath(),
				"scene":             "memory apply",
				"trigger_keywords":  []any{"memory", "apply"},
				"lesson":            "Memory apply returns read-back confirmation fields after append.",
				"recommended_steps": []any{"append entry", "read back file"},
				"evidence_ids":      []any{"tool:1:0"},
				"risk":              "low",
				"action":            "append",
			},
		},
		Evidence: []evolution.Evidence{{ID: "tool:1:0", Verified: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["applied"] != true {
		t.Fatalf("data = %#v", data)
	}
	if data["read_back_confirmed"] != true {
		t.Fatalf("read_back_confirmed = %#v", data["read_back_confirmed"])
	}
	if data["memory_root"] != filepath.Join(workspace, evolution.MemoryDirName) {
		t.Fatalf("memory_root = %#v", data["memory_root"])
	}
	if data["target_path"] != filepath.Join(workspace, filepath.FromSlash(manager.ProjectMemoryPath())) {
		t.Fatalf("target_path = %#v", data["target_path"])
	}
}
