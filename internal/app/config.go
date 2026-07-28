package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cohort/internal/contextmgr"
)

// Config 是 Cohort 的运行配置。当前只覆盖命令行 MVP 必需字段。
type Config struct {
	// Language 控制系统提示词和默认回复语言。
	Language string
	// Workspace 是文件、命令和截图工具解析相对路径的根目录。
	Workspace string
	// LogDir 是模型原始响应和上下文压缩日志的保存目录。
	LogDir string
	// MaxTurns 限制单次 Agent 任务的最大模型循环次数。
	MaxTurns int
	// LLM 保存模型供应商和连接配置。
	LLM LLMConfig
	// Context 保存请求前上下文压缩和预算配置。
	Context contextmgr.Config
	// Observability 保存本地和外部观测输出配置。
	Observability ObservabilityConfig
}

// LLMConfig 描述模型服务配置。
//
// 旧版配置直接把 provider/api_key/api_base/model 放在 llm: 下；新版配置使用
// active_profile + profiles。为了让旧调用点平滑迁移，finalizeConfig 会把当前
// active profile 回填到旧字段。
type LLMConfig struct {
	// ActiveProfile 是 profiles 中当前启用的模型配置 ID。
	ActiveProfile string
	// FallbackProfiles 是 active profile 失败且尚未产生模型增量时按顺序尝试的备用 profile ID。
	FallbackProfiles []string
	// Profiles 保存多个显式模型/API 配置。
	Profiles map[string]LLMProfile
	// Provider 是旧版单模型配置里的模型供应商标识。
	Provider string
	// Name 是配置展示用的模型服务名称。
	Name string
	// APIKey 是调用模型服务的鉴权密钥，支持从环境变量展开。
	APIKey string
	// APIBase 是 OpenAI-compatible 服务地址。
	APIBase string
	// Model 是默认使用的模型名称。
	Model string
	// Stream 控制是否启用流式输出。
	Stream bool
	// ConnectTimeoutSeconds 是模型请求连接阶段超时秒数。
	ConnectTimeoutSeconds int
	// ReadTimeoutSeconds 是模型请求读取响应阶段超时秒数。
	ReadTimeoutSeconds int
	// MaxRetries 是模型请求遇到可重试错误时的重试次数。
	MaxRetries int
}

// LLMProfile 是一个可独立选择的模型/API 配置。
type LLMProfile struct {
	// ID 是 profile 在配置文件中的 key。
	ID string
	// Provider 是模型协议或供应商类型，例如 openai。
	Provider string
	// Name 是展示名，未配置时默认等于 ID。
	Name string
	// APIKey 是调用模型服务的鉴权密钥，支持环境变量展开。
	APIKey string
	// APIBase 是模型服务基础地址。
	APIBase string
	// Model 是该 profile 使用的模型名称。
	Model string
	// Stream 控制是否启用流式输出。
	Stream bool
	// ConnectTimeoutSeconds 是连接阶段超时秒数。
	ConnectTimeoutSeconds int
	// ReadTimeoutSeconds 是读取响应阶段超时秒数。
	ReadTimeoutSeconds int
	// MaxRetries 是模型请求遇到可重试错误时的重试次数。
	MaxRetries int
}

// ObservabilityConfig 描述外部观测系统配置。本地 run.log.jsonl 始终由 Runner 默认写入。
type ObservabilityConfig struct {
	Langfuse LangfuseConfig
}

// LangfuseConfig 描述 Langfuse ingestion API 配置。
type LangfuseConfig struct {
	Enabled        bool
	Host           string
	PublicKey      string
	SecretKey      string
	Environment    string
	Release        string
	TimeoutSeconds int
}

