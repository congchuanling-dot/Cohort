package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsDoctorAll_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "version" ]; then echo "gopls test"; exit 0; fi
exit 2
`)
	writeExecutable(t, filepath.Join(dir, "tsc"), `#!/bin/sh
if [ "$1" = "--version" ]; then echo "Version 5.0.0-test"; exit 0; fi
exit 2
`)
	writeExecutable(t, filepath.Join(dir, "pyright"), `#!/bin/sh
if [ "$1" = "--version" ]; then echo "pyright 1.0.0-test"; exit 0; fi
exit 2
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	results := (Diagnostics{Root: t.TempDir()}).Doctor(context.Background(), LanguageAll)
	if len(results) != 3 {
		t.Fatalf("results = %#v", results)
	}
	for _, result := range results {
		if !result.OK || result.Version == "" {
			t.Fatalf("doctor result = %#v", result)
		}
	}
}

func TestDiagnosticsCheckTypeScript_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "tsc"), `#!/bin/sh
echo "tsc checked $@"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := (Diagnostics{Root: root}).Check(context.Background(), LanguageTypeScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != LanguageTypeScript || !strings.Contains(result.Output, "--project tsconfig.json") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiagnosticsCheckPython_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "pyright"), `#!/bin/sh
echo "pyright checked $@"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := (Diagnostics{Root: t.TempDir()}).Check(context.Background(), LanguagePython, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != LanguagePython || !strings.Contains(result.Output, "pkg") {
		t.Fatalf("result = %#v", result)
	}
}

func TestGoplsDefinitionAndReferences_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "definition" ]; then
  echo "foo.go:3:6-9: defined symbol"
  exit 0
fi
if [ "$1" = "references" ]; then
  echo "$2 $3"
  exit 0
fi
exit 2
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	client := Gopls{Root: t.TempDir()}

	definition, err := client.Definition(context.Background(), "foo.go:10:4")
	if err != nil {
		t.Fatal(err)
	}
	if definition.Kind != "definition" || !strings.Contains(definition.Output, "defined symbol") {
		t.Fatalf("definition = %#v", definition)
	}
	references, err := client.References(context.Background(), "foo.go:10:4", true)
	if err != nil {
		t.Fatal(err)
	}
	if references.Kind != "references" || !strings.Contains(strings.Join(references.Command, " "), "references -d foo.go:10:4") {
		t.Fatalf("references = %#v", references)
	}
}

func TestDiagnosticsQueryTypeScriptSymbolScan_BitsUT(t *testing.T) {
	root := t.TempDir()
	source := `export function greet(name: string) {
  return name
}

const message = greet("cohort")
`
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := (Diagnostics{Root: root, TypeScriptServerCommand: filepath.Join(root, "missing-typescript-language-server")}).Query(context.Background(), QueryOptions{
		Language:           LanguageTypeScript,
		Kind:               QueryReferences,
		Position:           "main.ts:5:17",
		IncludeDeclaration: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Engine != "symbol_scan" || !strings.Contains(result.Output, "function greet") || !strings.Contains(result.Output, "message = greet") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiagnosticsQueryPythonSymbols_BitsUT(t *testing.T) {
	root := t.TempDir()
	source := `class Greeter:
    pass

def greet():
    return "hi"
`
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := (Diagnostics{Root: root, PythonServerCommand: filepath.Join(root, "missing-pyright-langserver")}).Query(context.Background(), QueryOptions{
		Language: LanguagePython,
		Kind:     QuerySymbols,
		Target:   ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Engine != "symbol_scan" || !strings.Contains(result.Output, "class Greeter") || !strings.Contains(result.Output, "def greet") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiagnosticsInstallMissingTypeScriptAndPython_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "version" ]; then echo "gopls test"; exit 0; fi
exit 2
`)
	writeExecutable(t, filepath.Join(dir, "npm"), `#!/bin/sh
args="$*"
script_dir="${0%/*}"
if echo "$args" | /usr/bin/grep -q "typescript"; then
  /bin/cat > "$script_dir/tsc" <<'SCRIPT'
#!/bin/sh
echo "Version installed-typescript"
SCRIPT
  /bin/chmod +x "$script_dir/tsc"
  echo "installed typescript"
  exit 0
fi
if echo "$args" | /usr/bin/grep -q "pyright"; then
  /bin/cat > "$script_dir/pyright" <<'SCRIPT'
#!/bin/sh
echo "pyright installed-pyright"
SCRIPT
  /bin/chmod +x "$script_dir/pyright"
  echo "installed pyright"
  exit 0
fi
exit 3
`)
	t.Setenv("PATH", dir)
	client := Diagnostics{Root: t.TempDir()}

	before := client.Doctor(context.Background(), LanguageAll)
	missing := 0
	for _, item := range before {
		if !item.OK {
			missing++
		}
	}
	if missing != 2 {
		t.Fatalf("before = %#v, want tsc and pyright missing", before)
	}
	installed := client.InstallMissing(context.Background(), LanguageAll)
	if len(installed) != 2 {
		t.Fatalf("installed = %#v", installed)
	}
	for _, item := range installed {
		if !item.OK {
			t.Fatalf("install result = %#v", item)
		}
	}
	after := client.Doctor(context.Background(), LanguageAll)
	for _, item := range after {
		if !item.OK {
			t.Fatalf("after = %#v", after)
		}
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}
