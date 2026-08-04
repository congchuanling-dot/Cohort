package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAdapterCreatesToolScaffold_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	gap, err := store.AddGap(NewGapFromTask("inspect custom archive format"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}

	item, artifacts, err := store.BuildAdapter(proposal.ID, TypeTool)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "inspect_custom_archive_format" || item.Type != TypeTool || item.Status != StatusCandidate {
		t.Fatalf("capability = %#v", item)
	}
	if len(artifacts) < 3 {
		t.Fatalf("artifacts = %#v, want scaffold files", artifacts)
	}
	manifestPath := filepath.Join(root, ".cohort", "adapters", item.ID, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"commands"`) || !strings.Contains(string(data), item.ID) {
		t.Fatalf("manifest = %s", string(data))
	}
}

func TestBuildAdapterCreatesMCPScaffold_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	gap, err := store.AddGap(NewGapFromTask("query internal design index"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}

	item, artifacts, err := store.BuildAdapter(proposal.ID, TypeMCP)
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != TypeMCP || !strings.HasSuffix(item.Entry, "/plugin.json") {
		t.Fatalf("capability = %#v", item)
	}
	foundMCP := false
	for _, artifact := range artifacts {
		if strings.HasSuffix(artifact, "mcp.json") {
			foundMCP = true
		}
	}
	if !foundMCP {
		t.Fatalf("artifacts = %#v, want mcp.json", artifacts)
	}
}
