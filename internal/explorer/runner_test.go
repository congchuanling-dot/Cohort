package explorer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

type explorerFakeTools struct {
	calls int
}

func (f *explorerFakeTools) Schemas() []llm.ToolSchema {
	names := []string{"file_read", "file_write", "code_run", "lsp_symbols"}
	schemas := make([]llm.ToolSchema, 0, len(names))
	for _, name := range names {
		schemas = append(schemas, llm.ToolSchema{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:       name,
				Parameters: map[string]any{"type": "object"},
			},
		})
	}
	return schemas
}

func (f *explorerFakeTools) Run(context.Context, agent.ToolCallContext) (agent.Outcome, error) {
	f.calls++
	return agent.Outcome{Data: map[string]any{"status": "success"}}, nil
}

func TestReadOnlyToolRunnerFiltersWritesAndShellComposition_BitsUT(t *testing.T) {
	base := &explorerFakeTools{}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n// TODO: verify\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := NewReadOnlyToolRunner(base, workspace)
	for _, schema := range runner.Schemas() {
		if schema.Function.Name == "file_write" || schema.Function.Name == "code_run" {
			t.Fatalf("unsafe tool leaked into explorer schemas: %#v", runner.Schemas())
		}
	}
	outcome, err := runner.Run(context.Background(), agent.ToolCallContext{
		Name: explorerSearchTool,
		Args: map[string]any{"pattern": "TODO", "path": "../"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := outcome.Data.(agent.ToolErrorData)
	if !ok || data.Code != "explorer_read_only_policy" || base.calls != 0 {
		t.Fatalf("outcome = %#v calls=%d", outcome, base.calls)
	}
	outcome, err = runner.Run(context.Background(), agent.ToolCallContext{
		Name: explorerSearchTool,
		Args: map[string]any{"pattern": "TODO", "glob": "*.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := outcome.Data.(map[string]any)
	if !ok || result["status"] != agent.ToolStatusSuccess || result["count"] != 1 {
		t.Fatalf("search outcome = %#v", outcome)
	}
}
