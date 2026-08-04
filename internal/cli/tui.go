package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"cohort/internal/plan"
)

func runTUICommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	if args[0] != "status" {
		return fmt.Errorf("unknown tui command %q, use status", args[0])
	}
	if len(args) != 1 {
		return errors.New("usage: cohort tui status")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Cohort Status")
	fmt.Fprintln(out, "=============")
	fmt.Fprintf(out, "root: %s\n", root)
	fmt.Fprintln(out, "")
	printTUIPlan(root, out)
	fmt.Fprintln(out, "")
	printTUIGit(root, out)
	fmt.Fprintln(out, "")
	printTUILogs(root, out)
	return nil
}

func printTUIPlan(root string, out io.Writer) {
	store := plan.NewStore(root)
	state, err := store.Load()
	fmt.Fprintln(out, "Plan")
	if err != nil {
		fmt.Fprintln(out, "  status: no active plan")
		return
	}
	fmt.Fprintf(out, "  title: %s\n", state.Title)
	fmt.Fprintf(out, "  status: %s\n", state.Status)
	for _, step := range state.Steps {
		fmt.Fprintf(out, "  [%s] %d. %s\n", step.Status, step.ID, step.Text)
	}
}

func printTUIGit(root string, out io.Writer) {
	fmt.Fprintln(out, "Diff")
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		fmt.Fprintln(out, "  git: unavailable")
		return
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		fmt.Fprintln(out, "  clean")
		return
	}
	fmt.Fprintf(out, "  changed_files: %d\n", len(lines))
	limit := minInt(len(lines), 8)
	for _, line := range lines[:limit] {
		fmt.Fprintf(out, "  %s\n", line)
	}
	if len(lines) > limit {
		fmt.Fprintf(out, "  ... %d more\n", len(lines)-limit)
	}
}

func printTUILogs(root string, out io.Writer) {
	fmt.Fprintln(out, "Logs")
	paths := recentLogFiles(root)
	if len(paths) == 0 {
		fmt.Fprintln(out, "  recent: none")
		return
	}
	for _, path := range paths {
		fmt.Fprintf(out, "  %s\n", path)
	}
}

func recentLogFiles(root string) []string {
	var files []string
	for _, dir := range []string{
		filepath.Join(root, ".cohort"),
		filepath.Join(root, ".cohort", "logs"),
		filepath.Join(root, "logs"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".jsonl") {
				files = append(files, filepath.Join(dir, name))
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		left, _ := os.Stat(files[i])
		right, _ := os.Stat(files[j])
		if left == nil || right == nil {
			return files[i] > files[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	if len(files) > 5 {
		files = files[:5]
	}
	return files
}

func nonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
