package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/skill"
)

func TestLoadConfigParsesContextSection_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `language: zh
workspace: ./workspace
log_dir: ./temp/model_responses
max_turns: 7

context:
  max_history_messages: 9
  keep_recent_tool_results: 1
  max_tool_result_chars: 99
  compacted_tool_head_chars: 11
  compacted_tool_tail_chars: 12
  max_request_chars: 999
	  max_session_memory_chars: 88
  max_memory_index_chars: 99
  max_relevant_memory_chars: 144
  max_relevant_memory_entries: 3
  max_compact_summary_chars: 188
  enable_micro_compact: false

llm:
  provider: openai
  name: deepseek
  api_key: test-key
  api_base: https://example.com/v1
  model: test-model
  stream: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Context.MaxHistoryMessages != 9 {
		t.Fatalf("max history messages = %d, want 9", cfg.Context.MaxHistoryMessages)
	}
	if cfg.Context.KeepRecentToolResults != 1 {
		t.Fatalf("keep recent tool results = %d, want 1", cfg.Context.KeepRecentToolResults)
	}
	if cfg.Context.MaxToolResultChars != 99 {
		t.Fatalf("max tool result chars = %d, want 99", cfg.Context.MaxToolResultChars)
	}
	if cfg.Context.CompactedToolHeadChars != 11 || cfg.Context.CompactedToolTailChars != 12 {
		t.Fatalf("compacted head/tail = %d/%d, want 11/12", cfg.Context.CompactedToolHeadChars, cfg.Context.CompactedToolTailChars)
	}
	if cfg.Context.MaxRequestChars != 999 {
		t.Fatalf("max request chars = %d, want 999", cfg.Context.MaxRequestChars)
	}
	if cfg.Context.MaxSessionMemoryChars != 88 {
		t.Fatalf("max session memory chars = %d, want 88", cfg.Context.MaxSessionMemoryChars)
	}
	if cfg.Context.MaxMemoryIndexChars != 99 {
		t.Fatalf("max memory index chars = %d, want 99", cfg.Context.MaxMemoryIndexChars)
	}
	if cfg.Context.MaxRelevantMemoryChars != 144 {
		t.Fatalf("max relevant memory chars = %d, want 144", cfg.Context.MaxRelevantMemoryChars)
	}
	if cfg.Context.MaxRelevantMemoryEntries != 3 {
		t.Fatalf("max relevant memory entries = %d, want 3", cfg.Context.MaxRelevantMemoryEntries)
	}
	if cfg.Context.MaxCompactSummaryChars != 188 {
		t.Fatalf("max compact summary chars = %d, want 188", cfg.Context.MaxCompactSummaryChars)
	}
	if cfg.Context.ContextWindowTokens != 1000000 {
		t.Fatalf("resolved context window tokens = %d, want 1000000", cfg.Context.ContextWindowTokens)
	}
	if cfg.Context.EnableMicroCompact {
		t.Fatal("enable micro compact = true, want false")
	}
	if cfg.LLM.ActiveProfile != "" {
		t.Fatalf("active profile = %q, want empty legacy profile", cfg.LLM.ActiveProfile)
	}
	if active := cfg.LLM.Active(); active.Model != "test-model" || active.APIBase != "https://example.com/v1" {
		t.Fatalf("legacy active profile = %#v, want model/api_base from legacy llm fields", active)
	}
}

func TestLoadConfigParsesLLMProfiles_BitsUT(t *testing.T) {
	t.Setenv("LOCAL_OPENAI_API_KEY", "local-key")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `language: zh
workspace: ./workspace
log_dir: ./temp/model_responses
max_turns: 7

