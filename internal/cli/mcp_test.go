package cli

import (
	"context"
	"testing"

	"cohert/internal/mcp"
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
