package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/evolution"
	"cohort/internal/mcp"
	"cohort/internal/plan"
	"cohort/internal/project"
	"cohort/internal/repl"
	"cohort/internal/session"
	"cohort/internal/skill"
	"cohort/internal/version"
)

const mcpProbeTimeout = 90 * time.Second

const reflectTaskUsage = "session-archive|mine-sop-candidates|mine-skill-candidates|memory-quality-report|tool-failure-report"

type globalOptions struct {
	ConfigPath string
}

// Run 是命令行入口的主分发函数。
// 它只负责解析用户输入的子命令，真正的 Agent 执行交给 agent.Runner。
func Run(args []string) error {
	opts, remaining, err := parseGlobalOptions(args)
	if err != nil {
		return err
	}
	args = remaining
	// 不带参数时默认进入交互模式，方便开发阶段直接 go run .
	if len(args) == 0 {
		args = []string{"run"}
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) > 1 && args[1] == "--all" {
			printFullHelp()
		} else {
			printHelp()
		}
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		printVersion(os.Stdout)
		return nil
	}
	if args[0] == "extension" {
		return runExtensionCommand(args[1:], os.Stdout)
	}
	if args[0] == "capability" {
		return runCapabilityCommand(args[1:], os.Stdout)
	}
	if args[0] == "lsp" {
		return runLSPCommand(context.Background(), args[1:], os.Stdout)
	}
	if args[0] == "plugin" {
		return runPluginCommand(args[1:], os.Stdout)
	}
	if args[0] == "tui" {
		return runTUICommand(args[1:], os.Stdout)
	}
	if args[0] == "ui" {
		return runUICommand(context.Background(), opts.ConfigPath, args[1:], os.Stdout)
	}
	if args[0] == "init" {
		return runInitCommand(opts, args[1:], os.Stdout)
	}
	if args[0] == "project" {
		return runProjectCommand(args[1:], os.Stdout)
	}
	if args[0] == "plan" {
		return runPlanCommand(args[1:], os.Stdout)
	}
	if args[0] == "trace" {
		if len(args) > 1 && args[1] == "replay" {
			configPath, resolveErr := app.ResolveConfigPath(opts.ConfigPath)
			if resolveErr != nil {
				return resolveErr
			}
			cfg, loadErr := app.LoadConfig(configPath)
			if loadErr != nil {
				return loadErr
			}
			return runTraceReplayCommand(context.Background(), cfg, args[2:], os.Stdout)
		}
		return runTraceCommand(args[1:], os.Stdout)
	}
	if args[0] == "perf" {
		return runPerfCommand(args[1:], os.Stdout)
	}
	if args[0] == "deliver" {
		return runDeliveryCommand(context.Background(), opts.ConfigPath, args[1:], os.Stdout)
	}

	configPath, err := app.ResolveConfigPath(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := app.LoadConfig(configPath)
	if args[0] == "doctor" {
		return runDoctorCommand(context.Background(), args[1:], configPath, cfg, err, os.Stdout)
	}
	if err != nil {
		return err
	}

	// config/tools 是轻量命令，不需要初始化 LLM Client，也不需要 API Key。
	switch args[0] {
	case "config":
		fmt.Printf("config_path: %s\n", configPath)
		active := cfg.LLM.Active()
		if cfg.LLM.ActiveProfile != "" {
			fmt.Printf("active_profile: %s\n", cfg.LLM.ActiveProfile)
		}
		if len(cfg.LLM.FallbackProfiles) > 0 {
			fmt.Printf("fallback_profiles: %s\n", strings.Join(cfg.LLM.FallbackProfiles, ","))
		}
		fmt.Printf("provider: %s\n", active.Provider)
		fmt.Printf("model: %s\napi_base: %s\nworkspace: %s\n", cfg.LLM.Model, cfg.LLM.APIBase, cfg.Workspace)
		fmt.Printf("context.resolved_window_tokens: %d\n", cfg.Context.ContextWindowTokens)
		fmt.Printf("context.output_reserve_tokens: %d\n", cfg.Context.MaxOutputTokens)
		fmt.Printf("context.safety_tokens: %d\n", cfg.Context.SafetyTokens)
		fmt.Printf("context.compact_trigger_ratio: %.2f\n", cfg.Context.CompactTriggerRatio)
		fmt.Printf("context.max_history_messages: %d\n", cfg.Context.MaxHistoryMessages)
		fmt.Printf("context.max_tool_result_chars: %d\n", cfg.Context.MaxToolResultChars)
		fmt.Printf("context.max_request_chars: %d\n", cfg.Context.MaxRequestChars)
		fmt.Printf("context.enable_micro_compact: %t\n", cfg.Context.EnableMicroCompact)
		fmt.Printf("observability.auto_refresh: %t\n", cfg.Observability.AutoRefresh)
		fmt.Printf("observability.auto_refresh_limit: %d\n", cfg.Observability.AutoRefreshLimit)
		fmt.Printf("tools.adaptive_routing: %t\n", cfg.Tools.AdaptiveRouting)
		fmt.Printf("tools.adaptive_max_external_tools: %d\n", cfg.Tools.AdaptiveMaxExternalTools)
		fmt.Printf("tools.adaptive_failure_threshold: %d\n", cfg.Tools.AdaptiveFailureThreshold)
		fmt.Printf("tools.adaptive_min_schema_count: %d\n", cfg.Tools.AdaptiveMinSchemaCount)
		fmt.Printf("reflection.auto_enqueue: %t\n", cfg.Reflection.AutoEnqueue)
		fmt.Printf("reflection.debounce_seconds: %d\n", cfg.Reflection.DebounceSeconds)
		fmt.Printf("reflection.max_attempts: %d\n", cfg.Reflection.MaxAttempts)
		if cfg.LLM.APIKey == "" {
			fmt.Println("api_key: missing")
		} else {
			fmt.Println("api_key: set")
		}
		return nil
	case "components":
		return runComponentsCommand(cfg, args[1:], os.Stdout)
	case "session":
		return runSessionCommand(context.Background(), cfg, args[1:])
	case "reflect":
		return runReflectCommand(cfg, args[1:], os.Stdout)
	case "tuning":
		return runTuningCommand(cfg, args[1:], os.Stdout)
	case "eval":
		return runEvalCommand(context.Background(), cfg, args[1:], os.Stdout)
	case "hermes":
		return runHermesCommand(context.Background(), cfg, args[1:], os.Stdout)
	case "explorer":
		return runExplorerCommand(context.Background(), cfg, configPath, args[1:], os.Stdout)
	case "tools":
		schemas, schemasErr := app.ToolSchemas(cfg)
		if schemasErr != nil {
			return schemasErr
		}
		if len(args) > 1 {
			if args[1] != "route" || len(args) < 3 {
				return errors.New(`usage: cohort tools route "task"`)
			}
			selected, decision := agent.PlanAdaptiveToolRoute(
				agent.AdaptiveToolRoutingConfig{
					Enabled:          cfg.Tools.AdaptiveRouting,
					MaxExternalTools: cfg.Tools.AdaptiveMaxExternalTools,
					FailureThreshold: cfg.Tools.AdaptiveFailureThreshold,
					MinSchemaCount:   cfg.Tools.AdaptiveMinSchemaCount,
				},
				strings.Join(args[2:], " "),
				schemas,
			)
			data, marshalErr := json.MarshalIndent(decision, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			fmt.Println(string(data))
			fmt.Println("selected_tools:")
			for _, schema := range selected {
				fmt.Printf("- %s\n", schema.Function.Name)
			}
			return nil
		}
		for _, schema := range schemas {
			fmt.Println(schema.Function.Name)
		}
		return nil
	case "mcp":
		return runMCPCommand(context.Background(), args[1:])
	case "skill":
		return runSkillCommand(context.Background(), args[1:])
	}

	// 真正执行任务前才创建 Runner，此时会检查 API Key、工作区、日志目录等。
	runner, err := app.NewRunner(cfg)
	if err != nil {
		return err
	}
	defer runner.Close()

	switch args[0] {
	case "run":
		return startREPL(context.Background(), cfg, runner)
	case "ask":
		if len(args) < 2 {
			return errors.New(`usage: cohort ask "your task"`)
		}
		task := strings.Join(args[1:], " ")
		_, err := runner.Run(context.Background(), task, agent.NewConsoleSink(os.Stdout))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runComponentsCommand(cfg app.Config, args []string, out io.Writer) error {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			return fmt.Errorf("unknown components option %q", arg)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	inventory := app.BuildComponentInventory(cfg, root, nil)
	if jsonOutput {
		data, err := json.MarshalIndent(inventory, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	fmt.Fprintf(out, "project_root: %s\nworkspace: %s\n\n", inventory.ProjectRoot, inventory.Workspace)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSTATUS\tAGENT_ROUTE\tDETAIL\tCOMMAND")
	for _, component := range inventory.Components {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			component.ID,
			component.Kind,
			component.Status,
			component.AgentRoute,
			component.Detail,
			component.UserCommand,
		)
	}
	return w.Flush()
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	var opts globalOptions
	remaining := append([]string(nil), args...)
	for len(remaining) > 0 {
		arg := remaining[0]
		switch {
		case arg == "--":
			return opts, remaining[1:], nil
		case arg == "--config" || arg == "-c":
			if len(remaining) < 2 {
				return opts, nil, fmt.Errorf("%s requires a config file path", arg)
			}
			opts.ConfigPath = remaining[1]
			remaining = remaining[2:]
		case strings.HasPrefix(arg, "--config="):
			opts.ConfigPath = strings.TrimPrefix(arg, "--config=")
			remaining = remaining[1:]
		default:
			return opts, remaining, nil
		}
	}
	return opts, remaining, nil
}

func runProjectCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	store := project.NewStore(root)
	switch args[0] {
	case "init":
		force := false
		titleParts := make([]string, 0, len(args)-1)
		for _, arg := range args[1:] {
			if arg == "--force" {
				force = true
				continue
			}
			titleParts = append(titleParts, arg)
		}
		status, err := store.Init(strings.Join(titleParts, " "), force)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "project:")
		fmt.Fprintln(out, "  status: initialized")
		fmt.Fprintf(out, "  project_md: %s\n", status.ProjectPath)
		fmt.Fprintf(out, "  config: %s\n", status.ConfigPath)
		return nil
	case "status":
		status, err := store.Status()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "project:")
		fmt.Fprintf(out, "  root: %s\n", status.Root)
		fmt.Fprintf(out, "  project_md: %s\n", status.ProjectPath)
		fmt.Fprintf(out, "  config: %s\n", status.ConfigPath)
		if status.Exists {
			fmt.Fprintln(out, "  status: active")
		} else {
			fmt.Fprintln(out, "  status: not initialized")
			fmt.Fprintln(out, "  next: cohort project init <title>")
		}
		return nil
	default:
		return fmt.Errorf("unknown project command %q, use project init [title] or project status", args[0])
	}
}

func runPlanCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	store := plan.NewStore(root)
	switch args[0] {
	case "create":
		title, steps, err := parseCLIPlanCreateArgs(args[1:])
		if err != nil {
			return err
		}
		state, err := store.Create(title, steps)
		if err != nil {
			return err
		}
		printCLIPlanState(out, state, store.Path())
		return nil
	case "status":
		state, err := store.Load()
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "plan:")
			fmt.Fprintln(out, "  status: no active plan")
			fmt.Fprintln(out, "  next: cohort plan create <title> -- <step1> -- <step2>")
			return nil
		}
		if err != nil {
			return err
		}
		printCLIPlanState(out, state, store.Path())
		return nil
	case "start":
		if len(args) != 2 {
			return errors.New("usage: cohort plan start <step_id>")
		}
		id, err := plan.ParseStepID(args[1])
		if err != nil {
			return err
		}
		state, err := store.StartStep(id)
		if err != nil {
			return err
		}
		printCLIPlanState(out, state, store.Path())
		return nil
	case "verify":
		if len(args) < 3 {
			return errors.New("usage: cohort plan verify <step_id> <evidence>")
		}
		id, err := plan.ParseStepID(args[1])
		if err != nil {
			return err
		}
		state, err := store.VerifyStep(id, strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		printCLIPlanState(out, state, store.Path())
		return nil
	case "block":
		if len(args) < 2 {
			return errors.New("usage: cohort plan block <reason>")
		}
		state, err := store.Block(strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		printCLIPlanState(out, state, store.Path())
		return nil
	default:
		return fmt.Errorf("unknown plan command %q, use plan create|status|start|verify|block", args[0])
	}
}

