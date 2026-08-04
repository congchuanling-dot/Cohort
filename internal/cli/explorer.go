package cli

import (
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
		return errors.New("usage: cohort explorer create|list|show ...")
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
	default:
		return fmt.Errorf("unknown explorer command %q, use create, list, or show", args[0])
	}
}
