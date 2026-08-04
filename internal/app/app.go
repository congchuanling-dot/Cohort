package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/browser"
	"cohort/internal/capability"
	"cohort/internal/computeruse"
	"cohort/internal/contextmgr"
	"cohort/internal/desktop"
	"cohort/internal/llm"
	"cohort/internal/mcp"
	"cohort/internal/observability"
	"cohort/internal/session"
	"cohort/internal/skill"
	"cohort/internal/tools"
)

const (
	toolNarrationInstructionEN = " Before every tool call, first write a visible action note explaining: what you currently know, why this tool is needed, the exact information or state you want to obtain, the expected success signal, and likely blockers or fallback options. After each tool result, briefly interpret what was obtained, what is still missing, whether there is any blocker, and what the next step is. If issuing multiple independent tool calls in one response, explain the purpose of each tool call before calling them. Keep the explanation concrete and useful; do not dump secrets or large raw outputs."
	toolNarrationInstructionZH = " 每次调用工具前，必须先输出一段用户可见的行动说明，说明：当前已经知道什么、为什么需要这个工具、这次具体要获取或验证什么、成功信号是什么、可能的卡点和备选方案是什么。每次工具返回后，必须先解读结果：已经拿到了什么、还缺什么、有没有卡点、下一步准备怎么做。若同一轮要并行调用多个互不依赖的工具，调用前分别说明每个工具的目的。说明要具体、有信息量，避免泄露密钥或倾倒大段原始输出。"
	userQuestionInstructionEN  = " When missing information, approval, or a user decision blocks the next action, call ask_user instead of ending with a plain-text question."
	userQuestionInstructionZH  = " 当缺少信息、授权或用户决策导致下一步无法继续时，必须调用 ask_user，不要只用普通文本提问后结束。"
	mcpStartupTimeout          = 90 * time.Second
)

// NewRunner 根据配置创建完整的 Agent Runner。
// 这里是应用装配层：负责把 LLM Client、工具注册器、系统提示词组合到一起。
func NewRunner(cfg Config) (*agent.Runner, error) {
	active := cfg.LLM.Active()
	workspace := normalizeWorkspace(cfg.Workspace)
	// workspace 是文件和命令工具默认工作的目录。
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return nil, err
	}
	// LogDir 保存模型原始响应，方便排查工具调用和流式解析问题。
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, err
	}

	client, err := buildLLMClient(cfg.LLM)
	if err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mcpManager, err := loadMCPManager(context.Background(), cwd)
	if err != nil {
		return nil, err
	}
	mcpPermissions, err := loadMCPPermissions(cwd)
	if err != nil {
		_ = mcpManager.Close()
		return nil, err
	}
	skillStore, err := LoadSkillStore(cwd)
	if err != nil {
		_ = mcpManager.Close()
		return nil, err
	}
	browserClient := newBrowserClient()
	registry := newRegistry(workspace, browserClient, mcpManager, mcpPermissions, skillStore)
	sessionStore := session.NewStore(session.DefaultRootDir)
	contextManager := &contextmgr.Manager{
		Config:     cfg.Context.Normalize(),
		MemoryRoot: filepath.Join(workspace, "memory"),
	}

	// Runner 不直接知道具体工具类型，只依赖 ToolRunner 接口。
	return &agent.Runner{
		Client:           client,
		Tools:            registry,
		SystemPrompt:     BuildSystemPromptForProject(cfg, skillStore, cwd),
		MaxTurns:         cfg.MaxTurns,
		LogDir:           filepath.Clean(cfg.LogDir),
		ContextManager:   contextManager,
		SessionStore:     &sessionStore,
		SessionCWD:       cwd,
		SessionModel:     active.Model,
		CloseFunc:        mcpManager.Close,
		SkillStore:       skillStore,
		ObservationSinks: buildObservationSinks(cfg.Observability),
	}, nil
}

func buildObservationSinks(cfg ObservabilityConfig) []observability.Sink {
	cfg = normalizeObservabilityConfig(cfg)
	langfuse := cfg.Langfuse
	if !langfuse.Enabled || strings.TrimSpace(langfuse.PublicKey) == "" || strings.TrimSpace(langfuse.SecretKey) == "" {
		return nil
	}
	return []observability.Sink{
		observability.NewLangfuseSink(observability.LangfuseSinkConfig{
			Host:        langfuse.Host,
			PublicKey:   langfuse.PublicKey,
			SecretKey:   langfuse.SecretKey,
			Environment: langfuse.Environment,
			Release:     langfuse.Release,
			Timeout:     time.Duration(langfuse.TimeoutSeconds) * time.Second,
		}),
	}
}