// LoadConfig 读取项目根目录的配置文件，并用环境变量替换 ${VAR}。
// 当前为了保持 MVP 简单，没有引入 YAML 第三方库，只解析本项目需要的简单 key/value。
func LoadConfig(path string) (Config, error) {
	loadDotEnvFiles(dotEnvCandidatePaths(path))
	cfg := defaultConfig()
	if _, err := os.Stat(path); err != nil {
		// 配置文件不存在时也能运行，使用默认值和环境变量。
		return finalizeConfig(cfg)
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	section := ""
	llmSubsection := ""
	currentProfile := ""
	observabilitySubsection := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 只识别顶层字段、llm/context 一层字段，以及 llm.profiles 下的一层 profile 字段。
		indent := len(line) - len(strings.TrimLeft(line, " "))
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ":") {
			key := strings.TrimSuffix(line, ":")
			if indent == 0 {
				section = key
				llmSubsection = ""
				currentProfile = ""
				observabilitySubsection = ""
				continue
			}
			if section == "llm" && indent == 2 && key == "profiles" {
				llmSubsection = "profiles"
				currentProfile = ""
				if cfg.LLM.Profiles == nil {
					cfg.LLM.Profiles = map[string]LLMProfile{}
				}
				continue
			}
			if section == "llm" && llmSubsection == "profiles" && indent == 4 {
				currentProfile = key
				ensureLLMProfile(&cfg.LLM, currentProfile)
				continue
			}
			if section == "observability" && indent == 2 && key == "langfuse" {
				observabilitySubsection = "langfuse"
				continue
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		val = expandEnv(val)

		if section == "llm" {
			if llmSubsection == "profiles" && currentProfile != "" && indent >= 6 {
				profile := cfg.LLM.Profiles[currentProfile]
				applyLLMProfileValue(&profile, key, val)
				cfg.LLM.Profiles[currentProfile] = profile
			} else {
				applyLLMValue(&cfg.LLM, key, val)
			}
		} else if section == "context" {
			applyContextValue(&cfg.Context, key, val)
		} else if section == "observability" {
			if observabilitySubsection == "langfuse" && indent >= 4 {
				applyLangfuseValue(&cfg.Observability.Langfuse, key, val)
			}
		} else {
			applyRootValue(&cfg, key, val)
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}
	if cfg.Workspace != "" {
		cfg.Workspace = filepath.Clean(cfg.Workspace)
	}
	if cfg.LogDir != "" {
		cfg.LogDir = filepath.Clean(cfg.LogDir)
	}
	return finalizeConfig(cfg)
}

func finalizeConfig(cfg Config) (Config, error) {
	if err := normalizeLLMConfig(&cfg.LLM); err != nil {
		return cfg, err
	}
	cfg.Observability = normalizeObservabilityConfig(cfg.Observability)
	active := cfg.LLM.Active()
	cfg.Context.ContextWindowTokens = contextmgr.ResolveContextWindowTokens(active.Model)
	return cfg, nil
}

// defaultConfig 给出开箱即用的默认配置。
func defaultConfig() Config {
	return Config{
		Language:      "zh",
		Workspace:     "./workspace",
		LogDir:        "./temp/model_responses",
		MaxTurns:      100,
		Context:       contextmgr.DefaultConfig(),
		Observability: defaultObservabilityConfig(),
		LLM: LLMConfig{
			Provider:              "openai",
			Name:                  "deepseek",
			APIKey:                "${DEEPSEEK_API_KEY}",
			APIBase:               "https://api.deepseek.com",
			Model:                 "deepseek-v4-pro",
			Stream:                true,
			ConnectTimeoutSeconds: int((10 * time.Second).Seconds()),
			ReadTimeoutSeconds:    int((120 * time.Second).Seconds()),
			MaxRetries:            2,
		},
	}
}

func defaultObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		Langfuse: LangfuseConfig{
			Enabled:        parseBoolDefault(os.Getenv("COHORT_LANGFUSE_ENABLED"), false),
			Host:           langfuseHostFromEnvironment(),
			PublicKey:      "${LANGFUSE_PUBLIC_KEY}",
			SecretKey:      "${LANGFUSE_SECRET_KEY}",
			Environment:    os.Getenv("COHORT_ENV"),
			Release:        os.Getenv("COHORT_RELEASE"),
			TimeoutSeconds: 2,
		},
	}
}

