package app

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"cohert/internal/agent"
	"cohert/internal/llm"
	"cohert/internal/session"
	"cohert/internal/tools"
)

// NewRunner 根据配置创建完整的 Agent Runner。
// 这里是应用装配层：负责把 LLM Client、工具注册器、系统提示词组合到一起。
func NewRunner(cfg Config) (*agent.Runner, error) {
	if cfg.LLM.APIKey == "" {
		return nil, errors.New("missing API key: set DEEPSEEK_API_KEY or configs/config.yaml llm.api_key")
	}
	// workspace 是文件和命令工具默认工作的目录。
	if err := os.MkdirAll(cfg.Workspace, 0755); err != nil {
		return nil, err
	}
	// LogDir 保存模型原始响应，方便排查工具调用和流式解析问题。
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, err
	}

	// 当前 MVP 先支持 OpenAI-compatible 协议，DeepSeek 也走这套接口。
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
	sessionStore := session.NewStore(session.DefaultRootDir)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Runner 不直接知道具体工具类型，只依赖 ToolRunner 接口。
	return &agent.Runner{
		Client:       client,
		Tools:        registry,
		SystemPrompt: buildSystemPrompt(cfg),
		MaxTurns:     cfg.MaxTurns,
		LogDir:       filepath.Clean(cfg.LogDir),
		SessionStore: &sessionStore,
		SessionCWD:   cwd,
		SessionModel: cfg.LLM.Model,
	}, nil
}

// ToolSchemas 给 CLI 的 tools 命令使用，只列工具 schema，不初始化 LLM。
func ToolSchemas(cfg Config) []llm.ToolSchema {
	return newRegistry(cfg.Workspace).Schemas()
}

// newRegistry 集中注册当前 MVP 暴露给模型的本地工具。
func newRegistry(workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewFileRead(workspace))
	registry.Register(tools.NewFileWrite(workspace))
	registry.Register(tools.NewFilePatch(workspace))
	registry.Register(tools.NewCodeRun(workspace))
	registry.Register(tools.NewAskUser())
	return registry
}

// buildSystemPrompt 生成发送给模型的系统提示词。
func buildSystemPrompt(cfg Config) string {
	if cfg.Language == "en" {
		return "You are Cohert, a command-line coding agent. Use tools when needed, keep responses concise, and stop when the user task is complete."
	}
	return "你是 Cohert，一个命令行本地 Agent。需要读取文件、写文件或执行命令时必须调用工具；任务完成后直接给用户简洁结论。"
}