llm:
  active_profile: local
  fallback_profiles: [deepseek, deepseek]
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
      name: qwen-local
      api_key: ${LOCAL_OPENAI_API_KEY}
      api_base: http://127.0.0.1:11434/v1/
      model: qwen3-coder
      stream: false
      max_retries: 1
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.LLM.ActiveProfile != "local" {
		t.Fatalf("active profile = %q, want local", cfg.LLM.ActiveProfile)
	}
	if len(cfg.LLM.FallbackProfiles) != 1 || cfg.LLM.FallbackProfiles[0] != "deepseek" {
		t.Fatalf("fallback profiles = %#v, want [deepseek]", cfg.LLM.FallbackProfiles)
	}
	if len(cfg.LLM.Profiles) != 2 {
		t.Fatalf("profiles count = %d, want 2", len(cfg.LLM.Profiles))
	}
	active := cfg.LLM.Active()
	if active.ID != "local" {
		t.Fatalf("active id = %q, want local", active.ID)
	}
	if active.Name != "qwen-local" {
		t.Fatalf("active name = %q, want qwen-local", active.Name)
	}
	if active.APIKey != "local-key" {
		t.Fatalf("active api key = %q, want expanded local-key", active.APIKey)
	}
	if active.APIBase != "http://127.0.0.1:11434/v1" {
		t.Fatalf("active api base = %q, want trimmed base", active.APIBase)
	}
	if active.Model != "qwen3-coder" {
		t.Fatalf("active model = %q, want qwen3-coder", active.Model)
	}
	if active.Stream {
		t.Fatal("active stream = true, want false")
	}
	if active.ConnectTimeoutSeconds != 10 || active.ReadTimeoutSeconds != 120 {
		t.Fatalf("active timeouts = %d/%d, want defaults 10/120", active.ConnectTimeoutSeconds, active.ReadTimeoutSeconds)
	}
	if cfg.LLM.Model != active.Model || cfg.LLM.APIBase != active.APIBase || cfg.LLM.APIKey != active.APIKey {
		t.Fatalf("legacy fields were not backfilled from active profile: llm=%#v active=%#v", cfg.LLM, active)
	}
}

func TestLoadConfigParsesLangfuseObservability_BitsUT(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `language: zh
workspace: ./workspace
log_dir: ./temp/model_responses
max_turns: 7

observability:
  langfuse:
    enabled: true
    host: https://langfuse.example.com/
    public_key: ${LANGFUSE_PUBLIC_KEY}
    secret_key: ${LANGFUSE_SECRET_KEY}
    environment: test
    release: sha-123
    timeout_seconds: 3

llm:
  provider: openai
  name: deepseek
  api_key: test-key
  api_base: https://example.com/v1
  model: test-model
  stream: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	langfuse := cfg.Observability.Langfuse
	if !langfuse.Enabled {
		t.Fatal("langfuse enabled = false, want true")
	}
	if langfuse.Host != "https://langfuse.example.com" {
		t.Fatalf("langfuse host = %q, want trimmed host", langfuse.Host)
	}
	if langfuse.PublicKey != "pk-test" || langfuse.SecretKey != "sk-test" {
		t.Fatalf("langfuse keys = %q/%q, want expanded env keys", langfuse.PublicKey, langfuse.SecretKey)
	}
	if langfuse.Environment != "test" || langfuse.Release != "sha-123" || langfuse.TimeoutSeconds != 3 {
		t.Fatalf("langfuse metadata = %#v, want parsed metadata", langfuse)
	}
}

func TestLoadConfigLoadsDotEnvAndLangfuseBaseURLAlias_BitsUT(t *testing.T) {
	root := t.TempDir()
	oldWD, getwdErr := os.Getwd()
	if getwdErr != nil {
		t.Fatal(getwdErr)
	}
	if chdirErr := os.Chdir(root); chdirErr != nil {
		t.Fatal(chdirErr)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	restoreEnv := snapshotEnv(t, []string{
		"COHORT_LANGFUSE_ENABLED",
		"LANGFUSE_HOST",
		"LANGFUSE_BASE_URL",
		"LANGFUSE_PUBLIC_KEY",
		"LANGFUSE_SECRET_KEY",
	})
	defer restoreEnv()

	dotEnv := "COHORT_LANGFUSE_ENABLED=true\n" +
		"LANGFUSE_BASE_URL=\" `https://us.cloud.langfuse.com` \"\n" +
		"LANGFUSE_PUBLIC_KEY=pk-dotenv\n" +
		"LANGFUSE_SECRET_KEY=sk-dotenv\n"
	if writeErr := os.WriteFile(filepath.Join(root, ".env"), []byte(dotEnv), 0644); writeErr != nil {
		t.Fatal(writeErr)
	}
	path := filepath.Join(root, "config.yaml")
	content := `llm:
  provider: openai
  name: deepseek
  api_key: test-key
  api_base: https://example.com/v1
  model: test-model
  stream: false
`
	if writeErr := os.WriteFile(path, []byte(content), 0644); writeErr != nil {
		t.Fatal(writeErr)
	}

	cfg, loadErr := LoadConfig(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	langfuse := cfg.Observability.Langfuse
	if !langfuse.Enabled {
		t.Fatal("langfuse enabled = false, want true from .env")
	}
	if langfuse.Host != "https://us.cloud.langfuse.com" {
		t.Fatalf("langfuse host = %q, want sanitized base url alias", langfuse.Host)
	}
	if langfuse.PublicKey != "pk-dotenv" || langfuse.SecretKey != "sk-dotenv" {
		t.Fatalf("langfuse keys = %q/%q, want dotenv values", langfuse.PublicKey, langfuse.SecretKey)
	}
}

func snapshotEnv(t *testing.T, keys []string) func() {
	t.Helper()
	values := make(map[string]string, len(keys))
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		values[key] = value
		present[key] = ok
		_ = os.Unsetenv(key)
	}
	return func() {
		for _, key := range keys {
			if present[key] {
				_ = os.Setenv(key, values[key])
				continue
			}
			_ = os.Unsetenv(key)
		}
	}
}

