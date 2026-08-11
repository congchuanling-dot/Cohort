package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ProfileDeepSeek 是默认 DeepSeek OpenAI-compatible profile。
	ProfileDeepSeek = "deepseek"
	// ProfileLocal 是本地 OpenAI-compatible profile。
	ProfileLocal = "local"
	// ProfileClaude 是 Anthropic Messages API profile。
	ProfileClaude = "claude"
)

// InitConfigOptions 控制默认配置文件初始化。
type InitConfigOptions struct {
	// Path 是要写入的配置文件路径。
	Path string
	// ActiveProfile 是默认激活的 profile。
	ActiveProfile string
	// Force 允许覆盖已有配置。
	Force bool
}

// WriteDefaultConfig 写入一份用户级默认配置模板。
func WriteDefaultConfig(opts InitConfigOptions) error {
	path := filepath.Clean(strings.TrimSpace(opts.Path))
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	if _, err := os.Stat(path); err == nil && !opts.Force {
		return fmt.Errorf("config already exists: %s (use --force to overwrite)", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	workspace, logDir := defaultUserRuntimePaths(path)
	content, err := DefaultConfigContent(workspace, logDir, opts.ActiveProfile)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// DefaultConfigContent 返回默认配置文件正文。
func DefaultConfigContent(workspace string, logDir string, activeProfile string) (string, error) {
	activeProfile = normalizeInitProfile(activeProfile)
	if activeProfile == "" {
		activeProfile = ProfileDeepSeek
	}
	if !validInitProfile(activeProfile) {
		return "", fmt.Errorf("unsupported init profile %q, want one of: deepseek, local, claude", activeProfile)
	}
	return fmt.Sprintf(`language: zh
workspace: "%s"
log_dir: "%s"
max_turns: 300

tools:
  # Register the full tool surface; Component Map still reports runtime readiness separately.
  enabled_groups: [*]

reflection:
  # SessionEnd only enqueues lightweight metadata; reflection runs in the local daemon.
  auto_enqueue: true
  debounce_seconds: 30
  max_attempts: 3

llm:
  active_profile: %s
  # fallback_profiles: [local]
  profiles:
    deepseek:
      provider: openai
      name: deepseek
      api_key: ${DEEPSEEK_API_KEY}
      api_base: https://api.deepseek.com
      model: deepseek-v4-pro
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 2

    local:
      provider: openai
      name: local
      api_key: ${LOCAL_OPENAI_API_KEY}
      api_base: http://127.0.0.1:11434/v1
      model: qwen3-coder
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 1

    claude:
      provider: anthropic
      name: claude
      api_key: ${ANTHROPIC_API_KEY}
      api_base: https://api.anthropic.com
      model: claude-3-5-sonnet-latest
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 2

# observability:
#   auto_refresh: false
#   auto_refresh_limit: 50
#   langfuse:
#     enabled: false
#     host: https://cloud.langfuse.com
#     public_key: ${LANGFUSE_PUBLIC_KEY}
#     secret_key: ${LANGFUSE_SECRET_KEY}
#     environment: dev
#     release: local
#     timeout_seconds: 2

context:
  max_history_messages: 40
  keep_recent_tool_results: 2
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  max_request_chars: 100000
  max_session_memory_chars: 20000
  max_compact_summary_chars: 60000
  enable_micro_compact: true
  enable_auto_compact: false
  auto_compact_failure_limit: 3
`, quoteConfigValue(workspace), quoteConfigValue(logDir), activeProfile), nil
}

func defaultUserRuntimePaths(configPath string) (string, string) {
	root := filepath.Dir(filepath.Clean(configPath))
	return filepath.Join(root, "workspace"), filepath.Join(root, "logs", "model_responses")
}

func normalizeInitProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", ProfileDeepSeek, "openai", "deepseek-openai":
		return ProfileDeepSeek
	case ProfileLocal, "ollama":
		return ProfileLocal
	case ProfileClaude, "anthropic":
		return ProfileClaude
	default:
		return strings.ToLower(strings.TrimSpace(profile))
	}
}

func validInitProfile(profile string) bool {
	return profile == ProfileDeepSeek || profile == ProfileLocal || profile == ProfileClaude
}

func quoteConfigValue(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}
