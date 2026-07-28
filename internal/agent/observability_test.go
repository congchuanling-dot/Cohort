package agent

import (
	"testing"
	"time"

	"cohort/internal/llm"
)

func TestLLMResponseDataIncludesUsage_BitsUT(t *testing.T) {
	data := llmResponseData(&llm.Response{
		Content: "ok",
		Usage: llm.Usage{
			InputTokens:              10,
			OutputTokens:             5,
			TotalTokens:              15,
			CacheCreationInputTokens: 2,
			CacheReadInputTokens:     3,
		},
	}, 20*time.Millisecond)

	usage, ok := data["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage = %#v, want map", data["usage"])
	}
	if usage["input_tokens"] != 10 || usage["output_tokens"] != 5 || usage["total_tokens"] != 15 {
		t.Fatalf("usage = %#v, want token counts", usage)
	}
	if usage["cache_creation_input_tokens"] != 2 || usage["cache_read_input_tokens"] != 3 {
		t.Fatalf("usage = %#v, want cache token counts", usage)
	}
}
