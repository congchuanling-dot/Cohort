package foundation

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ==========================================
// 配置结构体（带 YAML 标签）
// ==========================================

// Config 全局配置，聚合所有子配置。
// 支持 YAML 加载 + 环境变量占位符 + 环境变量覆盖。
type Config struct {
	LLM       LLMConfig         `yaml:"llm"`       // 第 1 层：全局默认 LLM 配置（最低优先级）
	Roles     RolesLLMConfig    `yaml:"roles"`     // 第 2 层：按 Role 名称覆盖 LLM 配置
	Actions   ActionsLLMConfig  `yaml:"actions"`   // 第 3 层：按 Action 名称覆盖 LLM 配置（最高优先级）
	Agent     AgentConfig       `yaml:"agent"`     // Agent 运行参数
	Workspace WorkspaceConfig   `yaml:"workspace"` // 工作区配置
}

// LLMConfig LLM 调用配置（单一 Provider + 模型）。
// 这是"可继承的配置单元"——只填想覆盖的字段，其余自动继承。
type LLMConfig struct {
	Provider    string            `yaml:"provider"`              // openai / deepseek / anthropic / ollama / custom
	Model       string            `yaml:"model"`                 // 模型名称，如 deepseek-chat
	APIKey      string            `yaml:"api_key"`               // API 密钥（支持 ${ENV_VAR} 语法）
	BaseURL     string            `yaml:"base_url"`              // API 基础地址（空 = 使用 Provider 默认值）
	Temperature float64           `yaml:"temperature"`           // 温度参数，0.0-2.0
	MaxTokens   int               `yaml:"max_tokens"`            // 最大输出 token 数
	TimeoutSec  int               `yaml:"timeout_seconds"`       // 请求超时（秒）
	MaxRetries  int               `yaml:"max_retries"`           // 最大重试次数
	Extra       map[string]string `yaml:"extra,omitempty"`       // Provider 专属配置（如 anthropic_version）
}

// RolesLLMConfig 按 Role 名称覆盖 LLM 配置（第 2 层优先级）。
// key = Role 名称（如 "Alex"、"Edward"），只填想覆盖的字段。
type RolesLLMConfig map[string]*LLMConfig

// ActionsLLMConfig 按 Action 名称覆盖 LLM 配置（第 3 层，最高优先级）。
// key = Action 名称（如 "WriteCode"、"WritePRD"），只填想覆盖的字段。
type ActionsLLMConfig map[string]*LLMConfig

// AgentConfig Agent 运行时参数。
type AgentConfig struct {
	MaxReactLoop  int     `yaml:"max_react_loop"`   // 最大 ReAct 循环次数
	MaxBudgetUSD  float64 `yaml:"max_budget_usd"`   // 最大预算（美元）
	MemoryMaxSize int     `yaml:"memory_max_size"`  // Memory 容量上限
}

// WorkspaceConfig 工作区配置。
type WorkspaceConfig struct {
	Path string `yaml:"path"` // 输出目录
}

// ==========================================
// 加载——优先级：环境变量 > YAML 文件 > 默认值
// ==========================================

// Load 从 YAML 文件加载配置。
//
// 加载优先级链:
//  1. 从 DefaultConfig() 开始（兜底）
//  2. 读取 YAML 文件，展开 ${ENV_VAR} 占位符
//  3. YAML 覆盖默认值
//  4. 环境变量覆盖（最高优先级）
//
// 所以最终优先级: 环境变量 > config.yaml > 默认值
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	// 展开 ${VAR} 占位符（如 api_key: ${DEEPSEEK_API_KEY}）
	expanded := os.ExpandEnv(string(data))

	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}

	// 环境变量覆盖（最高优先级）
	cfg.ApplyEnvOverrides()

	return cfg, nil
}

// MustLoad 同 Load，但解析失败时 panic（用于启动阶段）。
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
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
func (c *Config) ApplyEnvOverrides() {
	// === COHORT_ 前缀（最高优先级）===
	if v := os.Getenv("COHORT_LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	} else if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
		// 回退：DEEPSEEK_API_KEY（用户已有的环境变量）
		c.LLM.APIKey = v
		c.LLM.Provider = "deepseek"
	}
	if v := os.Getenv("COHORT_LLM_PROVIDER"); v != "" {
		c.LLM.Provider = v
	}
	if v := os.Getenv("COHORT_LLM_MODEL"); v != "" {
		c.LLM.Model = v
	} else if v := os.Getenv("DEEPSEEK_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if v := os.Getenv("COHORT_LLM_BASE_URL"); v != "" {
		c.LLM.BaseURL = v
	} else if v := os.Getenv("DEEPSEEK_API_URL"); v != "" {
		c.LLM.BaseURL = v
	}
}
