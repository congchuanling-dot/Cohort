package llm

import (
	"fmt"
	"strings"
	"time"
)

// ProviderConfig 是应用层传给 LLM provider factory 的归一化配置。
type ProviderConfig struct {
	// ProfileID 是配置文件中的 profile 名，用于错误提示。
	ProfileID string
	// Provider 是模型协议或供应商类型，例如 openai。
	Provider string
	// Name 是展示名，主要用于日志和排查。
	Name string
	// APIKey 是访问模型服务的鉴权密钥。
	APIKey string
	// APIBase 是模型服务基础地址。
	APIBase string
	// Model 是请求使用的模型名称。
	Model string
	// Stream 控制是否使用流式响应。
	Stream bool
	// ConnectTimeout 是连接阶段超时预算。
	ConnectTimeout time.Duration
	// ReadTimeout 是读取响应阶段超时预算。
	ReadTimeout time.Duration
	// MaxRetries 是可重试错误的最大重试次数。
	MaxRetries int
}

// NewClient 根据 provider 创建具体模型客户端。
func NewClient(cfg ProviderConfig) (Client, error) {
	switch normalizeProvider(cfg.Provider) {
	case "openai":
		return NewOpenAIClient(OpenAIConfig{
			Name:           cfg.Name,
			APIKey:         cfg.APIKey,
			APIBase:        cfg.APIBase,
			Model:          cfg.Model,
			Stream:         cfg.Stream,
			ConnectTimeout: cfg.ConnectTimeout,
			ReadTimeout:    cfg.ReadTimeout,
			MaxRetries:     cfg.MaxRetries,
		}), nil
	case "anthropic":
		return NewAnthropicClient(AnthropicConfig{
			Name:           cfg.Name,
			APIKey:         cfg.APIKey,
			APIBase:        cfg.APIBase,
			Model:          cfg.Model,
			Stream:         cfg.Stream,
			ConnectTimeout: cfg.ConnectTimeout,
			ReadTimeout:    cfg.ReadTimeout,
			MaxRetries:     cfg.MaxRetries,
		}), nil
	default:
		profile := strings.TrimSpace(cfg.ProfileID)
		if profile == "" {
			profile = cfg.Name
		}
		return nil, fmt.Errorf("unsupported llm provider %q for profile %q", cfg.Provider, profile)
	}
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai", "openai-compatible", "openai_compatible":
		return "openai"
	case "anthropic", "claude":
		return "anthropic"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}
