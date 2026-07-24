package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/agent"
)

func TestFileReadAddsSOPCheckpointHint_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	sopsDir := filepath.Join(workspace, "sops")
	if err := os.MkdirAll(sopsDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sopsDir, "browser_sop.md")
	if err := os.WriteFile(path, []byte("# Browser SOP\n"), 0644); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewFileRead(workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"path": "sops/browser_sop.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outcome.NextPrompt, "update_working_checkpoint") {
		t.Fatalf("NextPrompt = %q, want checkpoint hint", outcome.NextPrompt)
	}
}

func TestFileReadFallsBackToProjectRootSOPs_BitsUT(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(root, "sops"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sops", "browser_sop.md"), []byte("# Browser SOP\n\nroot sop\n"), 0644); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewFileRead(workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"path": "sops/browser_sop.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(outcome.Data), "root sop") {
		t.Fatalf("file_read did not use project root SOP fallback: %#v", outcome.Data)
	}
	if !strings.Contains(outcome.NextPrompt, "update_working_checkpoint") {
		t.Fatalf("NextPrompt = %q, want checkpoint hint", outcome.NextPrompt)
	}
}
