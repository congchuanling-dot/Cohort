// Package llm 提供 LLM 调用的统一抽象层。
// 上层代码（Action/Role）只依赖 Client 接口，
// 完全不知道底层是 OpenAI 还是 DeepSeek 还是 Claude。
package llm

// ProviderType 枚举所有内置的 LLM 提供商类型。
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"    // OpenAI（GPT-4o, GPT-4.1 等）
	ProviderDeepSeek  ProviderType = "deepseek"  // DeepSeek（deepseek-chat 等）
	ProviderAnthropic ProviderType = "anthropic" // Anthropic Claude（Opus, Sonnet, Haiku）
	ProviderOllama    ProviderType = "ollama"    // Ollama 本地模型
	ProviderCustom    ProviderType = "custom"    // ★ 万能兜底：任意 OpenAI 兼容 API
)

// String 返回 Provider 的字符串标识。
func (p ProviderType) String() string {
	return string(p)
}

// ChatMessage 的 Role 常量。
// 对应 OpenAI/DeepSeek 等 API 的标准角色，避免散落魔法字符串。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)