func TestLoadConfigRejectsMissingFallbackProfile_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `llm:
  active_profile: deepseek
  fallback_profiles: [missing]
  profiles:
    deepseek:
      provider: openai
      api_key: test-key
      api_base: https://api.deepseek.com
      model: deepseek-v4-pro
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig error = nil, want missing fallback profile error")
	}
	if !strings.Contains(err.Error(), `llm.fallback_profiles "missing" does not exist`) {
		t.Fatalf("LoadConfig error = %v, want missing fallback profile message", err)
	}
}

func TestLoadConfigRejectsMissingActiveProfile_BitsUT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `llm:
  active_profile: missing
  profiles:
    deepseek:
      provider: openai
      api_key: test-key
      api_base: https://api.deepseek.com
      model: deepseek-v4-pro
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig error = nil, want missing active profile error")
	}
	if !strings.Contains(err.Error(), `llm.active_profile "missing" does not exist`) {
		t.Fatalf("LoadConfig error = %v, want missing active profile message", err)
	}
}

func TestNormalizeWorkspaceReturnsAbsolutePath_BitsUT(t *testing.T) {
	workspace := normalizeWorkspace("./workspace")
	if !filepath.IsAbs(workspace) {
		t.Fatalf("workspace = %q, want absolute path", workspace)
	}
	if !strings.HasSuffix(workspace, string(filepath.Separator)+"workspace") {
		t.Fatalf("workspace = %q, want workspace suffix", workspace)
	}
}

func TestBuildSystemPromptRequiresToolNarration_BitsUT(t *testing.T) {
	zhPrompt := buildSystemPrompt(Config{Language: "zh"})
	for _, want := range []string{"每次调用工具前", "当前已经知道什么", "可能的卡点", "每次工具返回后"} {
		if !strings.Contains(zhPrompt, want) {
			t.Fatalf("zh prompt does not contain %q:\n%s", want, zhPrompt)
		}
	}

	enPrompt := buildSystemPrompt(Config{Language: "en"})
	for _, want := range []string{"Before every tool call", "what you currently know", "likely blockers", "After each tool result"} {
		if !strings.Contains(enPrompt, want) {
			t.Fatalf("en prompt does not contain %q:\n%s", want, enPrompt)
		}
	}
}

func TestBuildSystemPromptInjectsSkillIndexOnly_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".cohort", "skills", "go-test", skill.SkillFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: Go Test
description: Run focused Go tests.
---

# Go Test

SECRET FULL BODY SHOULD NOT ENTER SYSTEM PROMPT.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	prompt := BuildSystemPrompt(Config{Language: "zh"}, store)
	for _, want := range []string{"[Skill Index]", "project/go-test", "Run focused Go tests.", "skill_read"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "SECRET FULL BODY") {
		t.Fatalf("system prompt leaked full skill body:\n%s", prompt)
	}
}

func TestBuildSystemPromptRequiresAskUserForBlockingQuestions_BitsUT(t *testing.T) {
	zhPrompt := BuildSystemPrompt(Config{Language: "zh"}, nil)
	for _, want := range []string{"必须调用 ask_user", "不要只用普通文本提问后结束"} {
		if !strings.Contains(zhPrompt, want) {
			t.Fatalf("zh prompt missing %q:\n%s", want, zhPrompt)
		}
	}

	enPrompt := BuildSystemPrompt(Config{Language: "en"}, nil)
	for _, want := range []string{"call ask_user", "plain-text question"} {
		if !strings.Contains(enPrompt, want) {
			t.Fatalf("en prompt missing %q:\n%s", want, enPrompt)
		}
	}
}