// Active 返回当前生效的模型配置。调用前配置已经过 normalizeLLMConfig 归一化。
func (cfg LLMConfig) Active() LLMProfile {
	if len(cfg.Profiles) > 0 && cfg.ActiveProfile != "" {
		if profile, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
			return profile
		}
	}
	return LLMProfile{
		ID:                    "default",
		Provider:              cfg.Provider,
		Name:                  cfg.Name,
		APIKey:                cfg.APIKey,
		APIBase:               cfg.APIBase,
		Model:                 cfg.Model,
		Stream:                cfg.Stream,
		ConnectTimeoutSeconds: cfg.ConnectTimeoutSeconds,
		ReadTimeoutSeconds:    cfg.ReadTimeoutSeconds,
		MaxRetries:            cfg.MaxRetries,
	}
}

func normalizeLLMConfig(cfg *LLMConfig) error {
	if len(cfg.Profiles) > 0 {
		if strings.TrimSpace(cfg.ActiveProfile) == "" {
			return fmt.Errorf("llm.active_profile is required when llm.profiles is configured")
		}
		active, ok := cfg.Profiles[cfg.ActiveProfile]
		if !ok {
			return fmt.Errorf("llm.active_profile %q does not exist in llm.profiles", cfg.ActiveProfile)
		}
		cfg.FallbackProfiles = normalizeProfileList(cfg.FallbackProfiles)
		for _, id := range cfg.FallbackProfiles {
			if id == cfg.ActiveProfile {
				return fmt.Errorf("llm.fallback_profiles must not include active profile %q", id)
			}
			if _, ok := cfg.Profiles[id]; !ok {
				return fmt.Errorf("llm.fallback_profiles %q does not exist in llm.profiles", id)
			}
		}
		active = normalizeLLMProfile(active)
		cfg.Profiles[cfg.ActiveProfile] = active
		for id, profile := range cfg.Profiles {
			if id == cfg.ActiveProfile {
				continue
			}
			cfg.Profiles[id] = normalizeLLMProfile(profile)
		}
		copyProfileToLegacyFields(cfg, active)
		return nil
	}

	cfg.Provider = strings.TrimSpace(cfg.Provider)
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = cfg.Model
	}
	cfg.APIKey = expandEnv(cfg.APIKey)
	cfg.APIBase = strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.ConnectTimeoutSeconds <= 0 {
		cfg.ConnectTimeoutSeconds = int((10 * time.Second).Seconds())
	}
	if cfg.ReadTimeoutSeconds <= 0 {
		cfg.ReadTimeoutSeconds = int((120 * time.Second).Seconds())
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	return nil
}

func normalizeLLMProfile(profile LLMProfile) LLMProfile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Provider = strings.TrimSpace(profile.Provider)
	if profile.Provider == "" {
		profile.Provider = "openai"
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	profile.APIKey = expandEnv(profile.APIKey)
	profile.APIBase = strings.TrimRight(strings.TrimSpace(profile.APIBase), "/")
	profile.Model = strings.TrimSpace(profile.Model)
	if profile.ConnectTimeoutSeconds <= 0 {
		profile.ConnectTimeoutSeconds = int((10 * time.Second).Seconds())
	}
	if profile.ReadTimeoutSeconds <= 0 {
		profile.ReadTimeoutSeconds = int((120 * time.Second).Seconds())
	}
	if profile.MaxRetries < 0 {
		profile.MaxRetries = 0
	}
	return profile
}

func copyProfileToLegacyFields(cfg *LLMConfig, profile LLMProfile) {
	cfg.Provider = profile.Provider
	cfg.Name = profile.Name
	cfg.APIKey = profile.APIKey
	cfg.APIBase = profile.APIBase
	cfg.Model = profile.Model
	cfg.Stream = profile.Stream
	cfg.ConnectTimeoutSeconds = profile.ConnectTimeoutSeconds
	cfg.ReadTimeoutSeconds = profile.ReadTimeoutSeconds
	cfg.MaxRetries = profile.MaxRetries
}

