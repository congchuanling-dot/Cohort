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

func TestVerifyAndPromoteToolAdapter_BitsUT(t *testing.T) {
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
	item, _, err := store.BuildAdapter(proposal.ID, TypeTool)
	if err != nil {
		t.Fatal(err)
	}

	verified, output, err := store.Verify(item.ID)
	if err != nil {
		t.Fatalf("verify error = %v\n%s", err, output)
	}
	if verified.Verification.LastPassedAt.IsZero() || !strings.Contains(output, "tool.go_run") {
		t.Fatalf("verified = %#v output=%q", verified, output)
	}
	promoted, err := store.Promote(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != StatusAvailable {
		t.Fatalf("promoted = %#v", promoted)
	}
}

func TestEnablePromotedToolAdapterWritesExplicitAllowlist_BitsUT(t *testing.T) {
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
	item, _, err := store.BuildAdapter(proposal.ID, TypeTool)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Verify(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Promote(item.ID); err != nil {
		t.Fatal(err)
	}

	result, err := store.EnableAdapter(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Enabled || result.StatePath == "" {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(result.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), item.ID) || !strings.Contains(string(data), TypeTool) {
		t.Fatalf("enabled adapters = %s", string(data))
	}
}

func TestVerifyMCPAdapter_BitsUT(t *testing.T) {
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
	item, _, err := store.BuildAdapter(proposal.ID, TypeMCP)
	if err != nil {
		t.Fatal(err)
	}

	verified, output, err := store.Verify(item.ID)
	if err != nil {
		t.Fatalf("verify error = %v\n%s", err, output)
	}
	if verified.Verification.LastPassedAt.IsZero() || !strings.Contains(output, "mcp.config") {
		t.Fatalf("verified = %#v output=%q", verified, output)
	}
}
