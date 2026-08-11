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
	"cohort/internal/debugperf"
	"cohort/internal/desktop"
	"cohort/internal/evolution"
	"cohort/internal/hooks"
	"cohort/internal/llm"
	"cohort/internal/lsp"
	"cohort/internal/mcp"
	"cohort/internal/observability"
	"cohort/internal/plan"
	"cohort/internal/plugin"
	"cohort/internal/project"
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
	// #region debug-point A:runner-startup
	debugStart := time.Now()
	debugperf.Event("pre-fix", "A", "internal/app/app.go:NewRunner", "NewRunner start", map[string]any{
		"workspace": cfg.Workspace,
		"log_dir":   cfg.LogDir,
	})
	// #endregion
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
	// #region debug-point A:llm-client-built
	debugperf.Event("pre-fix", "A", "internal/app/app.go:NewRunner", "LLM client built", map[string]any{
		"elapsed_ms": debugperf.Since(debugStart),
		"provider":   active.Provider,
		"model":      active.Model,
		"stream":     active.Stream,
	})
	// #endregion

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mcpStart := time.Now()
	mcpManager := mcp.NewManager()
	if cfg.Tools.groupEnabled("mcp") {
		loadedMCPManager, loadErr := loadMCPManager(context.Background(), cwd)
		if loadErr != nil {
			return nil, loadErr
		}
		mcpManager = loadedMCPManager
	}
	// #region debug-point A:mcp-loaded
	debugperf.Event("pre-fix", "A", "internal/app/app.go:NewRunner", "MCP manager loaded", map[string]any{
		"elapsed_ms":       debugperf.Since(debugStart),
		"mcp_elapsed_ms":   debugperf.Since(mcpStart),
		"registered_tools": len(mcpManager.Tools()),
		"enabled":          cfg.Tools.groupEnabled("mcp"),
	})
	// #endregion
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
	// #region debug-point B:skill-loaded
	debugperf.Event("pre-fix", "B", "internal/app/app.go:NewRunner", "Skill store loaded", map[string]any{
		"elapsed_ms": debugperf.Since(debugStart),
	})
	// #endregion
	var browserClient browser.Client
	if cfg.Tools.groupEnabled("browser") {
		browserClient = newBrowserClient()
	}
	registryStart := time.Now()
	registry := newRegistry(workspace, cwd, browserClient, mcpManager, mcpPermissions, skillStore, cfg.Tools)
	schemaCount := len(registry.Schemas())
	// #region debug-point B:registry-built
	debugperf.Event("pre-fix", "B", "internal/app/app.go:NewRunner", "Tool registry built", map[string]any{
		"elapsed_ms":          debugperf.Since(debugStart),
		"registry_elapsed_ms": debugperf.Since(registryStart),
		"schema_count":        schemaCount,
		"enabled_groups":      cfg.Tools.normalizedGroups(),
	})
	// #endregion
	sessionStore := session.NewStore(session.DefaultRootDir)
	sessionRoot := normalizeWorkspace(sessionStore.RootDir)
	contextManager := &contextmgr.Manager{
		Config:     cfg.Context.Normalize(),
		MemoryRoot: filepath.Join(workspace, "memory"),
	}
	reflectionQueue := evolution.NewReflectionQueue(cwd)
	reflectionHook := evolution.NewSessionEndReflectionHandler(
		reflectionQueue,
		evolution.SessionEndReflectionConfig{
			Enabled:         cfg.Reflection.AutoEnqueue,
			ProjectRoot:     cwd,
			MemoryWorkspace: workspace,
			SessionRoot:     sessionRoot,
			Debounce:        time.Duration(cfg.Reflection.DebounceSeconds) * time.Second,
			MaxAttempts:     cfg.Reflection.MaxAttempts,
		},
	)

	// Runner 不直接知道具体工具类型，只依赖 ToolRunner 接口。
	runner := &agent.Runner{
		Client:                    client,
		Tools:                     registry,
		SystemPrompt:              BuildSystemPromptForProject(cfg, skillStore, cwd),
		MaxTurns:                  cfg.MaxTurns,
		LogDir:                    filepath.Clean(cfg.LogDir),
		ContextManager:            contextManager,
		SessionStore:              &sessionStore,
		SessionCWD:                cwd,
		SessionModel:              active.Model,
		RunMode:                   agent.RunModeInteractive,
		ReflectionMemoryWorkspace: workspace,
		ReflectionSessionRoot:     sessionRoot,
		CloseFunc: func() error {
			lsp.CloseRoot(workspace)
			return mcpManager.Close()
		},
		SkillStore:       skillStore,
		ObservationSinks: buildObservationSinks(cfg.Observability),
		Hooks:            hooks.NewRegistry(reflectionHook),
	}
	// #region debug-point A:runner-ready
	debugperf.Event("pre-fix", "A", "internal/app/app.go:NewRunner", "NewRunner ready", map[string]any{
		"elapsed_ms":          debugperf.Since(debugStart),
		"schema_count":        schemaCount,
		"system_prompt_chars": len([]rune(runner.SystemPrompt)),
	})
	// #endregion
	return runner, nil
}

