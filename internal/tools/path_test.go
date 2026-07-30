package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimeScriptPathUsesOCRHelperEnv_BitsUT(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, browserOCRHelperFileName)
	if err := os.WriteFile(helper, []byte("# helper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envBrowserOCRHelperPath, helper)

	got := resolveRuntimeScriptPath(t.TempDir(), browserOCRHelperFileName)
	if got != helper {
		t.Fatalf("script path = %q, want %q", got, helper)
	}
}

func TestResolveRuntimeScriptPathUsesRuntimeScriptsDir_BitsUT(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, browserOCRHelperFileName)
	if err := os.WriteFile(helper, []byte("# helper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envRuntimeScriptsDir, dir)

	got := resolveRuntimeScriptPath(t.TempDir(), browserOCRHelperFileName)
	if got != helper {
		t.Fatalf("script path = %q, want %q", got, helper)
	}
}