func normalizeObservabilityConfig(cfg ObservabilityConfig) ObservabilityConfig {
	cfg.Langfuse.Host = normalizeHostValue(expandEnv(cfg.Langfuse.Host))
	if cfg.Langfuse.Host == "" {
		cfg.Langfuse.Host = normalizeHostValue(langfuseHostFromEnvironment())
	}
	if cfg.Langfuse.Host == "" {
		cfg.Langfuse.Host = "https://cloud.langfuse.com"
	}
	cfg.Langfuse.PublicKey = strings.TrimSpace(expandEnv(cfg.Langfuse.PublicKey))
	cfg.Langfuse.SecretKey = strings.TrimSpace(expandEnv(cfg.Langfuse.SecretKey))
	cfg.Langfuse.Environment = strings.TrimSpace(expandEnv(cfg.Langfuse.Environment))
	cfg.Langfuse.Release = strings.TrimSpace(expandEnv(cfg.Langfuse.Release))
	if cfg.Langfuse.TimeoutSeconds <= 0 {
		cfg.Langfuse.TimeoutSeconds = 2
	}
	return cfg
}

// applyRootValue 写入顶层配置字段。
func applyRootValue(cfg *Config, key, val string) {
	switch key {
	case "language":
		cfg.Language = val
	case "workspace":
		cfg.Workspace = val
	case "log_dir":
		cfg.LogDir = val
	case "max_turns":
		cfg.MaxTurns = atoiDefault(val, cfg.MaxTurns)
	}
}

func applyContextValue(cfg *contextmgr.Config, key, val string) {
	switch key {
	case "max_history_messages":
		cfg.MaxHistoryMessages = atoiDefault(val, cfg.MaxHistoryMessages)
	case "keep_recent_tool_results":
		cfg.KeepRecentToolResults = atoiDefault(val, cfg.KeepRecentToolResults)
	case "max_tool_result_chars":
		cfg.MaxToolResultChars = atoiDefault(val, cfg.MaxToolResultChars)
	case "compacted_tool_head_chars":
		cfg.CompactedToolHeadChars = atoiDefault(val, cfg.CompactedToolHeadChars)
	case "compacted_tool_tail_chars":
		cfg.CompactedToolTailChars = atoiDefault(val, cfg.CompactedToolTailChars)
	case "max_request_chars":
		cfg.MaxRequestChars = atoiDefault(val, cfg.MaxRequestChars)
	case "max_session_memory_chars":
		cfg.MaxSessionMemoryChars = atoiDefault(val, cfg.MaxSessionMemoryChars)
	case "max_memory_index_chars":
		cfg.MaxMemoryIndexChars = atoiDefault(val, cfg.MaxMemoryIndexChars)
	case "max_relevant_memory_chars":
		cfg.MaxRelevantMemoryChars = atoiDefault(val, cfg.MaxRelevantMemoryChars)
	case "max_relevant_memory_entries", "max_relevant_memory_files":
		cfg.MaxRelevantMemoryEntries = atoiDefault(val, cfg.MaxRelevantMemoryEntries)
	case "max_compact_summary_chars":
		cfg.MaxCompactSummaryChars = atoiDefault(val, cfg.MaxCompactSummaryChars)
	case "enable_micro_compact":
		cfg.EnableMicroCompact = parseBoolDefault(val, cfg.EnableMicroCompact)
	}
}

// applyLLMValue 写入 llm: 配置段里的字段。
func applyLLMValue(cfg *LLMConfig, key, val string) {
	switch key {
	case "active_profile":
		cfg.ActiveProfile = val
	case "fallback_profiles":
		cfg.FallbackProfiles = parseStringList(val)
	case "provider":
		cfg.Provider = val
	case "name":
		cfg.Name = val
	case "api_key":
		cfg.APIKey = val
	case "api_base":
		cfg.APIBase = strings.TrimRight(val, "/")
	case "model":
		cfg.Model = val
	case "stream":
		cfg.Stream = parseBoolDefault(val, cfg.Stream)
	case "connect_timeout_seconds":
		cfg.ConnectTimeoutSeconds = atoiDefault(val, cfg.ConnectTimeoutSeconds)
	case "read_timeout_seconds":
		cfg.ReadTimeoutSeconds = atoiDefault(val, cfg.ReadTimeoutSeconds)
	case "max_retries":
		cfg.MaxRetries = atoiDefault(val, cfg.MaxRetries)
	}
}

