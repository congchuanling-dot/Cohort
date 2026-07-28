package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultLangfuseHost    = "https://cloud.langfuse.com"
	defaultLangfuseTimeout = 2 * time.Second
)

// LangfuseSinkConfig 描述 Langfuse ingestion API 的连接配置。
type LangfuseSinkConfig struct {
	Host      string
	PublicKey string
	SecretKey string
	// Environment 和 Release 会写入 Langfuse metadata，便于按部署环境筛选。
	Environment string
	Release     string
	Timeout     time.Duration
}

// LangfuseSink 将 Cohort 观测事件转发到 Langfuse ingestion API。
type LangfuseSink struct {
	endpoint    string
	publicKey   string
	secretKey   string
	environment string
	release     string
	timeout     time.Duration
	client      *http.Client
}

func NewLangfuseSink(cfg LangfuseSinkConfig) *LangfuseSink {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		host = defaultLangfuseHost
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultLangfuseTimeout
	}
	return &LangfuseSink{
		endpoint:    host + "/api/public/ingestion",
		publicKey:   strings.TrimSpace(cfg.PublicKey),
		secretKey:   strings.TrimSpace(cfg.SecretKey),
		environment: strings.TrimSpace(cfg.Environment),
		release:     strings.TrimSpace(cfg.Release),
		timeout:     timeout,
		client:      &http.Client{Timeout: timeout},
	}
}

func (s *LangfuseSink) Emit(ctx context.Context, event Event) error {
	if s == nil || s.endpoint == "" || s.publicKey == "" || s.secretKey == "" {
		return nil
	}
	item := s.batchItem(event)
	if item.Type == "" {
		return nil
	}
	payload := langfuseIngestionPayload{Batch: []langfuseBatchItem{item}}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(s.publicKey, s.secretKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("langfuse ingestion status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func (s *LangfuseSink) Close(ctx context.Context) error {
	return nil
}

func (s *LangfuseSink) batchItem(event Event) langfuseBatchItem {
	switch event.EventType {
	case EventRunStarted:
		return langfuseBatchItem{
			ID:        event.EventID,
			Type:      "trace-create",
			Timestamp: event.Time,
			Body: map[string]any{
				"id":        event.RunID,
				"name":      "cohort.run",
				"timestamp": event.Time,
				"sessionId": event.SessionID,
				"metadata":  s.metadata(event),
			},
		}
	case EventLLMResponseFinished:
		return s.generationItem(event)
	default:
		return langfuseBatchItem{
			ID:        event.EventID,
			Type:      "event-create",
			Timestamp: event.Time,
			Body: map[string]any{
				"id":        event.EventID,
				"traceId":   event.RunID,
				"name":      string(event.EventType),
				"startTime": event.Time,
				"metadata":  s.metadata(event),
			},
		}
	}
}

func (s *LangfuseSink) generationItem(event Event) langfuseBatchItem {
	body := map[string]any{
		"id":       event.EventID,
		"traceId":  event.RunID,
		"name":     "llm",
		"endTime":  event.Time,
		"metadata": s.metadata(event),
	}
	if duration := intFromAny(event.Data["duration_ms"]); duration > 0 {
		body["startTime"] = event.Time.Add(-time.Duration(duration) * time.Millisecond)
	} else {
		body["startTime"] = event.Time
	}
	if usage := langfuseUsage(event.Data["usage"]); len(usage) > 0 {
		body["usage"] = usage
	}
	if input, ok := event.Data["langfuse_input"]; ok {
		body["input"] = input
	}
	if output, ok := event.Data["langfuse_output"]; ok {
		body["output"] = output
	}
	if event.Severity == SeverityError {
		body["level"] = "ERROR"
		if errMsg, ok := event.Data["error"].(string); ok && errMsg != "" {
			body["statusMessage"] = errMsg
		}
	}
	return langfuseBatchItem{
		ID:        event.EventID,
		Type:      "generation-create",
		Timestamp: event.Time,
		Body:      body,
	}
}

func (s *LangfuseSink) metadata(event Event) map[string]any {
	metadata := map[string]any{
		"schema_version": event.SchemaVersion,
		"event_type":     event.EventType,
		"severity":       event.Severity,
		"source":         event.Source,
		"workspace":      event.Workspace,
		"session_id":     event.SessionID,
		"turn":           event.Turn,
		"data":           event.Data,
		"redaction":      event.Redaction,
	}
	if s.environment != "" {
		metadata["environment"] = s.environment
	}
	if s.release != "" {
		metadata["release"] = s.release
	}
	return metadata
}

type langfuseIngestionPayload struct {
	Batch []langfuseBatchItem `json:"batch"`
}

type langfuseBatchItem struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Body      map[string]any `json:"body"`
}

func langfuseUsage(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	usage := map[string]any{}
	if input := intFromAny(raw["input_tokens"]); input > 0 {
		usage["input"] = input
	}
	if output := intFromAny(raw["output_tokens"]); output > 0 {
		usage["output"] = output
	}
	if total := intFromAny(raw["total_tokens"]); total > 0 {
		usage["total"] = total
	}
	if len(usage) == 0 {
		return nil
	}
	usage["unit"] = "TOKENS"
	return usage
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}
