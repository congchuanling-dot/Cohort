package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimeScriptPathUsesExplicitHelperEnv_BitsUT(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "desktop_darwin.py")
	if err := os.WriteFile(helper, []byte("# helper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDesktopDarwinHelperPath, helper)

	got := ResolveRuntimeScriptPath(t.TempDir(), DesktopDarwinHelperPath)
	if got != helper {
		t.Fatalf("script path = %q, want %q", got, helper)
	}
}

func TestResolveRuntimeScriptPathUsesRuntimeScriptsDir_BitsUT(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "browser_ocr.py")
	if err := os.WriteFile(helper, []byte("# helper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvRuntimeScriptsDir, dir)

	got := ResolveRuntimeScriptPath(t.TempDir(), BrowserOCRHelperPath)
	if got != helper {
		t.Fatalf("script path = %q, want %q", got, helper)
	}
}
