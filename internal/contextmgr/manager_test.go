package contextmgr

import (
	"strings"
	"testing"

	"cohert/internal/llm"
)

func TestManagerBuildCompactsOldToolResults_BitsUT(t *testing.T) {
	oldContent := strings.Repeat("A", 40) + "middle" + strings.Repeat("Z", 40)
	recentContent := strings.Repeat("R", 90)
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "run old command"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Function: llm.ToolFunction{Name: "code_run"}}}},
		{Role: llm.RoleTool, ToolCallID: "old", Name: "code_run", Content: oldContent},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Function: llm.ToolFunction{Name: "code_run"}}}},
		{Role: llm.RoleTool, ToolCallID: "recent", Name: "code_run", Content: recentContent},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     20,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     30,
		CompactedToolHeadChars: 8,
		CompactedToolTailChars: 8,
		MaxRequestChars:        10000,
		ContextWindowTokens:    120,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
	}}.Build(BuildInput{Messages: messages})

	if result.Stats.CompactedToolResults != 1 {
		t.Fatalf("compacted tool results = %d, want 1", result.Stats.CompactedToolResults)
	}
	oldTool := result.Messages[2]
	if !strings.Contains(oldTool.Content, "[tool result compacted]") {
		t.Fatalf("old tool result was not compacted:\n%s", oldTool.Content)
	}
	if !strings.Contains(oldTool.Content, "AAAAAAAA") || !strings.Contains(oldTool.Content, "ZZZZZZZZ") {
		t.Fatalf("compacted content does not keep head and tail:\n%s", oldTool.Content)
	}
	if result.Messages[4].Content != recentContent {
		t.Fatalf("recent tool result was compacted, got length %d", len(result.Messages[4].Content))
	}
	if messages[2].Content != oldContent {
		t.Fatal("Build mutated the original history message")
	}
}

func TestManagerBuildTrimsByToolCallGroup_BitsUT(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "old user"},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "middle user"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolFunction{Name: "file_read"}}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Name: "file_read", Content: "file content"},
		{Role: llm.RoleAssistant, Content: "final answer"},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     4,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    30,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
	}}.Build(BuildInput{Messages: messages})

	if !result.Stats.InsertedNotice {
		t.Fatal("expected context notice to be inserted")
	}
	if len(result.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(result.Messages))
	}
	if result.Messages[0].Content != contextNotice {
		t.Fatalf("first message = %#v, want context notice", result.Messages[0])
	}
	if len(result.Messages[1].ToolCalls) != 1 || result.Messages[2].ToolCallID != "call-1" {
		t.Fatalf("tool call group was split: %#v", result.Messages)
	}
	if result.Messages[3].Content != "final answer" {
		t.Fatalf("latest assistant message was not preserved: %#v", result.Messages[3])
	}
}

func TestManagerBuildDropsOrphanToolResults_BitsUT(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "missing", Name: "code_run", Content: "orphan"},
		{Role: llm.RoleUser, Content: "latest user"},
	}

	result := Manager{Config: DefaultConfig()}.Build(BuildInput{Messages: messages})

	if len(result.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Role != llm.RoleUser {
		t.Fatalf("orphan tool result was not dropped: %#v", result.Messages)
	}
	if len(result.Stats.Warnings) == 0 {
		t.Fatal("expected warning for orphan tool result")
	}
}

func TestManagerBuildSkipsCompactBelowTriggerThreshold_BitsUT(t *testing.T) {
	oldContent := strings.Repeat("A", 80)
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "run old command"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Function: llm.ToolFunction{Name: "code_run"}}}},
		{Role: llm.RoleTool, ToolCallID: "old", Name: "code_run", Content: oldContent},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     2,
		KeepRecentToolResults:  0,
		MaxToolResultChars:     30,
		CompactedToolHeadChars: 8,
		CompactedToolTailChars: 8,
		MaxRequestChars:        10,
		ContextWindowTokens:    1000000,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
	}}.Build(BuildInput{Messages: messages})

	if !result.Stats.SkippedCompact {
		t.Fatal("expected compact to be skipped below trigger threshold")
	}
	if result.Stats.TriggerReason != triggerReasonBelowThreshold {
		t.Fatalf("trigger reason = %q, want %q", result.Stats.TriggerReason, triggerReasonBelowThreshold)
	}
	if result.Stats.CompactedToolResults != 0 {
		t.Fatalf("compacted tool results = %d, want 0", result.Stats.CompactedToolResults)
	}
	if result.Stats.InsertedNotice {
		t.Fatal("did not expect context notice below trigger threshold")
	}
	if len(result.Messages) != len(messages) {
		t.Fatalf("messages = %d, want original %d", len(result.Messages), len(messages))
	}
	if result.Messages[2].Content != oldContent {
		t.Fatal("tool result was compacted below trigger threshold")
	}
}

func TestManagerBuildDoesNotTrimWhenMicroCompactFitsBudget_BitsUT(t *testing.T) {
	oldContent := strings.Repeat("A", 80) + strings.Repeat("Z", 80)
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "old user"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Function: llm.ToolFunction{Name: "code_run"}}}},
		{Role: llm.RoleTool, ToolCallID: "old", Name: "code_run", Content: oldContent},
		{Role: llm.RoleAssistant, Content: "final answer"},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     2,
		KeepRecentToolResults:  0,
		MaxToolResultChars:     30,
		CompactedToolHeadChars: 5,
		CompactedToolTailChars: 5,
		MaxRequestChars:        10000,
		ContextWindowTokens:    150,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
	}}.Build(BuildInput{Messages: messages})

	if result.Stats.CompactedToolResults != 1 {
		t.Fatalf("compacted tool results = %d, want 1", result.Stats.CompactedToolResults)
	}
	if result.Stats.InsertedNotice {
		t.Fatal("did not expect group trim after micro compact fits budget")
	}
	if len(result.Messages) != len(messages) {
		t.Fatalf("messages = %d, want original group count %d", len(result.Messages), len(messages))
	}
	if !strings.Contains(result.Messages[2].Content, "[tool result compacted]") {
		t.Fatalf("tool result was not compacted:\n%s", result.Messages[2].Content)
	}
}

func TestResolveContextWindowTokensUsesModelMap_BitsUT(t *testing.T) {
	if got := ResolveContextWindowTokens("dsv4pro", 0); got != 1000000 {
		t.Fatalf("dsv4pro context window = %d, want 1000000", got)
	}
	if got := ResolveContextWindowTokens("deepseek-v4-pro", 0); got != 1000000 {
		t.Fatalf("deepseek-v4-pro context window = %d, want 1000000", got)
	}
	if got := ResolveContextWindowTokens("unknown", 123); got != 123 {
		t.Fatalf("configured context window = %d, want 123", got)
	}
}
