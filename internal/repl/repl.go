package repl

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

	"github.com/chzyer/readline"
	"github.com/manifoldco/promptui"
	"golang.org/x/term"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/evolution"
	"cohert/internal/mcp"
	"cohert/internal/session"
	"cohert/internal/skill"
)

const (
	promptText = "cohert › "

	commandExit        = "exit"
	commandQuit        = "quit"
	commandHelp        = "help"
	commandTools       = "tools"
	commandModel       = "model"
	commandConfig      = "config"
	commandSession     = "session"
	commandResume      = "resume"
	commandCompact     = "compact"
	commandFullCompact = "full-compact"
	commandMemory      = "memory"
	commandSOP         = "sop"
	commandSkill       = "skill"
	commandMCP         = "mcp"
	commandClear       = "clear"

	sessionCommandList   = "list"
	sessionCommandResume = "resume"
	sessionCommandMemory = "memory"

	sopCommandCandidates = "candidates"
	sopCommandPromote    = "promote"

	skillCommandList    = "list"
	skillCommandShow    = "show"
	skillCommandInstall = "install"
	skillCommandDoctor  = "doctor"
	skillCommandReload  = "reload"
	skillCommandRun     = "run"
	skillCommandUpdate  = "update"
	skillCommandRemove  = "uninstall"

	mcpCommandList   = "list"
	mcpCommandStatus = "status"
	mcpCommandTools  = "tools"
	mcpCommandProbe  = "probe"

	mcpProbeTimeout = 90 * time.Second
)

// Options 是启动交互模式需要的依赖。
// CLI 负责加载配置和创建 Runner，REPL 只负责读取用户输入、展示界面和分发 slash 命令。
type Options struct {
	// Context 是 REPL 和 Agent 运行共用的取消上下文。
	Context context.Context
	// Config 是当前 CLI 加载后的运行配置。
	Config app.Config
	// Runner 是普通用户输入要交给的 Agent Runner。
	Runner *agent.Runner
	// SessionStore 是 slash 命令读取和恢复本地 session 的存储器。
	SessionStore session.Store
	// MCPStore 是当前项目的 MCP 配置入口。为空时 REPL 使用当前工作目录，
	// CLI 会显式注入启动目录，保证 /mcp 与外部 cohert mcp 命令看到同一份配置。
	MCPStore *mcp.Store
	// In 是 REPL 读取用户输入的来源。
	In io.Reader
	// Out 是 REPL 普通输出目标。
	Out io.Writer
	// Err 是 REPL 命令错误输出目标。
	Err io.Writer
}

// Start 启动 Cohert 交互模式。
//
// 普通输入会交给 Runner.Run；以 "/" 开头的输入会作为本地 slash 命令处理，
// 不会发送给模型。这样 `/model`、`/session list`、`/compact` 这类运行时控制命令
// 可以在对话框里完成，用户不需要退出后再跑额外 CLI 命令。
func Start(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	opts.Context = ctx
	if opts.In == nil {
		return fmt.Errorf("repl input is nil")
	}
	if opts.Out == nil {
		return fmt.Errorf("repl output is nil")
	}
	if opts.Err == nil {
		opts.Err = opts.Out
	}
	if opts.Runner == nil {
		return fmt.Errorf("repl runner is nil")
	}
	if opts.SessionStore.RootDir == "" {
		opts.SessionStore = session.NewStore(session.DefaultRootDir)
	}

	printWelcome(opts.Out, opts.Config, opts.Runner)

	reader, closeReader, err := newLineReader(opts)
	if err != nil {
		return err
	}
	defer closeReader()

	for {
		input, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			if err == readline.ErrInterrupt {
				fmt.Fprintln(opts.Out)
				continue
			}
			return err
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if isSlashInput(input) {
			done, err := handleSlashCommand(opts, parseSlashCommand(input))
			if err != nil {
				fmt.Fprintf(opts.Err, "command error: %v\n", err)
			}
			if done {
				return nil
			}
			continue
		}
		if _, err := opts.Runner.Run(ctx, input, agent.NewConsoleSink(opts.Out)); err != nil {
			fmt.Fprintf(opts.Err, "run error: %v\n", err)
		}
	}
}

type lineReader interface {
	ReadLine() (string, error)
}

type scannerLineReader struct {
	// scanner 从非交互输入流逐行读取命令。
	scanner *bufio.Scanner
	// out 用于在读取前打印提示符。
	out io.Writer
}

func (r *scannerLineReader) ReadLine() (string, error) {
	fmt.Fprint(r.out, "\n"+promptText)
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return r.scanner.Text(), nil
}

type readlineLineReader struct {
	// in 是真实终端输入文件。
	in *os.File
	// out 是 readline 普通输出目标。
	out io.Writer
	// err 是 readline 错误输出目标。
	err io.Writer
}

func (r readlineLineReader) ReadLine() (string, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            promptText,
		Stdin:             r.in,
		Stdout:            r.out,
		Stderr:            r.err,
		AutoComplete:      slashCompleter(),
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		return "", err
	}
	defer rl.Close()
	return rl.Readline()
}

func newLineReader(opts Options) (lineReader, func(), error) {
	if file, ok := opts.In.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return readlineLineReader{
			in:  file,
			out: opts.Out,
			err: opts.Err,
		}, func() {}, nil
	}

	reader := &scannerLineReader{
		scanner: bufio.NewScanner(opts.In),
		out:     opts.Out,
	}
	return reader, func() {}, nil
}

