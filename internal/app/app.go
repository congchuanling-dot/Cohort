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

const (
	toolNarrationInstructionEN = " Before every tool call, first write a visible action note explaining: what you currently know, why this tool is needed, the exact information or state you want to obtain, the expected success signal, and likely blockers or fallback options. After each tool result, briefly interpret what was obtained, what is still missing, whether there is any blocker, and what the next step is. If issuing multiple independent tool calls in one response, explain the purpose of each tool call before calling them. Keep the explanation concrete and useful; do not dump secrets or large raw outputs."
	toolNarrationInstructionZH = " 每次调用工具前，必须先输出一段用户可见的行动说明，说明：当前已经知道什么、为什么需要这个工具、这次具体要获取或验证什么、成功信号是什么、可能的卡点和备选方案是什么。每次工具返回后，必须先解读结果：已经拿到了什么、还缺什么、有没有卡点、下一步准备怎么做。若同一轮要并行调用多个互不依赖的工具，调用前分别说明每个工具的目的。说明要具体、有信息量，避免泄露密钥或倾倒大段原始输出。"
)

// NewRunner 根据配置创建完整的 Agent Runner。
// 这里是应用装配层：负责把 LLM Client、工具注册器、系统提示词组合到一起。
func NewRunner(cfg Config) (*agent.Runner, error) {
	if cfg.LLM.APIKey == "" {
		return nil, errors.New("missing API key: set DEEPSEEK_API_KEY or configs/config.yaml llm.api_key")
	}
	workspace := normalizeWorkspace(cfg.Workspace)
	// workspace 是文件和命令工具默认工作的目录。
	if err := os.MkdirAll(workspace, 0755); err != nil {
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
	registry := newRegistry(workspace, browserClient)
	sessionStore := session.NewStore(session.DefaultRootDir)
	contextManager := &contextmgr.Manager{
		Config:     cfg.Context.Normalize(),
		MemoryRoot: filepath.Join(workspace, "memory"),
	}
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
	return newRegistry(normalizeWorkspace(cfg.Workspace), browser.NewUnavailableClient(browser.ErrNotConnected)).Schemas()
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
	registry.Register(tools.NewBrowserDOMSummary(browserClient))
	registry.Register(tools.NewBrowserExecuteJS(browserClient))
	registry.Register(tools.NewBrowserClick(browserClient))
	registry.Register(tools.NewBrowserClickElement(browserClient))
	registry.Register(tools.NewBrowserType(browserClient))
	registry.Register(tools.NewBrowserTypeElement(browserClient))
	registry.Register(tools.NewBrowserPressKey(browserClient))
	registry.Register(tools.NewBrowserSnapshot(browserClient))
	registry.Register(tools.NewBrowserWaitForLoad(browserClient))
	registry.Register(tools.NewBrowserWaitForSelector(browserClient))
	registry.Register(tools.NewBrowserWaitForText(browserClient))
	registry.Register(tools.NewBrowserWaitForURL(browserClient))
	registry.Register(tools.NewBrowserWaitForStable(browserClient))
	registry.Register(tools.NewBrowserScreenshot(browserClient, workspace))
	registry.Register(tools.NewBrowserOCR(browserClient, workspace))
	registry.Register(tools.NewUpdateWorkingCheckpoint())
	registry.Register(tools.NewStartLongTermUpdate(workspace))
	registry.Register(tools.NewMemoryProposeUpdate(workspace))
	registry.Register(tools.NewMemoryApplyUpdate(workspace))
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

func normalizeWorkspace(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	return filepath.Clean(workspace)
}

// buildSystemPrompt 生成发送给模型的系统提示词。
func buildSystemPrompt(cfg Config) string {
	sopIndex := readSOPIndex()
	if cfg.Language == "en" {
		return "You are Cohert, a command-line local agent. Use tools when needed, keep responses concise, and stop when the user task is complete. When multiple tool calls are independent and do not depend on each other's results, issue them in the same assistant response instead of splitting them across turns; keep dependent actions sequential." + toolNarrationInstructionEN + " Use the SOP Index as navigation only: when a task matches an SOP scene, read the referenced SOP file before acting, then call update_working_checkpoint to store the key constraints and related_sop. For web lookup tasks, prefer browser_open, then browser_wait_for_load and browser_wait_for_stable, then browser_scan. For browser interaction, use browser_snapshot to discover clickable/input elements; use browser_dom_summary when scan/snapshot is insufficient but DOM is still available, especially for forms, same-origin iframes, open shadow roots, or fixed overlays; use browser_execute_js only for specific DOM reads, then browser_click_element, browser_type_element, or browser_press_key for real CDP input. After navigation or async actions, wait for load, url, selector, text, or stable before judging failure. When visual text remains unavailable after DOM summary, use browser_ocr with a workspace image or let it capture the viewport; its bounding boxes are screenshot-local and must not be treated as screen coordinates. Advanced browser internals may be routed through browser_execute_js JSON commands, but prefer high-level browser tools for normal actions. Do not use OCR for normal web pages unless DOM text is unavailable. After meaningful or long tasks, consider start_long_term_update; only persist verified reusable memory, and skip routine one-off facts." + sopIndex
	}
	return "你是 Cohert，一个命令行本地 Agent。需要读取文件、写文件、执行命令或查询网页时必须调用工具；当多个工具调用彼此独立、后一个不依赖前一个结果时，应在同一轮 assistant 响应中一次性发出多个 tool_calls，不要拆成多轮；有前后依赖的动作必须保持顺序执行。" + toolNarrationInstructionZH + " SOP Index 只作为导航：任务命中 SOP 场景时，先读取索引指向的 SOP 文件再行动，并调用 update_working_checkpoint 保存关键约束和 related_sop。网页查询优先使用 browser_open 打开页面，再用 browser_wait_for_load 和 browser_wait_for_stable 等页面稳定，然后用 browser_scan 读取 DOM 文本。浏览器交互优先用 browser_snapshot 发现可点击/可输入元素；当 scan/snapshot 不够但 DOM 仍可访问时，用 browser_dom_summary 查看表单、同源 iframe、open shadowRoot 和固定浮层；只在需要精确 DOM 信息时用 browser_execute_js，再用 browser_click_element、browser_type_element 或 browser_press_key 执行真实 CDP 输入。点击、输入、按键、跳转或异步操作后，必须先等待 load、url、selector、text 或 stable，再判断失败。DOM 文本和 DOM 摘要都无法读取页面文字时，使用 browser_ocr 读取 workspace 图片或让它自动截取浏览器视口；它返回的 bbox 是 screenshot-local 坐标，不能直接当作系统屏幕坐标。高级浏览器内部能力可通过 browser_execute_js 的 JSON 命令路由使用，但普通动作优先用高层浏览器工具。普通网页不要默认使用 OCR，只有 DOM 文本不可用时才考虑截图/OCR。完成有复用价值或耗时较长的任务后，可调用 start_long_term_update；只沉淀经过验证、未来可复用的记忆，普通一次性事实应 skip。任务完成后直接给用户简洁结论。" + sopIndex
}

func readSOPIndex() string {
	content, err := os.ReadFile(filepath.Clean("sops/index.md"))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return ""
	}
	return "\n\n[SOP Index]\n" + text
}
