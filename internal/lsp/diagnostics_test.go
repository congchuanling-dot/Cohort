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

func TestDiagnosticsInstallMissingTypeScriptAndPython_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "version" ]; then echo "gopls test"; exit 0; fi
exit 2
`)
	writeExecutable(t, filepath.Join(dir, "npm"), `#!/bin/sh
pkg="$3"
script_dir="${0%/*}"
if [ "$pkg" = "typescript" ]; then
  /bin/cat > "$script_dir/tsc" <<'SCRIPT'
#!/bin/sh
echo "Version installed-typescript"
SCRIPT
  /bin/chmod +x "$script_dir/tsc"
  echo "installed typescript"
  exit 0
fi
if [ "$pkg" = "pyright" ]; then
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
