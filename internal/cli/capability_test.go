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
