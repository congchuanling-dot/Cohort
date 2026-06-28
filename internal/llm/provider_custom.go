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
	Register("custom", newCustom)
}

// ==========================================
// Custom Provider —— 适配任意 OpenAI 兼容 API
// ==========================================
//
// 设计目标：用户不改一行 Go 代码，只需在 YAML 里配：
//
//	llm:
//	  provider: custom
//	  base_url: https://my-proxy.com/v1
//	  extra:
//	    auth_header: "X-API-Key"       ← 自定义认证头名称
//	    auth_prefix: "Bearer"          ← 可选，默认 "Bearer"
//	    chat_path: "/chat/completions"  ← 可选
//
// 常见使用场景：
//   - 自建 API 代理 / 网关（如 one-api、LiteLLM）
//   - Cloudflare AI Gateway
//   - 其他 OpenAI 兼容的第三方服务

type customClient struct {
	cfg        ProviderConfig
	baseURL    string
	authHeader string // 认证头名称
	authPrefix string // 认证头前缀（"Bearer" / "Token" / 空）
	chatPath   string // API 路径
	httpClient *http.Client
}

func newCustom(cfg ProviderConfig) (Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("custom provider requires base_url in config")
	}

	authHeader := cfg.Extra["auth_header"]
	if authHeader == "" {
		authHeader = "Authorization" // 标准 HTTP 认证头
	}
	authPrefix := cfg.Extra["auth_prefix"]
	if authPrefix == "" {
		authPrefix = "Bearer"
	}
	chatPath := cfg.Extra["chat_path"]
	if chatPath == "" {
		chatPath = "/chat/completions"
	}
	timeout := cfg.TimeoutSec
	if timeout == 0 {
		timeout = 120
	}

	return &customClient{
		cfg:        cfg,
		baseURL:    cfg.BaseURL,
		authHeader: authHeader,
		authPrefix: authPrefix,
		chatPath:   chatPath,
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 使用 OpenAI 兼容格式调用用户配置的端点。
// 请求格式与 OpenAI 一致（绝大多数自建 API 都是 OpenAI 兼容的）。
func (c *customClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	body := map[string]interface{}{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": c.cfg.Temperature,
		"max_tokens":  c.cfg.MaxTokens,
	}

	reqBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+c.chatPath,
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom provider (%s): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("custom provider HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// 解析 OpenAI 兼容响应（匿名 struct）
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *TokenUsage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("custom provider decode: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("custom provider: empty response")
	}

	return &ChatResponse{
		Content:      result.Choices[0].Message.Content,
		FinishReason: result.Choices[0].FinishReason,
		Usage:        result.Usage,
	}, nil
}

// ChatStream SSE 流式解析。
func (c *customClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
	body := map[string]interface{}{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": c.cfg.Temperature,
		"max_tokens":  c.cfg.MaxTokens,
		"stream":      true,
	}

	reqBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+c.chatPath,
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom provider stream (%s): %w", c.baseURL, err)
	}

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("custom provider stream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan *StreamChunk, 10)
	// 复用 OpenAI 的 SSE 解析（custom 使用兼容格式）
	go parseOpenAISSE(resp, ch)
	return ch, nil
}

func (c *customClient) CountTokens(messages []ChatMessage) int {
	return estimateTokens(messages)
}

func (c *customClient) Name() string {
	return "custom/" + c.cfg.Model + " @ " + c.baseURL
}

// setAuth 根据用户配置设置认证头。
func (c *customClient) setAuth(req *http.Request) {
	if c.cfg.APIKey == "" {
		return
	}
	if c.authPrefix != "" {
		req.Header.Set(c.authHeader, c.authPrefix+" "+c.cfg.APIKey)
	} else {
		req.Header.Set(c.authHeader, c.cfg.APIKey)
	}
}

// ==========================================
// 共享 SSE 解析器（OpenAI 兼容格式）
// 供 custom、ollama 等复用
// ==========================================

// parseOpenAISSE 解析 OpenAI 兼容的 SSE 流。
//
// goroutine 负责：逐行读取 SSE，解析 JSON chunk，推入 channel。
// 三种结束条件（都会先发 Done=true，再 close(ch)）：
//  1. 收到 "data: [DONE]" —— 标准 OpenAI 结束标记
//  2. finish_reason 非空 —— 最后一条有效 chunk
//  3. 流读完后自然关闭 —— 连接关闭，无 [DONE] 也无 finish_reason
func parseOpenAISSE(resp *http.Response, ch chan<- *StreamChunk) {
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
		// 兼容 "data: " 和 "data:" 两种 SSE 前缀格式
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if strings.TrimSpace(data) == "[DONE]" {
			ch <- &StreamChunk{Done: true}
			return
		}

		var oaiResp struct {
			Choices []struct {
				Delta        struct{ Content string `json:"content"` } `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &oaiResp); err != nil {
			ch <- &StreamChunk{Done: true, Error: fmt.Errorf("sse decode: %w", err)}
			return
		}
		if len(oaiResp.Choices) > 0 {
			chunk := &StreamChunk{Content: oaiResp.Choices[0].Delta.Content}
			if oaiResp.Choices[0].FinishReason != "" {
				chunk.Done = true
				ch <- chunk
				return
			}
			// 跳过空内容帧（某些 API 在最后一个 finish_reason chunk 之后还会发空 content）
			if chunk.Content != "" {
				ch <- chunk
			}
		}
	}

	// 流自然结束（连接关闭）：有些 API 不发 [DONE] 也不发 finish_reason
	if err := scanner.Err(); err != nil {
		ch <- &StreamChunk{Done: true, Error: fmt.Errorf("sse stream read: %w", err)}
	} else {
		ch <- &StreamChunk{Done: true}
	}
}