func parseCLIPlanCreateArgs(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, errors.New("usage: cohort plan create <title> -- <step1> -- <step2>")
	}
	segments := [][]string{{}}
	for _, arg := range args {
		if arg == "--" {
			segments = append(segments, []string{})
			continue
		}
		segments[len(segments)-1] = append(segments[len(segments)-1], arg)
	}
	if len(segments) < 2 {
		return "Active Plan", []string{strings.Join(args, " ")}, nil
	}
	title := strings.Join(segments[0], " ")
	steps := make([]string, 0, len(segments)-1)
	for _, segment := range segments[1:] {
		step := strings.TrimSpace(strings.Join(segment, " "))
		if step != "" {
			steps = append(steps, step)
		}
	}
	return title, steps, nil
}

func printCLIPlanState(out io.Writer, state plan.State, path string) {
	fmt.Fprintln(out, "plan:")
	fmt.Fprintf(out, "  path: %s\n", path)
	fmt.Fprintf(out, "  title: %s\n", state.Title)
	fmt.Fprintf(out, "  status: %s\n", state.Status)
	for _, step := range state.Steps {
		fmt.Fprintf(out, "  - [%s] %d. %s\n", step.Status, step.ID, step.Text)
		if step.Evidence != "" {
			fmt.Fprintf(out, "    evidence: %s\n", step.Evidence)
		}
	}
}

func runMCPCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohort mcp add|list|status|tools|probe|remove ...")
	}
	projectRoot, getwdErr := os.Getwd()
	if getwdErr != nil {
		return getwdErr
	}
	store := mcp.NewStore(projectRoot)
	switch args[0] {
	case "list":
		return printMCPList(store)
	case "status":
		if len(args) != 1 {
			return errors.New("usage: cohort mcp status")
		}
		return printMCPStatus(ctx, store)
	case "add":
		return addMCPServer(store, args[1:])
	case "remove":
		return removeMCPServer(store, args[1:])
	case "import":
		return importMCPConfig(store, args[1:])
	case "export":
		return exportMCPConfig(store, args[1:])
	case "policy":
		return runMCPPolicyCommand(store, args[1:])
	case "tools", "probe":
		if len(args) != 2 {
			return fmt.Errorf("usage: cohort mcp %s <server>", args[0])
		}
		return inspectMCPServer(ctx, store, args[1], args[0] == "probe")
	default:
		return fmt.Errorf("unknown mcp command %q", args[0])
	}
}

func importMCPConfig(store mcp.Store, args []string) error {
	scope := mcp.ScopeProject
	merge := true
	source := ""
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--scope":
			if len(args) < 1 {
				return errors.New("--scope requires project, user, or local")
			}
			parsed, err := mcp.ParseScope(args[0])
			if err != nil {
				return err
			}
			scope = parsed
			args = args[1:]
		case "--replace":
			merge = false
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown mcp import option %q", arg)
			}
			if source != "" {
				return errors.New("usage: cohort mcp import [--scope project|user|local] [--replace] <path>")
			}
			source = arg
		}
	}
	if source == "" {
		return errors.New("usage: cohort mcp import [--scope project|user|local] [--replace] <path>")
	}
	count, err := store.Import(scope, source, merge)
	if err != nil {
		return err
	}
	fmt.Printf("mcp import: %d server(s) into %s scope\n", count, scope)
	return nil
}

func exportMCPConfig(store mcp.Store, args []string) error {
	scope := mcp.ScopeProject
	target := ""
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--scope":
			if len(args) < 1 {
				return errors.New("--scope requires project, user, or local")
			}
			parsed, err := mcp.ParseScope(args[0])
			if err != nil {
				return err
			}
			scope = parsed
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown mcp export option %q", arg)
			}
			if target != "" {
				return errors.New("usage: cohort mcp export [--scope project|user|local] <path>")
			}
			target = arg
		}
	}
	if target == "" {
		return errors.New("usage: cohort mcp export [--scope project|user|local] <path>")
	}
	if err := store.Export(scope, target); err != nil {
		return err
	}
	fmt.Printf("mcp export: %s scope -> %s\n", scope, target)
	return nil
}

func runMCPPolicyCommand(store mcp.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohort mcp policy list|set|remove ...")
	}
	switch args[0] {
	case "list":
		config, err := store.LoadPermissions()
		if err != nil {
			return err
		}
		fmt.Println("mcp policy:")
		if len(config.Rules) == 0 {
			fmt.Println("  rules: none")
			return nil
		}
		keys := make([]string, 0, len(config.Rules))
		for key := range config.Rules {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rule := config.Rules[key]
			fmt.Printf("  - %s risk=%s decision=%s args_policy=%s\n", key, mcp.NormalizeRisk(rule.Risk), rule.Decision, rule.ArgsPolicy)
		}
		return nil
	case "set":
		return setMCPPolicyRule(store, args[1:])
	case "remove":
		if len(args) != 3 {
			return errors.New("usage: cohort mcp policy remove <server> <tool>")
		}
		removed, _, err := store.DeletePermissionRule(args[1], args[2])
		if err != nil {
			return err
		}
		if removed {
			fmt.Printf("mcp policy removed: %s/%s\n", args[1], args[2])
		} else {
			fmt.Printf("mcp policy not found: %s/%s\n", args[1], args[2])
		}
		return nil
	default:
		return fmt.Errorf("unknown mcp policy command %q, use list, set, or remove", args[0])
	}
}

func setMCPPolicyRule(store mcp.Store, args []string) error {
	if len(args) < 4 {
		return errors.New("usage: cohort mcp policy set <server> <tool> <allow|ask|deny> <R1|R2|R3> [--args-policy exact_args|tool_scope]")
	}
	rule := mcp.ToolPermissionRule{
		Decision:   mcp.PermissionDecision(args[2]),
		Risk:       mcp.Risk(args[3]),
		ArgsPolicy: mcp.ArgsPolicyExact,
	}
	for _, arg := range args[4:] {
		switch {
		case strings.HasPrefix(arg, "--args-policy="):
			rule.ArgsPolicy = mcp.ArgsPolicy(strings.TrimPrefix(arg, "--args-policy="))
		default:
			return fmt.Errorf("unknown mcp policy option %q", arg)
		}
	}
	if _, err := store.SetPermissionRule(args[0], args[1], rule); err != nil {
		return err
	}
	fmt.Printf("mcp policy set: %s/%s risk=%s decision=%s args_policy=%s\n", args[0], args[1], mcp.NormalizeRisk(rule.Risk), rule.Decision, rule.ArgsPolicy)
	return nil
}

func runSkillCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohort skill install|doctor|list|show|reload ...")
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	store, err := app.LoadSkillStore(projectRoot)
	if err != nil {
		return err
	}
	switch args[0] {
	case "install":
		return installSkill(ctx, projectRoot, args[1:])
	case "doctor":
		if len(args) != 2 {
			return errors.New("usage: cohort skill doctor <id>")
		}
		result, err := store.Doctor(args[1])
		if err != nil {
			return err
		}
		printSkillDoctor(result)
		if result.ErrorCount() > 0 {
			return fmt.Errorf("skill doctor found %d error(s)", result.ErrorCount())
		}
		return nil
	case "uninstall":
		if len(args) != 2 {
			return errors.New("usage: cohort skill uninstall <id>")
		}
		result, err := store.Uninstall(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("uninstalled skill %s\n", result.Skill.ID)
		fmt.Printf("  path: %s\n", result.Path)
		return nil
	case "update":
		opts, check, err := parseSkillUpdateArgs(args[1:])
		if err != nil {
			return err
		}
		if check {
			result, checkErr := store.CheckUpdate(ctx, opts)
			if checkErr != nil {
				return checkErr
			}
			printSkillUpdateCheck(result)
			return nil
		}
		result, updateErr := store.UpdateWithOptions(ctx, opts)
		if updateErr != nil {
			return updateErr
		}
		printSkillUpdateResult(result)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohort skill list")
		}
		return printSkillList(store.Skills())
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort skill show <id>")
		}
		return printSkill(store, args[1])
	case "reload":
		if len(args) != 1 {
			return errors.New("usage: cohort skill reload")
		}
		if err := store.Reload(); err != nil {
			return err
		}
		fmt.Printf("skills reloaded: %d\n", len(store.Skills()))
		return nil
	default:
		return fmt.Errorf("unknown skill command %q", args[0])
	}
}

