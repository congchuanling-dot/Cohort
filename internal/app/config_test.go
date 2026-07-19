package app

import (
	"os"
	"path/filepath"
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
