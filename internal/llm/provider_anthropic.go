package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func init() {
	Register("anthropic", newAnthropic)
}

// ==========================================
// Anthropic Messages API 原生格式
// ==========================================
// ★ 和 OpenAI 格式完全不同：
//   - system 是顶层字段，不在 messages 数组里
//   - content 是数组 [{type:"text", text:"..."}]，不是纯字符串
//   - 认证头是 x-api-key，不是 Bearer
//   - 路径是 /v1/messages，不是 /v1/chat/completions

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"` // ★ 系统提示单独字段
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string              `json:"role"`    // "user" | "assistant"
	Content []anthropicContent  `json:"content"` // ★ 数组格式
}

type anthropicContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ==========================================
// 适配器实现
// ==========================================

type anthropicClient struct {
	cfg        ProviderConfig
	baseURL    string
	apiVersion string
	httpClient *http.Client
}

func newAnthropic(cfg ProviderConfig) (Client, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	apiVersion := cfg.Extra["api_version"]
	if apiVersion == "" {
		apiVersion = "2023-06-01"
	}
	timeout := cfg.TimeoutSec
	if timeout == 0 {
		timeout = 120
	}
	return &anthropicClient{
		cfg:        cfg,
		baseURL:    baseURL,
		apiVersion: apiVersion,
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 同步对话：框架格式 ↔ Anthropic Messages API。
func (c *anthropicClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	// 1. ★ 关键适配：框架 ChatMessage → Anthropic Messages API
	body := c.buildRequest(messages)

	reqBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/messages",
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)           // ★ Anthropic 的认证头
	req.Header.Set("anthropic-version", c.apiVersion)   // ★ 必须指定 API 版本

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// 2. ★ Anthropic 响应格式 → 框架内部类型
	var antResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&antResp); err != nil {
		return nil, fmt.Errorf("anthropic decode: %w", err)
	}

	content := ""
	if len(antResp.Content) > 0 {
		content = antResp.Content[0].Text
	}

	return &ChatResponse{
		Content:      content,
		FinishReason: antResp.StopReason,
		Usage: &TokenUsage{
			PromptTokens:     antResp.Usage.InputTokens,
			CompletionTokens: antResp.Usage.OutputTokens,
			TotalTokens:      antResp.Usage.InputTokens + antResp.Usage.OutputTokens,
		},
	}, nil
}

// ChatStream 流式对话：Anthropic SSE 解析。
func (c *anthropicClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
	body := c.buildRequest(messages)
	body.Stream = true

	reqBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/messages",
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", c.apiVersion)
	req.Header.Set("Accept", "text/event-stream") // 非必需，但明确语义

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: %w", err)
	}

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic stream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan *StreamChunk, 10)
	go c.parseSSEStream(resp, ch)

	return ch, nil
}

// ==========================================
// 内部辅助
// ==========================================

// buildRequest 将框架 ChatMessage 转为 Anthropic 请求体。
// ★ 核心适配点：system 消息提升为顶层字段，其余转为 content 数组格式。
func (c *anthropicClient) buildRequest(messages []ChatMessage) anthropicRequest {
	var systemPrompt strings.Builder
	var antMsgs []anthropicMessage

	for _, m := range messages {
		if m.Role == "system" {
			// Anthropic 的 system 是请求顶层字段，不在 messages 数组里
			if systemPrompt.Len() > 0 {
				systemPrompt.WriteString("\n")
			}
			systemPrompt.WriteString(m.Content)
		} else if m.Role == "user" || m.Role == "assistant" {
			// user/assistant → Anthropic 直接对应
			antMsgs = append(antMsgs, anthropicMessage{
				Role: m.Role,
				Content: []anthropicContent{
					{Type: "text", Text: m.Content},
				},
			})
		}
		// 忽略其他 role（Anthropic 只支持 system/user/assistant）
	}

	return anthropicRequest{
		Model:       c.cfg.Model,
		MaxTokens:   c.cfg.MaxTokens,
		Temperature: c.cfg.Temperature,
		System:      systemPrompt.String(),
		Messages:    antMsgs,
	}
}

// parseSSEStream 解析 Anthropic SSE 流式响应。
//
// Anthropic SSE 事件类型：
//   - content_block_delta: token 增量
//   - message_stop: 消息结束
//   - ping: 心跳，忽略
func (c *anthropicClient) parseSSEStream(resp *http.Response, ch chan<- *StreamChunk) {
	defer resp.Body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}

		// 解析 SSE 事件
		var event struct {
			Type string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // 跳过无法解析的行
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				ch <- &StreamChunk{Content: event.Delta.Text}
			}
		case "message_stop":
			ch <- &StreamChunk{Done: true}
			return
		case "ping":
			// 心跳，忽略
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- &StreamChunk{
			Done:  true,
			Error: fmt.Errorf("anthropic stream read: %w", err),
		}
	}
}

func (c *anthropicClient) CountTokens(messages []ChatMessage) int {
	return estimateTokens(messages)
}

func (c *anthropicClient) Name() string {
	return "anthropic/" + c.cfg.Model
}
