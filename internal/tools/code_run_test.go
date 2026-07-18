package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cohert/internal/agent"
)

func TestCodeRunDoesNotLoadBashStartupFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash startup file behavior is Unix-specific")
	}

	workspace := t.TempDir()
	home := t.TempDir()
	// 如果 code_run 误用 bash -lc，bash 会读取 .bash_profile，
	// 这里的噪音就会进入 stdout。使用 bash -c 时不会读取这个文件。
	profilePath := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(profilePath, []byte("echo shell_startup_noise\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	outcome, err := NewCodeRun(workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"script": "echo cohert_clean_shell",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	stdout := data["stdout"].(string)
	if strings.Contains(stdout, "shell_startup_noise") {
		t.Fatalf("stdout contains bash startup noise: %q", stdout)
	}
	if !strings.Contains(stdout, "cohert_clean_shell") {
		t.Fatalf("stdout = %q, want command output", stdout)
	}
}

func TestCodeRunTimeoutReturnsStructuredResult(t *testing.T) {
	workspace := t.TempDir()
	script := "sleep 2"
	if runtime.GOOS == "windows" {
		script = "Start-Sleep -Seconds 2"
	}

	outcome, err := NewCodeRun(workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"script":  script,
			"timeout": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusError {
		t.Fatalf("status = %v, want %s", data["status"], agent.ToolStatusError)
	}
	if data["timeout"] != true {
		t.Fatalf("timeout = %v, want true", data["timeout"])
	}
	if data["timeout_seconds"] != 1 {
		t.Fatalf("timeout_seconds = %v, want 1", data["timeout_seconds"])
	}
	if data["hint"] == "" {
		t.Fatal("timeout hint is empty")
	}
}

func TestNormalizeCodeRunTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero uses default", in: 0, want: defaultCodeRunTimeoutSeconds},
		{name: "negative uses default", in: -1, want: defaultCodeRunTimeoutSeconds},
		{name: "small value kept", in: 3, want: 3},
		{name: "large value capped", in: maxCodeRunTimeoutSeconds + 1, want: maxCodeRunTimeoutSeconds},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCodeRunTimeout(tt.in); got != tt.want {
				t.Fatalf("normalizeCodeRunTimeout(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
