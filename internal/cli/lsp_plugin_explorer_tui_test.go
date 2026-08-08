package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/app"
	"cohort/internal/explorer"
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

func TestRunLSPCommandDefinitionAndReferences_BitsUT(t *testing.T) {
	dir := t.TempDir()
	writeExecutableForCLI(t, filepath.Join(dir, "gopls"), `#!/bin/sh
if [ "$1" = "definition" ]; then echo "foo.go:3:6 defined"; exit 0; fi
if [ "$1" = "references" ]; then echo "$2 $3"; exit 0; fi
exit 2
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var out bytes.Buffer
	if err := runLSPCommand(context.Background(), []string{"definition", "foo.go:10:4"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "kind: definition") || !strings.Contains(out.String(), "foo.go:3:6 defined") {
		t.Fatalf("definition output = %q", out.String())
	}
	out.Reset()
	if err := runLSPCommand(context.Background(), []string{"references", "--declaration", "foo.go:10:4"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "kind: references") || !strings.Contains(out.String(), "references -d foo.go:10:4") {
		t.Fatalf("references output = %q", out.String())
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
	if err := runExplorerCommand(context.Background(), app.Config{}, "", []string{"create", "verify logs"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "read_only: true") || !strings.Contains(out.String(), "result:") {
		t.Fatalf("explorer output = %q", out.String())
	}
}

func TestRunExplorerCommandKeepsChildEntryInternal_BitsUT(t *testing.T) {
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
	if err := runExplorerCommand(context.Background(), app.Config{}, "", []string{"create", "verify diff"}, &out); err != nil {
		t.Fatal(err)
	}
	id := outputValueFromText(t, out.String(), "explorer")
	out.Reset()
	err = runExplorerCommand(context.Background(), app.Config{}, "", []string{"run-child", id}, &out)
	if err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("run error = %v, want internal child guard", err)
	}
}

func TestExplorerChildArgs_BitsUT(t *testing.T) {
	args := explorerChildArgs("explorer_test", explorer.RunOptions{WithTests: true, Search: "TODO"})
	got := strings.Join(args, " ")
	want := "explorer run-child explorer_test --with-tests --search TODO"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
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

func TestRunTUICommandExplorers_BitsUT(t *testing.T) {
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
	if err := runExplorerCommand(context.Background(), app.Config{}, "", []string{"create", "verify explorer panel"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runTUICommand([]string{"explorers"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Explorers") || !strings.Contains(out.String(), "verify explorer panel") {
		t.Fatalf("tui explorers output = %q", out.String())
	}
}

func TestRunTUICommandWatchOnce_BitsUT(t *testing.T) {
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
	if err := runTUICommand([]string{"watch", "--interval", "1ms", "--iterations", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Cohort Live Panel") || !strings.Contains(out.String(), "Cohort Status") {
		t.Fatalf("watch output = %q", out.String())
	}
}

func outputValueFromText(t *testing.T, output string, key string) string {
	t.Helper()
	prefix := key + ": "
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("output %q missing key %q", output, key)
	return ""
}
