package foundation

import "os"

// Config 全局配置，聚合所有子配置。
// 后续阶段会加入 YAML 加载、环境变量覆盖等功能。
type Config struct {
	LLM       LLMConfig         // 第 1 层：全局默认 LLM 配置（最低优先级）
	Roles     RolesLLMConfig    // 第 2 层：按 Role 名称覆盖 LLM 配置
	Actions   ActionsLLMConfig  // 第 3 层：按 Action 名称覆盖 LLM 配置（最高优先级）
	Agent     AgentConfig       // Agent 运行参数
	Workspace WorkspaceConfig   // 工作区配置
}

// LLMConfig LLM 调用配置（单一 Provider + 模型）。
// 这是"可继承的配置单元"——只填想覆盖的字段，其余自动继承。
type LLMConfig struct {
	Provider    string  // openai / deepseek / anthropic / ollama / custom
	Model       string  // 模型名称，如 deepseek-chat
	APIKey      string  // API 密钥（支持 ${ENV_VAR} 语法）
	BaseURL     string  // API 基础地址（空 = 使用 Provider 默认值）
	Temperature float64 // 温度参数，0.0-2.0
	MaxTokens   int     // 最大输出 token 数
	TimeoutSec  int     // 请求超时（秒）
	MaxRetries  int     // 最大重试次数
}

// RolesLLMConfig 按 Role 名称覆盖 LLM 配置（第 2 层优先级）。
// key = Role 名称（如 "Alex"、"Edward"），只填想覆盖的字段。
type RolesLLMConfig map[string]*LLMConfig

// ActionsLLMConfig 按 Action 名称覆盖 LLM 配置（第 3 层，最高优先级）。
// key = Action 名称（如 "WriteCode"、"WritePRD"），只填想覆盖的字段。
type ActionsLLMConfig map[string]*LLMConfig

// AgentConfig Agent 运行时参数。
type AgentConfig struct {
	MaxReactLoop  int     // 最大 ReAct 循环次数
	MaxBudgetUSD  float64 // 最大预算（美元）
	MemoryMaxSize int     // Memory 容量上限
}

// WorkspaceConfig 工作区配置。
type WorkspaceConfig struct {
	Path string // 输出目录
}

// DefaultConfig 返回默认配置（零外部依赖的兜底值）。
func DefaultConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			Provider:    "deepseek",
			Model:       "deepseek-v4-pro",
			Temperature: 0.3,
			MaxTokens:   4096,
			TimeoutSec:  120,
			MaxRetries:  3,
		},
		Agent: AgentConfig{
			MaxReactLoop:  10,
			MaxBudgetUSD:  5.0,
			MemoryMaxSize: 100,
		},
		Workspace: WorkspaceConfig{
			Path: "./workspace",
		},
	}
}

// applyEnvOverrides 用环境变量覆盖配置（12-factor 原则）。
// 当前阶段仅覆盖 API Key，后续阶段会扩展为通用机制。
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("COHORT_LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("COHORT_LLM_PROVIDER"); v != "" {
		c.LLM.Provider = v
	}
	if v := os.Getenv("COHORT_LLM_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if v := os.Getenv("COHORT_LLM_BASE_URL"); v != "" {
		c.LLM.BaseURL = v
	}
}
