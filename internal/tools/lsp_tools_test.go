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

func TestLSPDefinitionAndReferencesTools_BitsUT(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gopls")
	script := `#!/bin/sh
if [ "$1" = "definition" ]; then echo "foo.go:3:6 defined"; exit 0; fi
if [ "$1" = "references" ]; then echo "$2 $3"; exit 0; fi
exit 2
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	definition, err := NewLSPDefinition(t.TempDir()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"position": "foo.go:10:4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defData := definition.Data.(map[string]any)
	if defData["status"] != agent.ToolStatusSuccess || defData["kind"] != "definition" {
		t.Fatalf("definition outcome = %#v", defData)
	}
	references, err := NewLSPReferences(t.TempDir()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"position": "foo.go:10:4", "include_declaration": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	refData := references.Data.(map[string]any)
	if refData["status"] != agent.ToolStatusSuccess || !strings.Contains(strings.Join(refData["command"].([]string), " "), "references -d") {
		t.Fatalf("references outcome = %#v", refData)
	}
}

func TestRegistryIncludesLSPTools_BitsUT(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewLSPDiagnostics(t.TempDir()))
	registry.Register(NewLSPDefinition(t.TempDir()))
	registry.Register(NewLSPReferences(t.TempDir()))
	schemas := registry.Schemas()
	if len(schemas) != 3 ||
		schemas[0].Function.Name != ToolNameLSPDiagnostics ||
		schemas[1].Function.Name != ToolNameLSPDefinition ||
		schemas[2].Function.Name != ToolNameLSPReferences {
		t.Fatalf("schemas = %#v", schemas)
	}
}