func parseSkillUpdateArgs(args []string) (skill.UpdateOptions, bool, error) {
	opts := skill.UpdateOptions{}
	check := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--check":
			check = true
		case "--pin":
			if len(args) < 1 {
				return opts, false, errors.New("--pin requires a git ref")
			}
			opts.Pin = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, false, fmt.Errorf("unknown skill update option %q", arg)
			}
			if opts.ID == "" {
				opts.ID = arg
			} else if opts.Source == "" {
				opts.Source = arg
			} else {
				return opts, false, errors.New("usage: cohort skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
			}
		}
	}
	if opts.ID == "" {
		return opts, false, errors.New("usage: cohort skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
	}
	return opts, check, nil
}

func installSkill(ctx context.Context, projectRoot string, args []string) error {
	return installSkillWithConfirmation(ctx, projectRoot, args, os.Stdin, os.Stdout)
}

func installSkillWithConfirmation(ctx context.Context, projectRoot string, args []string, in io.Reader, out io.Writer) error {
	opts := skill.InstallOptions{ProjectRoot: projectRoot, Scope: skill.ScopeProject}
	assumeYes := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--scope":
			if len(args) < 1 {
				return errors.New("--scope requires project or user")
			}
			scope, err := skill.ParseScope(args[0])
			if err != nil {
				return err
			}
			opts.Scope = scope
			args = args[1:]
		case "--name":
			if len(args) < 1 {
				return errors.New("--name requires a skill name")
			}
			opts.Name = args[0]
			args = args[1:]
		case "--force":
			opts.Force = true
		case "--dry-run":
			opts.DryRun = true
		case "--yes", "-y":
			assumeYes = true
		case "--pin":
			if len(args) < 1 {
				return errors.New("--pin requires a git ref")
			}
			opts.Pin = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown skill install option %q", arg)
			}
			if opts.Source != "" {
				return errors.New("usage: cohort skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
			}
			opts.Source = arg
		}
	}
	if opts.Source == "" {
		return errors.New("usage: cohort skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
	}
	previewOpts := opts
	previewOpts.DryRun = true
	preview, err := skill.Install(ctx, previewOpts)
	if err != nil {
		return err
	}
	printSkillInstallResultTo(out, preview)
	if opts.DryRun {
		return nil
	}
	confirmed, err := confirmSkillInstall(in, out, assumeYes)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(out, "install cancelled")
		return nil
	}
	result, err := skill.Install(ctx, opts)
	if err != nil {
		return err
	}
	printSkillInstallResultTo(out, result)
	return nil
}

func confirmSkillInstall(in io.Reader, out io.Writer, assumeYes bool) (bool, error) {
	if assumeYes {
		fmt.Fprintln(out, "install_confirmed: true")
		return true, nil
	}
	fmt.Fprint(out, "Install this skill? [y/N] ")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

func printSkillInstallResult(result skill.InstallResult) {
	printSkillInstallResultTo(os.Stdout, result)
}

func printSkillInstallResultTo(out io.Writer, result skill.InstallResult) {
	if result.DryRun {
		fmt.Fprintf(out, "preview skill %s\n", result.Skill.ID)
	} else {
		fmt.Fprintf(out, "installed skill %s\n", result.Skill.ID)
	}
	fmt.Fprintf(out, "  name:        %s\n", result.Skill.Name)
	fmt.Fprintf(out, "  requires:    %s\n", result.Skill.Requires.Summary())
	fmt.Fprintf(out, "  source:      %s\n", result.Source)
	fmt.Fprintf(out, "  source_type: %s\n", result.SourceType)
	printSkillRefFieldsTo(out, result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
	fmt.Fprintf(out, "  destination: %s\n", result.Destination)
	fmt.Fprintf(out, "  files:       %d\n", result.Files)
	fmt.Fprintf(out, "  hash:        %s\n", result.ContentHash)
	if result.DryRun {
		fmt.Fprintln(out, "  dry_run:     true")
	}
	if result.Replaced {
		fmt.Fprintln(out, "  replaced:    true")
	}
	if result.WouldReplace {
		fmt.Fprintln(out, "  would_replace: true")
	}
	if result.DryRun {
		printSkillInstallSecurityReview(out, result)
	}
}

func printSkillInstallSecurityReview(out io.Writer, result skill.InstallResult) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "security review:")
	fmt.Fprintln(out, "  - Installing a Skill lets the agent load and follow this SKILL.md as task instructions.")
	fmt.Fprintln(out, "  - Review the instructions below before confirming install.")
	fmt.Fprintln(out, "  - Cohort does not auto-install dependencies, grant permissions, or run commands during install.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "SKILL.md preview:")
	fmt.Fprintln(out, "```markdown")
	if strings.TrimSpace(result.SkillFile) == "" {
		fmt.Fprintln(out, "(empty)")
	} else {
		fmt.Fprintln(out, sanitizeTerminalText(result.SkillFile))
	}
	if result.Truncated {
		fmt.Fprintf(out, "\n... truncated after %d bytes ...\n", len(result.SkillFile))
	}
	fmt.Fprintln(out, "```")
}

func sanitizeTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f:
			return '?'
		default:
			return r
		}
	}, value)
}

