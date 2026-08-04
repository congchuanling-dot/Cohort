package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/skill"
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

func TestStoreBuildVerifyPromoteDisable_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	store := NewStore(projectRoot)
	gap, err := store.AddGap(NewGapFromTask("local csv analysis"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}

	item, err := store.Build(proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "local_csv_analysis" || item.Status != StatusCandidate || item.Type != TypeSkill {
		t.Fatalf("built capability = %#v", item)
	}
	skillPath := filepath.Join(projectRoot, ".cohort", "skills", item.ID, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatal(err)
	}
	skillStore := skill.NewStore(projectRoot, t.TempDir())
	if err := skillStore.Reload(); err != nil {
		t.Fatal(err)
	}
	found, err := skillStore.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != "project/local_csv_analysis" {
		t.Fatalf("skill id = %q, want project/local_csv_analysis", found.ID)
	}

	verified, output, err := store.Verify(item.ID)
	if err != nil {
		t.Fatalf("verify error = %v, output = %q", err, output)
	}
	if !strings.Contains(output, "capability smoke passed") || verified.Verification.LastPassedAt.IsZero() {
		t.Fatalf("verified = %#v, output = %q", verified, output)
	}
	if _, err := store.Promote(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Disable(item.ID); err != nil {
		t.Fatal(err)
	}

	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Capabilities) != 1 || registry.Capabilities[0].Status != StatusDisabled {
		t.Fatalf("capabilities = %#v, want disabled capability", registry.Capabilities)
	}
	if registry.Gaps[0].Status != StatusDisabled || registry.Proposals[0].Status != StatusDisabled {
		t.Fatalf("linked statuses gap/proposal = %s/%s, want disabled/disabled", registry.Gaps[0].Status, registry.Proposals[0].Status)
	}
}

func TestStorePromoteRequiresSuccessfulVerification_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	gap, err := store.AddGap(NewGapFromTask("local csv analysis"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Build(proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Promote(item.ID); err == nil || !strings.Contains(err.Error(), "no successful verification") {
		t.Fatalf("promote error = %v, want verification guard", err)
	}
}
