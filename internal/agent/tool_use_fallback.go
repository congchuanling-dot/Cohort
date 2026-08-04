package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"cohort/internal/llm"
)

const (
	toolUseOpenTag  = "<tool_use>"
	toolUseCloseTag = "</tool_use>"
)

type textToolUse struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type textToolUseParseResult struct {
	Content   string
	ToolCalls []llm.ToolCall
}

// parseTextToolUseFallback strictly parses assistant text blocks of the form:
//
//	<tool_use>{"name":"file_read","arguments":{"path":"README.md"}}</tool_use>
//
// It intentionally does not accept fuzzy variants. The fallback is only for
// providers that fail to emit native tool_calls, not for guessing from prose.
func parseTextToolUseFallback(content string) (textToolUseParseResult, error) {
	if !strings.Contains(content, toolUseOpenTag) && !strings.Contains(content, toolUseCloseTag) {
		return textToolUseParseResult{Content: content}, nil
	}
	remaining := content
	var visible strings.Builder
	var calls []llm.ToolCall
	for {
		start := strings.Index(remaining, toolUseOpenTag)
		if start < 0 {
			if strings.Contains(remaining, toolUseCloseTag) {
				return textToolUseParseResult{}, fmt.Errorf("found %s without matching %s", toolUseCloseTag, toolUseOpenTag)
			}
			visible.WriteString(remaining)
			break
		}
		visible.WriteString(remaining[:start])
		afterOpen := remaining[start+len(toolUseOpenTag):]
		end := strings.Index(afterOpen, toolUseCloseTag)
		if end < 0 {
			return textToolUseParseResult{}, fmt.Errorf("missing closing %s", toolUseCloseTag)
		}
		rawBlock := strings.TrimSpace(afterOpen[:end])
		call, err := parseSingleTextToolUse(rawBlock, len(calls))
		if err != nil {
			return textToolUseParseResult{}, err
		}
		calls = append(calls, call)
		remaining = afterOpen[end+len(toolUseCloseTag):]
	}
	return textToolUseParseResult{
		Content:   strings.TrimSpace(visible.String()),
		ToolCalls: calls,
	}, nil
}

func parseSingleTextToolUse(raw string, index int) (llm.ToolCall, error) {
	if raw == "" {
		return llm.ToolCall{}, fmt.Errorf("empty tool_use block")
	}
	var decoded textToolUse
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return llm.ToolCall{}, fmt.Errorf("tool_use JSON is invalid: %w", err)
	}
	name := strings.TrimSpace(decoded.Name)
	if name == "" {
		return llm.ToolCall{}, fmt.Errorf("tool_use.name is required")
	}
	args := strings.TrimSpace(string(decoded.Arguments))
	if args == "" || args == "null" {
		args = "{}"
	}
	if !json.Valid([]byte(args)) {
		return llm.ToolCall{}, fmt.Errorf("tool_use.arguments must be valid JSON")
	}
	return llm.ToolCall{
		ID:   fmt.Sprintf("text_tool_use_%d", index+1),
		Type: "function",
		Function: llm.ToolFunction{
			Name:      name,
			Arguments: args,
		},
	}, nil
}
