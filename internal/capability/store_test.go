package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestStoreIndexPromptOnlyIncludesAvailableCapabilities_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	now := testTime()
	if err := store.Save(Registry{
		Capabilities: []Capability{
			{
				ID:       "local_csv_analysis",
				Status:   StatusAvailable,
				Type:     TypeSkill,
				Entry:    filepath.Join(ProjectDirName, "skills", "local_csv_analysis", "SKILL.md"),
				Triggers: []string{"分析本地 CSV", "local csv analysis"},
				Risk:     "R1: read local files",
				Verification: Verification{
					LastPassedAt: now,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "candidate_pdf",
				Status:    StatusCandidate,
				Type:      TypeSkill,
				Entry:     filepath.Join(ProjectDirName, "skills", "candidate_pdf", "SKILL.md"),
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "disabled_video",
				Status:    StatusDisabled,
				Type:      TypeSkill,
				Entry:     filepath.Join(ProjectDirName, "skills", "disabled_video", "SKILL.md"),
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	prompt := store.IndexPrompt()
	for _, want := range []string{"[Capability Index]", "local_csv_analysis", "project/local_csv_analysis", "分析本地 CSV", "skill_read"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"candidate_pdf", "disabled_video"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt leaked %q:\n%s", forbidden, prompt)
		}
	}
}

func TestStoreSuggestionsAggregateRepeatedUnresolvedGaps_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, task := range []string{"parse local foo file", "parse another foo file"} {
		gap := NewGapFromTask(task)
		gap.MissingCapability = "local_foo_parser"
		gap.Source = "runner:no_tool"
		if _, err := store.AddGap(gap); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AddGap(NewGapFromTask("single unsupported task")); err != nil {
		t.Fatal(err)
	}

	suggestions, err := store.Suggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want 1 repeated gap suggestion", suggestions)
	}
	item := suggestions[0]
	if item.MissingCapability != "local_foo_parser" || item.Count != 2 {
		t.Fatalf("suggestion = %#v, want local_foo_parser count 2", item)
	}
	if !strings.Contains(item.NextCommand, "cohort capability propose") || !strings.Contains(strings.Join(item.Sources, ","), "runner:no_tool") {
		t.Fatalf("suggestion command/sources = %#v", item)
	}
}

func TestStoreSuggestionsSkipActiveProposalOrCapability_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	first := NewGapFromTask("parse local foo file")
	first.MissingCapability = "local_foo_parser"
	recorded, err := store.AddGap(first)
	if err != nil {
		t.Fatal(err)
	}
	second := NewGapFromTask("parse another foo file")
	second.MissingCapability = "local_foo_parser"
	if _, err := store.AddGap(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProposal(NewProposalFromGap(recorded)); err != nil {
		t.Fatal(err)
	}
	if suggestions, err := store.Suggestions(); err != nil {
		t.Fatal(err)
	} else if len(suggestions) != 0 {
		t.Fatalf("suggestions = %#v, want none with active proposal", suggestions)
	}

	store = NewStore(t.TempDir())
	for _, task := range []string{"parse local bar file", "parse another bar file"} {
		gap := NewGapFromTask(task)
		gap.MissingCapability = "local_bar_parser"
		if _, err := store.AddGap(gap); err != nil {
			t.Fatal(err)
		}
	}
	now := testTime()
	if err := store.Save(Registry{
		Gaps: mustLoadRegistry(t, store).Gaps,
		Capabilities: []Capability{
			{
				ID:        "local_bar_parser",
				Status:    StatusAvailable,
				Type:      TypeSkill,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if suggestions, err := store.Suggestions(); err != nil {
		t.Fatal(err)
	} else if len(suggestions) != 0 {
		t.Fatalf("suggestions = %#v, want none with registered capability", suggestions)
	}
}

func testTime() time.Time {
	return time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
}

func mustLoadRegistry(t *testing.T, store Store) Registry {
	t.Helper()
	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
