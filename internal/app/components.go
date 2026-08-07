package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cohort/internal/capability"
	"cohort/internal/evaluation"
	"cohort/internal/hermes"
	"cohort/internal/lsp"
	"cohort/internal/mcp"
	"cohort/internal/plan"
	"cohort/internal/plugin"
	"cohort/internal/project"
	"cohort/internal/skill"
)

const maxComponentPromptItems = 18

// ComponentInventory 是 Cohort 对自身能力面的轻量快照。
//
// 它不启动 LLM、MCP server、Browser Bridge 或桌面 helper，只读取本地配置和状态文件。
// 这样既能给用户做 `cohort components` 总览，也能安全注入 system prompt，避免
// Agent 只看到零散 tool schema，却不知道系统级入口在哪里。
type ComponentInventory struct {
	GeneratedAt time.Time         `json:"generated_at"`
	ProjectRoot string            `json:"project_root"`
	Workspace   string            `json:"workspace"`
	Components  []ComponentStatus `json:"components"`
}

type ComponentStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	AgentRoute  string `json:"agent_route,omitempty"`
	UserCommand string `json:"user_command,omitempty"`
}

// BuildComponentInventory 收集当前项目的组件地图。
func BuildComponentInventory(cfg Config, projectRoot string, skillStore *skill.Store) ComponentInventory {
	if strings.TrimSpace(projectRoot) == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectRoot = cwd
		}
	}
	if abs, err := filepath.Abs(projectRoot); err == nil {
		projectRoot = abs
	}
	projectRoot = filepath.Clean(projectRoot)
	workspace := normalizeWorkspace(cfg.Workspace)
	inventory := ComponentInventory{
		GeneratedAt: time.Now().UTC(),
		ProjectRoot: projectRoot,
		Workspace:   workspace,
	}
	add := func(component ComponentStatus) {
		component.ID = strings.TrimSpace(component.ID)
		if component.ID == "" {
			return
		}
		if component.Status == "" {
			component.Status = "unknown"
		}
		inventory.Components = append(inventory.Components, component)
	}

	addToolGroups(&inventory, cfg)
	addProjectPlanComponents(add, projectRoot)
	addSkillComponent(add, projectRoot, skillStore)
	addCapabilityComponents(add, projectRoot)
	addMCPComponent(add, projectRoot, cfg)
	addPluginComponent(add, projectRoot)
	addEvalComponent(add, projectRoot)
	addHermesComponent(add, projectRoot)
	addObservabilityComponent(add, cfg)

	sort.SliceStable(inventory.Components, func(i, j int) bool {
		return inventory.Components[i].ID < inventory.Components[j].ID
	})
	return inventory
}

// ComponentPrompt 生成给模型看的紧凑组件地图。
func ComponentPrompt(cfg Config, projectRoot string, skillStore *skill.Store) string {
	inventory := BuildComponentInventory(cfg, projectRoot, skillStore)
	if len(inventory.Components) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Component Map]\n")
	b.WriteString("这是 Cohort 当前组件地图。遇到相关任务时优先按 agent_route 路由；需要人工自查或用户询问系统状态时，运行 user_command；完整地图可运行 `cohort components`。不要把 disabled/missing/empty 组件当作已可用能力。\n")
	count := 0
	for _, component := range inventory.Components {
		if count >= maxComponentPromptItems {
			fmt.Fprintf(&b, "- ... %d more components; use `cohort components` for the full map.\n", len(inventory.Components)-count)
			break
		}
		if component.Status == "disabled" && component.AgentRoute == "" {
			continue
		}
		fmt.Fprintf(&b, "- `%s` [%s/%s]: %s", component.ID, component.Kind, component.Status, component.Name)
		if component.AgentRoute != "" {
			fmt.Fprintf(&b, "; route=%s", component.AgentRoute)
		}
		if component.UserCommand != "" {
			fmt.Fprintf(&b, "; command=`%s`", component.UserCommand)
		}
		if component.Detail != "" {
			fmt.Fprintf(&b, "; %s", truncateComponentDetail(component.Detail, 160))
		}
		b.WriteByte('\n')
		count++
	}
	return strings.TrimRight(b.String(), "\n")
}

