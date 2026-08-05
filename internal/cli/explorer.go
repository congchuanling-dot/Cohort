package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"cohort/internal/explorer"
)

const explorerChildEnv = "COHORT_EXPLORER_CHILD"

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
		return runExplorerIsolated(root, id, opts, out)
	case "run-batch":
		ids, opts, err := parseExplorerRunBatchArgs(args[1:])
		if err != nil {
			return err
		}
		return runExplorerBatchIsolated(root, ids, opts, out)
	case "run-child":
		if os.Getenv(explorerChildEnv) != "1" {
			return errors.New("explorer run-child is internal; use cohort explorer run <id>")
		}
		id, opts, err := parseExplorerRunArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := store.Run(context.Background(), id, opts)
		if err != nil {
			return err
		}
		return printExplorerRunResult(result, out)
	case "run-batch-child":
		if os.Getenv(explorerChildEnv) != "1" {
			return errors.New("explorer run-batch-child is internal; use cohort explorer run-batch <id...>")
		}
		ids, opts, err := parseExplorerRunBatchArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := store.RunBatch(context.Background(), ids, opts)
		if printErr := printExplorerBatchRunResult(result, out); printErr != nil {
			return printErr
		}
		return err
	default:
		return fmt.Errorf("unknown explorer command %q, use create, list, show, run, or run-batch", args[0])
	}
}

func runExplorerIsolated(root string, id string, opts explorer.RunOptions, out io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := explorerChildArgs(id, opts)
	cmd := exec.Command(executable, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), explorerChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		if _, writeErr := out.Write(output); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return fmt.Errorf("isolated explorer process failed: %w", err)
	}
	return nil
}

func explorerChildArgs(id string, opts explorer.RunOptions) []string {
	args := []string{"explorer", "run-child", id}
	if opts.WithTests {
		args = append(args, "--with-tests")
	}
	if strings.TrimSpace(opts.Search) != "" {
		args = append(args, "--search", opts.Search)
	}
	return args
}

func runExplorerBatchIsolated(root string, ids []string, opts explorer.RunOptions, out io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := explorerBatchChildArgs(ids, opts)
	cmd := exec.Command(executable, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), explorerChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		if _, writeErr := out.Write(output); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return fmt.Errorf("isolated explorer batch process failed: %w", err)
	}
	return nil
}

func explorerBatchChildArgs(ids []string, opts explorer.RunOptions) []string {
	args := []string{"explorer", "run-batch-child"}
	args = append(args, ids...)
	if opts.WithTests {
		args = append(args, "--with-tests")
	}
	if strings.TrimSpace(opts.Search) != "" {
		args = append(args, "--search", opts.Search)
	}
	return args
}

func printExplorerRunResult(result explorer.RunResult, out io.Writer) error {
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
}

func printExplorerBatchRunResult(result explorer.BatchRunResult, out io.Writer) error {
	fmt.Fprintf(out, "lanes: %d\n", len(result.Results))
	fmt.Fprintf(out, "failed: %d\n", result.Failed)
	fmt.Fprintf(out, "report: %s\n", result.ReportPath)
	for _, lane := range result.Results {
		status := lane.Task.Status
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(out, "  - [%s] %s checks=%d result=%s\n", status, lane.Task.ID, len(lane.Checks), lane.Task.ResultPath)
	}
	if result.Failed > 0 {
		return fmt.Errorf("explorer batch failed: %d lane(s) failed", result.Failed)
	}
	return nil
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

func parseExplorerRunBatchArgs(args []string) ([]string, explorer.RunOptions, error) {
	if len(args) == 0 {
		return nil, explorer.RunOptions{}, errors.New("usage: cohort explorer run-batch <id...> [--with-tests] [--search <pattern>]")
	}
	ids := []string{}
	opts := explorer.RunOptions{}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--with-tests":
			opts.WithTests = true
		case "--search":
			if len(args) == 0 {
				return nil, opts, errors.New("--search requires a pattern")
			}
			opts.Search = args[0]
			args = args[1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, opts, fmt.Errorf("unknown explorer run-batch option %q", arg)
			}
			ids = append(ids, arg)
		}
	}
	if len(ids) == 0 {
		return nil, opts, errors.New("usage: cohort explorer run-batch <id...> [--with-tests] [--search <pattern>]")
	}
	return ids, opts, nil
}