func printSkillUpdateResult(result skill.UpdateResult) {
	fmt.Printf("updated skill %s\n", result.Skill.ID)
	fmt.Printf("  requires:    %s\n", result.Skill.Requires.Summary())
	fmt.Printf("  source:      %s\n", result.Source)
	fmt.Printf("  source_type: %s\n", result.SourceType)
	printSkillRefFields(result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
	fmt.Printf("  destination: %s\n", result.Destination)
	fmt.Printf("  files:       %d\n", result.Files)
	fmt.Printf("  hash:        %s\n", result.ContentHash)
	if result.Replaced {
		fmt.Println("  replaced:    true")
	}
}

func printSkillUpdateCheck(result skill.UpdateCheckResult) {
	status := "update-available"
	if result.UpToDate {
		status = "up-to-date"
	}
	fmt.Printf("skill update check %s\n", result.Skill.ID)
	fmt.Printf("  status:         %s\n", status)
	fmt.Printf("  requires:       %s\n", result.Requires.Summary())
	fmt.Printf("  source:         %s\n", result.Source)
	fmt.Printf("  source_type:    %s\n", result.SourceType)
	printSkillRefFields(result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
	fmt.Printf("  destination:    %s\n", result.Destination)
	fmt.Printf("  files:          %d\n", result.Files)
	fmt.Printf("  current_hash:   %s\n", result.CurrentHash)
	if result.ManifestHash != "" {
		fmt.Printf("  manifest_hash:  %s\n", result.ManifestHash)
	}
	fmt.Printf("  candidate_hash: %s\n", result.CandidateHash)
}

func printSkillRefFields(sourceRef, requestedRef, resolvedRef string, pinned bool) {
	printSkillRefFieldsTo(os.Stdout, sourceRef, requestedRef, resolvedRef, pinned)
}

func printSkillRefFieldsTo(out io.Writer, sourceRef, requestedRef, resolvedRef string, pinned bool) {
	if requestedRef != "" {
		fmt.Fprintf(out, "  requested_ref: %s\n", requestedRef)
	}
	if sourceRef != "" {
		fmt.Fprintf(out, "  source_ref:   %s\n", sourceRef)
	}
	if resolvedRef != "" {
		fmt.Fprintf(out, "  resolved_ref: %s\n", resolvedRef)
	}
	if pinned {
		fmt.Fprintln(out, "  pinned:       true")
	}
}

func printSkillList(skills []skill.Skill) error {
	if len(skills) == 0 {
		fmt.Println("no skills")
		return nil
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSCOPE\tINVOKE\tREQUIRES\tPERMISSIONS\tNAME\tDESCRIPTION\tPATH")
	for _, item := range skills {
		invoke := "-"
		if item.UserInvocable {
			invoke = "/" + item.Alias
			if item.ArgumentHint != "" {
				invoke += " " + item.ArgumentHint
			}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.Scope, invoke, item.Requires.Summary(), item.Permissions.Summary(), item.Name, item.Description, item.Path)
	}
	return writer.Flush()
}

func printSkill(store *skill.Store, id string) error {
	result, err := store.Read(id)
	if err != nil {
		return err
	}
	fmt.Printf("id:        %s\n", result.Skill.ID)
	fmt.Printf("name:      %s\n", result.Skill.Name)
	fmt.Printf("scope:     %s\n", result.Skill.Scope)
	fmt.Printf("invocable: %t\n", result.Skill.UserInvocable)
	fmt.Printf("requires:  %s\n", result.Skill.Requires.Summary())
	if result.Skill.ArgumentHint != "" {
		fmt.Printf("hint:      %s\n", result.Skill.ArgumentHint)
	}
	fmt.Printf("path:      %s\n", result.Skill.Path)
	fmt.Printf("truncated: %t\n\n", result.Truncated)
	fmt.Println(result.Content)
	return nil
}

func printSkillDoctor(result skill.DoctorResult) {
	fmt.Printf("skill doctor %s\n", result.Skill.ID)
	fmt.Printf("  path:     %s\n", result.Path)
	fmt.Printf("  healthy:  %t\n", result.Healthy)
	fmt.Printf("  warnings: %d\n", result.WarningCount())
	fmt.Printf("  errors:   %d\n", result.ErrorCount())
	if result.Manifest != nil {
		fmt.Println("manifest:")
		fmt.Printf("  source:      %s\n", result.Manifest.Source)
		fmt.Printf("  source_type: %s\n", result.Manifest.SourceType)
		printSkillRefFields(result.Manifest.SourceRef, result.Manifest.RequestedRef, result.Manifest.ResolvedRef, result.Manifest.Pinned)
		fmt.Printf("  scope:       %s\n", result.Manifest.Scope)
		fmt.Printf("  alias:       %s\n", result.Manifest.Alias)
		fmt.Printf("  installed:   %s\n", result.Manifest.InstalledAt)
		fmt.Printf("  hash:        %s\n", result.Manifest.ContentHash)
	}
	fmt.Println("checks:")
	for _, check := range result.Checks {
		fmt.Printf("  [%s] %s: %s\n", check.Severity, check.Code, check.Message)
		if check.Detail != "" {
			fmt.Printf("        %s\n", check.Detail)
		}
	}
}

// printMCPStatus 尝试连接用户已经显式配置的 MCP Server，并输出运行状态。
//
// 它只读取 Store 的有效配置；空配置时不会启动任何子进程，直接显示 no MCP
// servers configured。status 是诊断命令，不会写入或添加飞书等默认 Server。
func printMCPStatus(ctx context.Context, store mcp.Store) error {
	servers, err := store.LoadEffective()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("no MCP servers configured")
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	manager := mcp.NewManager()
	manager.Load(timeoutCtx, servers)
	defer manager.Close()

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTRANSPORT\tSTATUS\tTOOLS\tDETAIL")
	for _, status := range manager.Statuses() {
		state := "unavailable"
		detail := status.Error
		if status.Available {
			state = "available"
			detail = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\n", status.Name, status.Transport, state, status.Tools, detail)
	}
	return writer.Flush()
}

func printMCPList(store mcp.Store) error {
	servers, err := store.LoadEffectiveWithScopes()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("no MCP servers configured")
		return nil
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSCOPE\tTRANSPORT\tTARGET")
	for _, entry := range servers {
		server := entry.Server
		target := server.Command
		if server.Type == mcp.TransportHTTP {
			target = server.URL
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", server.Name, entry.Scope, server.Type, target)
	}
	return writer.Flush()
}

func addMCPServer(store mcp.Store, args []string) error {
	scope := mcp.ScopeProject
	transport := mcp.TransportStdio
	env := map[string]string{}
	name := ""
	positionals := []string{}
	commandArgs := []string{}
	afterSeparator := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if afterSeparator {
			commandArgs = append(commandArgs, arg)
			continue
		}
		if arg == "--" {
			afterSeparator = true
			continue
		}
		switch arg {
		case "--scope":
			if len(args) < 1 {
				return errors.New("--scope requires project, user, or local")
			}
			parsed, err := mcp.ParseScope(args[0])
			if err != nil {
				return err
			}
			scope = parsed
			args = args[1:]
		case "--transport":
			if len(args) < 1 {
				return errors.New("--transport requires stdio or http")
			}
			transport = strings.ToLower(args[0])
			args = args[1:]
		case "-e", "--env":
			if len(args) < 1 {
				return errors.New("-e requires KEY=VALUE")
			}
			key, value, ok := strings.Cut(args[0], "=")
			if !ok || strings.TrimSpace(key) == "" {
				return fmt.Errorf("invalid env assignment %q", args[0])
			}
			env[key] = value
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown mcp add option %q", arg)
			}
			if name == "" {
				name = arg
			} else {
				positionals = append(positionals, arg)
			}
		}
	}
	if name == "" {
		return errors.New("usage: cohort mcp add [--scope project|user|local] [--transport http] [-e KEY=VALUE] <name> -- <command> [args...]")
	}
	server := mcp.ServerConfig{Name: name, Type: transport, Env: env}
	if transport == mcp.TransportHTTP {
		if len(positionals) != 1 || len(commandArgs) != 0 {
			return errors.New("usage: cohort mcp add --transport http <name> <url>")
		}
		server.URL = positionals[0]
	} else {
		if len(positionals) != 0 || len(commandArgs) == 0 {
			return errors.New("stdio MCP usage: cohort mcp add <name> -- <command> [args...]")
		}
		server.Command = commandArgs[0]
		server.Args = commandArgs[1:]
	}
	if err := store.Add(scope, server); err != nil {
		return err
	}
	path, _ := store.Path(scope)
	fmt.Printf("added MCP server %s to %s\n", name, path)
	return nil
}

func removeMCPServer(store mcp.Store, args []string) error {
	scope := mcp.ScopeProject
	if len(args) >= 2 && args[0] == "--scope" {
		parsed, err := mcp.ParseScope(args[1])
		if err != nil {
			return err
		}
		scope = parsed
		args = args[2:]
	}
	if len(args) != 1 {
		return errors.New("usage: cohort mcp remove [--scope project|user|local] <server>")
	}
	removed, err := store.Remove(scope, args[0])
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("MCP server %q is not configured in %s scope", args[0], scope)
	}
	fmt.Printf("removed MCP server %s from %s scope\n", args[0], scope)
	return nil
}

func inspectMCPServer(ctx context.Context, store mcp.Store, name string, probe bool) error {
	servers, err := store.LoadEffective()
	if err != nil {
		return err
	}
	var config *mcp.ServerConfig
	for i := range servers {
		if servers[i].Name == name {
			config = &servers[i]
			break
		}
	}
	if config == nil {
		return fmt.Errorf("MCP server %q is not configured", name)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	client, err := mcp.Open(timeoutCtx, *config)
	if err != nil {
		return err
	}
	defer client.Close()
	tools, err := client.ListTools(timeoutCtx)
	if err != nil {
		return err
	}
	if probe {
		fmt.Printf("MCP server %s is available (%s)\n", config.Name, config.Type)
	}
	if len(tools) == 0 {
		fmt.Println("no tools")
		return nil
	}
	for _, tool := range tools {
		fmt.Printf("%s\t%s\n", tool.Name, tool.Description)
	}
	return nil
}

// runSessionCommand 处理本地会话命令。
//
// list 只读取 temp/sessions，不需要 API Key；
// resume 会恢复历史并进入交互模式，继续对话时才需要初始化 Runner。
func runSessionCommand(ctx context.Context, cfg app.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohort session list | cohort session resume <id>")
	}
	store := session.NewStore(session.DefaultRootDir)

	switch args[0] {
	case "list":
		return printSessionList(store)
	case "resume":
		if len(args) < 2 {
			return errors.New("usage: cohort session resume <id>")
		}
		return resumeSession(ctx, cfg, store, args[1])
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
}

func runReflectCommand(cfg app.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cohort reflect once --task %s | status | drain | retry <job_id>", reflectTaskUsage)
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	queue := evolution.NewReflectionQueue(projectRoot)
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: cohort reflect status")
		}
		status, statusErr := queue.Status()
		if statusErr != nil {
			return statusErr
		}
		data, marshalErr := json.MarshalIndent(status, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Fprintln(out, string(data))
		return nil
	case "drain":
		if len(args) != 1 {
			return errors.New("usage: cohort reflect drain")
		}
		worker := evolution.NewReflectionWorker(queue, evolution.ReflectionWorkerConfig{})
		result, drainErr := worker.Drain(context.Background())
		data, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Fprintln(out, string(data))
		return drainErr
	case "retry":
		if len(args) != 2 {
			return errors.New("usage: cohort reflect retry <job_id>")
		}
		item, retryErr := queue.Retry(args[1])
		if retryErr != nil {
			return retryErr
		}
		fmt.Fprintf(out, "retried: %s\navailable_at: %s\n", item.ID, item.AvailableAt.Format(time.RFC3339))
		return nil
	case "once":
	default:
		return fmt.Errorf("unknown reflect command %q", args[0])
	}
	task := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--task":
			if i+1 >= len(args) {
				return errors.New("--task requires a reflection task")
			}
			task = args[i+1]
			i++
		case strings.HasPrefix(arg, "--task="):
			task = strings.TrimPrefix(arg, "--task=")
		default:
			return fmt.Errorf("unknown reflect option %q", arg)
		}
	}
	if task == "" {
		return fmt.Errorf("usage: cohort reflect once --task %s", reflectTaskUsage)
	}
	manager := evolution.NewManager(cfg.Workspace)
	result, reflectErr := manager.ReflectOnce(task, session.DefaultRootDir)
	if reflectErr != nil {
		return reflectErr
	}
	fmt.Fprintf(out, "reflect task: %s\n", result.Task)
	fmt.Fprintf(out, "sessions_scanned: %d\n", result.SessionsScanned)
	fmt.Fprintf(out, "history_messages: %d\n", result.HistoryMessages)
	if result.ToolFailures > 0 {
		fmt.Fprintf(out, "tool_failures: %d\n", result.ToolFailures)
	}
	if result.SOPCandidatesWritten > 0 {
		fmt.Fprintf(out, "sop_candidates_written: %d\n", result.SOPCandidatesWritten)
	}
	if result.MemoryHitSessions > 0 {
		fmt.Fprintf(out, "memory_hit_sessions: %d\n", result.MemoryHitSessions)
	}
	for _, path := range result.OutputPaths {
		fmt.Fprintf(out, "output: %s\n", path)
	}
	return nil
}

