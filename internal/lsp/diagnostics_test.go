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

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}
