package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/app"
)

func Run(args []string) error {
	if len(args) == 0 {
		args = []string{"run"}
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printHelp()
		return nil
	}

	cfg, err := app.LoadConfig("configs/config.yaml")
	if err != nil {
		return err
	}

	switch args[0] {
	case "config":
		fmt.Printf("model: %s\napi_base: %s\nworkspace: %s\n", cfg.LLM.Model, cfg.LLM.APIBase, cfg.Workspace)
		if cfg.LLM.APIKey == "" {
			fmt.Println("api_key: missing")
		} else {
			fmt.Println("api_key: set")
		}
		return nil
	case "tools":
		for _, schema := range app.ToolSchemas(cfg) {
			fmt.Println(schema.Function.Name)
		}
		return nil
	}

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
		if _, err := runner.Run(ctx, input, agent.NewConsoleSink(os.Stdout)); err != nil {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
	}
}

func printHelp() {
	fmt.Print(`Cohert Go MVP

Usage:
  cohert                 start interactive CLI
  cohert ask "task"      run one task
  cohert tools           list mounted tools
  cohert config          show effective config

Development:
  go run .               start interactive CLI
  go run . ask "task"    run one task

Environment:
  DEEPSEEK_API_KEY       required unless configs/config.yaml contains api_key
`)
}
