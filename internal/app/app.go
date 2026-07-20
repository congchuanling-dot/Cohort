package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohert/internal/agent"
	"cohert/internal/browser"
	"cohert/internal/contextmgr"
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

	browserClient := newBrowserClient()
	registry := newRegistry(cfg.Workspace, browserClient)
	sessionStore := session.NewStore(session.DefaultRootDir)
	contextManager := &contextmgr.Manager{Config: cfg.Context.Normalize()}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Runner 不直接知道具体工具类型，只依赖 ToolRunner 接口。
	return &agent.Runner{
		Client:         client,
		Tools:          registry,
		SystemPrompt:   buildSystemPrompt(cfg),
		MaxTurns:       cfg.MaxTurns,
		LogDir:         filepath.Clean(cfg.LogDir),
		ContextManager: contextManager,
		SessionStore:   &sessionStore,
		SessionCWD:     cwd,
		SessionModel:   cfg.LLM.Model,
	}, nil
}

// ToolSchemas 给 CLI 的 tools 命令使用，只列工具 schema，不初始化 LLM。
func ToolSchemas(cfg Config) []llm.ToolSchema {
	return newRegistry(cfg.Workspace, browser.NewUnavailableClient(browser.ErrNotConnected)).Schemas()
}

// newRegistry 集中注册当前 MVP 暴露给模型的本地工具。
func newRegistry(workspace string, browserClient browser.Client) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewFileRead(workspace))
	registry.Register(tools.NewFileWrite(workspace))
	registry.Register(tools.NewFilePatch(workspace))
	registry.Register(tools.NewCodeRun(workspace))
	registry.Register(tools.NewBrowserTabs(browserClient))
	registry.Register(tools.NewBrowserOpen(browserClient))
	registry.Register(tools.NewBrowserScan(browserClient))
	registry.Register(tools.NewBrowserExecuteJS(browserClient))
	registry.Register(tools.NewBrowserClick(browserClient))
	registry.Register(tools.NewBrowserClickElement(browserClient))
	registry.Register(tools.NewBrowserType(browserClient))
	registry.Register(tools.NewBrowserTypeElement(browserClient))
	registry.Register(tools.NewBrowserWaitForLoad(browserClient))
	registry.Register(tools.NewBrowserWaitForSelector(browserClient))
	registry.Register(tools.NewBrowserWaitForText(browserClient))
	registry.Register(tools.NewBrowserWaitForStable(browserClient))
	registry.Register(tools.NewUpdateWorkingCheckpoint())
	registry.Register(tools.NewAskUser())
	return registry
}

func newBrowserClient() browser.Client {
	bridge := browser.NewBridge(browser.DefaultListenAddr, browser.DefaultPath)
	if err := bridge.Start(); err != nil {
		return browser.NewUnavailableClient(err)
	}
	return bridge
}

// buildSystemPrompt 生成发送给模型的系统提示词。
func buildSystemPrompt(cfg Config) string {
	sopIndex := readSOPIndex()
	if cfg.Language == "en" {
		return "You are Cohert, a command-line local agent. Use tools when needed, keep responses concise, and stop when the user task is complete. When multiple tool calls are independent and do not depend on each other's results, issue them in the same assistant response instead of splitting them across turns; keep dependent actions sequential. Use the SOP Index as navigation only: when a task matches an SOP scene, read the referenced SOP file before acting, then call update_working_checkpoint to store the key constraints and related_sop. For web lookup tasks, prefer browser_open, then browser_wait_for_load and browser_wait_for_stable, then browser_scan. For browser interaction, use browser_execute_js to read DOM state, then browser_click_element or browser_type_element for real CDP input; after navigation or async actions, wait for load, selector, text, or stable before judging failure. Advanced browser internals may be routed through browser_execute_js JSON commands, but prefer high-level browser tools for normal actions. Do not use OCR for normal web pages unless DOM text is unavailable." + sopIndex
	}
	return "你是 Cohert，一个命令行本地 Agent。需要读取文件、写文件、执行命令或查询网页时必须调用工具；当多个工具调用彼此独立、后一个不依赖前一个结果时，应在同一轮 assistant 响应中一次性发出多个 tool_calls，不要拆成多轮；有前后依赖的动作必须保持顺序执行。SOP Index 只作为导航：任务命中 SOP 场景时，先读取索引指向的 SOP 文件再行动，并调用 update_working_checkpoint 保存关键约束和 related_sop。网页查询优先使用 browser_open 打开页面，再用 browser_wait_for_load 和 browser_wait_for_stable 等页面稳定，然后用 browser_scan 读取 DOM 文本。浏览器交互优先用 browser_execute_js 读取 DOM 状态，再用 browser_click_element 或 browser_type_element 执行真实 CDP 输入；点击、输入、跳转或异步操作后，必须先等待 load、selector、text 或 stable，再判断失败。高级浏览器内部能力可通过 browser_execute_js 的 JSON 命令路由使用，但普通动作优先用高层浏览器工具。普通网页不要默认使用 OCR，只有 DOM 文本不可用时才考虑截图/OCR。任务完成后直接给用户简洁结论。" + sopIndex
}

func readSOPIndex() string {
	content, err := os.ReadFile(filepath.Clean("docs/sop_index.md"))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return ""
	}
	return "\n\n[SOP Index]\n" + text
}
