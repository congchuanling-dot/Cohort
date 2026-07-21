package contextmgr

import (
	"os"
	"path/filepath"
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

func TestManagerBuildInjectsSessionMemory_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	memoryText := "# Session Memory\n\n## 用户目标\n\n- 实现上下文管理"
	if err := os.WriteFile(filepath.Join(sessionDir, SessionMemoryFileName), []byte(memoryText), 0644); err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "继续"},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     20,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    1000000,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
		MaxSessionMemoryChars:  20000,
	}}.Build(BuildInput{Messages: messages, SessionDir: sessionDir})

	if !result.Stats.InjectedSessionMemory {
		t.Fatal("expected session memory to be injected")
	}
	if result.Stats.SessionMemoryChars != len([]rune(memoryText)) {
		t.Fatalf("session memory chars = %d, want %d", result.Stats.SessionMemoryChars, len([]rune(memoryText)))
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(result.Messages))
	}
	if result.Messages[0].Role != llm.RoleAssistant || !strings.Contains(result.Messages[0].Content, sessionMemoryNotice) {
		t.Fatalf("first message is not session memory: %#v", result.Messages[0])
	}
	if !strings.Contains(result.Messages[0].Content, "实现上下文管理") {
		t.Fatalf("session memory content missing:\n%s", result.Messages[0].Content)
	}
	if result.Messages[1].Content != "继续" {
		t.Fatalf("user message shifted incorrectly: %#v", result.Messages)
	}
}

func TestManagerBuildInjectsLongTermMemoryIndexBeforeSessionMemory_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	memoryRoot := t.TempDir()
	indexText := "# Memory Index\n\n- Project memory: memory/projects/default/project.md"
	sessionMemoryText := "# Session Memory\n\n- current task facts"
	if err := os.WriteFile(filepath.Join(memoryRoot, LongTermMemoryIndexFileName), []byte(indexText), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, SessionMemoryFileName), []byte(sessionMemoryText), 0644); err != nil {
		t.Fatal(err)
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     20,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    1000000,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
		MaxSessionMemoryChars:  20000,
		MaxMemoryIndexChars:    20000,
	}, MemoryRoot: memoryRoot}.Build(BuildInput{
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "继续"}},
		SessionDir: sessionDir,
	})

	if !result.Stats.InjectedMemoryIndex || !result.Stats.InjectedSessionMemory {
		t.Fatalf("expected memory index and session memory to be injected: %#v", result.Stats)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(result.Messages), result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content, longTermMemoryIndexNotice) {
		t.Fatalf("first message is not long-term memory index: %#v", result.Messages[0])
	}
	if !strings.Contains(result.Messages[1].Content, sessionMemoryNotice) {
		t.Fatalf("second message is not session memory: %#v", result.Messages[1])
	}
	if result.Messages[2].Content != "继续" {
		t.Fatalf("recent history shifted incorrectly: %#v", result.Messages)
	}
}

func TestManagerBuildTruncatesInjectedSessionMemory_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	memoryText := "1234567890"
	if err := os.WriteFile(filepath.Join(sessionDir, SessionMemoryFileName), []byte(memoryText), 0644); err != nil {
		t.Fatal(err)
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     20,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    1000000,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
		MaxSessionMemoryChars:  4,
	}}.Build(BuildInput{
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "继续"}},
		SessionDir: sessionDir,
	})

	if !result.Stats.SessionMemoryTruncated {
		t.Fatal("expected session memory to be truncated")
	}
	if result.Stats.SessionMemoryChars != 4 {
		t.Fatalf("session memory chars = %d, want 4", result.Stats.SessionMemoryChars)
	}
	if !strings.Contains(result.Messages[0].Content, "1234") {
		t.Fatalf("truncated head missing:\n%s", result.Messages[0].Content)
	}
	if strings.Contains(result.Messages[0].Content, "567890") {
		t.Fatalf("untruncated tail leaked into request:\n%s", result.Messages[0].Content)
	}
	if !strings.Contains(result.Messages[0].Content, "[Cohert session memory truncated]") {
		t.Fatalf("truncate notice missing:\n%s", result.Messages[0].Content)
	}
}

func TestManagerBuildPreservesSessionMemoryDuringTrim_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessionDir, SessionMemoryFileName), []byte("stable project facts"), 0644); err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "old user"},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "latest user"},
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     3,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    20,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
		MaxSessionMemoryChars:  20000,
	}}.Build(BuildInput{Messages: messages, SessionDir: sessionDir})

	if len(result.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(result.Messages), result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content, sessionMemoryNotice) {
		t.Fatalf("session memory was not preserved as protected prefix: %#v", result.Messages)
	}
	if result.Messages[1].Content != contextNotice {
		t.Fatalf("second message = %#v, want context notice", result.Messages[1])
	}
	if result.Messages[2].Content != "latest user" {
		t.Fatalf("latest user was not preserved: %#v", result.Messages)
	}
}

func TestManagerBuildInjectsCompactSummaryAfterSessionMemory_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	memoryText := "# Session Memory\n\n- stable facts"
	compactText := "1. Primary Request and Intent:\n\n- long history summary"
	if err := os.WriteFile(filepath.Join(sessionDir, SessionMemoryFileName), []byte(memoryText), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, CompactSummaryFileName), []byte(compactText), 0644); err != nil {
		t.Fatal(err)
	}

	result := Manager{Config: Config{
		MaxHistoryMessages:     20,
		KeepRecentToolResults:  1,
		MaxToolResultChars:     1000,
		CompactedToolHeadChars: 100,
		CompactedToolTailChars: 100,
		MaxRequestChars:        10000,
		ContextWindowTokens:    1000000,
		MaxOutputTokens:        0,
		SafetyTokens:           0,
		CompactTriggerRatio:    0.70,
		EnableMicroCompact:     true,
		MaxSessionMemoryChars:  20000,
		MaxCompactSummaryChars: 60000,
	}}.Build(BuildInput{
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "继续"}},
		SessionDir: sessionDir,
	})

	if !result.Stats.InjectedSessionMemory || !result.Stats.InjectedCompactSummary {
		t.Fatalf("expected memory and compact summary to be injected: %#v", result.Stats)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(result.Messages), result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content, sessionMemoryNotice) {
		t.Fatalf("first message is not session memory: %#v", result.Messages[0])
	}
	if !strings.Contains(result.Messages[1].Content, compactSummaryNotice) {
		t.Fatalf("second message is not compact summary: %#v", result.Messages[1])
	}
	if result.Messages[2].Content != "继续" {
		t.Fatalf("recent history shifted incorrectly: %#v", result.Messages)
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
	if got := ResolveContextWindowTokens("dsv4pro"); got != 1000000 {
		t.Fatalf("dsv4pro context window = %d, want 1000000", got)
	}
	if got := ResolveContextWindowTokens("deepseek-v4-pro"); got != 1000000 {
		t.Fatalf("deepseek-v4-pro context window = %d, want 1000000", got)
	}
	if got := ResolveContextWindowTokens("unknown"); got != defaultContextWindowTokens {
		t.Fatalf("unknown model context window = %d, want %d", got, defaultContextWindowTokens)
	}
}
