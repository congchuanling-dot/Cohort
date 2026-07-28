package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestLangfuseSinkPostsGenerationUsage_BitsUT(t *testing.T) {
	var captured langfuseIngestionPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			t.Fatalf("path = %q, want /api/public/ingestion", r.URL.Path)
		}
		publicKey, secretKey, ok := r.BasicAuth()
		if !ok || publicKey != "pk-test" || secretKey != "sk-test" {
			t.Fatalf("basic auth = %q/%q/%v, want configured keys", publicKey, secretKey, ok)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sink := NewLangfuseSink(LangfuseSinkConfig{
		Host:        server.URL,
		PublicKey:   "pk-test",
		SecretKey:   "sk-test",
		Environment: "test",
		Release:     "sha-123",
	})
	event := NewEvent(EventLLMResponseFinished, "run_1", "sess_1", 2, "/tmp/work", "runner", SeverityInfo, map[string]any{
		"status":      "success",
		"duration_ms": int64(120),
		"langfuse_input": map[string]any{
			"messages": []any{map[string]any{"role": "user", "content": "say ok"}},
		},
		"langfuse_output": map[string]any{
			"content": "ok",
		},
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
			"total_tokens":  15,
		},
	})
	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	if len(captured.Batch) != 1 {
		t.Fatalf("batch count = %d, want 1", len(captured.Batch))
	}
	item := captured.Batch[0]
	if item.Type != "generation-create" {
		t.Fatalf("item type = %q, want generation-create", item.Type)
	}
	if item.Body["traceId"] != "run_1" || item.Body["name"] != "llm" {
		t.Fatalf("body = %#v, want trace generation body", item.Body)
	}
	usage, ok := item.Body["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage = %#v, want usage map", item.Body["usage"])
	}
	if intFromAny(usage["input"]) != 10 || intFromAny(usage["output"]) != 5 || intFromAny(usage["total"]) != 15 || usage["unit"] != "TOKENS" {
		t.Fatalf("langfuse usage = %#v, want token usage", usage)
	}
	if item.Body["input"] == nil || item.Body["output"] == nil {
		t.Fatalf("langfuse body missing input/output: %#v", item.Body)
	}
	metadata, ok := item.Body["metadata"].(map[string]any)
	if !ok || metadata["environment"] != "test" || metadata["release"] != "sha-123" {
		t.Fatalf("metadata = %#v, want env/release", item.Body["metadata"])
	}
}
