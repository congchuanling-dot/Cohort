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
		return errors.New("usage: cohort lsp doctor|diagnostics [--language go|typescript|python|all] [path...]")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	client := lsp.Diagnostics{Root: root}
	switch args[0] {
	case "doctor":
		language, targets, install, err := parseLSPDoctorArgs(args[1:])
		if err != nil {
			return err
		}
		if len(targets) > 0 {
			return errors.New("usage: cohort lsp doctor [--language go|typescript|python|all] [--install]")
		}
		if install {
			if err := installLSPDoctorMissing(ctx, client, language, out); err != nil {
				return err
			}
		}
		return printLSPDoctor(ctx, client, language, out)
	case "diagnostics", "check":
		language, targets, parseErr := parseLSPLanguageArgs(args[1:], lsp.LanguageGo)
		if parseErr != nil {
			return parseErr
		}
		if language == lsp.LanguageAll {
			return errors.New("usage: cohort lsp diagnostics [--language go|typescript|python] [path...]")
		}
		result, err := client.Check(ctx, language, targets)
		fmt.Fprintf(out, "language: %s\n", result.Language)
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

func parseLSPDoctorArgs(args []string) (string, []string, bool, error) {
	filtered := make([]string, 0, len(args))
	install := false
	for _, arg := range args {
		if arg == "--install" || arg == "--fix" {
			install = true
			continue
		}
		filtered = append(filtered, arg)
	}
	language, targets, err := parseLSPLanguageArgs(filtered, lsp.LanguageGo)
	return language, targets, install, err
}

func parseLSPLanguageArgs(args []string, fallback string) (string, []string, error) {
	language := fallback
	targets := make([]string, 0, len(args))
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--language" || arg == "--lang":
			if len(args) == 0 {
				return "", nil, errors.New("--language requires go, typescript, python, or all")
			}
			language = lsp.NormalizeLanguage(args[0])
			args = args[1:]
		case strings.HasPrefix(arg, "--language="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--language="))
		case strings.HasPrefix(arg, "--lang="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--lang="))
		default:
			if strings.HasPrefix(arg, "-") {
				return "", nil, fmt.Errorf("unknown lsp option %q", arg)
			}
			targets = append(targets, arg)
		}
	}
	if len(lsp.SupportedLanguages(language)) == 0 {
		return "", nil, fmt.Errorf("unsupported lsp language %q", language)
	}
	return language, targets, nil
}

func printLSPDoctor(ctx context.Context, client lsp.Diagnostics, language string, out io.Writer) error {
	results := client.Doctor(ctx, language)
	failures := 0
	for _, result := range results {
		if result.OK {
			fmt.Fprintf(out, "%s: ok\n", result.Language)
			fmt.Fprintf(out, "  command: %s\n", result.Command)
			fmt.Fprintf(out, "  path: %s\n", result.Path)
			fmt.Fprintf(out, "  version: %s\n", firstLine(result.Version))
			continue
		}
		failures++
		fmt.Fprintf(out, "%s: fail\n", result.Language)
		fmt.Fprintf(out, "  command: %s\n", result.Command)
		if result.Error != "" {
			fmt.Fprintf(out, "  error: %s\n", result.Error)
		}
	}
	if failures > 0 {
		return fmt.Errorf("lsp doctor found %d failure(s)", failures)
	}
	return nil
}

func installLSPDoctorMissing(ctx context.Context, client lsp.Diagnostics, language string, out io.Writer) error {
	results := client.InstallMissing(ctx, language)
	if len(results) == 0 {
		fmt.Fprintln(out, "install: nothing to do")
		return nil
	}
	failures := 0
	fmt.Fprintln(out, "install:")
	for _, result := range results {
		if result.Skipped {
			failures++
			fmt.Fprintf(out, "  [%s] %s skipped: %s\n", result.Language, result.Package, result.Error)
			continue
		}
		fmt.Fprintf(out, "  [%s] %s\n", result.Language, strings.Join(result.Command, " "))
		if result.Output != "" {
			fmt.Fprintf(out, "    output: %s\n", firstLine(result.Output))
		}
		if result.OK {
			fmt.Fprintln(out, "    status: ok")
			continue
		}
		failures++
		fmt.Fprintf(out, "    status: fail\n")
		if result.Error != "" {
			fmt.Fprintf(out, "    error: %s\n", result.Error)
		}
	}
	fmt.Fprintln(out, "")
	if failures > 0 {
		return fmt.Errorf("lsp install found %d failure(s)", failures)
	}
	return nil
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return line
}
