package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLSPCommandUsesGopls_BitsUT(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gopls")
	script := `#!/bin/sh
if [ "$1" = "version" ]; then
  echo "golang.org/x/tools/gopls v0.0.0-test"
  exit 0
fi
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

	var out bytes.Buffer
	if err := runLSPCommand(context.Background(), []string{"doctor"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go: ok") || !strings.Contains(out.String(), "v0.0.0-test") {
		t.Fatalf("doctor output = %q", out.String())
	}
	out.Reset()
	if err := runLSPCommand(context.Background(), []string{"diagnostics", "./..."}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "command: gopls check ") || !strings.Contains(out.String(), "checked ") {
		t.Fatalf("diagnostics output = %q", out.String())
	}
}

func TestRunLSPDoctorRejectsPositionalArgs_BitsUT(t *testing.T) {
	err := runLSPCommand(context.Background(), []string{"doctor", "unexpected"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "usage: cohort lsp doctor") {
		t.Fatalf("error = %v, want doctor usage", err)
	}
}

func TestRunLSPDoctorInstallMissing_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutableForCLI(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "version" ]; then echo "gopls test"; exit 0; fi
exit 2
`)
	writeExecutableForCLI(t, filepath.Join(dir, "npm"), `#!/bin/sh
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

	var out bytes.Buffer
	if err := runLSPCommand(context.Background(), []string{"doctor", "--language", "all", "--install"}, &out); err != nil {
		t.Fatalf("doctor install error = %v\n%s", err, out.String())
	}
	for _, want := range []string{"install:", "npm install -g typescript", "npm install -g pyright", "typescript: ok", "python: ok"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want %q", out.String(), want)
		}
	}
}

func TestRunPluginCommandListsManifest_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	pluginRoot := filepath.Join(root, ".cohort", "plugins", "local")
	if err := os.MkdirAll(pluginRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(`{"name":"local","version":"0.1.0"}`), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runPluginCommand([]string{"list"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "local") || !strings.Contains(out.String(), "0.1.0") {
		t.Fatalf("plugin list output = %q", out.String())
	}
}

func writeExecutableForCLI(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestRunExplorerCommandCreatesTask_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	var out bytes.Buffer
	if err := runExplorerCommand([]string{"create", "verify logs"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "read_only: true") || !strings.Contains(out.String(), "result:") {
		t.Fatalf("explorer output = %q", out.String())
	}
}

func TestRunTUICommandStatus_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	var out bytes.Buffer
	if err := runTUICommand([]string{"status"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cohort Status", "Plan", "Diff", "Logs"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("tui output = %q, want %q", out.String(), want)
		}
	}
}
