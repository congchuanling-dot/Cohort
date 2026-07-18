package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/session"
)

const (
	commandExit    = "exit"
	commandQuit    = "quit"
	commandHelp    = "help"
	commandTools   = "tools"
	commandModel   = "model"
	commandConfig  = "config"
	commandSession = "session"
	commandResume  = "resume"
	commandCompact = "compact"
	commandClear   = "clear"

	sessionCommandList   = "list"
	sessionCommandResume = "resume"
)

// Options 是启动交互模式需要的依赖。
// CLI 负责加载配置和创建 Runner，REPL 只负责读取用户输入、展示界面和分发 slash 命令。
type Options struct {
	Config       app.Config
	Runner       *agent.Runner
	SessionStore session.Store
	In           io.Reader
	Out          io.Writer
	Err          io.Writer
}

// Start 启动 Cohert 交互模式。
//
// 普通输入会交给 Runner.Run；以 "/" 开头的输入会作为本地 slash 命令处理，
// 不会发送给模型。这样 `/model`、`/session list`、`/compact` 这类运行时控制命令
// 可以在对话框里完成，用户不需要退出后再跑额外 CLI 命令。
func Start(ctx context.Context, opts Options) error {
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

	scanner := bufio.NewScanner(opts.In)
	for {
		fmt.Fprint(opts.Out, "\ncohert> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
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

func isSlashInput(input string) bool {
	return strings.HasPrefix(input, "/") || input == commandExit || input == commandQuit
}

// SlashCommand 是对话内命令的解析结果。
// Raw 保留原始输入，Name 和 Args 用于后续分发。
type SlashCommand struct {
	Raw  string
	Name string
	Args []string
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

func handleSlashCommand(opts Options, cmd SlashCommand) (bool, error) {
	switch cmd.Name {
	case commandExit, commandQuit:
		fmt.Fprintln(opts.Out, "bye")
		return true, nil
	case commandHelp:
		printSlashHelp(opts.Out)
	case commandTools:
		printTools(opts.Out, opts.Runner)
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
		printCompactPlaceholder(opts.Out)
	case commandClear:
		opts.Runner.Reset()
		fmt.Fprintln(opts.Out, "current in-memory session cleared; next task will create a new session")
	default:
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
	default:
		return fmt.Errorf("unknown session command %q, use /session list or /resume <id>", args[0])
	}
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
	width := 72
	line := strings.Repeat("-", width-2)
	fmt.Fprintf(out, "+%s+\n", line)
	fmt.Fprintf(out, "| %-68s |\n", "Cohert")
	fmt.Fprintf(out, "| %-68s |\n", "Command-line Agent Runtime")
	fmt.Fprintf(out, "+%s+\n", line)
	fmt.Fprintf(out, "| %-12s %-55s |\n", "Model", cfg.LLM.Model)
	fmt.Fprintf(out, "| %-12s %-55s |\n", "Workspace", shorten(cfg.Workspace, 55))
	fmt.Fprintf(out, "| %-12s %-55s |\n", "Session", shorten(sessionID, 55))
	fmt.Fprintf(out, "| %-12s %-55d |\n", "Tools", tools)
	fmt.Fprintf(out, "+%s+\n", line)
	fmt.Fprintln(out, "输入任务直接执行；输入 /help 查看命令。")
	fmt.Fprintln(out, "常用命令：/model /tools /session list /resume <id> /clear /exit")
}

func printSlashHelp(out io.Writer) {
	fmt.Fprint(out, `Cohert slash commands

  /help                    显示当前对话内命令
  /model                   查看当前模型配置摘要
  /config                  查看当前运行配置摘要
  /tools                   查看当前可用工具
  /session                 查看当前 session 状态
  /session list            列出本地历史 session
  /session resume <id>     恢复指定 session
  /resume <id>             恢复指定 session 的简写
  /compact                 预留：后续接入上下文压缩
  /clear                   清空当前内存上下文，下一次输入会创建新 session
  /exit                    退出 Cohert

普通输入不会走 slash 命令，会直接交给 Agent 执行。
`)
}

func printTools(out io.Writer, runner *agent.Runner) {
	fmt.Fprintln(out, "tools:")
	for _, schema := range runner.ToolSchemas() {
		fmt.Fprintf(out, "  - %s\n", schema.Function.Name)
	}
}

func printModel(out io.Writer, cfg app.Config) {
	fmt.Fprintln(out, "model:")
	fmt.Fprintf(out, "  provider: %s\n", cfg.LLM.Provider)
	fmt.Fprintf(out, "  name:     %s\n", cfg.LLM.Name)
	fmt.Fprintf(out, "  model:    %s\n", cfg.LLM.Model)
	fmt.Fprintf(out, "  api_base: %s\n", cfg.LLM.APIBase)
	fmt.Fprintf(out, "  stream:   %t\n", cfg.LLM.Stream)
}

func printConfig(out io.Writer, cfg app.Config) {
	fmt.Fprintln(out, "config:")
	fmt.Fprintf(out, "  language:  %s\n", cfg.Language)
	fmt.Fprintf(out, "  workspace: %s\n", cfg.Workspace)
	fmt.Fprintf(out, "  log_dir:   %s\n", cfg.LogDir)
	fmt.Fprintf(out, "  max_turns: %d\n", cfg.MaxTurns)
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

func printCompactPlaceholder(out io.Writer) {
	fmt.Fprintln(out, "compact:")
	fmt.Fprintln(out, "  status: not implemented")
	fmt.Fprintln(out, "  note: Context Manager 方案已写好，后续会把 /compact 接到上下文压缩层。")
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