func addToolGroups(inventory *ComponentInventory, cfg Config) {
	groups := []struct {
		id      string
		name    string
		route   string
		command string
		detail  string
	}{
		{"tools.core", "文件、补丁和命令执行工具", "file_read/file_patch/code_run", "cohort tools", "用于代码阅读、修改和本地验证"},
		{"tools.lsp", "语言诊断和符号工具", "lsp_diagnostics/lsp_symbols/lsp_definition/lsp_references", "cohort lsp doctor", lspServerDetail(inventory.Workspace)},
		{"tools.browser", "浏览器 DOM、输入、截图和 OCR 工具", "browser_open/browser_snapshot/browser_dom_summary/browser_type_element", "cohort tools", "需要本机 Browser Bridge"},
		{"tools.desktop", "原生桌面 AX、截图和受控输入工具", "desktop_permissions/desktop_windows/desktop_ax_snapshot", "cohort doctor computer", "需要 macOS Accessibility 和 Screen Recording 权限"},
		{"tools.computer", "Observe-Act-Verify 电脑操作工具", "computer_see/computer_find/computer_execute_step", "cohort doctor computer", "基于目标缓存、窗口校验和风险确认"},
		{"tools.memory", "短期 checkpoint 和长期记忆工具", "update_working_checkpoint/start_long_term_update", "cohort reflect once --task memory-quality-report", "只沉淀已验证的可复用经验"},
		{"tools.skill", "Skill 读取工具", "skill_read", "cohort skill list", "系统 prompt 只注入 Skill 摘要，正文需显式读取"},
		{"tools.ask", "阻塞式用户确认工具", "ask_user", "cohort tools", "用于缺少授权、凭证或关键决策时阻塞询问"},
		{"tools.adapter", "已启用 command adapter 工具", "enabled adapter tools", "cohort capability list", "由 verified capability 显式 enable 后注册"},
		{"tools.mcp", "MCP 外部工具", "mcp_* tools", "cohort mcp status", "只加载显式配置的 MCP server"},
	}
	for _, group := range groups {
		status := "disabled"
		if cfg.Tools.groupEnabled(strings.TrimPrefix(group.id, "tools.")) {
			status = "enabled"
		}
		inventory.Components = append(inventory.Components, ComponentStatus{
			ID:          group.id,
			Name:        group.name,
			Kind:        "tool_group",
			Status:      status,
			Detail:      group.detail,
			AgentRoute:  enabledRoute(status, group.route),
			UserCommand: group.command,
		})
	}
}

func addProjectPlanComponents(add func(ComponentStatus), projectRoot string) {
	projectStatus, err := project.NewStore(projectRoot).Status()
	if err != nil {
		add(ComponentStatus{ID: "project.mode", Name: "Project Mode", Kind: "state", Status: "degraded", Detail: err.Error(), UserCommand: "cohort project status"})
	} else if projectStatus.Exists {
		add(ComponentStatus{ID: "project.mode", Name: "Project Mode", Kind: "state", Status: "ready", Detail: ".cohort/project.md exists", AgentRoute: "follow Project Mode hard constraints", UserCommand: "cohort project status"})
	} else {
		add(ComponentStatus{ID: "project.mode", Name: "Project Mode", Kind: "state", Status: "missing", Detail: "no .cohort/project.md", UserCommand: "cohort project init <title>"})
	}
	planState, err := plan.NewStore(projectRoot).Load()
	if errors.Is(err, os.ErrNotExist) {
		add(ComponentStatus{ID: "plan.mode", Name: "Plan Mode", Kind: "state", Status: "missing", Detail: "no active .cohort/plan.json", UserCommand: "cohort plan create <title> -- <step>"})
	} else if err != nil {
		add(ComponentStatus{ID: "plan.mode", Name: "Plan Mode", Kind: "state", Status: "degraded", Detail: err.Error(), UserCommand: "cohort plan status"})
	} else {
		add(ComponentStatus{ID: "plan.mode", Name: "Plan Mode", Kind: "state", Status: string(planState.Status), Detail: fmt.Sprintf("%s; %d steps", planState.Title, len(planState.Steps)), AgentRoute: "respect active plan state and verification evidence", UserCommand: "cohort plan status"})
	}
}

