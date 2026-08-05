package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"cohort/internal/app"
	"cohort/internal/session"
	"cohort/internal/tuning"
)

func runTuningCommand(cfg app.Config, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "report" {
		return errors.New("usage: cohort tuning report [--limit N] [--out path]")
	}
	opts, err := parseTuningReportOptions(args[1:])
	if err != nil {
		return err
	}
	if opts.OutputPath != "" && !filepath.IsAbs(opts.OutputPath) {
		opts.OutputPath = filepath.Join(cfg.Workspace, opts.OutputPath)
	}
	report, err := tuning.Generate(cfg.Workspace, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "tuning report: %s\n", report.OutputPath)
	fmt.Fprintf(out, "runs_scanned: %d\n", report.RunsScanned)
	fmt.Fprintf(out, "sessions_scanned: %d\n", report.SessionsScanned)
	fmt.Fprintf(out, "llm_time: %s\n", formatDurationMS(report.LLMDurationMS))
	fmt.Fprintf(out, "tool_time: %s\n", formatDurationMS(report.ToolDurationMS))
	fmt.Fprintf(out, "tool_failures: %d\n", report.ToolFailures)
	fmt.Fprintf(out, "schema_bloat_runs: %d\n", report.SchemaBloatRuns)
	fmt.Fprintf(out, "request_bloat_runs: %d\n", report.RequestBloatRuns)
	return nil
}

func parseTuningReportOptions(args []string) (tuning.Options, error) {
	opts := tuning.Options{
		SessionRoot: session.DefaultRootDir,
		Limit:       50,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--limit":
			if i+1 >= len(args) {
				return opts, errors.New("--limit requires a number")
			}
			limit, err := strconv.Atoi(args[i+1])
			if err != nil || limit <= 0 {
				return opts, fmt.Errorf("invalid --limit %q", args[i+1])
			}
			opts.Limit = limit
			i++
		case strings.HasPrefix(arg, "--limit="):
			value := strings.TrimPrefix(arg, "--limit=")
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				return opts, fmt.Errorf("invalid --limit %q", value)
			}
			opts.Limit = limit
		case arg == "--out":
			if i+1 >= len(args) {
				return opts, errors.New("--out requires a path")
			}
			opts.OutputPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--out="):
			opts.OutputPath = strings.TrimPrefix(arg, "--out=")
		default:
			return opts, fmt.Errorf("unknown tuning report option %q", arg)
		}
	}
	return opts, nil
}
