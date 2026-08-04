package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreImportExportAndSSECompatibility_BitsUT(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(source, []byte(`{
  "mcpServers": {
    "legacy": {
      "type": "sse",
      "url": "https://example.com/mcp"
    }
  }
}`), 0644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	count, err := store.Import(ScopeProject, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	servers, err := store.LoadEffective()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Type != TransportHTTP {
		t.Fatalf("servers = %#v, want sse normalized to http", servers)
	}
	target := filepath.Join(t.TempDir(), "export.json")
	if err := store.Export(ScopeProject, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"legacy"`) {
		t.Fatalf("export missing server:\n%s", data)
	}
}