func applyLangfuseValue(cfg *LangfuseConfig, key, val string) {
	switch key {
	case "enabled":
		cfg.Enabled = parseBoolDefault(val, cfg.Enabled)
	case "host":
		cfg.Host = normalizeHostValue(val)
	case "public_key":
		cfg.PublicKey = val
	case "secret_key":
		cfg.SecretKey = val
	case "environment":
		cfg.Environment = val
	case "release":
		cfg.Release = val
	case "timeout_seconds":
		cfg.TimeoutSeconds = atoiDefault(val, cfg.TimeoutSeconds)
	}
}

func parseStringList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.Trim(strings.TrimSpace(part), `"'`)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func normalizeProfileList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func ensureLLMProfile(cfg *LLMConfig, id string) {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]LLMProfile{}
	}
	if _, ok := cfg.Profiles[id]; ok {
		return
	}
	cfg.Profiles[id] = LLMProfile{
		ID:                    id,
		Provider:              "openai",
		Name:                  id,
		Stream:                true,
		ConnectTimeoutSeconds: int((10 * time.Second).Seconds()),
		ReadTimeoutSeconds:    int((120 * time.Second).Seconds()),
		MaxRetries:            2,
	}
}

// applyLLMProfileValue 写入 llm.profiles.<id> 下的字段。
func applyLLMProfileValue(profile *LLMProfile, key, val string) {
	switch key {
	case "provider":
		profile.Provider = val
	case "name":
		profile.Name = val
	case "api_key":
		profile.APIKey = val
	case "api_base":
		profile.APIBase = strings.TrimRight(val, "/")
	case "model":
		profile.Model = val
	case "stream":
		profile.Stream = parseBoolDefault(val, profile.Stream)
	case "connect_timeout_seconds":
		profile.ConnectTimeoutSeconds = atoiDefault(val, profile.ConnectTimeoutSeconds)
	case "read_timeout_seconds":
		profile.ReadTimeoutSeconds = atoiDefault(val, profile.ReadTimeoutSeconds)
	case "max_retries":
		profile.MaxRetries = atoiDefault(val, profile.MaxRetries)
	}
}

// expandEnv 支持 ${DEEPSEEK_API_KEY} 这种环境变量写法。
func expandEnv(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return os.Getenv(strings.TrimSuffix(strings.TrimPrefix(v, "${"), "}"))
	}
	return os.ExpandEnv(v)
}

func langfuseHostFromEnvironment() string {
	if host := normalizeHostValue(os.Getenv("LANGFUSE_HOST")); host != "" {
		return host
	}
	return normalizeHostValue(os.Getenv("LANGFUSE_BASE_URL"))
}

func normalizeHostValue(value string) string {
	value = strings.TrimSpace(value)
	for _, cutset := range []string{`"'`, "`"} {
		value = strings.Trim(value, cutset)
		value = strings.TrimSpace(value)
	}
	return strings.TrimRight(value, "/")
}

func dotEnvCandidatePaths(configPath string) []string {
	var paths []string
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, ".env"))
	}
	if trimmed := strings.TrimSpace(configPath); trimmed != "" {
		paths = append(paths, filepath.Join(filepath.Dir(filepath.Clean(trimmed)), ".env"))
	}
	seen := make(map[string]bool, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, path)
	}
	return unique
}

func loadDotEnvFiles(paths []string) {
	for _, path := range paths {
		loadDotEnvFile(path)
	}
}

func loadDotEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value := parseDotEnvValue(parts[1])
		_ = os.Setenv(key, value)
	}
	_ = scanner.Err()
}

func parseDotEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`)) {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

// atoiDefault 解析失败时返回已有默认值，避免配置写错导致零值覆盖。
func atoiDefault(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// parseBoolDefault 支持常见布尔写法，解析失败时保留默认值。
func parseBoolDefault(v string, fallback bool) bool {
	switch strings.ToLower(v) {
	case "true", "yes", "1", "on":
		return true
	case "false", "no", "0", "off":
		return false
	default:
		return fallback
	}
}
