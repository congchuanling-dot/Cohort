package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"cohort/internal/capability"
)

func TestRunCapabilityProposeCommand_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"propose", "处理一种新的文件格式"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"gap:", "proposal:", "missing_capability:", "registry:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"gaps"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "MISSING_CAPABILITY") || !strings.Contains(out.String(), "处理一种新的文件格式") {
		t.Fatalf("gaps output = %q, want recorded task", out.String())
	}
}

func TestRunCapabilityLifecycleCommand_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"propose", "local csv analysis"}, &out); err != nil {
		t.Fatal(err)
	}
	proposalID := outputValue(t, out.String(), "proposal")

	out.Reset()
	if err := runCapabilityCommand([]string{"build", proposalID}, &out); err != nil {
		t.Fatal(err)
	}
	capabilityID := outputValue(t, out.String(), "capability")
	if capabilityID != "local_csv_analysis" || !strings.Contains(out.String(), ".cohort/skills/local_csv_analysis/SKILL.md") {
		t.Fatalf("build output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"doctor", capabilityID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ready_to_verify: true") || !strings.Contains(out.String(), "smoke_test") {
		t.Fatalf("doctor output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"verify", capabilityID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "capability smoke passed") || !strings.Contains(out.String(), "promote: cohort capability promote local_csv_analysis") {
		t.Fatalf("verify output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"promote", capabilityID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "status: available") {
		t.Fatalf("promote output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"disable", capabilityID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "status: disabled") {
		t.Fatalf("disable output = %q", out.String())
	}
}

func TestRunCapabilityAdapterCommand_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	store := capability.NewStore(dir)
	gap, err := store.AddGap(capability.NewGapFromTask("query incident timeline"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(capability.NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"adapter", proposal.ID, "--type", "mcp"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "type: mcp") || !strings.Contains(out.String(), ".cohort/adapters/") || !strings.Contains(out.String(), "mcp.json") {
		t.Fatalf("adapter output = %q", out.String())
	}
}

func TestRunCapabilityAdapterVerifyPromote_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	store := capability.NewStore(dir)
	gap, err := store.AddGap(capability.NewGapFromTask("inspect local adapter"))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.AddProposal(capability.NewProposalFromGap(gap))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"adapter", proposal.ID, "--type", "tool"}, &out); err != nil {
		t.Fatal(err)
	}
	capabilityID := outputValue(t, out.String(), "capability")
	out.Reset()
	if err := runCapabilityCommand([]string{"verify", capabilityID}, &out); err != nil {
		t.Fatalf("verify error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "tool.go_run") || !strings.Contains(out.String(), "promote: cohort capability promote "+capabilityID) {
		t.Fatalf("verify output = %q", out.String())
	}
	out.Reset()
	if err := runCapabilityCommand([]string{"promote", capabilityID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "status: available") {
		t.Fatalf("promote output = %q", out.String())
	}
}

func TestRunCapabilitySuggestionsCommand_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	store := capability.NewStore(dir)
	for _, task := range []string{"parse local foo file", "parse another foo file"} {
		gap := capability.NewGapFromTask(task)
		gap.MissingCapability = "local_foo_parser"
		gap.Source = "runner:no_tool"
		if _, err := store.AddGap(gap); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"suggestions"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"MISSING_CAPABILITY", "local_foo_parser", "runner:no_tool", "cohort capability propose"} {
		if !strings.Contains(got, want) {
			t.Fatalf("suggestions output = %q, want %q", got, want)
		}
	}
}

func TestRunCapabilityDepsCommand_BitsUT(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	store := capability.NewStore(dir)
	gap, err := store.AddGap(capability.NewGapFromTask("analyze csv with pandas"))
	if err != nil {
		t.Fatal(err)
	}
	proposal := capability.NewProposalFromGap(gap)
	proposal.Dependencies.Python = []string{"pandas"}
	proposal, err = store.AddProposal(proposal)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runCapabilityCommand([]string{"deps", "plan", proposal.ID}, &out); err != nil {
		t.Fatal(err)
	}
	planID := outputValue(t, out.String(), "plan")
	if !strings.Contains(out.String(), "python3 -m pip install --user pandas") {
		t.Fatalf("deps plan output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"deps", "approve", planID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "status: approved") {
		t.Fatalf("deps approve output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"deps", "install", planID, "--dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry_run") || !strings.Contains(out.String(), "pandas") {
		t.Fatalf("deps install dry-run output = %q", out.String())
	}

	out.Reset()
	if err := runCapabilityCommand([]string{"deps", "list"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), planID) || !strings.Contains(out.String(), "approved") {
		t.Fatalf("deps list output = %q", out.String())
	}
}

func outputValue(t *testing.T, output string, key string) string {
	t.Helper()
	prefix := key + ": "
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("output %q missing key %q", output, key)
	return ""
}
