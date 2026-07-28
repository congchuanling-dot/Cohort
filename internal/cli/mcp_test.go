package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/mcp"
	"cohort/internal/skill"
)

func TestPrintMCPStatusAllowsEmptyUserAssembly(t *testing.T) {
	if err := printMCPStatus(context.Background(), mcp.NewStore(t.TempDir())); err != nil {
		t.Fatal(err)
	}
}

func TestAddMCPServerAcceptsOptionsAfterName(t *testing.T) {
	store := mcp.NewStore(t.TempDir())
	if err := addMCPServer(store, []string{
		"lark",
		"-e", "LARK_APP_ID=${LARK_APP_ID}",
		"-e", "LARK_APP_SECRET=${LARK_APP_SECRET}",
		"--",
		"npx", "-y", "lark-mcp-server",
	}); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load(mcp.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["lark"]
	if server.Type != mcp.TransportStdio || server.Command != "npx" {
		t.Fatalf("server = %#v", server)
	}
	if len(server.Args) != 2 || server.Args[1] != "lark-mcp-server" {
		t.Fatalf("args = %#v", server.Args)
	}
	if server.Env["LARK_APP_ID"] != "${LARK_APP_ID}" {
		t.Fatalf("env = %#v", server.Env)
	}
}

func TestAddMCPServerWritesHTTPProjectConfig(t *testing.T) {
	store := mcp.NewStore(t.TempDir())
	if err := addMCPServer(store, []string{
		"--scope", "project",
		"--transport", "http",
		"docs", "https://example.com/mcp",
	}); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load(mcp.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["docs"]
	if server.Type != mcp.TransportHTTP || server.URL != "https://example.com/mcp" {
		t.Fatalf("server = %#v", server)
	}
}

func TestInstallSkillCommandWritesProjectSkill_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, skill.SkillFileName), []byte("# Installed Skill\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var dryRunOut strings.Builder
	if err := installSkillWithConfirmation(context.Background(), projectRoot, []string{"--dry-run", "--name", "cli-skill", source}, strings.NewReader(""), &dryRunOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"preview skill project/cli-skill", "security review:", "# Installed Skill"} {
		if !strings.Contains(dryRunOut.String(), want) {
			t.Fatalf("dry-run output does not contain %q:\n%s", want, dryRunOut.String())
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".cohort", "skills", "cli-skill")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote skill directory or stat failed differently: %v", err)
	}

	if err := installSkillWithConfirmation(context.Background(), projectRoot, []string{"--name", "cli-skill", source}, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".cohort", "skills", "cli-skill", skill.SkillFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestParseSkillUpdateArgsAcceptsCheckAndPin_BitsUT(t *testing.T) {
	opts, check, err := parseSkillUpdateArgs([]string{"--check", "--pin", "v1.2.3", "project/demo", "https://example.com/demo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if !check || opts.ID != "project/demo" || opts.Source != "https://example.com/demo.git" || opts.Pin != "v1.2.3" {
		t.Fatalf("opts = %#v, check = %t", opts, check)
	}
}
