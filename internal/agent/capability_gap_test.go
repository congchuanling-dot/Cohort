package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/capability"
	"cohort/internal/observability"
)

func TestLooksLikeCapabilityGap_BitsUT(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{content: "当前没有可用工具处理这种文件格式。", want: true},
		{content: "This format is unsupported without an adapter.", want: true},
		{content: "任务已完成。", want: false},
	}
	for _, tc := range cases {
		if got := looksLikeCapabilityGap(tc.content); got != tc.want {
			t.Fatalf("looksLikeCapabilityGap(%q) = %t, want %t", tc.content, got, tc.want)
		}
	}
}

func TestRunnerRecordsCapabilityGap_BitsUT(t *testing.T) {
	dir := t.TempDir()
	runner := &Runner{SessionCWD: dir}
	runner.maybeRecordCapabilityGap(
		context.Background(),
		observability.NoopBus{},
		"run-test",
		1,
		"处理新的专有文件格式",
		"当前缺少工具，无法处理这种专有文件格式。",
	)

	store := capability.NewStore(dir)
	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(registry.Gaps))
	}
	if registry.Gaps[0].Source != "runner:no_tool" {
		t.Fatalf("gap source = %q, want runner:no_tool", registry.Gaps[0].Source)
	}
	if !strings.Contains(registry.Gaps[0].Task, "专有文件格式") {
		t.Fatalf("gap task = %q, want original task", registry.Gaps[0].Task)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cohort", "capabilities", "registry.json")); err != nil {
		t.Fatal(err)
	}
}