func slashCompleter() *readline.PrefixCompleter {
	return readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/model"),
		readline.PcItem("/config"),
		readline.PcItem("/tools"),
		readline.PcItem("/mcp",
			readline.PcItem("list"),
			readline.PcItem("status"),
			readline.PcItem("tools"),
			readline.PcItem("probe"),
		),
		readline.PcItem("/session",
			readline.PcItem("list"),
			readline.PcItem("resume"),
		),
		readline.PcItem("/resume"),
		readline.PcItem("/compact"),
		readline.PcItem("/full-compact"),
		readline.PcItem("/sop",
			readline.PcItem("candidates"),
			readline.PcItem("promote"),
		),
		readline.PcItem("/skill",
			readline.PcItem("install"),
			readline.PcItem("doctor"),
			readline.PcItem("list"),
			readline.PcItem("show"),
			readline.PcItem("run"),
			readline.PcItem("update"),
			readline.PcItem("uninstall"),
			readline.PcItem("reload"),
		),
		readline.PcItem("/clear"),
		readline.PcItem("/exit"),
	)
}

func isSlashInput(input string) bool {
	return strings.HasPrefix(input, "/") || input == commandExit || input == commandQuit
}

// SlashCommand 是对话内命令的解析结果。
// Raw 保留原始输入，Name 和 Args 用于后续分发。
type SlashCommand struct {
	// Raw 是用户输入的原始 slash 命令文本。
	Raw string
	// Name 是规范化后的命令名，不包含前导斜杠。
	Name string
	// Args 是命令名之后的空白分隔参数。
	Args []string
}

type slashMenuItem struct {
	// Usage 是展示给用户看的命令用法。
	Usage string
	// Description 是命令在选择菜单里的说明。
	Description string
	// Command 是选择该菜单项后实际执行的 slash 命令。
	Command SlashCommand
	// NeedSession 表示该命令需要当前 Runner 已绑定 session。
	NeedSession bool
}

func parseSlashCommand(input string) SlashCommand {
	raw := strings.TrimSpace(input)
	trimmed := strings.TrimPrefix(raw, "/")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return SlashCommand{Raw: raw}
	}
	return SlashCommand{
		Raw:  raw,
		Name: strings.ToLower(fields[0]),
		Args: fields[1:],
	}
}

func selectSlashCommand(opts Options) (SlashCommand, bool, error) {
	inFile, inOK := opts.In.(*os.File)
	outFile, outOK := opts.Out.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return SlashCommand{}, false, nil
	}

	items := []slashMenuItem{
		{
			Usage:       "/help",
			Description: "显示所有对话内命令",
			Command:     SlashCommand{Raw: "/help", Name: commandHelp},
		},
		{
			Usage:       "/model",
			Description: "查看当前模型、供应商和 API 地址",
			Command:     SlashCommand{Raw: "/model", Name: commandModel},
		},
		{
			Usage:       "/config",
			Description: "查看当前运行配置摘要",
			Command:     SlashCommand{Raw: "/config", Name: commandConfig},
		},
		{
			Usage:       "/tools",
			Description: "查看当前可用工具",
			Command:     SlashCommand{Raw: "/tools", Name: commandTools},
		},
		{
			Usage:       "/mcp",
			Description: "查看 MCP 管理命令",
			Command:     SlashCommand{Raw: "/mcp", Name: commandMCP},
		},
		{
			Usage:       "/session",
			Description: "查看当前 session 状态",
			Command:     SlashCommand{Raw: "/session", Name: commandSession},
		},
		{
			Usage:       "/session list",
			Description: "列出本地历史 session",
			Command:     SlashCommand{Raw: "/session list", Name: commandSession, Args: []string{sessionCommandList}},
		},
		{
			Usage:       "/resume <id>",
			Description: "恢复一个历史 session",
			Command:     SlashCommand{Raw: "/resume", Name: commandResume},
			NeedSession: true,
		},
		{
			Usage:       "/compact",
			Description: "生成或更新当前 session 的 memory.md",
			Command:     SlashCommand{Raw: "/compact", Name: commandCompact},
		},
		{
			Usage:       "/full-compact",
			Description: "生成或更新当前 session 的 compact.md",
			Command:     SlashCommand{Raw: "/full-compact", Name: commandFullCompact},
		},
		{
			Usage:       "/sop candidates",
			Description: "列出可人工升级的 SOP 候选",
			Command:     SlashCommand{Raw: "/sop candidates", Name: commandSOP, Args: []string{sopCommandCandidates}},
		},
		{
			Usage:       "/skill list",
			Description: "列出当前发现的 Skills",
			Command:     SlashCommand{Raw: "/skill list", Name: commandSkill, Args: []string{skillCommandList}},
		},
		{
			Usage:       "/skill reload",
			Description: "重新扫描 Skills 并刷新系统提示词",
			Command:     SlashCommand{Raw: "/skill reload", Name: commandSkill, Args: []string{skillCommandReload}},
		},
		{
			Usage:       "/clear",
			Description: "清空当前内存上下文，下一次输入创建新 session",
			Command:     SlashCommand{Raw: "/clear", Name: commandClear},
		},
		{
			Usage:       "/exit",
			Description: "退出 Cohert",
			Command:     SlashCommand{Raw: "/exit", Name: commandExit},
		},
	}

	selectPrompt := promptui.Select{
		Label: "Slash commands",
		Items: items,
		Size:  10,
		Templates: &promptui.SelectTemplates{
			Label:    "{{ . }}",
			Active:   "▸ {{ .Usage | cyan }}  {{ .Description }}",
			Inactive: "  {{ .Usage }}  {{ .Description }}",
			Selected: "✓ {{ .Usage | cyan }}",
			Details: `
{{ "Command" | faint }}: {{ .Usage }}
{{ "Action" | faint }}:  {{ .Description }}`,
		},
		Stdin:  inFile,
		Stdout: outFile,
	}
	index, _, err := selectPrompt.Run()
	if err != nil {
		if err == promptui.ErrInterrupt {
			return SlashCommand{}, true, nil
		}
		return SlashCommand{}, false, err
	}
	selected := items[index]
	cmd := selected.Command
	if selected.NeedSession {
		sessionID, err := promptSessionID(inFile, outFile)
		if err != nil {
			if err == promptui.ErrInterrupt {
				return SlashCommand{}, true, nil
			}
			return SlashCommand{}, false, err
		}
		cmd.Args = []string{sessionID}
		cmd.Raw = "/resume " + sessionID
	}
	return cmd, true, nil
}