func addSkillComponent(add func(ComponentStatus), projectRoot string, skillStore *skill.Store) {
	if skillStore == nil {
		skillStore = skill.NewStore(projectRoot, "")
		if err := skillStore.Reload(); err != nil {
			add(ComponentStatus{ID: "skill.index", Name: "Skill Index", Kind: "registry", Status: "degraded", Detail: err.Error(), UserCommand: "cohort skill list"})
			return
		}
	}
	skills := skillStore.Skills()
	add(ComponentStatus{
		ID:          "skill.index",
		Name:        "Skill Index",
		Kind:        "registry",
		Status:      emptyIf(len(skills) > 0),
		Detail:      fmt.Sprintf("%d discovered skills", len(skills)),
		AgentRoute:  routeIf(len(skills) > 0, "match summary then call skill_read before using a Skill"),
		UserCommand: "cohort skill list",
	})
}

func addCapabilityComponents(add func(ComponentStatus), projectRoot string) {
	store := capability.NewStore(projectRoot)
	registry, err := store.Load()
	if err != nil {
		add(ComponentStatus{ID: "capability.registry", Name: "Capability Registry", Kind: "registry", Status: "degraded", Detail: err.Error(), UserCommand: "cohort capability list"})
		return
	}
	counts := map[string]int{}
	for _, item := range registry.Capabilities {
		counts[item.Status]++
	}
	detail := fmt.Sprintf("available=%d candidate=%d failed=%d disabled=%d gaps=%d proposals=%d",
		counts[capability.StatusAvailable], counts[capability.StatusCandidate], counts[capability.StatusFailed], counts[capability.StatusDisabled], len(registry.Gaps), len(registry.Proposals))
	status := "empty"
	if counts[capability.StatusAvailable] > 0 {
		status = "ready"
	}
	add(ComponentStatus{ID: "capability.registry", Name: "Capability Registry", Kind: "registry", Status: status, Detail: detail, AgentRoute: routeIf(counts[capability.StatusAvailable] > 0, "use only status=available capabilities; skill type still requires skill_read"), UserCommand: "cohort capability list"})
	adapters, err := store.ListEnabledAdapters()
	if err != nil {
		add(ComponentStatus{ID: "capability.adapters", Name: "Enabled Adapters", Kind: "registry", Status: "degraded", Detail: err.Error(), UserCommand: "cohort capability list"})
		return
	}
	add(ComponentStatus{ID: "capability.adapters", Name: "Enabled Adapters", Kind: "runtime", Status: emptyIf(len(adapters) > 0), Detail: fmt.Sprintf("%d enabled adapters", len(adapters)), AgentRoute: routeIf(len(adapters) > 0, "enabled tool adapters appear as tools on next runner start"), UserCommand: "cohort capability list"})
}

func addMCPComponent(add func(ComponentStatus), projectRoot string, cfg Config) {
	servers, err := mcp.NewStore(projectRoot).LoadEffectiveWithScopes()
	if err != nil {
		add(ComponentStatus{ID: "mcp.registry", Name: "MCP Registry", Kind: "registry", Status: "degraded", Detail: err.Error(), UserCommand: "cohort mcp status"})
		return
	}
	status := "disabled"
	if cfg.Tools.groupEnabled("mcp") {
		status = emptyIf(len(servers) > 0)
	}
	add(ComponentStatus{ID: "mcp.registry", Name: "MCP Registry", Kind: "registry", Status: status, Detail: fmt.Sprintf("%d configured servers", len(servers)), AgentRoute: enabledRoute(status, "use mcp_* tools exposed by configured servers"), UserCommand: "cohort mcp status"})
}

