package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigParsesContextSection_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `language: zh
workspace: ./workspace
log_dir: ./temp/model_responses
max_turns: 7

context:
  max_history_messages: 9
  keep_recent_tool_results: 1
  max_tool_result_chars: 99
  compacted_tool_head_chars: 11
  compacted_tool_tail_chars: 12
  max_request_chars: 999
	  max_session_memory_chars: 88
  max_memory_index_chars: 99
  max_relevant_memory_chars: 144
  max_relevant_memory_files: 3
  max_compact_summary_chars: 188
  enable_micro_compact: false

llm:
  provider: openai
  name: deepseek
  api_key: test-key
  api_base: https://example.com/v1
  model: test-model
  stream: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Context.MaxHistoryMessages != 9 {
		t.Fatalf("max history messages = %d, want 9", cfg.Context.MaxHistoryMessages)
	}
	if cfg.Context.KeepRecentToolResults != 1 {
		t.Fatalf("keep recent tool results = %d, want 1", cfg.Context.KeepRecentToolResults)
	}
	if cfg.Context.MaxToolResultChars != 99 {
		t.Fatalf("max tool result chars = %d, want 99", cfg.Context.MaxToolResultChars)
	}
	if cfg.Context.CompactedToolHeadChars != 11 || cfg.Context.CompactedToolTailChars != 12 {
		t.Fatalf("compacted head/tail = %d/%d, want 11/12", cfg.Context.CompactedToolHeadChars, cfg.Context.CompactedToolTailChars)
	}
	if cfg.Context.MaxRequestChars != 999 {
		t.Fatalf("max request chars = %d, want 999", cfg.Context.MaxRequestChars)
	}
	if cfg.Context.MaxSessionMemoryChars != 88 {
		t.Fatalf("max session memory chars = %d, want 88", cfg.Context.MaxSessionMemoryChars)
	}
	if cfg.Context.MaxMemoryIndexChars != 99 {
		t.Fatalf("max memory index chars = %d, want 99", cfg.Context.MaxMemoryIndexChars)
	}
	if cfg.Context.MaxRelevantMemoryChars != 144 {
		t.Fatalf("max relevant memory chars = %d, want 144", cfg.Context.MaxRelevantMemoryChars)
	}
	if cfg.Context.MaxRelevantMemoryFiles != 3 {
		t.Fatalf("max relevant memory files = %d, want 3", cfg.Context.MaxRelevantMemoryFiles)
	}
	if cfg.Context.MaxCompactSummaryChars != 188 {
		t.Fatalf("max compact summary chars = %d, want 188", cfg.Context.MaxCompactSummaryChars)
	}
	if cfg.Context.ContextWindowTokens != 1000000 {
		t.Fatalf("resolved context window tokens = %d, want 1000000", cfg.Context.ContextWindowTokens)
	}
	if cfg.Context.EnableMicroCompact {
		t.Fatal("enable micro compact = true, want false")
	}
}

func TestNormalizeWorkspaceReturnsAbsolutePath_BitsUT(t *testing.T) {
	workspace := normalizeWorkspace("./workspace")
	if !filepath.IsAbs(workspace) {
		t.Fatalf("workspace = %q, want absolute path", workspace)
	}
	if !strings.HasSuffix(workspace, string(filepath.Separator)+"workspace") {
		t.Fatalf("workspace = %q, want workspace suffix", workspace)
	}
}

func TestBuildSystemPromptRequiresToolNarration_BitsUT(t *testing.T) {
	zhPrompt := buildSystemPrompt(Config{Language: "zh"})
	for _, want := range []string{"每次调用工具前", "当前已经知道什么", "可能的卡点", "每次工具返回后"} {
		if !strings.Contains(zhPrompt, want) {
			t.Fatalf("zh prompt does not contain %q:\n%s", want, zhPrompt)
		}
	}

	enPrompt := buildSystemPrompt(Config{Language: "en"})
	for _, want := range []string{"Before every tool call", "what you currently know", "likely blockers", "After each tool result"} {
		if !strings.Contains(enPrompt, want) {
			t.Fatalf("en prompt does not contain %q:\n%s", want, enPrompt)
		}
	}
}