// printSessionList 输出本地已有会话。
//
// 这里使用 tabwriter，是为了让不同长度的标题和 ID 在终端里对齐，
// 但仍保持纯文本输出，方便复制和后续脚本处理。
func printSessionList(store session.Store) error {
	summaries, err := store.List()
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		fmt.Println("no sessions")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tTITLE\tMESSAGES\tUPDATED\tCWD")
	for _, summary := range summaries {
		sess := summary.Session
		fmt.Fprintf(
			writer,
			"%s\t%s\t%d\t%s\t%s\n",
			sess.ID,
			sess.Title,
			summary.MessageCount,
			sess.UpdatedAt.Format("2006-01-02 15:04:05"),
			sess.CWD,
		)
	}
	return writer.Flush()
}

// resumeSession 读取 history.jsonl 并把消息恢复到 Runner.history。
//
// 恢复后不会立刻向模型发送消息，而是进入 REPL，等待用户输入下一条任务；
// 下一条用户消息会继续追加到同一个 history.jsonl。
func resumeSession(ctx context.Context, cfg app.Config, store session.Store, sessionID string) error {
	sess, err := store.LoadMeta(sessionID)
	if err != nil {
		return err
	}
	history, err := store.LoadHistory(sessionID)
	if err != nil {
		return err
	}
	runner, err := app.NewRunner(cfg)
	if err != nil {
		return err
	}
	defer runner.Close()
	runner.ResumeSession(sess.ID, history)
	fmt.Printf("resumed session %s (%d messages): %s\n", sess.ID, len(history), sess.Title)
	return startREPL(ctx, cfg, runner)
}