func promptSessionID(inFile *os.File, outFile *os.File) (string, error) {
	prompt := promptui.Prompt{
		Label: "Session ID",
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("session id is required")
			}
			return nil
		},
		Stdin:  inFile,
		Stdout: outFile,
	}
	return prompt.Run()
}

func handleSlashCommand(opts Options, cmd SlashCommand) (bool, error) {
	switch cmd.Name {
	case "":
		selected, ok, err := selectSlashCommand(opts)
		if err != nil {
			return false, err
		}
		if !ok {
			printCommandPalette(opts.Out)
			return false, nil
		}
		return handleSlashCommand(opts, selected)
	case commandExit, commandQuit:
		fmt.Fprintln(opts.Out, "bye")
		return true, nil
	case commandHelp:
		printSlashHelp(opts.Out)
	case commandTools:
		printTools(opts.Out, opts.Runner)
	case commandMCP:
		return false, handleMCPCommand(opts, cmd.Args)
	case commandModel:
		printModel(opts.Out, opts.Config)
	case commandConfig:
		printConfig(opts.Out, opts.Config)
	case commandSession:
		return false, handleSessionCommand(opts, cmd.Args)
	case commandResume:
		if len(cmd.Args) == 0 {
			return false, fmt.Errorf("usage: /resume <session_id>")
		}
		return false, resumeSession(opts, cmd.Args[0])
	case commandCompact:
		return false, compactSessionMemory(opts)
	case commandFullCompact:
		return false, fullCompactSession(opts)
	case commandMemory:
		return false, printSessionMemory(opts.Out, opts.Runner)
	case commandSOP:
		return false, handleSOPCommand(opts, cmd.Args)
	case commandSkill:
		return false, handleSkillCommand(opts, cmd.Args)
	case commandClear:
		opts.Runner.Reset()
		fmt.Fprintln(opts.Out, "current in-memory session cleared; next task will create a new session")
	default:
		if handled, err := runInvocableSkillAlias(opts, cmd); handled || err != nil {
			return false, err
		}
		return false, fmt.Errorf("unknown slash command %q, use /help", cmd.Raw)
	}
	return false, nil
}

func handleSessionCommand(opts Options, args []string) error {
	if len(args) == 0 {
		printCurrentSession(opts.Out, opts.Runner)
		return nil
	}
	switch strings.ToLower(args[0]) {
	case sessionCommandList:
		return printSessionList(opts.Out, opts.SessionStore)
	case sessionCommandResume:
		if len(args) < 2 {
			return fmt.Errorf("usage: /session resume <session_id>")
		}
		return resumeSession(opts, args[1])
	case sessionCommandMemory:
		return printSessionMemory(opts.Out, opts.Runner)
	default:
		return fmt.Errorf("unknown session command %q, use /session list, /session memory, or /resume <id>", args[0])
	}
}

// handleMCPCommand 在正在运行的 REPL 内执行只读的 MCP 管理操作。
//
// list 不会启动任何进程；status/tools/probe 只连接用户已经显式装配的 Server。
// 这里不提供 add/remove，因为当前 Runner 的工具集合在启动时确定，运行中写配置却
// 不自动挂载会制造误导。添加或删除 Server 后应重启 REPL，或使用外部 CLI。
func handleMCPCommand(opts Options, args []string) error {
	if len(args) == 0 {
		printMCPHelp(opts.Out)
		return nil
	}
	store, err := mcpStoreForOptions(opts)
	if err != nil {
		return err
	}
	switch strings.ToLower(args[0]) {
	case mcpCommandList:
		if len(args) != 1 {
			return fmt.Errorf("usage: /mcp list")
		}
		return printMCPList(opts.Out, store)
	case mcpCommandStatus:
		if len(args) != 1 {
			return fmt.Errorf("usage: /mcp status")
		}
		return printMCPStatus(opts.Context, opts.Out, store)
	case mcpCommandTools, mcpCommandProbe:
		if len(args) != 2 {
			return fmt.Errorf("usage: /mcp %s <server>", args[0])
		}
		return printMCPTools(opts.Context, opts.Out, store, args[1], strings.EqualFold(args[0], mcpCommandProbe))
	default:
		return fmt.Errorf("unknown MCP command %q, use /mcp", args[0])
	}
}

// mcpStoreForOptions 解析 REPL 当前项目对应的配置存储。
func mcpStoreForOptions(opts Options) (mcp.Store, error) {
	if opts.MCPStore != nil {
		return *opts.MCPStore, nil
	}
	root, err := os.Getwd()
	if err != nil {
		return mcp.Store{}, err
	}
	return mcp.NewStore(root), nil
}

func printMCPHelp(out io.Writer) {
	fmt.Fprint(out, `mcp commands

  /mcp list                 查看已显式装配的 MCP Server
  /mcp status               检查所有已装配 Server 的连通性和工具数量
  /mcp tools <server>       列出一个 Server 暴露的工具
  /mcp probe <server>       完整握手并列出工具

提示：/mcp 不会添加默认 Server，也不会修改配置。
`)
}

