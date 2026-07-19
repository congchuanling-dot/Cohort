package app

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cohert/internal/contextmgr"
)

// Config 是 Cohert 的运行配置。当前只覆盖命令行 MVP 必需字段。
type Config struct {
	Language  string
	Workspace string
	LogDir    string
	MaxTurns  int
	LLM       LLMConfig
	Context   contextmgr.Config
}

// LLMConfig 描述模型服务配置，当前默认使用 OpenAI-compatible 接口。
type LLMConfig struct {
	Provider              string
	Name                  string
	APIKey                string
	APIBase               string
	Model                 string
	Stream                bool
	ConnectTimeoutSeconds int
	ReadTimeoutSeconds    int
	MaxRetries            int
}

// LoadConfig 读取项目根目录的配置文件，并用环境变量替换 ${VAR}。
// 当前为了保持 MVP 简单，没有引入 YAML 第三方库，只解析本项目需要的简单 key/value。
func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if _, err := os.Stat(path); err != nil {
		// 配置文件不存在时也能运行，使用默认值和环境变量。
		cfg.LLM.APIKey = expandEnv(cfg.LLM.APIKey)
		return finalizeConfig(cfg), nil
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 只识别顶层字段和 llm: 下的一层字段。
		indent := len(line) - len(strings.TrimLeft(line, " "))
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ":") && indent == 0 {
			section = strings.TrimSuffix(line, ":")
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
			applyLLMValue(&cfg.LLM, key, val)
		} else if section == "context" {
			applyContextValue(&cfg.Context, key, val)
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
	return finalizeConfig(cfg), nil
}

func finalizeConfig(cfg Config) Config {
	cfg.Context.ContextWindowTokens = contextmgr.ResolveContextWindowTokens(cfg.LLM.Model)
	return cfg
}

// defaultConfig 给出开箱即用的默认配置。
func defaultConfig() Config {
	return Config{
		Language:  "zh",
		Workspace: "./workspace",
		LogDir:    "./temp/model_responses",
		MaxTurns:  40,
		Context:   contextmgr.DefaultConfig(),
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
	case "enable_micro_compact":
		cfg.EnableMicroCompact = parseBoolDefault(val, cfg.EnableMicroCompact)
	}
}

// applyLLMValue 写入 llm: 配置段里的字段。
func applyLLMValue(cfg *LLMConfig, key, val string) {
	switch key {
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

// expandEnv 支持 ${DEEPSEEK_API_KEY} 这种环境变量写法。
func expandEnv(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return os.Getenv(strings.TrimSuffix(strings.TrimPrefix(v, "${"), "}"))
	}
	return os.ExpandEnv(v)
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
