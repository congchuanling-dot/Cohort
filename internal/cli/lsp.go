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
		return errors.New("usage: cohort lsp doctor|diagnostics|definition|references ...")
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
	case "definition":
		language, position, err := parseLSPPositionQueryArgs(args[1:], lsp.LanguageGo)
		if err != nil {
			return err
		}
		result, err := client.Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryDefinition, Position: position})
		printLSPQueryResult(result, out)
		return err
	case "references":
		language, position, includeDeclaration, err := parseLSPReferencesArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := client.Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryReferences, Position: position, IncludeDeclaration: includeDeclaration})
		printLSPQueryResult(result, out)
		return err
	case "hover":
		language, position, err := parseLSPPositionQueryArgs(args[1:], lsp.LanguageGo)
		if err != nil {
			return err
		}
		result, err := client.Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryHover, Position: position})
		printLSPQueryResult(result, out)
		return err
	case "symbols":
		language, target, err := parseLSPSymbolsArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := client.Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QuerySymbols, Target: target})
		printLSPQueryResult(result, out)
		return err
	default:
		return fmt.Errorf("unknown lsp command %q, use doctor, diagnostics, definition, references, hover, or symbols", args[0])
	}
}

func parseLSPPositionQueryArgs(args []string, fallbackLanguage string) (string, string, error) {
	position := ""
	language := fallbackLanguage
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--language" || arg == "--lang":
			if len(args) == 0 {
				return "", "", errors.New("--language requires go, typescript, or python")
			}
			language = lsp.NormalizeLanguage(args[0])
			args = args[1:]
		case strings.HasPrefix(arg, "--language="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--language="))
		case strings.HasPrefix(arg, "--lang="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--lang="))
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", fmt.Errorf("unknown lsp query option %q", arg)
			}
			if position != "" {
				return "", "", errors.New("usage: cohort lsp <definition|hover> [--language go|typescript|python] <file:line:column>")
			}
			position = arg
		}
	}
	if position == "" {
		return "", "", errors.New("usage: cohort lsp <definition|hover> [--language go|typescript|python] <file:line:column>")
	}
	if language == lsp.LanguageAll || len(lsp.SupportedLanguages(language)) == 0 {
		return "", "", fmt.Errorf("unsupported lsp query language %q", language)
	}
	return language, position, nil
}

func parseLSPReferencesArgs(args []string) (string, string, bool, error) {
	position := ""
	language := lsp.LanguageGo
	includeDeclaration := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch arg {
		case "--declaration", "-d":
			includeDeclaration = true
		case "--language", "--lang":
			if len(args) == 0 {
				return "", "", false, errors.New("--language requires go, typescript, or python")
			}
			language = lsp.NormalizeLanguage(args[0])
			args = args[1:]
		default:
			switch {
			case strings.HasPrefix(arg, "--language="):
				language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--language="))
				continue
			case strings.HasPrefix(arg, "--lang="):
				language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--lang="))
				continue
			case strings.HasPrefix(arg, "-"):
				return "", "", false, fmt.Errorf("unknown lsp references option %q", arg)
			}
			if position != "" {
				return "", "", false, errors.New("usage: cohort lsp references [--language go|typescript|python] [--declaration] <file:line:column>")
			}
			position = arg
		}
	}
	if position == "" {
		return "", "", false, errors.New("usage: cohort lsp references [--language go|typescript|python] [--declaration] <file:line:column>")
	}
	if language == lsp.LanguageAll || len(lsp.SupportedLanguages(language)) == 0 {
		return "", "", false, fmt.Errorf("unsupported lsp query language %q", language)
	}
	return language, position, includeDeclaration, nil
}

func parseLSPSymbolsArgs(args []string) (string, string, error) {
	language := lsp.LanguageGo
	target := "."
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--language" || arg == "--lang":
			if len(args) == 0 {
				return "", "", errors.New("--language requires go, typescript, or python")
			}
			language = lsp.NormalizeLanguage(args[0])
			args = args[1:]
		case strings.HasPrefix(arg, "--language="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--language="))
		case strings.HasPrefix(arg, "--lang="):
			language = lsp.NormalizeLanguage(strings.TrimPrefix(arg, "--lang="))
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", fmt.Errorf("unknown lsp symbols option %q", arg)
			}
			target = arg
		}
	}
	if language == lsp.LanguageAll || len(lsp.SupportedLanguages(language)) == 0 {
		return "", "", fmt.Errorf("unsupported lsp query language %q", language)
	}
	return language, target, nil
}

func printLSPQueryResult(result lsp.QueryResult, out io.Writer) {
	fmt.Fprintf(out, "language: %s\n", result.Language)
	fmt.Fprintf(out, "kind: %s\n", result.Kind)
	if result.Engine != "" {
		fmt.Fprintf(out, "engine: %s\n", result.Engine)
	}
	fmt.Fprintf(out, "position: %s\n", result.Position)
	fmt.Fprintf(out, "command: %s\n", strings.Join(result.Command, " "))
	fmt.Fprintf(out, "exit_code: %d\n", result.ExitCode)
	if result.Output != "" {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, result.Output)
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