func printMCPList(out io.Writer, store mcp.Store) error {
	servers, err := store.LoadEffectiveWithScopes()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Fprintln(out, "no MCP servers configured")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSCOPE\tTRANSPORT\tTARGET")
	for _, entry := range servers {
		target := entry.Server.Command
		if entry.Server.Type == mcp.TransportHTTP {
			target = entry.Server.URL
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", entry.Server.Name, entry.Scope, entry.Server.Type, target)
	}
	return writer.Flush()
}

func printMCPStatus(ctx context.Context, out io.Writer, store mcp.Store) error {
	servers, err := store.LoadEffective()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Fprintln(out, "no MCP servers configured")
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	manager := mcp.NewManager()
	manager.Load(timeoutCtx, servers)
	defer manager.Close()

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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

func printMCPTools(ctx context.Context, out io.Writer, store mcp.Store, name string, probe bool) error {
	servers, err := store.LoadEffective()
	if err != nil {
		return err
	}
	var config *mcp.ServerConfig
	for index := range servers {
		if servers[index].Name == name {
			config = &servers[index]
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
		fmt.Fprintf(out, "MCP server %s is available (%s)\n", config.Name, config.Type)
	}
	if len(tools) == 0 {
		fmt.Fprintln(out, "no tools")
		return nil
	}
	for _, tool := range tools {
		fmt.Fprintf(out, "%s\t%s\n", tool.Name, tool.Description)
	}
	return nil
}

func resumeSession(opts Options, sessionID string) error {
	sess, err := opts.SessionStore.LoadMeta(sessionID)
	if err != nil {
		return err
	}
	history, err := opts.SessionStore.LoadHistory(sessionID)
	if err != nil {
		return err
	}
	opts.Runner.ResumeSession(sess.ID, history)
	fmt.Fprintf(opts.Out, "resumed session %s (%d messages): %s\n", sess.ID, len(history), sess.Title)
	return nil
}

func printWelcome(out io.Writer, cfg app.Config, runner *agent.Runner) {
	sessionID := runner.SessionID()
	if sessionID == "" {
		sessionID = "new session"
	}
	tools := len(runner.ToolSchemas())
	active := cfg.LLM.Active()
	fmt.Fprintln(out, "╭────────────────────────────────────────────────────────────╮")
	fmt.Fprintln(out, "│ Cohert                                                     │")
	fmt.Fprintln(out, "│ Command-line Agent Runtime                                │")
	fmt.Fprintln(out, "├────────────────────────────────────────────────────────────┤")
	fmt.Fprintf(out, "│ Model      %-47s │\n", shorten(active.Model, 47))
	fmt.Fprintf(out, "│ Workspace  %-47s │\n", shorten(cfg.Workspace, 47))
	fmt.Fprintf(out, "│ Session    %-47s │\n", shorten(sessionID, 47))
	fmt.Fprintf(out, "│ Tools      %-47d │\n", tools)
	fmt.Fprintln(out, "├────────────────────────────────────────────────────────────┤")
	fmt.Fprintln(out, "│ 直接输入任务开始执行                                      │")
	fmt.Fprintln(out, "│ 输入 / 打开命令菜单；用 ↑↓ 选择，Enter 执行              │")
	fmt.Fprintln(out, "╰────────────────────────────────────────────────────────────╯")
}

func printSlashHelp(out io.Writer) {
	fmt.Fprint(out, `Cohert slash commands

  /help                    显示当前对话内命令
  /model                   查看当前模型配置摘要
  /config                  查看当前运行配置摘要
  /tools                   查看当前可用工具
  /mcp                     查看 MCP 管理命令
  /mcp list                查看已装配 MCP Server
  /mcp status              检查已装配 MCP Server 连通性
  /mcp tools <server>      列出 Server 工具
  /mcp probe <server>      完整握手并列出 Server 工具
  /session                 查看当前 session 状态
  /session list            列出本地历史 session
  /session memory          查看当前 session memory.md
  /session resume <id>     恢复指定 session
  /resume <id>             恢复指定 session 的简写
  /compact                 生成或更新当前 session 的 memory.md
  /full-compact            生成或更新当前 session 的 compact.md
  /memory                  查看当前 session memory.md
  /sop candidates          列出可升级为正式 SOP 的候选
  /sop promote <id>        生成 sops/*.md；确认后更新 sops/index.md
  /skill install [--yes] [--dry-run] [--pin ref] <source>
                           预览、确认并安装一个 Skill
  /skill doctor <id>       诊断一个已安装 Skill
  /skill list              列出当前发现的 Skills
  /skill show <id>         查看一个 Skill 的 SKILL.md
  /skill run <id> [args]   直接按指定 Skill 执行任务
  /<skill-alias> [args]    运行 user-invocable Skill 的快捷形式
  /skill update [--check] [--pin ref] <id>
                           更新或检查已安装 Skill
  /skill uninstall <id>    删除已安装 Skill
  /skill reload            重新扫描 Skills 并刷新系统提示词
  /clear                   清空当前内存上下文，下一次输入会创建新 session
  /exit                    退出 Cohert

普通输入不会走 slash 命令，会直接交给 Agent 执行。
`)
}

func printCommandPalette(out io.Writer) {
	fmt.Fprint(out, `Slash commands

  /help                 显示命令帮助
  /model                查看当前模型
  /config               查看运行配置
  /tools                查看工具列表
  /mcp                  MCP 管理命令
  /session              查看当前 session
  /session list         列出历史 session
  /session memory       查看 session memory
  /resume <id>          恢复 session
  /compact              生成或更新 session memory
  /full-compact         生成或更新 compact summary
  /memory               查看 session memory
  /sop candidates       列出 SOP 候选
  /sop promote <id>     升级候选 SOP；--confirm-index 显式更新索引
  /skill install <src>  预览确认后安装 Skill；可加 --yes/--dry-run/--pin
  /skill doctor <id>    诊断 Skill
  /skill list           列出 Skills
  /skill show <id>      查看 Skill 正文
  /skill run <id>       运行 Skill
  /skill update <id>    更新 Skill；可加 --check/--pin
  /skill uninstall <id> 删除 Skill
  /skill reload         重新扫描 Skills
  /clear                清空当前内存上下文
  /exit                 退出

提示：真实终端里输入 / 会打开可选择菜单；输入命令前缀后也可以按 Tab 补全。
`)
}

func printTools(out io.Writer, runner *agent.Runner) {
	fmt.Fprintln(out, "tools:")
	for _, schema := range runner.ToolSchemas() {
		fmt.Fprintf(out, "  - %s\n", schema.Function.Name)
	}
}

func printModel(out io.Writer, cfg app.Config) {
	active := cfg.LLM.Active()
	fmt.Fprintln(out, "model:")
	if cfg.LLM.ActiveProfile != "" {
		fmt.Fprintf(out, "  active_profile: %s\n", cfg.LLM.ActiveProfile)
	}
	if len(cfg.LLM.FallbackProfiles) > 0 {
		fmt.Fprintf(out, "  fallback_profiles: %s\n", strings.Join(cfg.LLM.FallbackProfiles, ","))
	}
	fmt.Fprintf(out, "  provider: %s\n", active.Provider)
	fmt.Fprintf(out, "  name:     %s\n", active.Name)
	fmt.Fprintf(out, "  model:    %s\n", active.Model)
	fmt.Fprintf(out, "  api_base: %s\n", active.APIBase)
	fmt.Fprintf(out, "  stream:   %t\n", active.Stream)
}

func printConfig(out io.Writer, cfg app.Config) {
	fmt.Fprintln(out, "config:")
	fmt.Fprintf(out, "  language:  %s\n", cfg.Language)
	fmt.Fprintf(out, "  workspace: %s\n", cfg.Workspace)
	fmt.Fprintf(out, "  log_dir:   %s\n", cfg.LogDir)
	fmt.Fprintf(out, "  max_turns: %d\n", cfg.MaxTurns)
	fmt.Fprintln(out, "context:")
	fmt.Fprintf(out, "  resolved_window_tokens:  %d\n", cfg.Context.ContextWindowTokens)
	fmt.Fprintf(out, "  output_reserve_tokens:   %d\n", cfg.Context.MaxOutputTokens)
	fmt.Fprintf(out, "  safety_tokens:           %d\n", cfg.Context.SafetyTokens)
	fmt.Fprintf(out, "  compact_trigger_ratio:   %.2f\n", cfg.Context.CompactTriggerRatio)
	fmt.Fprintf(out, "  max_history_messages:    %d\n", cfg.Context.MaxHistoryMessages)
	fmt.Fprintf(out, "  keep_recent_tool_results: %d\n", cfg.Context.KeepRecentToolResults)
	fmt.Fprintf(out, "  max_tool_result_chars:    %d\n", cfg.Context.MaxToolResultChars)
	fmt.Fprintf(out, "  max_request_chars:        %d\n", cfg.Context.MaxRequestChars)
	fmt.Fprintf(out, "  max_session_memory_chars: %d\n", cfg.Context.MaxSessionMemoryChars)
	fmt.Fprintf(out, "  max_compact_summary_chars: %d\n", cfg.Context.MaxCompactSummaryChars)
	fmt.Fprintf(out, "  enable_micro_compact:     %t\n", cfg.Context.EnableMicroCompact)
	printModel(out, cfg)
}

func printCurrentSession(out io.Writer, runner *agent.Runner) {
	sessionID := runner.SessionID()
	if sessionID == "" {
		sessionID = "new session"
	}
	fmt.Fprintln(out, "session:")
	fmt.Fprintf(out, "  id:       %s\n", sessionID)
	fmt.Fprintf(out, "  messages: %d\n", runner.HistoryLen())
}

func printSessionList(out io.Writer, store session.Store) error {
	summaries, err := store.List()
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		fmt.Fprintln(out, "no sessions")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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

func compactSessionMemory(opts Options) error {
	fmt.Fprintln(opts.Out, "compact:")
	fmt.Fprintln(opts.Out, "  generating session memory...")
	result, err := opts.Runner.CompactSessionMemory(opts.Context)
	if err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, "  status: updated memory.md")
	fmt.Fprintf(opts.Out, "  session: %s\n", result.SessionID)
	fmt.Fprintf(opts.Out, "  path: %s\n", result.Path)
	if result.BackedUp {
		fmt.Fprintf(opts.Out, "  backup: %s\n", result.BackupPath)
	} else {
		fmt.Fprintln(opts.Out, "  backup: none")
	}
	fmt.Fprintf(opts.Out, "  chars: %d\n", result.Chars)
	return nil
}

func fullCompactSession(opts Options) error {
	fmt.Fprintln(opts.Out, "full compact:")
	fmt.Fprintln(opts.Out, "  generating compact.md...")
	result, err := opts.Runner.FullCompactSession(opts.Context)
	if err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, "  status: updated compact.md")
	fmt.Fprintf(opts.Out, "  session: %s\n", result.SessionID)
	fmt.Fprintf(opts.Out, "  path: %s\n", result.Path)
	if result.BackedUp {
		fmt.Fprintf(opts.Out, "  backup: %s\n", result.BackupPath)
	} else {
		fmt.Fprintln(opts.Out, "  backup: none")
	}
	fmt.Fprintf(opts.Out, "  chars: %d\n", result.Chars)
	return nil
}

func printSessionMemory(out io.Writer, runner *agent.Runner) error {
	snapshot, err := runner.LoadSessionMemory()
	if err != nil {
		if errors.Is(err, agent.ErrNoActiveSession) {
			fmt.Fprintln(out, "memory:")
			fmt.Fprintln(out, "  status: no active session")
			return nil
		}
		return err
	}
	fmt.Fprintln(out, "memory:")
	fmt.Fprintf(out, "  session: %s\n", snapshot.SessionID)
	fmt.Fprintf(out, "  path: %s\n", snapshot.Path)
	if !snapshot.Exists {
		fmt.Fprintln(out, "  status: no session memory")
		return nil
	}
	fmt.Fprintf(out, "  chars: %d\n\n", snapshot.Chars)
	fmt.Fprintln(out, snapshot.Content)
	return nil
}

func handleSOPCommand(opts Options, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /sop candidates or /sop promote <id>")
	}
	switch strings.ToLower(args[0]) {
	case sopCommandCandidates:
		return printSOPCandidates(opts.Out, evolution.NewManager(opts.Config.Workspace))
	case sopCommandPromote:
		if len(args) < 2 {
			return fmt.Errorf("usage: /sop promote <id> [--confirm-index]")
		}
		return promoteSOPCandidate(opts, args[1], args[2:])
	default:
		return fmt.Errorf("unknown sop command %q, use /sop candidates or /sop promote <id>", args[0])
	}
}

func handleSkillCommand(opts Options, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /skill install [options] <source> | /skill doctor <id> | /skill list | /skill show <id> | /skill run <id> [arguments...] | /skill update <id> [source] | /skill uninstall <id> | /skill reload")
	}
	if opts.Runner.SkillStore == nil {
		return fmt.Errorf("skill store is not configured")
	}
	switch strings.ToLower(args[0]) {
	case skillCommandInstall:
		return installSkillFromREPL(opts, args[1:])
	case skillCommandDoctor:
		if len(args) != 2 {
			return fmt.Errorf("usage: /skill doctor <id>")
		}
		result, err := opts.Runner.SkillStore.Doctor(args[1])
		if err != nil {
			return err
		}
		printSkillDoctor(opts.Out, result)
		if result.ErrorCount() > 0 {
			return fmt.Errorf("skill doctor found %d error(s)", result.ErrorCount())
		}
		return nil
	case skillCommandList:
		return printSkillList(opts.Out, opts.Runner.SkillStore.Skills())
	case skillCommandShow:
		if len(args) < 2 {
			return fmt.Errorf("usage: /skill show <id>")
		}
		return printSkill(opts.Out, opts.Runner.SkillStore, args[1])
	case skillCommandRun:
		if len(args) < 2 {
			return fmt.Errorf("usage: /skill run <id> [arguments...]")
		}
		return runSkill(opts, args[1], args[2:])
	case skillCommandUpdate:
		updateOpts, check, err := parseSkillUpdateArgs(args[1:])
		if err != nil {
			return err
		}
		if check {
			result, err := opts.Runner.SkillStore.CheckUpdate(opts.Context, updateOpts)
			if err != nil {
				return err
			}
			printSkillUpdateCheck(opts.Out, result)
			return nil
		}
		result, err := opts.Runner.SkillStore.UpdateWithOptions(opts.Context, updateOpts)
		if err != nil {
			return err
		}
		opts.Runner.SystemPrompt = app.BuildSystemPrompt(opts.Config, opts.Runner.SkillStore)
		printSkillUpdateResult(opts.Out, result)
		return nil
	case skillCommandRemove, "remove", "rm":
		if len(args) != 2 {
			return fmt.Errorf("usage: /skill uninstall <id>")
		}
		result, err := opts.Runner.SkillStore.Uninstall(args[1])
		if err != nil {
			return err
		}
		opts.Runner.SystemPrompt = app.BuildSystemPrompt(opts.Config, opts.Runner.SkillStore)
		fmt.Fprintf(opts.Out, "uninstalled skill %s\n", result.Skill.ID)
		fmt.Fprintf(opts.Out, "  path: %s\n", result.Path)
		return nil
	case skillCommandReload:
		if len(args) != 1 {
			return fmt.Errorf("usage: /skill reload")
		}
		if err := opts.Runner.SkillStore.Reload(); err != nil {
			return err
		}
		opts.Runner.SystemPrompt = app.BuildSystemPrompt(opts.Config, opts.Runner.SkillStore)
		fmt.Fprintf(opts.Out, "skills reloaded: %d\n", len(opts.Runner.SkillStore.Skills()))
		return nil
	default:
		return fmt.Errorf("unknown skill command %q, use /skill install, /skill doctor <id>, /skill list, /skill show <id>, /skill run <id>, /skill update <id>, /skill uninstall <id>, or /skill reload", args[0])
	}
}

