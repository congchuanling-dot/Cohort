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
	// ★ 只注册 "openai"，不和 DeepSeek/Ollama 混在一起
	Register("openai", newOpenAI)
}

// ==========================================
// OpenAI 原生 API 的请求/响应格式
// ==========================================
// 这些 struct 是 OpenAI 特有的，不暴露给外部。
// 核心职责：框架内部类型 ↔ OpenAI API 格式的双向翻译。

type openaiChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message      openaiMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
		Delta        struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ==========================================
// 适配器实现
// ==========================================

// openaiClient 实现 Client 接口，封装 OpenAI 原生 API。
// 其他 OpenAI 兼容的 Provider（DeepSeek、Ollama）可通过组合此结构体复用逻辑。
type openaiClient struct {
	cfg        ProviderConfig
	baseURL    string
	httpClient *http.Client
}

// newOpenAI 创建 OpenAI 客户端。
// 如果未配置 BaseURL，默认用 api.openai.com。
func newOpenAI(cfg ProviderConfig) (Client, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	timeout := cfg.TimeoutSec
	if timeout == 0 {
		timeout = 120
	}
	return &openaiClient{
		cfg:     cfg,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 同步对话：框架 ChatMessage → OpenAI API → 框架 ChatResponse。
func (c *openaiClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	// 1. ★ 框架内部类型 → OpenAI API 格式
	oaiMsgs := make([]openaiMessage, len(messages))
	for i, m := range messages {
		oaiMsgs[i] = openaiMessage{Role: m.Role, Content: m.Content}
	}

	body := openaiChatRequest{
		Model:       c.cfg.Model,
		Messages:    oaiMsgs,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai marshal: %w", err)
	}

	// 2. 发送 HTTP POST
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions",
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// 3. ★ OpenAI API 格式 → 框架内部类型
	var oaiResp openaiChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}

	return &ChatResponse{
		Content:      oaiResp.Choices[0].Message.Content,
		FinishReason: oaiResp.Choices[0].FinishReason,
		Usage: &TokenUsage{
			PromptTokens:     oaiResp.Usage.PromptTokens,
			CompletionTokens: oaiResp.Usage.CompletionTokens,
			TotalTokens:      oaiResp.Usage.TotalTokens,
		},
	}, nil
}

// ChatStream 流式对话：SSE 解析，实时推送 token 片段到 channel。
func (c *openaiClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
	// 1. 构建请求（stream=true）
	oaiMsgs := make([]openaiMessage, len(messages))
	for i, m := range messages {
		oaiMsgs[i] = openaiMessage{Role: m.Role, Content: m.Content}
	}

	body := openaiChatRequest{
		Model:       c.cfg.Model,
		Messages:    oaiMsgs,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
		Stream:      true,
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai stream marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions",
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("openai stream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// 2. 启动后台 goroutine 解析 SSE 流
	ch := make(chan *StreamChunk, 10)
	go c.parseSSEStream(resp, ch)

	return ch, nil
}

// parseSSEStream 解析 Server-Sent Events 流，将 token 片段推入 channel。
//
// SSE 格式：
//
//	data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}
//
//	data: [DONE]
//
// goroutine 会在以下情况关闭 channel：解析到 [DONE]、出现错误、ctx 被取消。
func (c *openaiClient) parseSSEStream(resp *http.Response, ch chan<- *StreamChunk) {
	defer resp.Body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(resp.Body)
	// 增大 buffer：某些模型单行可能很长（如一次返回大段代码）
	scanner.Buffer(make([]byte, 0, 4096), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE 空行是事件分隔符，跳过
		if line == "" {
			continue
		}

		// SSE 注释行（以 : 开头），跳过
		if strings.HasPrefix(line, ":") {
			continue
		}

		// 提取 "data: " 前缀
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}

		// ★ [DONE] 标记流正常结束
		if strings.TrimSpace(data) == "[DONE]" {
			ch <- &StreamChunk{Done: true}
			return
		}

		// 解析 JSON chunk
		var oaiResp openaiChatResponse
		if err := json.Unmarshal([]byte(data), &oaiResp); err != nil {
			ch <- &StreamChunk{
				Done:  true,
				Error: fmt.Errorf("openai stream decode: %w", err),
			}
			return
		}

		if len(oaiResp.Choices) > 0 {
			chunk := &StreamChunk{
				Content: oaiResp.Choices[0].Delta.Content,
			}
			// finish_reason 非空 → 这条是最后一个 chunk
			if oaiResp.Choices[0].FinishReason != "" {
				chunk.Done = true
				ch <- chunk
				return
			}
			ch <- chunk
		}
	}

	// scanner 出错（网络断连等）
	if err := scanner.Err(); err != nil {
		ch <- &StreamChunk{
			Done:  true,
			Error: fmt.Errorf("openai stream read: %w", err),
		}
	}
}

// CountTokens 估算消息的 token 数。
// 当前用字符数/4 的粗略估算，后续可用 tiktoken-go 替代。
func (c *openaiClient) CountTokens(messages []ChatMessage) int {
	return estimateTokens(messages)
}

// Name 返回客户端标识，用于日志和调试。
func (c *openaiClient) Name() string {
	return "openai/" + c.cfg.Model
}

// ==========================================
// 共享工具函数（同包内其他 Provider 也可使用）
// ==========================================

// estimateTokens 粗略估算 token 数。
// 规则：每条消息 ~4 token 格式开销 + 内容每 4 个字符 ≈ 1 token。
// 正式实现可用 tiktoken-go（github.com/pkoukk/tiktoken-go）替代。
func estimateTokens(messages []ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += 4                    // 消息格式开销
		total += len(m.Role) / 4      // role 字段
		total += len(m.Content) / 4   // content 字段
	}
	if total == 0 {
		total = 1 // 至少 1 token
	}
	return total
}
