package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/agent"
)

func TestLSPDiagnosticsRunsGoplsCheck_BitsUT(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gopls")
	script := `#!/bin/sh
if [ "$1" = "check" ]; then
  echo "checked $2"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	outcome, err := NewLSPDiagnostics(t.TempDir()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"targets": []any{"main.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["exit_code"] != 0 || !strings.Contains(data["output"].(string), "checked main.go") {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestLSPDiagnosticsRunsTypeScriptCheck_BitsUT(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tsc")
	script := `#!/bin/sh
echo "tsc checked $@"
exit 0
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	outcome, err := NewLSPDiagnostics(t.TempDir()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"language": "typescript", "targets": []any{"index.ts"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess ||
		data["language"] != "typescript" ||
		!strings.Contains(data["output"].(string), "index.ts") {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestRegistryIncludesLSPDiagnostics_BitsUT(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewLSPDiagnostics(t.TempDir()))
	schemas := registry.Schemas()
	if len(schemas) != 1 || schemas[0].Function.Name != ToolNameLSPDiagnostics {
		t.Fatalf("schemas = %#v", schemas)
	}
}
