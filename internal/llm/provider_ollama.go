package llm

import (
	"net/http"
	"time"
)

func init() {
	Register("ollama", newOllama)
}

// ollamaClient 通过组合 *openaiClient 复用 OpenAI 兼容的 Chat/ChatStream 逻辑。
// Ollama v0.5+ 支持 OpenAI 兼容端点，默认在 localhost:11434 运行。
type ollamaClient struct {
	*openaiClient
}

func newOllama(cfg ProviderConfig) (Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434/v1" // Ollama 默认本地地址
	}
	timeout := cfg.TimeoutSec
	if timeout == 0 {
		timeout = 300 // 本地模型推理较慢，默认 5 分钟
	}
	return &ollamaClient{
		openaiClient: &openaiClient{
			cfg:     cfg,
			baseURL: cfg.BaseURL,
			httpClient: &http.Client{
				Timeout: time.Duration(timeout) * time.Second,
			},
		},
	}, nil
}

// Name 返回 Ollama 的标识。
func (c *ollamaClient) Name() string {
	return "ollama/" + c.cfg.Model
}
