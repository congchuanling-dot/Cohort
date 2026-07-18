package app

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Language  string
	Workspace string
	LogDir    string
	MaxTurns  int
	LLM       LLMConfig
}

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

func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if _, err := os.Stat(path); err != nil {
		cfg.LLM.APIKey = expandEnv(cfg.LLM.APIKey)
		return cfg, nil
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
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Language:  "zh",
		Workspace: "./workspace",
		LogDir:    "./temp/model_responses",
		MaxTurns:  40,
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

func expandEnv(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return os.Getenv(strings.TrimSuffix(strings.TrimPrefix(v, "${"), "}"))
	}
	return os.ExpandEnv(v)
}

func atoiDefault(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

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
