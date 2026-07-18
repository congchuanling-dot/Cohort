package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIConfig struct {
	Name           string
	APIKey         string
	APIBase        string
	Model          string
	Stream         bool
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	MaxRetries     int
}

type OpenAIClient struct {
	cfg    OpenAIConfig
	client *http.Client
}

func NewOpenAIClient(cfg OpenAIConfig) *OpenAIClient {
	timeout := cfg.ConnectTimeout + cfg.ReadTimeout
	if timeout <= 0 {
		timeout = 130 * time.Second
	}
	return &OpenAIClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		var lastErr error
		for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					out <- Event{Type: EventError, Err: ctx.Err()}
					return
				case <-time.After(time.Duration(attempt) * 600 * time.Millisecond):
				}
			}
			resp, err := c.doChat(ctx, req, out)
			if err == nil {
				out <- Event{Type: EventDone, Response: resp}
				return
			}
			lastErr = err
			if !isRetryable(err) {
				break
			}
		}
		out <- Event{Type: EventError, Err: lastErr}
	}()
	return out, nil
}

func (c *OpenAIClient) doChat(ctx context.Context, req ChatRequest, out chan<- Event) (*Response, error) {
	body := openAIRequest{
		Model:    c.cfg.Model,
		Messages: buildOpenAIMessages(req.System, req.Messages),
		Stream:   c.cfg.Stream,
		Tools:    req.Tools,
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = "auto"
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(c.cfg.APIBase), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, httpStatusError{Code: httpResp.StatusCode, Body: string(data)}
	}

	if !c.cfg.Stream {
		return parseOpenAIJSON(httpResp.Body)
	}
	return parseOpenAISSE(httpResp.Body, out)
}

type openAIRequest struct {
	Model      string       `json:"model"`
	Messages   []Message    `json:"messages"`
	Stream     bool         `json:"stream"`
	Tools      []ToolSchema `json:"tools,omitempty"`
	ToolChoice string       `json:"tool_choice,omitempty"`
}

func buildOpenAIMessages(system string, messages []Message) []Message {
	result := make([]Message, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		result = append(result, Message{Role: "system", Content: system})
	}
	result = append(result, messages...)
	return result
}

func chatCompletionsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

type httpStatusError struct {
	Code int
	Body string
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("llm http status %d: %s", e.Code, e.Body)
}

func isRetryable(err error) bool {
	var httpErr httpStatusError
	if errors.As(err, &httpErr) {
		return httpErr.Code == 408 || httpErr.Code == 429 || httpErr.Code >= 500
	}
	return true
}

func parseOpenAIJSON(r io.Reader) (*Response, error) {
	var data openAIResponse
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Choices) == 0 {
		return &Response{}, nil
	}
	msg := data.Choices[0].Message
	return &Response{
		Content:   msg.Content,
		ToolCalls: normalizeToolCalls(msg.ToolCalls),
	}, nil
}

func parseOpenAISSE(r io.Reader, out chan<- Event) (*Response, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 1024*1024*8)

	var content strings.Builder
	toolCalls := map[int]*ToolCall{}
	var raw strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		raw.WriteString(payload)
		raw.WriteByte('\n')

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return nil, err
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			out <- Event{Type: EventText, Text: delta.Content}
		}
		for _, tc := range delta.ToolCalls {
			dst := toolCalls[tc.Index]
			if dst == nil {
				dst = &ToolCall{ID: tc.ID, Type: "function"}
				toolCalls[tc.Index] = dst
			}
			if tc.ID != "" {
				dst.ID = tc.ID
			}
			if tc.Type != "" {
				dst.Type = tc.Type
			}
			if tc.Function.Name != "" {
				dst.Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				dst.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	calls := make([]ToolCall, 0, len(toolCalls))
	for i := 0; i < len(toolCalls); i++ {
		if tc := toolCalls[i]; tc != nil {
			calls = append(calls, *tc)
		}
	}
	return &Response{
		Content:   content.String(),
		ToolCalls: normalizeToolCalls(calls),
		Raw:       raw.String(),
	}, nil
}

func normalizeToolCalls(calls []ToolCall) []ToolCall {
	for i := range calls {
		if calls[i].Type == "" {
			calls[i].Type = "function"
		}
	}
	return calls
}

type openAIResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}
