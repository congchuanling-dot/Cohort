package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const maxInlineStringChars = 1000

// TextSummary 保存文本的可关联摘要，不保存原文。
type TextSummary struct {
	Chars int    `json:"chars"`
	Lines int    `json:"lines"`
	Hash  string `json:"hash"`
}

func SummarizeText(text string) TextSummary {
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return TextSummary{
		Chars: len([]rune(text)),
		Lines: lines,
		Hash:  HashString(text),
	}
}

func HashString(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// RedactEvent 是写入 sink 前的最后一道防线。调用方应主动传摘要；
// 这里负责兜底处理误放进 Data 的敏感字段或大文本。
func RedactEvent(event Event) Event {
	if len(event.Data) == 0 {
		return event
	}
	fields := []string{}
	event.Data = redactMap(event.Data, "data", &fields)
	sort.Strings(fields)
	if len(fields) > 0 {
		event.Redaction = RedactionSummary{Applied: true, Fields: fields}
	}
	return event
}

func redactMap(input map[string]any, path string, fields *[]string) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		childPath := path + "." + key
		output[key] = redactValue(key, value, childPath, fields)
	}
	return output
}

func redactValue(key string, value any, path string, fields *[]string) any {
	if isSensitiveKey(key) {
		*fields = append(*fields, path)
		return summarizeSecret(fmt.Sprint(value))
	}
	switch typed := value.(type) {
	case map[string]any:
		return redactMap(typed, path, fields)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = redactValue(key, item, fmt.Sprintf("%s[%d]", path, i), fields)
		}
		return result
	case string:
		if isBodyKey(key) || len([]rune(typed)) > maxInlineStringChars {
			*fields = append(*fields, path)
			return SummarizeText(typed)
		}
		return typed
	default:
		return value
	}
}

func summarizeSecret(value string) map[string]any {
	return map[string]any{
		"redacted": true,
		"chars":    len([]rune(value)),
		"hash":     HashString(value),
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	if strings.HasSuffix(normalized, "_tokens") || normalized == "total_tokens" {
		return false
	}
	needles := []string{
		"api_key",
		"apikey",
		"authorization",
		"cookie",
		"password",
		"secret",
		"token",
	}
	for _, needle := range needles {
		if strings.Contains(normalized, needle) {
			return true
		}
	}
	return false
}

func isBodyKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "content", "text", "clipboard", "prompt", "input", "output", "user_input":
		return true
	default:
		return false
	}
}
