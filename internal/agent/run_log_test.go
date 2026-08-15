package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunesKeepsMultibyteCharsIntact(t *testing.T) {
	// 200 个中文字符（每个 UTF-8 占 3 字节），字节级切割会切坏第 160 字节处的字符。
	input := strings.Repeat("中", 200)
	got := truncateRunes(input, 160)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated string must remain valid UTF-8, got invalid bytes")
	}
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	body := strings.TrimSuffix(got, "...[truncated]")
	if utf8.RuneCountInString(body) != 160 {
		t.Fatalf("expected 160 runes before marker, got %d", utf8.RuneCountInString(body))
	}
}

func TestTruncateRunesShortStringUnchanged(t *testing.T) {
	input := "短文本"
	if got := truncateRunes(input, 160); got != input {
		t.Fatalf("short string must be returned unchanged, got %q", got)
	}
}

func TestRedactArgsSummaryTruncatesByRune(t *testing.T) {
	args := map[string]any{"note": strings.Repeat("汉", 500)}
	got := redactArgsSummary(args)
	if !utf8.ValidString(got) {
		t.Fatalf("redacted summary must remain valid UTF-8")
	}
}

func TestOutcomeAuditShapePreservesToolErrorMessage(t *testing.T) {
	outcome := Outcome{Data: NewToolError("bad_json", "参数缺少必填字段 path", "先调用 file_read 确认路径")}
	_, _, code, message := outcomeAuditShape(outcome.Data)
	if code != "bad_json" {
		t.Fatalf("expected error code preserved, got %q", code)
	}
	if message != "参数缺少必填字段 path" {
		t.Fatalf("expected error message preserved for triage, got %q", message)
	}
}

func TestOutcomeAuditShapeTruncatesLongErrorMessageByRune(t *testing.T) {
	long := strings.Repeat("错", 500)
	_, _, _, message := outcomeAuditShape(NewToolError("boom", long, ""))
	if !utf8.ValidString(message) {
		t.Fatalf("truncated error message must remain valid UTF-8")
	}
	if utf8.RuneCountInString(message) > maxErrorMessageRunes+len([]rune("...[truncated]")) {
		t.Fatalf("error message exceeded truncation cap: %d runes", utf8.RuneCountInString(message))
	}
}

func TestOutcomeAuditShapeSuccessHasNoErrorMessage(t *testing.T) {
	_, _, code, message := outcomeAuditShape(map[string]any{"content": "ok"})
	if code != "" || message != "" {
		t.Fatalf("successful outcome must not carry error fields, got code=%q message=%q", code, message)
	}
}
