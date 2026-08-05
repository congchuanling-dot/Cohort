package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/session"
	"cohort/internal/traceview"
)

func runTraceCommand(args []string, out io.Writer) error {
	view, err := loadTraceView(args)
	if err != nil {
		return err
	}
	printTraceView(out, view)
	return nil
}

func runPerfCommand(args []string, out io.Writer) error {
	view, err := loadTraceView(args)
	if err != nil {
		return err
	}
	printPerfView(out, view)
	return nil
}

func loadTraceView(args []string) (traceview.RunView, error) {
	if len(args) == 0 {
		return traceview.RunView{}, errors.New("usage: cohort trace last | cohort trace show <session_id> [--run <run_id>]")
	}
	switch args[0] {
	case "last":
		runID, err := parseOptionalRunID(args[1:])
		if err != nil {
			return traceview.RunView{}, err
		}
		view, err := traceview.LoadLatest(session.DefaultRootDir)
		if err != nil {
			return traceview.RunView{}, err
		}
		if runID == "" || runID == view.RunID {
			return view, nil
		}
		return traceview.LoadSessionRun(session.DefaultRootDir, view.SessionID, runID)
	case "show":
		if len(args) < 2 {
			return traceview.RunView{}, errors.New("usage: cohort trace show <session_id> [--run <run_id>]")
		}
		runID, err := parseOptionalRunID(args[2:])
		if err != nil {
			return traceview.RunView{}, err
		}
		return traceview.LoadSessionRun(session.DefaultRootDir, args[1], runID)
	default:
		return traceview.RunView{}, fmt.Errorf("unknown trace command %q", args[0])
	}
}

func parseOptionalRunID(args []string) (string, error) {
	runID := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--run":
			if i+1 >= len(args) {
				return "", errors.New("--run requires a run id")
			}
			runID = args[i+1]
			i++
		case strings.HasPrefix(arg, "--run="):
			runID = strings.TrimPrefix(arg, "--run=")
		default:
			return "", fmt.Errorf("unknown trace option %q", arg)
		}
	}
	return runID, nil
}

func printTraceView(out io.Writer, view traceview.RunView) {
	summary := view.Summary()
	fmt.Fprintf(out, "session: %s\n", summary.SessionID)
	fmt.Fprintf(out, "run: %s\n", summary.RunID)
	fmt.Fprintf(out, "status: %s\n", summary.Status)
	fmt.Fprintf(out, "events: %d\n", summary.EventCount)
	fmt.Fprintf(out, "turns: %d\n", summary.TurnCount)
	fmt.Fprintf(out, "duration: %s\n", formatDurationMS(summary.DurationMS))
	fmt.Fprintf(out, "warnings: %d\n", summary.WarningCount)
	fmt.Fprintf(out, "errors: %d\n", summary.ErrorCount)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "timeline:")
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "OFFSET\tTURN\tEVENT\tSEVERITY\tDELTA\tSUMMARY")
	for _, item := range summary.Timeline {
		fmt.Fprintf(writer, "+%s\t%d\t%s\t%s\t%s\t%s\n",
			formatDurationMS(item.OffsetMS),
			item.Turn,
			item.EventType,
			item.Severity,
			formatDurationMS(item.SincePrevious),
			item.Summary,
		)
	}
	_ = writer.Flush()
}

func printPerfView(out io.Writer, view traceview.RunView) {
	summary := view.Summary()
	fmt.Fprintf(out, "session: %s\n", summary.SessionID)
	fmt.Fprintf(out, "run: %s\n", summary.RunID)
	fmt.Fprintf(out, "status: %s\n", summary.Status)
	fmt.Fprintf(out, "duration: %s\n", formatDurationMS(summary.DurationMS))
	fmt.Fprintf(out, "llm_calls: %d\n", summary.LLMCalls)
	fmt.Fprintf(out, "llm_time: %s\n", formatDurationMS(summary.LLMDurationMS))
	fmt.Fprintf(out, "tool_calls: %d\n", summary.ToolCalls)
	fmt.Fprintf(out, "tool_failures: %d\n", summary.ToolFailures)
	fmt.Fprintf(out, "tool_time: %s\n", formatDurationMS(summary.ToolDurationMS))
	fmt.Fprintf(out, "context_builds: %d\n", summary.ContextBuilds)
	fmt.Fprintf(out, "last_context_tokens: %d\n", summary.LastFinalTokens)
	fmt.Fprintf(out, "last_context_chars: %d\n", summary.LastFinalChars)
	fmt.Fprintf(out, "last_request_chars: %d\n", summary.LastRequestChars)
	fmt.Fprintf(out, "last_tool_schema_count: %d\n", summary.LastToolSchemaCount)
	if summary.TotalTokens > 0 {
		fmt.Fprintf(out, "usage_total_tokens: %d\n", summary.TotalTokens)
		fmt.Fprintf(out, "usage_input_tokens: %d\n", summary.InputTokens)
		fmt.Fprintf(out, "usage_output_tokens: %d\n", summary.OutputTokens)
		fmt.Fprintf(out, "usage_cache_read_tokens: %d\n", summary.CacheReadTokens)
	}
	fmt.Fprintln(out)
	printLLMTable(out, summary)
	printToolTable(out, summary)
	printGapTable(out, summary)
}

func printLLMTable(out io.Writer, summary traceview.Summary) {
	if len(summary.LLMs) == 0 {
		return
	}
	fmt.Fprintln(out, "llm:")
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TURN\tDURATION\tTOOL_CALLS\tCONTENT_CHARS\tRAW_CHARS\tTOKENS")
	for _, item := range summary.LLMs {
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%d\t%d\n",
			item.Turn,
			formatDurationMS(item.DurationMS),
			item.ToolCallCount,
			item.ContentChars,
			item.RawChars,
			item.TotalTokens,
		)
	}
	_ = writer.Flush()
	fmt.Fprintln(out)
}

func printToolTable(out io.Writer, summary traceview.Summary) {
	if len(summary.Tools) == 0 {
		return
	}
	fmt.Fprintln(out, "tools:")
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TURN\tTOOL\tSTATUS\tDURATION\tERROR")
	for _, item := range summary.Tools {
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\n",
			item.Turn,
			item.Name,
			item.Status,
			formatDurationMS(item.DurationMS),
			item.ErrorCode,
		)
	}
	_ = writer.Flush()
	fmt.Fprintln(out)
}

func printGapTable(out io.Writer, summary traceview.Summary) {
	if len(summary.Gaps) == 0 {
		return
	}
	fmt.Fprintln(out, "largest_gaps:")
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "GAP\tTURN\tFROM\tTO")
	limit := len(summary.Gaps)
	if limit > 8 {
		limit = 8
	}
	for _, item := range summary.Gaps[:limit] {
		fmt.Fprintf(writer, "%s\t%d\t%s\t%s\n",
			formatDurationMS(item.GapMS),
			item.Turn,
			item.FromEvent,
			item.ToEvent,
		)
	}
	_ = writer.Flush()
}

func formatDurationMS(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}
