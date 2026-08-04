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
	if !strings.Contains(out.String(), "gopls: ok") || !strings.Contains(out.String(), "v0.0.0-test") {
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