func installSkillFromREPL(opts Options, args []string) error {
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	installOpts := skill.InstallOptions{ProjectRoot: projectRoot, Scope: skill.ScopeProject}
	assumeYes := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--scope":
			if len(args) < 1 {
				return fmt.Errorf("--scope requires project or user")
			}
			scope, err := skill.ParseScope(args[0])
			if err != nil {
				return err
			}
			installOpts.Scope = scope
			args = args[1:]
		case "--name":
			if len(args) < 1 {
				return fmt.Errorf("--name requires a skill name")
			}
			installOpts.Name = args[0]
			args = args[1:]
		case "--force":
			installOpts.Force = true
		case "--dry-run":
			installOpts.DryRun = true
		case "--yes", "-y":
			assumeYes = true
		case "--pin":
			if len(args) < 1 {
				return fmt.Errorf("--pin requires a git ref")
			}
			installOpts.Pin = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown skill install option %q", arg)
			}
			if installOpts.Source != "" {
				return fmt.Errorf("usage: /skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
			}
			installOpts.Source = arg
		}
	}
	if installOpts.Source == "" {
		return fmt.Errorf("usage: /skill install [--scope project|user] [--name name] [--force] [--yes] [--dry-run] [--pin git-ref] <path-or-git-url>")
	}
	previewOpts := installOpts
	previewOpts.DryRun = true
	preview, err := skill.Install(opts.Context, previewOpts)
	if err != nil {
		return err
	}
	printSkillInstallResult(opts.Out, preview)
	if installOpts.DryRun {
		return nil
	}
	confirmed, err := confirmSkillInstall(opts.In, opts.Out, assumeYes)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(opts.Out, "install cancelled")
		return nil
	}
	result, err := skill.Install(opts.Context, installOpts)
	if err != nil {
		return err
	}
	printSkillInstallResult(opts.Out, result)
	if err := opts.Runner.SkillStore.Reload(); err != nil {
		return err
	}
	opts.Runner.SystemPrompt = app.BuildSystemPrompt(opts.Config, opts.Runner.SkillStore)
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

