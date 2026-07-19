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
  context_window_tokens: 123456
  max_output_tokens: 333
  safety_tokens: 444
  compact_trigger_ratio: 0.75
  max_history_messages: 9
  keep_recent_tool_results: 1
  max_tool_result_chars: 99
  compacted_tool_head_chars: 11
  compacted_tool_tail_chars: 12
  max_request_chars: 999
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
	if cfg.Context.ContextWindowTokens != 123456 {
		t.Fatalf("context window tokens = %d, want 123456", cfg.Context.ContextWindowTokens)
	}
	if cfg.Context.MaxOutputTokens != 333 {
		t.Fatalf("max output tokens = %d, want 333", cfg.Context.MaxOutputTokens)
	}
	if cfg.Context.SafetyTokens != 444 {
		t.Fatalf("safety tokens = %d, want 444", cfg.Context.SafetyTokens)
	}
	if cfg.Context.CompactTriggerRatio != 0.75 {
		t.Fatalf("compact trigger ratio = %f, want 0.75", cfg.Context.CompactTriggerRatio)
	}
	if cfg.Context.EnableMicroCompact {
		t.Fatal("enable micro compact = true, want false")
	}
}