// startREPL 启动新的对话内命令交互层。
// CLI 仍负责外部子命令分发；进入交互模式后，/model、/tools、/session 等命令由 repl 包处理。
func startREPL(ctx context.Context, cfg app.Config, runner *agent.Runner) error {
	store := session.NewStore(session.DefaultRootDir)
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	mcpStore := mcp.NewStore(projectRoot)
	refresher := newAsyncTuningRefresher(cfg, os.Stderr)
	defer refresher.Close()
	var queueAutoRefresh func()
	if refresher != nil {
		queueAutoRefresh = refresher.Queue
	}
	return repl.Start(ctx, repl.Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: store,
		MCPStore:     &mcpStore,
		In:           os.Stdin,
		Out:          os.Stdout,
		Err:          os.Stderr,
		EvalCommand: func(commandCtx context.Context, args []string, out io.Writer) error {
			return runEvalCommand(commandCtx, cfg, args, out)
		},
		TraceCommand: func(args []string, out io.Writer) error {
			return runTraceCommand(args, out)
		},
		PerfCommand: func(args []string, out io.Writer) error {
			return runPerfCommand(args, out)
		},
		TuningCommand: func(args []string, out io.Writer) error {
			return runTuningCommand(cfg, args, out)
		},
		QueueAutoRefresh: queueAutoRefresh,
	})
}

// printHelp 只展示普通用户完成安装、诊断和执行任务所需的稳定入口。
func printHelp() {
	fmt.Print(`Cohort

Usage:
  cohort                         start interactive Agent
  cohort ask "task"              run one task
  cohort init [--provider name]  initialize model configuration
  cohort doctor [--connect]      diagnose configuration and connectivity
  cohort doctor computer         diagnose browser and desktop runtime
  cohort extension open          install/open the Chrome Bridge
  cohort ui                      open the local visual control center
  cohort session list            list saved sessions
  cohort session resume <id>     resume a session
  cohort components              show component readiness
  cohort --version               show version

Inside the interactive Agent, type / to open the command menu.
Advanced operator and developer commands: cohort help --all
`)
}

