package cli

import (
	"bytes"
	"strings"
	"testing"

	"cohort/internal/version"
)

func TestPrintVersion_BitsUT(t *testing.T) {
	oldVersion := version.Version
	oldCommit := version.Commit
	oldBuiltAt := version.BuiltAt
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuiltAt = oldBuiltAt
	})

	version.Version = "v1.2.3"
	version.Commit = "abc1234"
	version.BuiltAt = "2026-07-30T00:00:00Z"

	var out bytes.Buffer
	printVersion(&out)

	for _, want := range []string{
		"cohort v1.2.3",
		"commit: abc1234",
		"built_at: 2026-07-30T00:00:00Z",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}
