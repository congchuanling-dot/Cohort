package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
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