func buildLLMClient(cfg LLMConfig) (llm.Client, error) {
	active := cfg.Active()
	chain := []LLMProfile{active}
	if len(cfg.Profiles) > 0 {
		for _, id := range cfg.FallbackProfiles {
			chain = append(chain, cfg.Profiles[id])
		}
	}
	clients := make([]llm.NamedClient, 0, len(chain))
	for _, profile := range chain {
		name := profile.ID
		if name == "" {
			name = profile.Name
		}
		if profile.APIKey == "" {
			return nil, fmt.Errorf("missing API key for llm profile %q: set environment variable or active config llm.api_key / llm.profiles.%s.api_key", name, name)
		}
		client, err := llm.NewClient(llm.ProviderConfig{
			ProfileID:      profile.ID,
			Provider:       profile.Provider,
			Name:           profile.Name,
			APIKey:         profile.APIKey,
			APIBase:        profile.APIBase,
			Model:          profile.Model,
			Stream:         profile.Stream,
			ConnectTimeout: time.Duration(profile.ConnectTimeoutSeconds) * time.Second,
			ReadTimeout:    time.Duration(profile.ReadTimeoutSeconds) * time.Second,
			MaxRetries:     profile.MaxRetries,
		})
		if err != nil {
			return nil, err
		}
		clients = append(clients, llm.NamedClient{Name: name, Client: client})
	}
	return llm.NewFallbackClient(clients)
}

// ToolSchemas 给 CLI 的 tools 命令使用，只列工具 schema，不初始化 LLM。
func ToolSchemas(cfg Config) ([]llm.ToolSchema, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mcpManager, err := loadMCPManager(context.Background(), cwd)
	if err != nil {
		return nil, err
	}
	defer mcpManager.Close()
	mcpPermissions, err := loadMCPPermissions(cwd)
	if err != nil {
		return nil, err
	}
	skillStore, err := LoadSkillStore(cwd)
	if err != nil {
		return nil, err
	}
	return newRegistry(
		normalizeWorkspace(cfg.Workspace),
		browser.NewUnavailableClient(browser.ErrNotConnected),
		mcpManager,
		mcpPermissions,
		skillStore,
	).Schemas(), nil
}

