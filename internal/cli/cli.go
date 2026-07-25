package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/mcp"
	"cohert/internal/repl"
	"cohert/internal/session"
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
  /session list           list local sessions
  /resume <id>            resume a session
  /compact                reserved for Context Manager
  /clear                  clear current in-memory session
  /exit                   exit

Environment:
  DEEPSEEK_API_KEY       required unless configs/config.yaml contains api_key
`)
}