func buildObservationSinks(cfg ObservabilityConfig) []observability.Sink {
	cfg = normalizeObservabilityConfig(cfg)
	langfuse := cfg.Langfuse
	if !langfuse.Enabled || strings.TrimSpace(langfuse.PublicKey) == "" || strings.TrimSpace(langfuse.SecretKey) == "" {
		return nil
	}
	return []observability.Sink{
		observability.NewAsyncSink(observability.NewLangfuseSink(observability.LangfuseSinkConfig{
			Host:        langfuse.Host,
			PublicKey:   langfuse.PublicKey,
			SecretKey:   langfuse.SecretKey,
			Environment: langfuse.Environment,
			Release:     langfuse.Release,
			Timeout:     time.Duration(langfuse.TimeoutSeconds) * time.Second,
		}), 256),
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
	mcpManager := mcp.NewManager()
	if cfg.Tools.groupEnabled("mcp") {
		loadedMCPManager, loadErr := loadMCPManager(context.Background(), cwd)
		if loadErr != nil {
			return nil, loadErr
		}
		mcpManager = loadedMCPManager
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
		cwd,
		browser.NewUnavailableClient(browser.ErrNotConnected),
		mcpManager,
		mcpPermissions,
		skillStore,
		cfg.Tools,
	).Schemas(), nil
}

// newRegistry 集中注册当前 MVP 暴露给模型的本地工具。
func newRegistry(
	workspace string,
	projectRoot string,
	browserClient browser.Client,
	mcpManager *mcp.Manager,
	mcpPermissions *tools.MCPPermissionStore,
	skillStore *skill.Store,
	toolConfig ToolConfig,
) *tools.Registry {
	registry := tools.NewRegistry()
	desktopDriver := newDesktopDriver(workspace)
	confirmations := tools.NewConfirmationStore()
	visualFocuses := tools.NewVisualFocusStore()
	computerStore := computeruse.NewStore(computeruse.DefaultTargetTTL)
	if mcpPermissions == nil {
		mcpPermissions = tools.NewMCPPermissionStore()
	}
	if toolConfig.groupEnabled("core") {
		registry.Register(tools.NewFileRead(workspace))
		registry.Register(tools.NewFileWrite(workspace))
		registry.Register(tools.NewFilePatch(workspace))
		registry.Register(tools.NewCodeRun(workspace))
	}
	if toolConfig.groupEnabled("lsp") {
		registry.Register(tools.NewLSPDiagnostics(workspace))
		registry.Register(tools.NewLSPDefinition(workspace))
		registry.Register(tools.NewLSPReferences(workspace))
		registry.Register(tools.NewLSPHover(workspace))
		registry.Register(tools.NewLSPSymbols(workspace))
	}
	if toolConfig.groupEnabled("browser") {
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
	}
	if toolConfig.groupEnabled("desktop") {
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
	}
	if toolConfig.groupEnabled("computer") {
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
	}
	if toolConfig.groupEnabled("memory") {
		registry.Register(tools.NewUpdateWorkingCheckpoint())
		registry.Register(tools.NewStartLongTermUpdate(workspace))
		registry.Register(tools.NewMemoryProposeUpdate(workspace))
		registry.Register(tools.NewMemoryApplyUpdate(workspace))
	}
	if toolConfig.groupEnabled("ask") {
		registry.Register(tools.NewAskUser(confirmations))
	}
	if toolConfig.groupEnabled("skill") {
		registry.Register(tools.NewSkillRead(skillStore))
	}
	if toolConfig.groupEnabled("adapter") {
		registerEnabledCommandAdapters(registry, projectRoot)
	}
	if toolConfig.groupEnabled("mcp") && mcpManager != nil {
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

func registerEnabledCommandAdapters(registry *tools.Registry, projectRoot string) {
	if registry == nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	adapters, err := capability.NewStore(projectRoot).ListEnabledAdapters()
	if err != nil {
		return
	}
	for _, adapter := range adapters {
		if adapter.Type != capability.TypeTool {
			continue
		}
		entry := filepath.Join(projectRoot, filepath.FromSlash(adapter.Entry))
		manifest, err := plugin.Load(entry)
		if err != nil {
			continue
		}
		for _, command := range manifest.Manifest.Commands {
			if strings.TrimSpace(command.Name) == "" || len(command.Command) == 0 {
				continue
			}
			registry.Register(tools.NewCommandAdapterTool(manifest, command))
		}
	}
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
	return buildSystemPromptWithIndex(cfg, skillStore, "", "", "", "")
}

func BuildSystemPromptForProject(cfg Config, skillStore *skill.Store, projectRoot string) string {
	capabilityIndex := ""
	projectPrompt := ""
	planPrompt := ""
	componentMap := ""
	if strings.TrimSpace(projectRoot) != "" {
		capabilityIndex = capability.NewStore(projectRoot).IndexPrompt()
		projectPrompt = project.NewStore(projectRoot).Prompt()
		planPrompt = plan.NewStore(projectRoot).Prompt()
		componentMap = ComponentPrompt(cfg, projectRoot, skillStore)
	}
	return buildSystemPromptWithIndex(cfg, skillStore, capabilityIndex, projectPrompt, planPrompt, componentMap)
}

func buildSystemPromptWithIndex(cfg Config, skillStore *skill.Store, capabilityIndex string, projectPrompt string, planPrompt string, componentMap string) string {
	sopIndex := readSOPIndex()
	skillIndex := ""
	if skillStore != nil {
		skillIndex = skillStore.IndexPrompt()
	}
	var b strings.Builder
	if cfg.Language == "en" {
		b.WriteString("You are Cohort, a command-line local agent. Use only tools present in the current tool schema, keep responses concise, and stop when the user task is complete. Run independent tool calls in one response and keep dependent actions sequential.")
		b.WriteString(toolNarrationInstructionEN)
		if cfg.Tools.groupEnabled("ask") {
			b.WriteString(userQuestionInstructionEN)
		}
		b.WriteString(" Use Project Mode as the durable project contract when present. Use Plan Mode as recoverable task state and never complete a step without verification evidence. Use the Component Map as status-aware routing: configured is not connected, registered is not necessarily healthy, and only ready/running capabilities may be assumed operational.")
		if cfg.Tools.groupEnabled("core") {
			b.WriteString(" Use the SOP Index as navigation only. Read a matching SOP before acting.")
			if cfg.Tools.groupEnabled("memory") {
				b.WriteString(" After reading it, call update_working_checkpoint with its constraints and related_sop.")
			}
		}
		if cfg.Tools.groupEnabled("skill") {
			b.WriteString(" Use the Skill Index as navigation only. For a matching Skill, call skill_read before acting.")
			if cfg.Tools.groupEnabled("memory") {
				b.WriteString(" Then call update_working_checkpoint with related_skill and key constraints.")
			}
			b.WriteString(" Use the Capability Index only for an available capability. Skill capabilities require skill_read and must not be inferred from candidate, failed, disabled, or missing entries.")
		}
		if cfg.Tools.groupEnabled("browser") {
			b.WriteString(" For web tasks use browser_open, wait for load/stability, then browser_scan or browser_snapshot. Prefer selectors and real browser input; use DOM summary and OCR only as fallbacks, and verify state after actions.")
		}
		if cfg.Tools.groupEnabled("computer") {
			b.WriteString(" For native GUI tasks prefer computer_see -> computer_find -> computer_execute_step and verify each action. R2 actions require the exact one-time confirmation token; R3 actions are refused.")
		} else if cfg.Tools.groupEnabled("desktop") {
			b.WriteString(" For native desktop diagnosis use desktop_permissions -> desktop_windows -> desktop_activate -> desktop_ax_snapshot. Use only verified AX nodes or screenshot-local visual targets; never bypass desktop boundaries with scripts.")
		}
		if cfg.Tools.groupEnabled("memory") {
			b.WriteString(" After meaningful reusable work, consider start_long_term_update and persist only verified durable knowledge.")
		}
		b.WriteString(projectPrompt)
		b.WriteString(planPrompt)
		b.WriteString(componentMap)
		if cfg.Tools.groupEnabled("core") {
			b.WriteString(sopIndex)
		}
		if cfg.Tools.groupEnabled("skill") {
			b.WriteString(skillIndex)
		}
		if cfg.Tools.groupEnabled("skill") {
			b.WriteString(capabilityIndex)
		}
		return b.String()
	}
	b.WriteString("你是 Cohort，一个命令行本地 Agent。只能调用当前 tool schema 中真实存在的工具；回复保持简洁，任务完成后停止。互不依赖的工具调用应在同一轮并行发出，有依赖的动作保持顺序。")
	b.WriteString(toolNarrationInstructionZH)
	if cfg.Tools.groupEnabled("ask") {
		b.WriteString(userQuestionInstructionZH)
	}
	b.WriteString(" Project Mode 是项目级持久契约；Plan Mode 是可恢复任务状态，未取得验证证据不得完成步骤。Component Map 是带状态的路由真值：configured 不代表已连接，registered 不代表运行健康，只有 ready/running 可以直接视为可用。")
	if cfg.Tools.groupEnabled("core") {
		b.WriteString(" SOP Index 只用于导航；命中场景时先读取对应 SOP 再行动。")
		if cfg.Tools.groupEnabled("memory") {
			b.WriteString("读取后调用 update_working_checkpoint 保存关键约束和 related_sop。")
		}
	}
	if cfg.Tools.groupEnabled("skill") {
		b.WriteString(" Skill Index 只用于导航；命中 Skill 时先调用 skill_read 读取正文。")
		if cfg.Tools.groupEnabled("memory") {
			b.WriteString("随后调用 update_working_checkpoint 保存 related_skill 和关键约束。")
		}
		b.WriteString(" Capability Index 只允许使用 available capability；Skill 类型先调用 skill_read，不得从 candidate、failed、disabled 或 missing 条目推断能力。")
	}
	if cfg.Tools.groupEnabled("browser") {
		b.WriteString("网页任务使用 browser_open，等待 load/stable 后再用 browser_scan 或 browser_snapshot；优先 selector 和真实浏览器输入，DOM summary 与 OCR 仅作为回退，动作后必须验证状态。")
	}
	if cfg.Tools.groupEnabled("computer") {
		b.WriteString("原生 GUI 任务优先使用 computer_see -> computer_find -> computer_execute_step，并逐步验证。R2 动作必须使用精确的一次性确认令牌，R3 动作拒绝自动执行。")
	} else if cfg.Tools.groupEnabled("desktop") {
		b.WriteString("桌面诊断使用 desktop_permissions -> desktop_windows -> desktop_activate -> desktop_ax_snapshot；只能操作已验证 AX 节点或截图局部目标，不得用脚本绕过桌面边界。")
	}
	if cfg.Tools.groupEnabled("memory") {
		b.WriteString("完成有复用价值的任务后可调用 start_long_term_update，只沉淀经过验证的长期知识。")
	}
	b.WriteString("任务完成后直接给用户结论。")
	b.WriteString(projectPrompt)
	b.WriteString(planPrompt)
	b.WriteString(componentMap)
	if cfg.Tools.groupEnabled("core") {
		b.WriteString(sopIndex)
	}
	if cfg.Tools.groupEnabled("skill") {
		b.WriteString(skillIndex)
	}
	if cfg.Tools.groupEnabled("skill") {
		b.WriteString(capabilityIndex)
	}
	return b.String()
}

func buildSystemPrompt(cfg Config) string {
	return buildSystemPromptWithIndex(cfg, nil, "", "", "", "")
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