// printFullHelp 输出高级运维和开发命令；普通帮助不再倾倒整套内部控制面。
func printFullHelp() {
	fmt.Print(`Cohort

Usage:
  cohort [--config file]  start interactive CLI
  cohort [--config file] ask "task"
                          run one task without entering REPL
  cohort ui [--no-open] [--listen 127.0.0.1:0]
                          start the local visual control center
  cohort --version        show version, commit, and build time
  cohort extension path   print local Chrome extension directory
  cohort extension open   open Chrome extensions page and print loading steps
  cohort capability list  list registered capabilities
  cohort capability gaps  list recorded capability gaps
  cohort capability suggestions
                          suggest builds for repeated unresolved gaps
  cohort capability show <id>
                          show a capability, gap, or proposal
  cohort capability doctor <capability_id>
                          diagnose artifacts, dependencies, and verification state
  cohort capability deps plan <proposal_id>
                          generate a dependency install plan without installing
  cohort capability deps approve <plan_id>
                          approve a dependency install plan
  cohort capability deps install <plan_id> [--dry-run]
                          install approved dependencies and record audit entries
  cohort capability deps list
                          list dependency install plans
  cohort capability propose "task"
                          record a gap and generate a proposal draft
  cohort capability build <proposal_id>
                          generate a project Skill scaffold for a proposal
  cohort capability adapter <proposal_id> --type tool|mcp
                          generate a reviewable Tool or MCP adapter scaffold
  cohort capability verify <capability_id>
                          run the capability smoke test
  cohort capability promote <capability_id>
                          mark a verified capability as available
  cohort capability enable <capability_id>
                          explicitly enable a promoted Tool/MCP adapter
  cohort capability disable <capability_id>
                          disable a registered capability
  cohort config           show effective config and config path
  cohort tools route "task"
                          preview adaptive tool routing without calling an LLM
  cohort deliver plan "requirement"
                          compile a repository-grounded acceptance contract and task DAG
  cohort deliver run <delivery_id>
                          execute isolated builders, integration, and deterministic gates
  cohort deliver integrate <delivery_id>
                          resume integration and fresh-evidence generation
  cohort deliver verify|revise <delivery_id>
                          run independent verifier council or bounded targeted revision
  cohort deliver review <delivery_id> [--open]
                          generate the offline acceptance/evidence review report
  cohort deliver approve|accept <delivery_id>
                          record human approval or approve+transactionally merge+reverify
  cohort deliver merge|recover <delivery_id>
                          resume approved merge or recover an interrupted transaction
  cohort deliver list|status|show|cancel
                          inspect or cancel persistent deliveries
  cohort components [--json]
                          show system component map and visibility status
  cohort project init [title]
                          bootstrap .cohort/project.md and project config entry
  cohort project status   show Project Mode state
  cohort plan create <title> -- <step1> -- <step2>
                          create recoverable .cohort/plan.json
  cohort plan start <id>  mark one plan step in progress
  cohort plan verify <id> <evidence>
                          complete one step with verification evidence
  cohort plan status      show Plan Mode state
  cohort init [--provider deepseek|local|anthropic] [--force]
                          create a user config at ~/.cohort/config.yaml
  cohort doctor [--connect]
                          check config, API key, provider, and local paths
  cohort doctor computer  check macOS computer-use permissions and helpers
  cohort lsp doctor [--language go|typescript|python|all] [--install]
                          check local diagnostic backends; --install installs missing tsc/pyright via npm
  cohort lsp diagnostics [--language go|typescript|python] [path...]
                          run read-only language diagnostics
  cohort lsp definition [--language go|typescript|python] <file:line:column>
                          find symbol definition; Go uses gopls, TS/Python use symbol_scan fallback
  cohort lsp references [--language go|typescript|python] [--declaration] <file:line:column>
                          find symbol references; Go uses gopls, TS/Python use symbol_scan fallback
  cohort lsp hover [--language go|typescript|python] <file:line:column>
                          show symbol hover/context
  cohort lsp symbols [--language go|typescript|python] [path]
                          list symbols in a file or directory
  cohort lsp server status|restart|stop [--language typescript|python|all]
                          inspect or manage persistent language servers
  cohort plugin list|show|doctor
                          inspect .cohort/plugins manifests
  cohort explorer create "question"
                          create a read-only explorer validation task
  cohort explorer list|show|run|run-batch
                          inspect or run isolated read-only explorer tasks and aggregate lanes
  cohort tui status|plan|diff|logs|explorers|watch
                          show terminal task, plan, diff, log, and explorer panels
  cohort trace last       show latest run timeline from run.log.jsonl
  cohort trace show <session_id> [--run <run_id>]
                          show one session run timeline
  cohort trace graph last|show <session_id> [--run id] [--out path] [--open] [--json]
                          build an offline causal DAG and critical-path analysis
  cohort trace replay exact <session_id> --run <run_id> [--json]
                          verify an offline replay bundle without model or tool side effects
  cohort trace replay fork <session_id> --run <run_id> --fork-turn N [--model name] [--system-prompt path] [--repeat N]
                          fork a historical run in isolated worktrees and emit a proof report
  cohort perf last        show latest run latency, usage, and bottlenecks
  cohort perf show <session_id> [--run <run_id>]
                          show one session run performance summary
  cohort tuning report [--limit N] [--out path]
                          generate an offline tuning report from run.log.jsonl
  cohort eval init [--force]
                          create built-in core, tool-routing, and stateful eval suites
  cohort eval list        list local eval suites
  cohort eval run [suite] [--case id] [--tag tag] [--workers N] [--repeat N] [--judge heuristic|llm]
                          run deterministic assertions and compare the previous baseline
  cohort eval judge run [run_id|latest] [--profile id]
                          run real LLM Judge over a persisted eval result
  cohort eval judge calibrate [--profile id]
                          run local Judge calibration samples
  cohort eval history     list persisted eval runs
  cohort eval report [run_id|latest] [--open]
                          generate or open the offline HTML dashboard
  cohort eval status      refresh and print the historical stability summary
  cohort eval stability [--open]
                          refresh the historical stability dashboard
  cohort eval stability report [--window N] [--suite id] [--profile id] [--open]
                          aggregate historical eval runs into a stability dashboard
  cohort eval stability cases [--flaky]
                          list unstable cases from historical eval runs
  cohort eval stability regressions
                          list pass-to-fail case regressions
  cohort hermes start|stop|status|logs
                          manage the local Hermes daemon
  cohort hermes fix|review|accept|reject|cancel|auto-repair
                          short workflow for Hermes repair tasks
  cohort hermes jobs init|add|list|show|run|enable|disable|remove
                          configure persistent scheduled eval jobs
  cohort hermes actions|repairs ...
                          advanced Hermes queue and repair commands
  cohort mcp list         list configured MCP servers
  cohort mcp status       check configured MCP server availability
  cohort mcp add ...      add an MCP server
  cohort mcp tools <name> inspect an MCP server's tools
  cohort mcp probe <name> verify an MCP server
  cohort mcp remove <name>
  cohort mcp import [--scope project|user|local] [--replace] <path>
                          import Claude-compatible MCP JSON
  cohort mcp export [--scope project|user|local] <path>
                          export MCP JSON for one scope
  cohort mcp policy list|set|remove ...
                          manage per-tool MCP risk and permission policy
  cohort skill install [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>
                          preview, confirm, then install a Skill
  cohort skill doctor <id>
                          diagnose an installed Skill
  cohort skill update [--check] [--pin git-ref] <id> [path-or-git-url]
                          update or check an installed Skill
  cohort skill uninstall <id>
                          remove an installed Skill
  cohort skill list       list discovered Skills
  cohort skill show <id>  show one Skill's SKILL.md
  cohort skill reload     rescan Skills
  cohort session list     list local sessions
  cohort session resume <id>
                          resume a local session and enter REPL
  cohort reflect once --task session-archive|mine-sop-candidates|mine-skill-candidates|memory-quality-report|tool-failure-report
                          generate offline reflection reports without starting an LLM
  cohort reflect status|drain|retry <job_id>
                          inspect, consume, or retry the persistent reflection queue

Development:
  go run .                start interactive CLI
  go run . ask "task"     run one task

Interactive slash commands:
  /help                   show in-REPL command help
  /model                  show current model
  /tools                  list tools
  /project status         show Project Mode files and pointers
  /project init <title>   bootstrap .cohort/project.md
  /plan status            show recoverable plan state
  /plan create <title> -- <step1> -- <step2>
                          create .cohort/plan.json
  /plan start <id>        mark one step in progress
  /plan verify <id> <evidence>
                          complete one step with verification evidence
  /mcp list               list configured MCP servers
  /mcp status             check MCP server availability
  /mcp tools <server>     inspect MCP server tools
  /mcp probe <server>     verify MCP server connectivity
  /skill install [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>
                          preview, confirm, then install a Skill
  /skill doctor <id>      diagnose an installed Skill
  /skill list             list discovered Skills
  /skill show <id>        show one Skill's SKILL.md
  /skill update [--check] [--pin git-ref] <id>
                          update or check an installed Skill
  /skill reload           rescan Skills and refresh system prompt
  /session list           list local sessions
  /resume <id>            resume a session
  /compact                reserved for Context Manager
  /diff                   review Git working tree changes
  /diff show [file]       show full diff
  /diff rollback <file> --confirm
                          rollback one tracked file
  /clear                  clear current in-memory session
  /exit                   exit

Environment:
  COHORT_CONFIG          optional config file path
  COHORT_BROWSER_EXTENSION_DIR
                         optional Cohort Browser Bridge extension directory
  COHORT_RUNTIME_SCRIPTS_DIR
                         optional runtime helper script directory
  COHORT_DESKTOP_DARWIN_HELPER_PATH
                         optional macOS desktop helper script path
  COHORT_BROWSER_OCR_HELPER_PATH
                         optional OCR helper script path
  DEEPSEEK_API_KEY       required unless active config contains api_key
`)
}

func printVersion(out io.Writer) {
	info := version.Current()
	fmt.Fprintf(out, "cohort %s\n", info.Version)
	fmt.Fprintf(out, "commit: %s\n", info.Commit)
	fmt.Fprintf(out, "built_at: %s\n", info.BuiltAt)
}
