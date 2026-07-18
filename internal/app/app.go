package app

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"cohert/internal/agent"
	"cohert/internal/llm"
	"cohert/internal/tools"
)

func NewRunner(cfg Config) (*agent.Runner, error) {
	if cfg.LLM.APIKey == "" {
		return nil, errors.New("missing API key: set DEEPSEEK_API_KEY or configs/config.yaml llm.api_key")
	}
	if err := os.MkdirAll(cfg.Workspace, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, err
	}

	client := llm.NewOpenAIClient(llm.OpenAIConfig{
		Name:           cfg.LLM.Name,
		APIKey:         cfg.LLM.APIKey,
		APIBase:        cfg.LLM.APIBase,
		Model:          cfg.LLM.Model,
		Stream:         cfg.LLM.Stream,
		ConnectTimeout: time.Duration(cfg.LLM.ConnectTimeoutSeconds) * time.Second,
		ReadTimeout:    time.Duration(cfg.LLM.ReadTimeoutSeconds) * time.Second,
		MaxRetries:     cfg.LLM.MaxRetries,
	})

	registry := newRegistry(cfg.Workspace)

	return &agent.Runner{
		Client:       client,
		Tools:        registry,
		SystemPrompt: buildSystemPrompt(cfg),
		MaxTurns:     cfg.MaxTurns,
		LogDir:       filepath.Clean(cfg.LogDir),
	}, nil
}

func ToolSchemas(cfg Config) []llm.ToolSchema {
	return newRegistry(cfg.Workspace).Schemas()
}

func newRegistry(workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewFileRead(workspace))
	registry.Register(tools.NewFileWrite(workspace))
	registry.Register(tools.NewFilePatch(workspace))
	registry.Register(tools.NewCodeRun(workspace))
	registry.Register(tools.NewAskUser())
	return registry
}

func buildSystemPrompt(cfg Config) string {
	if cfg.Language == "en" {
		return "You are Cohert Go MVP, a command-line coding agent. Use tools when needed, keep responses concise, and stop when the user task is complete."
	}
	return "你是 Cohert Go MVP，一个命令行本地 Agent。需要读取文件、写文件或执行命令时必须调用工具；任务完成后直接给用户简洁结论。"
}
