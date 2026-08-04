package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"cohort/internal/lsp"
)

func runLSPCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort lsp doctor|diagnostics [path...]")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	client := lsp.Gopls{Root: root}
	switch args[0] {
	case "doctor":
		if len(args) != 1 {
			return errors.New("usage: cohort lsp doctor")
		}
		result, err := client.Doctor(ctx)
		if err != nil {
			fmt.Fprintf(out, "gopls: fail\n")
			fmt.Fprintf(out, "  command: %s\n", result.Command)
			return err
		}
		fmt.Fprintln(out, "gopls: ok")
		fmt.Fprintf(out, "  path: %s\n", result.Path)
		fmt.Fprintf(out, "  version: %s\n", firstLine(result.Version))
		return nil
	case "diagnostics", "check":
		result, err := client.Check(ctx, args[1:])
		fmt.Fprintf(out, "command: %s\n", strings.Join(result.Command, " "))
		fmt.Fprintf(out, "exit_code: %d\n", result.ExitCode)
		if result.Output != "" {
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, result.Output)
		}
		if err != nil {
			return err
		}
		if result.Output == "" {
			fmt.Fprintln(out, "diagnostics: clean")
		}
		return nil
	default:
		return fmt.Errorf("unknown lsp command %q, use doctor or diagnostics", args[0])
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return line
}