func parseSkillUpdateArgs(args []string) (skill.UpdateOptions, bool, error) {
	updateOpts := skill.UpdateOptions{}
	check := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--check":
			check = true
		case "--pin":
			if len(args) < 1 {
				return updateOpts, false, fmt.Errorf("--pin requires a git ref")
			}
			updateOpts.Pin = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return updateOpts, false, fmt.Errorf("unknown skill update option %q", arg)
			}
			if updateOpts.ID == "" {
				updateOpts.ID = arg
			} else if updateOpts.Source == "" {
				updateOpts.Source = arg
			} else {
				return updateOpts, false, fmt.Errorf("usage: /skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
			}
		}
	}
	if updateOpts.ID == "" {
		return updateOpts, false, fmt.Errorf("usage: /skill update [--check] [--pin git-ref] <id> [path-or-git-url]")
	}
	return updateOpts, check, nil
}

func runInvocableSkillAlias(opts Options, cmd SlashCommand) (bool, error) {
	if opts.Runner.SkillStore == nil {
		return false, nil
	}
	item, err := opts.Runner.SkillStore.Find(cmd.Name)
	if err != nil {
		return false, nil
	}
	if !item.UserInvocable {
		return false, nil
	}
	return true, runSkill(opts, item.ID, cmd.Args)
}

