package llm

import (
	"fmt"
	"cohort/internal/foundation"
	"sync"
)

// ==========================================
// ProviderConfig —— 传给 Provider 工厂的配置
// ==========================================

// ProviderConfig 是每个 Provider 工厂收到的一份"完整配置"。
// 字段含义由各 Provider 自行解释，registry 不做假设。
// 它由 LLMResolver 从 foundation.Config（三级继承）解析得到。
type ProviderConfig struct {
	Provider    string            // ★ Provider 名称（openai/deepseek/anthropic/ollama/custom）
	Model       string            // 模型名称
	APIKey      string            // API 密钥
	BaseURL     string            // API 基础地址（空 = 使用 Provider 默认值）
	Temperature float64           // 温度参数
	MaxTokens   int               // 最大输出 token 数
	TimeoutSec  int               // 请求超时（秒）
	Extra       map[string]string // ★ Provider 专属参数（如 anthropic_version）
}

// ProviderFactory 创建 Client 的工厂函数。
// 每个 Provider 在 init() 中通过 Register 注册自己的工厂。
type ProviderFactory func(cfg ProviderConfig) (Client, error)

// ==========================================
// Provider 注册表 —— init() 自动注册，不改代码就能加新的
// ==========================================

var (
	registryMu  sync.RWMutex
	registryMap = make(map[string]ProviderFactory)
)

// Register 注册一个 Provider 工厂。
// 各 provider_xxx.go 的 init() 中调用此函数。
// 重复注册同名 Provider 会 panic（阻止开发阶段隐式覆盖）。
func Register(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registryMap[name]; exists {
		panic(fmt.Sprintf("llm: provider %q already registered", name))
	}
	registryMap[name] = factory
}

// NewClient 通过 Provider 名称 + 配置创建 Client。
// 这是框架创建 LLM 客户端的唯一入口——上层不需要知道具体类型。
func NewClient(provider string, cfg ProviderConfig) (Client, error) {
	registryMu.RLock()
	factory, ok := registryMap[provider]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown provider: %q (available: %v)", provider, AvailableProviders())
	}
	return factory(cfg)
}

// AvailableProviders 列出所有已注册的 Provider 名称。
func AvailableProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registryMap))
	for name := range registryMap {
		names = append(names, name)
	}
	return names
}

// ==========================================
// LLMResolver —— 三级配置继承：Action > Role > 全局
// ==========================================

// LLMResolver 根据 RoleName + ActionName 解析最终的 Provider + Config。
//
// 三级优先级（从高到低）：
//  1. Actions 覆盖 — 如 WriteCode 强制用 Claude Opus
//  2. Roles 覆盖   — 如 Alex（Engineer）整体用 Claude
//  3. 全局默认      — 兜底，如 deepseek-chat
//
// 每层只覆盖已设置的字段，其余继承下一层（字段级 merge）。
type LLMResolver struct {
	defaultCfg      ProviderConfig           // 第 1 层：全局默认
	roleOverrides   map[string]ProviderConfig // 第 2 层：按 Role
	actionOverrides map[string]ProviderConfig // 第 3 层：按 Action（最高优先级）
}

// NewLLMResolver 从 foundation.Config 构造解析器。
// 将 foundation.LLMConfig 转换为 llm.ProviderConfig 以便 resolve。
func NewLLMResolver(cfg *foundation.Config) *LLMResolver {
	r := &LLMResolver{
		roleOverrides:   make(map[string]ProviderConfig),
		actionOverrides: make(map[string]ProviderConfig),
	}
	r.defaultCfg = llmConfigToProviderConfig(&cfg.LLM)

	for roleName, override := range cfg.Roles {
		r.roleOverrides[roleName] = llmConfigToProviderConfig(override)
	}
	for actionName, override := range cfg.Actions {
		r.actionOverrides[actionName] = llmConfigToProviderConfig(override)
	}
	return r
}

// Resolve 为指定 Role + Action 解析最终的 Provider + Config。
//
// 典型调用：
//
//	providerName, cfg := resolver.Resolve("Alex", "WriteCode")
//	client, _ := llm.NewClient(providerName, cfg)
//
// 解析流程：
//  1. 从全局默认开始
//  2. 如果该 Role 有覆盖，merge 进去
//  3. 如果该 Action 有覆盖，merge 进去（最高优先级）
func (r *LLMResolver) Resolve(roleName, actionName string) (string, ProviderConfig) {
	result := r.defaultCfg

	// 第 2 层：Role 覆盖
	if roleCfg, ok := r.roleOverrides[roleName]; ok {
		result = r.merge(result, roleCfg)
	}

	// 第 3 层：Action 覆盖（最高优先级）
	if actionCfg, ok := r.actionOverrides[actionName]; ok {
		result = r.merge(result, actionCfg)
	}

	return result.Provider, result
}

// merge 用 override 中非零值覆盖 base 中对应字段。
// 字段级细粒度合并：只覆盖 override 中已设置（非零值）的字段。
func (r *LLMResolver) merge(base, override ProviderConfig) ProviderConfig {
	if override.Provider != "" {
		base.Provider = override.Provider
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.APIKey != "" {
		base.APIKey = override.APIKey
	}
	if override.BaseURL != "" {
		base.BaseURL = override.BaseURL
	}
	if override.Temperature != 0 {
		base.Temperature = override.Temperature
	}
	if override.MaxTokens != 0 {
		base.MaxTokens = override.MaxTokens
	}
	if override.TimeoutSec != 0 {
		base.TimeoutSec = override.TimeoutSec
	}
	for k, v := range override.Extra {
		if base.Extra == nil {
			base.Extra = make(map[string]string)
		}
		base.Extra[k] = v
	}
	return base
}

// ==========================================
// 类型转换辅助
// ==========================================

// llmConfigToProviderConfig 将 foundation 的 LLMConfig 转换为 llm 的 ProviderConfig。
func llmConfigToProviderConfig(cfg *foundation.LLMConfig) ProviderConfig {
	if cfg == nil {
		return ProviderConfig{}
	}
	return ProviderConfig{
		Provider:    cfg.Provider,
		Model:       cfg.Model,
		APIKey:      cfg.APIKey,
		BaseURL:     cfg.BaseURL,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		TimeoutSec:  cfg.TimeoutSec,
		Extra:       cfg.Extra,
	}
}
