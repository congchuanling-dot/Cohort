package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"cohort/internal/explorer"
)

func runExplorerCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort explorer create|list|show|run ...")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	store := explorer.NewStore(root)
	switch args[0] {
	case "create":
		if len(args) < 2 {
			return errors.New(`usage: cohort explorer create "question"`)
		}
		task, err := store.Create(strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "explorer: %s\n", task.ID)
		fmt.Fprintf(out, "status: %s\n", task.Status)
		fmt.Fprintf(out, "read_only: %t\n", task.ReadOnly)
		fmt.Fprintf(out, "task: %s\n", task.TaskPath)
		fmt.Fprintf(out, "result: %s\n", task.ResultPath)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohort explorer list")
		}
		tasks, err := store.List()
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			fmt.Fprintln(out, "no explorer tasks")
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tREAD_ONLY\tQUESTION")
		for _, task := range tasks {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", task.ID, task.Status, task.ReadOnly, task.Question)
		}
		return tw.Flush()
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort explorer show <id>")
		}
		task, err := store.Find(args[1])
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	case "run":
		id, opts, err := parseExplorerRunArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := store.Run(context.Background(), id, opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "explorer: %s\n", result.Task.ID)
		fmt.Fprintf(out, "status: %s\n", result.Task.Status)
		fmt.Fprintf(out, "checks: %d\n", len(result.Checks))
		fmt.Fprintf(out, "result: %s\n", result.Task.ResultPath)
		for _, check := range result.Checks {
			status := "ok"
			if !check.OK {
				status = "fail"
			}
			fmt.Fprintf(out, "  - [%s] %s (%s)\n", status, check.Name, strings.Join(check.Command, " "))
		}
		if result.Task.Status == "failed" {
			return fmt.Errorf("explorer run failed: %s", result.Task.LastError)
		}
		return nil
	default:
		return fmt.Errorf("unknown explorer command %q, use create, list, show, or run", args[0])
	}
}

func parseExplorerRunArgs(args []string) (string, explorer.RunOptions, error) {
	if len(args) == 0 {
		return "", explorer.RunOptions{}, errors.New("usage: cohort explorer run <id> [--with-tests] [--search <pattern>]")
	}
	id := ""
	opts := explorer.RunOptions{}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--with-tests":
			opts.WithTests = true
		case "--search":
			if len(args) == 0 {
				return "", opts, errors.New("--search requires a pattern")
			}
			opts.Search = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return "", opts, fmt.Errorf("unknown explorer run option %q", arg)
			}
			if id != "" {
				return "", opts, errors.New("usage: cohort explorer run <id> [--with-tests] [--search <pattern>]")
			}
			id = arg
		}
	}
	if id == "" {
		return "", opts, errors.New("usage: cohort explorer run <id> [--with-tests] [--search <pattern>]")
	}
	return id, opts, nil
}
