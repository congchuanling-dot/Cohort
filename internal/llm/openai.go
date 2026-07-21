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

// OpenAIConfig 是 OpenAI-compatible 模型服务的连接配置。
type OpenAIConfig struct {
	// Name 是本配置的可读名称，主要用于展示和排查。
	Name string
	// APIKey 是访问 OpenAI-compatible 服务的鉴权密钥。
	APIKey string
	// APIBase 是模型服务基础地址，可以是域名、/v1 或完整 chat/completions。
	APIBase string
	// Model 是本次请求使用的模型名称。
	Model string
	// Stream 控制是否使用 SSE 流式响应。
	Stream bool
	// ConnectTimeout 是连接建立阶段的超时预算。
	ConnectTimeout time.Duration
	// ReadTimeout 是读取响应阶段的超时预算。
	ReadTimeout time.Duration
	// MaxRetries 是遇到可重试错误时的最大重试次数。
	MaxRetries int
}

// OpenAIClient 实现 Client 接口，负责调用 /v1/chat/completions。
type OpenAIClient struct {
	// cfg 保存模型服务连接和请求策略配置。
	cfg OpenAIConfig
	// client 是实际发送 HTTP 请求的客户端。
	client *http.Client
}

// NewOpenAIClient 创建模型客户端，并设置整体 HTTP 超时时间。
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

// Chat 启动一次模型请求，并用 channel 向 Runner 返回流式事件。
// 这里开 goroutine 是为了让 Runner 可以像消费流一样读取模型输出。
func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		var lastErr error
		for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
			if attempt > 0 {
				// 简单退避重试，避免瞬时网络或 5xx 问题直接失败。
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

// doChat 构造 HTTP 请求，发送给 OpenAI-compatible Chat Completions 接口。
func (c *OpenAIClient) doChat(ctx context.Context, req ChatRequest, out chan<- Event) (*Response, error) {
	body := openAIRequest{
		Model:    c.cfg.Model,
		Messages: buildOpenAIMessages(req.System, req.Messages),
		Stream:   c.cfg.Stream,
		Tools:    req.Tools,
	}
	if len(body.Tools) > 0 {
		// tool_choice=auto 表示让模型自行决定是否调用工具。
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

	// MVP 默认使用流式输出；保留非流式解析方便以后调试或兼容其他服务商。
	if !c.cfg.Stream {
		return parseOpenAIJSON(httpResp.Body)
	}
	return parseOpenAISSE(httpResp.Body, out)
}

// openAIRequest 是 /v1/chat/completions 的请求体。
type openAIRequest struct {
	// Model 是请求的目标模型名称。
	Model string `json:"model"`
	// Messages 是 OpenAI-compatible 协议的对话消息列表。
	Messages []Message `json:"messages"`
	// Stream 表示是否要求服务端返回 SSE 流。
	Stream bool `json:"stream"`
	// Tools 是本次请求暴露给模型的工具定义。
	Tools []ToolSchema `json:"tools,omitempty"`
	// ToolChoice 控制模型是否自动选择工具。
	ToolChoice string `json:"tool_choice,omitempty"`
}

// buildOpenAIMessages 把 system prompt 合并到消息列表最前面。
func buildOpenAIMessages(system string, messages []Message) []Message {
	result := make([]Message, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		result = append(result, Message{Role: RoleSystem, Content: system})
	}
	result = append(result, messages...)
	return result
}

// chatCompletionsURL 兼容 api_base 写成域名、/v1 或完整 chat/completions 的情况。
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

// httpStatusError 保留 HTTP 状态码和响应体，方便上层打印具体失败原因。
type httpStatusError struct {
	// Code 是 HTTP 响应状态码。
	Code int
	// Body 是服务端返回的错误响应体，最多读取前 4096 字节。
	Body string
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("llm http status %d: %s", e.Code, e.Body)
}

// isRetryable 判断一次请求失败是否值得重试。
func isRetryable(err error) bool {
	var httpErr httpStatusError
	if errors.As(err, &httpErr) {
		return httpErr.Code == 408 || httpErr.Code == 429 || httpErr.Code >= 500
	}
	return true
}

// parseOpenAIJSON 解析非流式响应。
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

// parseOpenAISSE 解析流式 SSE 响应。
// 文本 delta 会实时发送 EventText；tool_calls 的 name/arguments 会按 index 增量拼接。
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
		// Raw 保存每个 SSE payload，方便排查模型原始返回。
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
				// arguments 通常会被模型分片返回，需要按顺序拼接成完整 JSON 字符串。
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

// normalizeToolCalls 补齐协议默认值，避免后续处理遇到空 Type。
func normalizeToolCalls(calls []ToolCall) []ToolCall {
	for i := range calls {
		if calls[i].Type == "" {
			calls[i].Type = "function"
		}
	}
	return calls
}

// openAIResponse 是非流式接口返回结构。
type openAIResponse struct {
	// Choices 是非流式响应的候选结果列表，当前只取第一个。
	Choices []struct {
		// Message 是该候选结果里的 assistant 消息。
		Message Message `json:"message"`
	} `json:"choices"`
}

// openAIStreamChunk 是 SSE 每个 data payload 的结构。
type openAIStreamChunk struct {
	// Choices 是本个 SSE chunk 的候选增量，当前只消费第一个。
	Choices []struct {
		// Delta 是本次增量里的文本和工具调用片段。
		Delta struct {
			// Content 是本次流式文本增量。
			Content string `json:"content"`
			// ToolCalls 是本次流式工具调用增量。
			ToolCalls []struct {
				// Index 是工具调用在本轮 tool_calls 数组中的位置。
				Index int `json:"index"`
				// ID 是工具调用标识，可能只在第一个 chunk 出现。
				ID string `json:"id"`
				// Type 是工具调用类型，通常为 function。
				Type string `json:"type"`
				// Function 保存函数名和参数的流式片段。
				Function struct {
					// Name 是函数名片段。
					Name string `json:"name"`
					// Arguments 是 JSON 参数片段，需要按 index 拼接。
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}
