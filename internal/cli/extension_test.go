package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBrowserExtensionDirFromEnv_BitsUT(t *testing.T) {
	dir := makeTestBrowserExtensionDir(t)
	t.Setenv(envBrowserExtensionDir, dir)

	got, err := resolveBrowserExtensionDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("extension dir = %q, want %q", got, dir)
	}
}

func TestRunExtensionPathCommand_BitsUT(t *testing.T) {
	dir := makeTestBrowserExtensionDir(t)
	t.Setenv(envBrowserExtensionDir, dir)

	var out bytes.Buffer
	if err := runExtensionCommand([]string{"path"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != dir {
		t.Fatalf("output = %q, want %q", out.String(), dir)
	}
}

func makeTestBrowserExtensionDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), browserExtensionName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"manifest_version":3}`), 0644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