// newRegistry 集中注册当前 MVP 暴露给模型的本地工具。
func newRegistry(
	workspace string,
	browserClient browser.Client,
	mcpManager *mcp.Manager,
	mcpPermissions *tools.MCPPermissionStore,
	skillStore *skill.Store,
) *tools.Registry {
	registry := tools.NewRegistry()
	desktopDriver := newDesktopDriver(workspace)
	confirmations := tools.NewConfirmationStore()
	visualFocuses := tools.NewVisualFocusStore()
	computerStore := computeruse.NewStore(computeruse.DefaultTargetTTL)
	if mcpPermissions == nil {
		mcpPermissions = tools.NewMCPPermissionStore()
	}
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
	registry.Register(tools.NewDesktopPermissions(desktopDriver))
	registry.Register(tools.NewDesktopWindows(desktopDriver))
	registry.Register(tools.NewDesktopActivate(desktopDriver))
	registry.Register(tools.NewDesktopScreenshot(desktopDriver, workspace))
	registry.Register(tools.NewDesktopAXSnapshot(desktopDriver))
	registry.Register(tools.NewDesktopOCR(workspace))
	registry.Register(tools.NewDesktopAXPress(desktopDriver, confirmations))
	registry.Register(tools.NewDesktopAXFocus(desktopDriver))
	registry.Register(tools.NewDesktopClick(desktopDriver, confirmations))
	registry.Register(tools.NewDesktopVisualClick(desktopDriver, confirmations, workspace, visualFocuses))
	registry.Register(tools.NewDesktopPressKey(desktopDriver, confirmations))
	registry.Register(tools.NewDesktopTypeText(desktopDriver, visualFocuses))
	registry.Register(tools.NewComputerSee(desktopDriver, computerStore, workspace))
	registry.Register(tools.NewComputerFind(computerStore))
	registry.Register(tools.NewComputerClickWithVisualFocus(desktopDriver, computerStore, confirmations, visualFocuses))
	registry.Register(tools.NewComputerDoubleClick(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerRightClick(desktopDriver, computerStore))
	registry.Register(tools.NewComputerType(desktopDriver, computerStore, visualFocuses))
	registry.Register(tools.NewComputerPress(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerWait(desktopDriver, computerStore))
	registry.Register(tools.NewComputerCheck(desktopDriver, computerStore))
	registry.Register(tools.NewComputerScroll(desktopDriver, computerStore))
	registry.Register(tools.NewComputerDrag(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerDrop(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerClipboardWrite(desktopDriver))
	registry.Register(tools.NewComputerPaste(desktopDriver, computerStore))
	registry.Register(tools.NewComputerWindowSwitch(desktopDriver, computerStore))
	registry.Register(tools.NewComputerMenu(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerFileDialog(desktopDriver, computerStore, confirmations))
	registry.Register(tools.NewComputerWindowMove(desktopDriver, computerStore))
	registry.Register(tools.NewComputerWindowResize(desktopDriver, computerStore))
	registry.Register(tools.NewComputerVisualSnapshot(computerStore))
	registry.Register(tools.NewComputerExecuteStep(desktopDriver, computerStore, confirmations, visualFocuses))
	registry.Register(tools.NewComputerExecutePlan(desktopDriver, computerStore, confirmations, visualFocuses, workspace))
	registry.Register(tools.NewUpdateWorkingCheckpoint())
	registry.Register(tools.NewStartLongTermUpdate(workspace))
	registry.Register(tools.NewMemoryProposeUpdate(workspace))
	registry.Register(tools.NewMemoryApplyUpdate(workspace))
	registry.Register(tools.NewAskUser(confirmations))
	registry.Register(tools.NewSkillRead(skillStore))
	if mcpManager != nil {
		for _, registered := range mcpManager.Tools() {
			registry.Register(tools.NewMCPTool(
				registered,
				mcpManager,
				mcpPermissions,
				tools.NewTerminalMCPPermissionPrompter(),
			))
		}
	}
	return registry
}

// LoadSkillStore 扫描项目级和用户级 Skill，并返回可运行中 reload 的 Store。
func LoadSkillStore(projectRoot string) (*skill.Store, error) {
	store := skill.NewStore(projectRoot, "")
	if err := store.Reload(); err != nil {
		return nil, err
	}
	return store, nil
}

func loadMCPManager(ctx context.Context, projectRoot string) (*mcp.Manager, error) {
	store := mcp.NewStore(projectRoot)
	servers, err := store.LoadEffective()
	if err != nil {
		return nil, err
	}
	// 首次通过 npx 启动 MCP Server 时，npm 安装依赖可能远超普通进程启动时间。
	// 这里允许一次较长冷启动；后续命中本地 npm cache 时通常只需几秒。
	loadCtx, cancel := context.WithTimeout(ctx, mcpStartupTimeout)
	defer cancel()
	manager := mcp.NewManager()
	manager.Load(loadCtx, servers)
	return manager, nil
}

// loadMCPPermissions 只读取当前项目的 MCP 策略和已确认授权。
//
// 这里不读取、更不创建任何 Server 配置；因此 Cohort 启动时没有默认飞书或其他
// MCP，所有外部能力仍必须由用户通过 mcp add 或配置文件显式装配。
func loadMCPPermissions(projectRoot string) (*tools.MCPPermissionStore, error) {
	return tools.NewProjectMCPPermissionStore(mcp.NewStore(projectRoot))
}

func newBrowserClient() browser.Client {
	bridge := browser.NewBridge(browser.DefaultListenAddr, browser.DefaultPath)
	if err := bridge.Start(); err != nil {
		return browser.NewUnavailableClient(err)
	}
	return bridge
}

func newDesktopDriver(workspace string) desktop.Driver {
	scriptPath := ResolveRuntimeScriptPath(workspace, DesktopDarwinHelperPath)
	if absolutePath, err := filepath.Abs(scriptPath); err == nil {
		scriptPath = absolutePath
	}
	return desktop.NewPythonDriver("python3", scriptPath, desktop.DefaultTimeout)
}

func findProjectRoot(path string) string {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
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

// BuildSystemPrompt 生成发送给模型的系统提示词。
func BuildSystemPrompt(cfg Config, skillStore *skill.Store) string {
	return buildSystemPromptWithIndex(cfg, skillStore, "")
}

func BuildSystemPromptForProject(cfg Config, skillStore *skill.Store, projectRoot string) string {
	capabilityIndex := ""
	if strings.TrimSpace(projectRoot) != "" {
		capabilityIndex = capability.NewStore(projectRoot).IndexPrompt()
	}
	return buildSystemPromptWithIndex(cfg, skillStore, capabilityIndex)
}

func buildSystemPromptWithIndex(cfg Config, skillStore *skill.Store, capabilityIndex string) string {
	sopIndex := readSOPIndex()
	skillIndex := ""
	if skillStore != nil {
		skillIndex = skillStore.IndexPrompt()
	}
	if cfg.Language == "en" {
		return "You are Cohort, a command-line local agent. Use tools when needed, keep responses concise, and stop when the user task is complete. When multiple tool calls are independent and do not depend on each other's results, issue them in the same assistant response instead of splitting them across turns; keep dependent actions sequential." + toolNarrationInstructionEN + userQuestionInstructionEN + " Use the SOP Index as navigation only: when a task matches an SOP scene, read the referenced SOP file before acting, then call update_working_checkpoint to store the key constraints and related_sop. Use the Skill Index as navigation only: when a task matches a Skill, call skill_read with the listed skill_id before acting, then call update_working_checkpoint to store the key constraints and related_skill. Use the Capability Index as verified capability navigation only: when a task matches an available capability, follow its skill_id via skill_read before acting; do not use candidate, failed, disabled, or missing capabilities as active instructions. For web lookup tasks, prefer browser_open, then browser_wait_for_load and browser_wait_for_stable, then browser_scan. For browser interaction, use browser_snapshot to discover clickable/input elements; use browser_dom_summary when scan/snapshot is insufficient but DOM is still available, especially for forms, same-origin iframes, open shadow roots, or fixed overlays; use browser_execute_js only for specific DOM reads, then browser_click_element, browser_type_element, or browser_press_key for real CDP input. After navigation or async actions, wait for load, url, selector, text, or stable before judging failure. When visual text remains unavailable after DOM summary, use browser_ocr with a workspace image or let it capture the viewport; its bounding boxes are screenshot-local and must not be treated as screen coordinates. For native desktop applications, read the desktop SOP and use desktop_permissions -> desktop_windows -> desktop_activate -> desktop_ax_snapshot. Desktop input is restricted to desktop_ax_press with exact current AX node metadata, desktop_ax_focus for editable AX nodes, desktop_click only on a current AX node center, desktop_visual_click only with a desktop_screenshot image plus OCR/UI bbox, desktop_press_key with an allowlisted key, and desktop_type_text for drafting into the currently focused editable field or consuming a visual_focus_token returned by desktop_visual_click when AX cannot prove WebView editable focus. For transient search/dropdown/autocomplete UI, use desktop_press_key with key=Enter and intent=open_selected_result to open the selected result as R1 navigation; do not call ask_user in the middle because the transient UI may disappear on focus loss. Do not use code_run or scripts to bypass desktop input boundaries. R2 actions require the one-time token issued by ask_user for the exact pid and node_id, image_path+bbox, or key plus reason. R3 actions are refused for manual completion. desktop_type_text must not be used to send; send/submit remains a separate desktop_press_key or confirmed desktop_click/desktop_visual_click action when risky, and open_selected_result must never be used for send/submit. Use desktop_screenshot and desktop_ocr when AX is insufficient; their bbox coordinates are screenshot-local and may only be converted by desktop_visual_click using the screenshot manifest. Do not pass OCR bbox to desktop_click. Use a visual_focus_token only when desktop_visual_click returned it for an input/search bbox; the token is short-lived, single-use, and only permits drafting text, never sending. No arbitrary desktop coordinate click tool exists. Advanced browser internals may be routed through browser_execute_js JSON commands, but prefer high-level browser tools for normal actions. Do not use OCR for normal web pages unless DOM text is unavailable. After meaningful or long tasks, consider start_long_term_update; only persist verified reusable memory, and skip routine one-off facts." + sopIndex + skillIndex + capabilityIndex
	}
	return "你是 Cohort，一个命令行本地 Agent。需要读取文件、写文件、执行命令或查询网页时必须调用工具；当多个工具调用彼此独立、后一个不依赖前一个结果时，应在同一轮 assistant 响应中一次性发出多个 tool_calls，不要拆成多轮；有前后依赖的动作必须保持顺序执行。" + toolNarrationInstructionZH + userQuestionInstructionZH + " SOP Index 只作为导航：任务命中 SOP 场景时，先读取索引指向的 SOP 文件再行动，并调用 update_working_checkpoint 保存关键约束和 related_sop。Skill Index 只作为导航：任务命中 Skill 场景时，先用 skill_read 读取对应 skill_id 的完整 SKILL.md 再行动，并调用 update_working_checkpoint 保存关键约束和 related_skill。Capability Index 只作为已验证能力导航：任务命中 available capability 时，先根据 skill_id 调用 skill_read 再行动；candidate、failed、disabled、missing 能力不得作为已启用能力执行。网页查询优先使用 browser_open 打开页面，再用 browser_wait_for_load 和 browser_wait_for_stable 等页面稳定，然后用 browser_scan 读取 DOM 文本。浏览器交互优先用 browser_snapshot 发现可点击/可输入元素；当 scan/snapshot 不够但 DOM 仍可访问时，用 browser_dom_summary 查看表单、同源 iframe、open shadowRoot 和固定浮层；只在需要精确 DOM 信息时用 browser_execute_js，再用 browser_click_element、browser_type_element 或 browser_press_key 执行真实 CDP 输入。点击、输入、按键、跳转或异步操作后，必须先等待 load、url、selector、text 或 stable，再判断失败。DOM 文本和 DOM 摘要都无法读取页面文字时，使用 browser_ocr 读取 workspace 图片或让它自动截取浏览器视口；它返回的 bbox 是 screenshot-local 坐标，不能直接当作系统屏幕坐标。处理桌面原生应用时，先读取 desktop SOP 并遵循 desktop_permissions -> desktop_windows -> desktop_activate -> desktop_ax_snapshot 的顺序。桌面输入只允许基于当前 AX 节点精确语义执行 desktop_ax_press、用 desktop_ax_focus 聚焦可编辑 AX 节点、用 desktop_click 点击当前 AX 节点中心、用 desktop_visual_click 基于 desktop_screenshot 图片和 OCR/UI bbox 执行受控视觉点击、使用 allowlist 的 desktop_press_key，以及用 desktop_type_text 在当前焦点可编辑输入框起草文本，或在 AX 无法证明 WebView 可编辑焦点时消费 desktop_visual_click 返回的 visual_focus_token 起草文本；搜索结果/下拉候选/自动补全等临时 UI 中，使用 desktop_press_key 的 key=Enter 且 intent=open_selected_result 打开已选结果，这是 R1 导航，中途不得调用 ask_user，避免失焦导致临时 UI 消失；不能借助 code_run 或脚本绕过桌面输入边界。R2 操作必须使用 ask_user 为同一 pid、node_id、image_path+bbox 或 key、reason 签发的一次性令牌，R3 操作拒绝自动执行。desktop_type_text 不能负责发送，发送/提交必须拆成单独 desktop_press_key 或经确认的 desktop_click/desktop_visual_click 且按风险确认，open_selected_result 绝不能用于发送/提交。AX 不可用时才用 desktop_screenshot 和 desktop_ocr；它们的 bbox 仅为 screenshot-local 坐标，只能由 desktop_visual_click 读取截图 manifest 后转换。不得把 OCR bbox 传给 desktop_click。visual_focus_token 只能在 desktop_visual_click 为输入框/搜索框 bbox 返回后使用，短时一次性，只允许起草文本，不授权发送。当前没有任意桌面坐标点击工具。高级浏览器内部能力可通过 browser_execute_js 的 JSON 命令路由使用，但普通动作优先用高层浏览器工具。普通网页不要默认使用 OCR，只有 DOM 文本不可用时才考虑截图/OCR。完成有复用价值或耗时较长的任务后，可调用 start_long_term_update；只沉淀经过验证、未来可复用的记忆，普通一次性事实应 skip。任务完成后直接给用户简洁结论。" + sopIndex + skillIndex + capabilityIndex
}

func buildSystemPrompt(cfg Config) string {
	return BuildSystemPrompt(cfg, nil)
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
