package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/app"
)

func TestParseGlobalOptionsConsumesConfigPath_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"--config", "/tmp/cohert.yaml", "config"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "/tmp/cohert.yaml" {
		t.Fatalf("config path = %q, want /tmp/cohert.yaml", opts.ConfigPath)
	}
	if len(args) != 1 || args[0] != "config" {
		t.Fatalf("args = %#v, want [config]", args)
	}
}

func TestParseGlobalOptionsConsumesConfigEquals_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"--config=/tmp/cohert.yaml", "tools"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "/tmp/cohert.yaml" {
		t.Fatalf("config path = %q, want /tmp/cohert.yaml", opts.ConfigPath)
	}
	if len(args) != 1 || args[0] != "tools" {
		t.Fatalf("args = %#v, want [tools]", args)
	}
}

func TestParseGlobalOptionsStopsAtCommand_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"ask", "--config", "not-global", "task"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "" {
		t.Fatalf("config path = %q, want empty", opts.ConfigPath)
	}
	if len(args) != 4 || args[0] != "ask" || args[1] != "--config" {
		t.Fatalf("args = %#v, want original command args", args)
	}
}

func TestRunInitCommandWritesUserConfig_BitsUT(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(app.EnvConfigPath, "")

	var out bytes.Buffer
	if err := runInitCommand(globalOptions{}, []string{"--provider", "local"}, &out); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, app.UserConfigRelativePath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "active_profile: local") {
		t.Fatalf("config content does not set local active profile:\n%s", string(data))
	}
	if !strings.Contains(out.String(), "initialized config:") {
		t.Fatalf("output = %q, want initialized config", out.String())
	}
}

func TestRunInitCommandRefusesExistingConfig_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := runInitCommand(globalOptions{ConfigPath: path}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runInitCommand error = nil, want existing config error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want already exists", err)
	}
}

func TestRunDoctorCommandFailsMissingAPIKey_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := app.WriteDefaultConfig(app.InitConfigOptions{Path: path, ActiveProfile: "deepseek"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "")
	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runDoctorCommand(context.Background(), nil, path, cfg, nil, &out)
	if err == nil {
		t.Fatal("runDoctorCommand error = nil, want missing API key failure")
	}
	if !strings.Contains(out.String(), "[fail] llm.api_key") {
		t.Fatalf("output = %s, want missing api key failure", out.String())
	}
}

func TestRunDoctorCommandPassesLocalChecks_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("LOCAL_OPENAI_API_KEY", "local-key")
	if err := app.WriteDefaultConfig(app.InitConfigOptions{Path: path, ActiveProfile: "local"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDoctorCommand(context.Background(), nil, path, cfg, nil, &out); err != nil {
		t.Fatalf("runDoctorCommand error = %v\n%s", err, out.String())
	}
	for _, want := range []string{"[pass] config.file", "[pass] llm.api_key", "[pass] workspace", "doctor: ok"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}