func addPluginComponent(add func(ComponentStatus), projectRoot string) {
	plugins, err := plugin.Discover(projectRoot)
	if err != nil {
		add(ComponentStatus{ID: "plugin.manifests", Name: "Plugin Manifests", Kind: "registry", Status: "degraded", Detail: err.Error(), UserCommand: "cohort plugin list"})
		return
	}
	add(ComponentStatus{ID: "plugin.manifests", Name: "Plugin Manifests", Kind: "registry", Status: emptyIf(len(plugins) > 0), Detail: fmt.Sprintf("%d project plugins", len(plugins)), UserCommand: "cohort plugin list"})
}

func addEvalComponent(add func(ComponentStatus), projectRoot string) {
	store := evaluation.NewStore(projectRoot)
	suiteCount := countJSONFiles(store.SuitesDir())
	results, err := store.ListResults()
	if err != nil {
		add(ComponentStatus{ID: "eval.suites", Name: "Eval Suites", Kind: "quality", Status: "degraded", Detail: err.Error(), UserCommand: "cohort eval list"})
		return
	}
	add(ComponentStatus{ID: "eval.suites", Name: "Eval Suites", Kind: "quality", Status: readyIf(suiteCount > 0), Detail: fmt.Sprintf("%d suites; %d saved runs", suiteCount, len(results)), AgentRoute: "use eval suites for regression verification and gates", UserCommand: "cohort eval list"})
}

func addHermesComponent(add func(ComponentStatus), projectRoot string) {
	store := hermes.NewStore(projectRoot)
	status := "stopped"
	var snapshot hermes.Status
	if data, err := os.ReadFile(store.StatusPath()); err == nil {
		_ = json.Unmarshal(data, &snapshot)
		if snapshot.Running {
			status = "running"
		}
	}
	queue, _ := store.LoadQueue()
	jobs, _ := store.LoadJobs()
	repairs, _ := store.LoadRepairs()
	open, critical, high := hermes.CountOpen(queue)
	detail := fmt.Sprintf("open_actions=%d critical=%d high=%d jobs=%d repairs=%d", open, critical, high, len(jobs.Jobs), len(repairs.Repairs))
	add(ComponentStatus{ID: "hermes.daemon", Name: "Hermes Quality Daemon", Kind: "runtime", Status: status, Detail: detail, AgentRoute: "use Hermes for scheduled eval, action queue, and repair workflow", UserCommand: "cohort hermes status"})
}

func addObservabilityComponent(add func(ComponentStatus), cfg Config) {
	status := "ready"
	detail := "local run.log.jsonl enabled"
	if cfg.Observability.Langfuse.Enabled {
		detail += "; langfuse enabled"
	}
	if cfg.Observability.AutoRefresh {
		detail += "; auto refresh enabled"
	}
	add(ComponentStatus{ID: "observability.trace", Name: "Trace and Performance Reports", Kind: "observability", Status: status, Detail: detail, AgentRoute: "use trace/perf/eval reports for post-run diagnosis", UserCommand: "cohort trace last"})
}

func lspServerDetail(root string) string {
	statuses := lsp.ServerStatuses(root)
	running := 0
	for _, status := range statuses {
		if status.Running {
			running++
		}
	}
	return fmt.Sprintf("persistent servers running=%d/%d", running, len(statuses))
}

func countJSONFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			count++
		}
	}
	return count
}

func readyIf(ok bool) string {
	if ok {
		return "ready"
	}
	return "missing"
}

func emptyIf(ok bool) string {
	if ok {
		return "ready"
	}
	return "empty"
}

func enabledRoute(status string, route string) string {
	if status == "enabled" || status == "ready" || status == "running" {
		return route
	}
	return ""
}

func routeIf(ok bool, route string) string {
	if ok {
		return route
	}
	return ""
}

func truncateComponentDetail(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}
