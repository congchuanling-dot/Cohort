package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/session"
)

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
		if cfg.LLM.APIKey == "" {
			fmt.Println("api_key: missing")
		} else {
			fmt.Println("api_key: set")
		}
		return nil
	case "session":
		return runSessionCommand(context.Background(), cfg, args[1:])
	case "tools":
		for _, schema := range app.ToolSchemas(cfg) {
			fmt.Println(schema.Function.Name)
		}
		return nil
	}

	// 真正执行任务前才创建 Runner，此时会检查 API Key、工作区、日志目录等。
	runner, err := app.NewRunner(cfg)
	if err != nil {
		return err
	}

	switch args[0] {
	case "run":
		return runREPL(context.Background(), runner)
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
	runner.ResumeSession(sess.ID, history)
	fmt.Printf("resumed session %s (%d messages): %s\n", sess.ID, len(history), sess.Title)
	return runREPL(ctx, runner)
}

// runREPL 是交互模式。每输入一行任务，就复用同一个 Runner 继续执行。
// 因为 Runner 内部保留 history，所以 REPL 模式天然带上下文；/clear 会清空它。
func runREPL(ctx context.Context, runner *agent.Runner) error {
	fmt.Println("Cohert Go MVP")
	fmt.Println("输入任务开始执行；输入 /exit 退出，/tools 查看工具。")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		// 以 / 开头的内置命令只在本地处理，不会发送给模型。
		switch input {
		case "/exit", "exit", "quit":
			return nil
		case "/tools":
			for _, schema := range runner.ToolSchemas() {
				fmt.Println("-", schema.Function.Name)
			}
			continue
		case "/clear":
			runner.Reset()
			fmt.Println("session cleared")
			continue
		}
		// 普通输入会作为一次用户任务交给 Agent Runner。
		if _, err := runner.Run(ctx, input, agent.NewConsoleSink(os.Stdout)); err != nil {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
	}
}

// printHelp 输出当前支持的最小命令集合。
func printHelp() {
	fmt.Print(`Cohert Go MVP

Usage:
  cohert                 start interactive CLI
  cohert ask "task"      run one task
  cohert tools           list mounted tools
  cohert config          show effective config
  cohert session list    list local sessions
  cohert session resume <id>
                         resume a local session

Development:
  go run .               start interactive CLI
  go run . ask "task"    run one task

Environment:
  DEEPSEEK_API_KEY       required unless configs/config.yaml contains api_key
`)
}
