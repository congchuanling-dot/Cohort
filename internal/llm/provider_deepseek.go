package llm

import (
	"net/http"
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
// 默认 BaseURL = https://api.deepseek.com/v1，其余与 OpenAI 完全一致。
func newDeepSeek(cfg ProviderConfig) (Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
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
