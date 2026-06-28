package llm

import (
	"net/http"
	"strings"
	"time"
)

func init() {
	Register("deepseek", newDeepSeek)
}

// deepseekClient 通过组合 *openaiClient 复用 OpenAI 兼容的 Chat/ChatStream 逻辑。
// DeepSeek API 与 OpenAI 完全兼容（路径 /chat/completions，认证 Bearer token），
// 唯一的区别是默认 BaseURL 和 Name() 标识。
type deepseekClient struct {
	*openaiClient
}

// newDeepSeek 创建 DeepSeek 客户端。
// ★ DeepSeek base_url 是 https://api.deepseek.com（无 /v1），
// 路径直接是 /chat/completions，即 https://api.deepseek.com/chat/completions
func newDeepSeek(cfg ProviderConfig) (Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com"
	} else {
		cfg.BaseURL = trimChatPath(cfg.BaseURL)
	}
	timeout := cfg.TimeoutSec
	if timeout == 0 {
		timeout = 120
	}
	return &deepseekClient{
		openaiClient: &openaiClient{
			cfg:     cfg,
			baseURL: cfg.BaseURL,
			httpClient: &http.Client{
				Timeout: time.Duration(timeout) * time.Second,
			},
		},
	}, nil
}

// Name 返回 DeepSeek 的标识，覆盖嵌入的 openaiClient.Name()。
func (c *deepseekClient) Name() string {
	return "deepseek/" + c.cfg.Model
}

// trimChatPath 截掉 BaseURL 中已有的 API 路径（DeepSeek 专用）。
//
// DeepSeek base_url = https://api.deepseek.com（无 /v1），
// 路径是 /chat/completions，所以需要去掉可能误配的 /v1。
//
//	"https://api.deepseek.com/v1/chat/completions" → "https://api.deepseek.com"
//	"https://api.deepseek.com/chat/completions"    → "https://api.deepseek.com"
func trimChatPath(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return baseURL
}