func runSkill(opts Options, id string, args []string) error {
	item, err := opts.Runner.SkillStore.Find(id)
	if err != nil {
		return err
	}
	arguments := strings.Join(args, " ")
	task := fmt.Sprintf("使用 Skill `%s` 执行。请先调用 skill_read 读取该 Skill，按其流程行动，并把 related_skill 设为 `%s`。", item.ID, item.ID)
	if arguments == "" {
		task += " $ARGUMENTS 为空；如果流程需要用户决策，请调用 ask_user。"
	} else {
		task += " $ARGUMENTS: " + arguments
	}
	_, err = opts.Runner.Run(opts.Context, task, agent.NewConsoleSink(opts.Out))
	return err
}

func printSkillList(out io.Writer, skills []skill.Skill) error {
	fmt.Fprintln(out, "skills:")
	if len(skills) == 0 {
		fmt.Fprintln(out, "  status: no skills")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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

func printSkill(out io.Writer, store *skill.Store, id string) error {
	result, err := store.Read(id)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "skill:")
	fmt.Fprintf(out, "  id:          %s\n", result.Skill.ID)
	fmt.Fprintf(out, "  name:        %s\n", result.Skill.Name)
	fmt.Fprintf(out, "  scope:       %s\n", result.Skill.Scope)
	fmt.Fprintf(out, "  invocable:   %t\n", result.Skill.UserInvocable)
	fmt.Fprintf(out, "  requires:    %s\n", result.Skill.Requires.Summary())
	if result.Skill.ArgumentHint != "" {
		fmt.Fprintf(out, "  hint:        %s\n", result.Skill.ArgumentHint)
	}
	fmt.Fprintf(out, "  path:        %s\n", result.Skill.Path)
	fmt.Fprintf(out, "  truncated:   %t\n\n", result.Truncated)
	fmt.Fprintln(out, result.Content)
	return nil
}

func printSkillInstallResult(out io.Writer, result skill.InstallResult) {
	if result.DryRun {
		fmt.Fprintf(out, "preview skill %s\n", result.Skill.ID)
	} else {
		fmt.Fprintf(out, "installed skill %s\n", result.Skill.ID)
	}
	fmt.Fprintf(out, "  name:        %s\n", result.Skill.Name)
	fmt.Fprintf(out, "  requires:    %s\n", result.Skill.Requires.Summary())
	fmt.Fprintf(out, "  source:      %s\n", result.Source)
	fmt.Fprintf(out, "  source_type: %s\n", result.SourceType)
	printSkillRefFields(out, result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
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

func printSkillUpdateResult(out io.Writer, result skill.UpdateResult) {
	fmt.Fprintf(out, "updated skill %s\n", result.Skill.ID)
	fmt.Fprintf(out, "  requires:    %s\n", result.Skill.Requires.Summary())
	fmt.Fprintf(out, "  source:      %s\n", result.Source)
	fmt.Fprintf(out, "  source_type: %s\n", result.SourceType)
	printSkillRefFields(out, result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
	fmt.Fprintf(out, "  destination: %s\n", result.Destination)
	fmt.Fprintf(out, "  files:       %d\n", result.Files)
	fmt.Fprintf(out, "  hash:        %s\n", result.ContentHash)
	if result.Replaced {
		fmt.Fprintln(out, "  replaced:    true")
	}
}

func printSkillUpdateCheck(out io.Writer, result skill.UpdateCheckResult) {
	status := "update-available"
	if result.UpToDate {
		status = "up-to-date"
	}
	fmt.Fprintf(out, "skill update check %s\n", result.Skill.ID)
	fmt.Fprintf(out, "  status:         %s\n", status)
	fmt.Fprintf(out, "  requires:       %s\n", result.Requires.Summary())
	fmt.Fprintf(out, "  source:         %s\n", result.Source)
	fmt.Fprintf(out, "  source_type:    %s\n", result.SourceType)
	printSkillRefFields(out, result.SourceRef, result.RequestedRef, result.ResolvedRef, result.Pinned)
	fmt.Fprintf(out, "  destination:    %s\n", result.Destination)
	fmt.Fprintf(out, "  files:          %d\n", result.Files)
	fmt.Fprintf(out, "  current_hash:   %s\n", result.CurrentHash)
	if result.ManifestHash != "" {
		fmt.Fprintf(out, "  manifest_hash:  %s\n", result.ManifestHash)
	}
	fmt.Fprintf(out, "  candidate_hash: %s\n", result.CandidateHash)
}

func printSkillRefFields(out io.Writer, sourceRef, requestedRef, resolvedRef string, pinned bool) {
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

func printSkillDoctor(out io.Writer, result skill.DoctorResult) {
	fmt.Fprintf(out, "skill doctor %s\n", result.Skill.ID)
	fmt.Fprintf(out, "  path:     %s\n", result.Path)
	fmt.Fprintf(out, "  healthy:  %t\n", result.Healthy)
	fmt.Fprintf(out, "  warnings: %d\n", result.WarningCount())
	fmt.Fprintf(out, "  errors:   %d\n", result.ErrorCount())
	if result.Manifest != nil {
		fmt.Fprintln(out, "manifest:")
		fmt.Fprintf(out, "  source:      %s\n", result.Manifest.Source)
		fmt.Fprintf(out, "  source_type: %s\n", result.Manifest.SourceType)
		printSkillRefFields(out, result.Manifest.SourceRef, result.Manifest.RequestedRef, result.Manifest.ResolvedRef, result.Manifest.Pinned)
		fmt.Fprintf(out, "  scope:       %s\n", result.Manifest.Scope)
		fmt.Fprintf(out, "  alias:       %s\n", result.Manifest.Alias)
		fmt.Fprintf(out, "  installed:   %s\n", result.Manifest.InstalledAt)
		fmt.Fprintf(out, "  hash:        %s\n", result.Manifest.ContentHash)
	}
	fmt.Fprintln(out, "checks:")
	for _, check := range result.Checks {
		fmt.Fprintf(out, "  [%s] %s: %s\n", check.Severity, check.Code, check.Message)
		if check.Detail != "" {
			fmt.Fprintf(out, "        %s\n", check.Detail)
		}
	}
}

func printSOPCandidates(out io.Writer, manager evolution.Manager) error {
	candidates, err := manager.ListSOPCandidates()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "sop candidates:")
	if len(candidates) == 0 {
		fmt.Fprintln(out, "  status: no candidates")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tTITLE\tSOP_PATH\tSCENE")
	for _, candidate := range candidates {
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\n",
			candidate.ID,
			candidate.Title,
			candidate.ProposedSOPPath,
			candidate.Scene,
		)
	}
	return writer.Flush()
}

func promoteSOPCandidate(opts Options, id string, args []string) error {
	manager := evolution.NewManager(opts.Config.Workspace)
	if hasFlag(args, "--confirm-index") || hasFlag(args, "--yes") {
		confirmation := "explicit slash command /sop promote " + id + " --confirm-index"
		result, err := manager.PromoteSOPCandidate(id, true, confirmation)
		if err != nil {
			return err
		}
		printSOPPromotionResult(opts.Out, result)
		return nil
	}

	result, err := manager.PromoteSOPCandidate(id, false, "")
	if err != nil {
		return err
	}
	printSOPPromotionResult(opts.Out, result)
	if confirmed, confirmation := promptSOPIndexConfirmation(opts, result.Candidate.ID); confirmed {
		result, err = manager.PromoteSOPCandidate(result.Candidate.ID, true, confirmation)
		if err != nil {
			return err
		}
		printSOPPromotionResult(opts.Out, result)
		return nil
	}
	fmt.Fprintln(opts.Out, "  index: pending human confirmation; rerun with --confirm-index to update sops/index.md")
	return nil
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, flag) {
			return true
		}
	}
	return false
}

func promptSOPIndexConfirmation(opts Options, id string) (bool, string) {
	inFile, inOK := opts.In.(*os.File)
	outFile, outOK := opts.Out.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return false, ""
	}
	prompt := promptui.Prompt{
		Label: "Type SOP candidate ID to update sops/index.md, or press Enter to skip",
		Validate: func(input string) error {
			input = strings.TrimSpace(input)
			if input == "" || input == id {
				return nil
			}
			return fmt.Errorf("input must be empty or %s", id)
		},
		AllowEdit: true,
		Stdin:     inFile,
		Stdout:    outFile,
	}
	value, err := prompt.Run()
	if err != nil || strings.TrimSpace(value) != id {
		return false, ""
	}
	return true, "typed SOP candidate id " + id + " in interactive prompt"
}

func printSOPPromotionResult(out io.Writer, result evolution.SOPPromotionResult) {
	fmt.Fprintln(out, "sop promote:")
	fmt.Fprintf(out, "  candidate: %s\n", result.Candidate.ID)
	fmt.Fprintf(out, "  sop:       %s\n", result.SOPPath)
	fmt.Fprintf(out, "  path:      %s\n", result.SOPAbsolutePath)
	if result.SOPCreated {
		fmt.Fprintln(out, "  file:      created")
	} else {
		fmt.Fprintln(out, "  file:      already exists")
	}
	if result.IndexUpdated {
		fmt.Fprintf(out, "  index:     updated %s\n", result.IndexPath)
	} else if result.RequiresIndexConfirmation {
		fmt.Fprintln(out, "  index:     not updated")
	} else {
		fmt.Fprintln(out, "  index:     already contained SOP path")
	}
	if result.Confirmation != "" {
		fmt.Fprintf(out, "  confirm:   %s\n", result.Confirmation)
	}
}

func shorten(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
