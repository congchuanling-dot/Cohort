package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreAddGapAndProposal_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())

	gap, err := store.AddGap(NewGapFromTask("处理一种新的文件格式"))
	if err != nil {
		t.Fatal(err)
	}
	if gap.ID == "" || gap.Status != StatusMissing {
		t.Fatalf("gap = %#v, want id and missing status", gap)
	}
	if gap.MissingCapability == "" {
		t.Fatalf("gap missing capability is empty: %#v", gap)
	}

	proposal, err := store.AddProposal(NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ID == "" || proposal.GapID != gap.ID || proposal.Status != StatusProposed {
		t.Fatalf("proposal = %#v, want linked proposed item", proposal)
	}
	if _, err := os.Stat(filepath.Join(store.RootDir, RegistryFile)); err != nil {
		t.Fatal(err)
	}

	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Gaps) != 1 || len(registry.Proposals) != 1 {
		t.Fatalf("registry gaps/proposals = %d/%d, want 1/1", len(registry.Gaps), len(registry.Proposals))
	}
}

func TestStoreUniqueGapIDs_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	first, err := store.AddGap(NewGapFromTask("same task"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddGap(NewGapFromTask("same task"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("duplicate ids: %q", first.ID)
	}
	if !strings.HasPrefix(second.ID, first.ID+"_") {
		t.Fatalf("second id = %q, want suffix of %q", second.ID, first.ID)
	}
}
