package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactEventHidesSecretsAndLargeText_BitsUT(t *testing.T) {
	event := NewEvent(EventToolStarted, "run_1", "sess_1", 1, "/tmp/work", "runner", SeverityInfo, map[string]any{
		"token":   "super-secret-token",
		"content": strings.Repeat("A", 1200),
		"safe":    "value",
	})

	redacted := RedactEvent(event)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-token") || strings.Contains(string(data), strings.Repeat("A", 80)) {
		t.Fatalf("event leaked sensitive data: %s", data)
	}
	if !redacted.Redaction.Applied || len(redacted.Redaction.Fields) == 0 {
		t.Fatalf("redaction summary = %#v, want applied fields", redacted.Redaction)
	}
}

func TestJSONLSinkWritesRedactedEvents_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log.jsonl")
	sink := NewJSONLSink(path)
	bus := NewBus(sink)
	bus.Emit(context.Background(), NewEvent(EventUserPromptSubmitted, "run_1", "sess_1", 0, "/tmp/work", "runner", SeverityInfo, map[string]any{
		"user_input": "password=secret-value",
	}))

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("run.log.jsonl is empty")
	}
	line := scanner.Text()
	if strings.Contains(line, "secret-value") {
		t.Fatalf("jsonl leaked secret: %s", line)
	}
	var event Event
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatal(err)
	}
	if event.EventType != EventUserPromptSubmitted || !event.Redaction.Applied {
		t.Fatalf("event = %#v", event)
	}
}
