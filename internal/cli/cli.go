package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/mcp"
	"cohert/internal/repl"
	"cohert/internal/session"
	"cohert/internal/skill"
)

const mcpProbeTimeout = 90 * time.Second

// Run 是命令行入口的主分发函数。
// 它只负责解析用户输入的子命令，真正的 Agent 执行交给 agent.Runner。
func Run(args []string) error {
	// 不带参数时默认进入交互模式，方便开发阶段直接 go run .
	if len(args) == 0 {
		args = []string{"run"}
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printHelp()
		return nil
	}

	// 当前 MVP 先从项目根目录的 configs/config.yaml 读取配置。
	cfg, err := app.LoadConfig("configs/config.yaml")
	if err != nil {
		return err
	}

	// config/tools 是轻量命令，不需要初始化 LLM Client，也不需要 API Key。
	switch args[0] {
	case "config":
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
		if cfg.LLM.APIKey == "" {
			fmt.Println("api_key: missing")
		} else {
			fmt.Println("api_key: set")
		}
		return nil
	case "session":
		return runSessionCommand(context.Background(), cfg, args[1:])
	case "tools":
		schemas, schemasErr := app.ToolSchemas(cfg)
		if schemasErr != nil {
			return schemasErr
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
			return errors.New(`usage: cohert ask "your task"`)
		}
		task := strings.Join(args[1:], " ")
		_, err := runner.Run(context.Background(), task, agent.NewConsoleSink(os.Stdout))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runMCPCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohert mcp add|list|status|tools|probe|remove ...")
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	store := mcp.NewStore(projectRoot)
	switch args[0] {
	case "list":
		return printMCPList(store)
	case "status":
		if len(args) != 1 {
			return errors.New("usage: cohert mcp status")
		}
		return printMCPStatus(ctx, store)
	case "add":
		return addMCPServer(store, args[1:])
	case "remove":
		return removeMCPServer(store, args[1:])
	case "tools", "probe":
		if len(args) != 2 {
			return fmt.Errorf("usage: cohert mcp %s <server>", args[0])
		}
		return inspectMCPServer(ctx, store, args[1], args[0] == "probe")
	default:
		return fmt.Errorf("unknown mcp command %q", args[0])
	}
}

func runSkillCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cohert skill install|doctor|list|show|reload ...")
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
			return errors.New("usage: cohert skill doctor <id>")
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
			return errors.New("usage: cohert skill uninstall <id>")
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
			result, err := store.CheckUpdate(ctx, opts)
			if err != nil {
				return err
			}
			printSkillUpdateCheck(result)
			return nil
		}
		result, err := store.UpdateWithOptions(ctx, opts)
		if err != nil {
			return err
		}
		printSkillUpdateResult(result)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohert skill list")
		}
		return printSkillList(store.Skills())
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohert skill show <id>")
		}
		return printSkill(store, args[1])
	case "reload":
		if len(args) != 1 {
			return errors.New("usage: cohert skill reload")
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
				return opts, false, errors.New("usage: cohert skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
			}
		}
	}
	if opts.ID == "" {
		return opts, false, errors.New("usage: cohert skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
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
				return errors.New("usage: cohert skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
			}
			opts.Source = arg
		}
	}
	if opts.Source == "" {
		return errors.New("usage: cohert skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
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
	fmt.Fprintln(out, "  - Cohert does not auto-install dependencies, grant permissions, or run commands during install.")
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
	fmt.Fprintln(writer, "ID\tSCOPE\tINVOKE\tREQUIRES\tNAME\tDESCRIPTION\tPATH")
	for _, item := range skills {
		invoke := "-"
		if item.UserInvocable {
			invoke = "/" + item.Alias
			if item.ArgumentHint != "" {
				invoke += " " + item.ArgumentHint
			}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.Scope, invoke, item.Requires.Summary(), item.Name, item.Description, item.Path)
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
		return errors.New("usage: cohert mcp add [--scope project|user|local] [--transport http] [-e KEY=VALUE] <name> -- <command> [args...]")
	}
	server := mcp.ServerConfig{Name: name, Type: transport, Env: env}
	if transport == mcp.TransportHTTP {
		if len(positionals) != 1 || len(commandArgs) != 0 {
			return errors.New("usage: cohert mcp add --transport http <name> <url>")
		}
		server.URL = positionals[0]
	} else {
		if len(positionals) != 0 || len(commandArgs) == 0 {
			return errors.New("stdio MCP usage: cohert mcp add <name> -- <command> [args...]")
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
		return errors.New("usage: cohert mcp remove [--scope project|user|local] <server>")
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
		return errors.New("usage: cohert session list | cohert session resume <id>")
	}
	store := session.NewStore(session.DefaultRootDir)

	switch args[0] {
	case "list":
		return printSessionList(store)
	case "resume":
		if len(args) < 2 {
			return errors.New("usage: cohert session resume <id>")
		}
		return resumeSession(ctx, cfg, store, args[1])
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
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
	return repl.Start(ctx, repl.Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: store,
		MCPStore:     &mcpStore,
		In:           os.Stdin,
		Out:          os.Stdout,
		Err:          os.Stderr,
	})
}

// printHelp 输出当前支持的最小命令集合。
func printHelp() {
	fmt.Print(`Cohert

Usage:
  cohert                  start interactive CLI
  cohert ask "task"       run one task without entering REPL
  cohert tools            list mounted tools
  cohert config           show effective config
  cohert mcp list         list configured MCP servers
  cohert mcp status       check configured MCP server availability
  cohert mcp add ...      add an MCP server
  cohert mcp tools <name> inspect an MCP server's tools
  cohert mcp probe <name> verify an MCP server
  cohert mcp remove <name>
  cohert skill install [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>
                          preview, confirm, then install a Skill
  cohert skill doctor <id>
                          diagnose an installed Skill
  cohert skill update [--check] [--pin git-ref] <id> [path-or-git-url]
                          update or check an installed Skill
  cohert skill uninstall <id>
                          remove an installed Skill
  cohert skill list       list discovered Skills
  cohert skill show <id>  show one Skill's SKILL.md
  cohert skill reload     rescan Skills
  cohert session list     list local sessions
  cohert session resume <id>
                          resume a local session and enter REPL

Development:
  go run .                start interactive CLI
  go run . ask "task"     run one task

Interactive slash commands:
  /help                   show in-REPL command help
  /model                  show current model
  /tools                  list tools
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
  /clear                  clear current in-memory session
  /exit                   exit

Environment:
  DEEPSEEK_API_KEY       required unless configs/config.yaml contains api_key
`)
}
