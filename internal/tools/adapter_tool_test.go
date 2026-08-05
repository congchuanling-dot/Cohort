package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/agent"
	"cohort/internal/plugin"
)

func TestCommandAdapterToolRunsCommandWithJSONStdin_BitsUT(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "adapter.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
input="$(cat)"
echo "adapter saw: $input"
`), 0755); err != nil {
		t.Fatal(err)
	}
	tool := NewCommandAdapterTool(plugin.Plugin{Root: root}, plugin.Command{
		Name:    "sample_adapter",
		Command: []string{"./adapter.sh"},
	})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"args": map[string]any{"value": "ok"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || !strings.Contains(data["output"].(string), `"value":"ok"`) {
		t.Fatalf("data = %#v", data)
	}
}
