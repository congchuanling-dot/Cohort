package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPathPrefersExplicitPath_BitsUT(t *testing.T) {
	cwd := chdirTemp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfigPath, filepath.Join(cwd, "env.yaml"))

	explicit := filepath.Join(cwd, "custom.yaml")
	if err := os.WriteFile(explicit, []byte("language: zh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "env.yaml"), []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Fatalf("config path = %q, want explicit %q", got, explicit)
	}
}

func TestResolveConfigPathUsesEnvBeforeProject_BitsUT(t *testing.T) {
	cwd := chdirTemp(t)
	t.Setenv("HOME", t.TempDir())
	envPath := filepath.Join(cwd, "env.yaml")
	t.Setenv(EnvConfigPath, envPath)
	if err := os.WriteFile(envPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "configs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ProjectConfigPath), []byte("language: zh\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != envPath {
		t.Fatalf("config path = %q, want env %q", got, envPath)
	}
}

func TestResolveConfigPathPrefersProjectBeforeUser_BitsUT(t *testing.T) {
	cwd := chdirTemp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfigPath, "")
	if err := os.MkdirAll(filepath.Join(cwd, "configs"), 0755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(cwd, ProjectConfigPath)
	if err := os.WriteFile(project, []byte("language: zh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(home, UserConfigRelativePath)
	if err := os.MkdirAll(filepath.Dir(user), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(ProjectConfigPath) {
		t.Fatalf("config path = %q, want project-relative %q", got, filepath.Clean(ProjectConfigPath))
	}
}

func TestResolveConfigPathUsesUserConfigWithoutProject_BitsUT(t *testing.T) {
	chdirTemp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfigPath, "")
	user := filepath.Join(home, UserConfigRelativePath)
	if err := os.MkdirAll(filepath.Dir(user), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != user {
		t.Fatalf("config path = %q, want user %q", got, user)
	}
}

func TestResolveConfigPathRejectsMissingExplicitPath_BitsUT(t *testing.T) {
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir())
	_, err := ResolveConfigPath(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("ResolveConfigPath error = nil, want missing explicit path error")
	}
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	return dir
}
