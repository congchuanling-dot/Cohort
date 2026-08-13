package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/replay"
	"cohort/internal/session"
	"cohort/internal/traceview"
	"cohort/internal/worktree"
)

func runTraceCommand(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "replay" {
		return errors.New("trace replay requires the top-level CLI so model configuration can be loaded")
	}
	if len(args) > 0 && args[0] == "graph" {
		return runTraceGraphCommand(args[1:], out)
	}
	view, err := loadTraceView(args)
	if err != nil {
		return err
	}
	printTraceView(out, view)
	return nil
}

type replayCLIOptions struct {
	mode             replay.Mode
	sessionID        string
	runID            string
	forkTurn         int
	repeat           int
	model            string
	systemPromptPath string
	jsonOutput       bool
	keepWorktree     bool
}

func runTraceReplayCommand(ctx context.Context, cfg app.Config, args []string, out io.Writer) error {
	options, err := parseReplayCLIOptions(args)
	if err != nil {
		return err
	}
	if options.mode == replay.ModeExact {
		result, err := replay.ExactReplay(session.DefaultRootDir, options.sessionID, options.runID)
		if err != nil {
			return err
		}
		if options.jsonOutput {
			return writeReplayJSON(out, result)
		}
		fmt.Fprintf(out, "verified: %t\n", result.Verified)
		fmt.Fprintf(out, "session: %s\nrun: %s\n", result.SessionID, result.RunID)
		fmt.Fprintf(out, "frames: %d\nturns: %d\nllm_calls: %d\ntool_calls: %d\n", result.FrameCount, result.TurnCount, result.LLMCalls, result.ToolCalls)
		fmt.Fprintf(out, "proof_hash: %s\n", result.ProofHash)
		if result.FirstDivergence != nil {
			fmt.Fprintf(out, "first_divergence: sequence=%d turn=%d kind=%s reason=%s\n",
				result.FirstDivergence.Sequence,
				result.FirstDivergence.Turn,
				result.FirstDivergence.Kind,
				result.FirstDivergence.Reason,
			)
		}
		return replay.ValidateExact(result)
	}
	return runForkReplay(ctx, cfg, options, out)
}

func parseReplayCLIOptions(args []string) (replayCLIOptions, error) {
	options := replayCLIOptions{repeat: 1}
	if len(args) == 0 {
		return options, replayUsageError()
	}
	switch replay.Mode(args[0]) {
	case replay.ModeExact, replay.ModeFork:
		options.mode = replay.Mode(args[0])
		args = args[1:]
	default:
		options.mode = replay.ModeFork
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return options, replayUsageError()
	}
	options.sessionID = args[0]
	args = args[1:]
	var err error
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := func(name string) (string, error) {
			if index+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			index++
			return args[index], nil
		}
		switch {
		case arg == "--run":
			options.runID, err = value(arg)
		case strings.HasPrefix(arg, "--run="):
			options.runID = strings.TrimPrefix(arg, "--run=")
		case arg == "--fork-turn":
			var raw string
			raw, err = value(arg)
			if err == nil {
				options.forkTurn, err = strconv.Atoi(raw)
			}
		case strings.HasPrefix(arg, "--fork-turn="):
			options.forkTurn, err = strconv.Atoi(strings.TrimPrefix(arg, "--fork-turn="))
		case arg == "--repeat":
			var raw string
			raw, err = value(arg)
			if err == nil {
				options.repeat, err = strconv.Atoi(raw)
			}
		case strings.HasPrefix(arg, "--repeat="):
			options.repeat, err = strconv.Atoi(strings.TrimPrefix(arg, "--repeat="))
		case arg == "--model":
			options.model, err = value(arg)
		case strings.HasPrefix(arg, "--model="):
			options.model = strings.TrimPrefix(arg, "--model=")
		case arg == "--system-prompt":
			options.systemPromptPath, err = value(arg)
		case strings.HasPrefix(arg, "--system-prompt="):
			options.systemPromptPath = strings.TrimPrefix(arg, "--system-prompt=")
		case arg == "--json":
			options.jsonOutput = true
		case arg == "--keep-worktree":
			options.keepWorktree = true
		default:
			err = fmt.Errorf("unknown replay option %q", arg)
		}
		if err != nil {
			return options, err
		}
	}
	if strings.TrimSpace(options.runID) == "" {
		return options, errors.New("--run is required")
	}
	if options.mode == replay.ModeFork && options.forkTurn <= 0 {
		return options, errors.New("--fork-turn must be positive")
	}
	if options.repeat <= 0 || options.repeat > 20 {
		return options, errors.New("--repeat must be between 1 and 20")
	}
	return options, nil
}

func replayUsageError() error {
	return errors.New("usage: cohort trace replay exact <session_id> --run <run_id> [--json] | cohort trace replay fork <session_id> --run <run_id> --fork-turn N [--model name] [--system-prompt path] [--repeat N] [--keep-worktree] [--json]")
}

func runForkReplay(ctx context.Context, cfg app.Config, options replayCLIOptions, out io.Writer) error {
	plan, err := replay.BuildForkPlan(session.DefaultRootDir, options.sessionID, options.runID, options.forkTurn)
	if err != nil {
		return err
	}
	if plan.Manifest.Replayability != replay.ReplayabilityForkable {
		return fmt.Errorf("run is not forkable: %s", plan.Manifest.ReplayBlockReason)
	}
	systemPrompt := ""
	if options.systemPromptPath != "" {
		data, err := os.ReadFile(options.systemPromptPath)
		if err != nil {
			return err
		}
		systemPrompt = string(data)
	}
	baselineMetrics := replay.Metrics(plan.Manifest, plan.Frames)
	experimentID := fmt.Sprintf("fork-%s-%d", safeTraceFileID(options.runID), time.Now().UTC().UnixNano())
	sourceBundleDir := filepath.Join(session.DefaultRootDir, options.sessionID, replay.ReplayDirName, options.runID)
	experimentDir := filepath.Join(sourceBundleDir, "experiments", experimentID)
	report := replay.ExperimentReport{
		SchemaVersion: 1,
		ID:            experimentID,
		CreatedAt:     time.Now().UTC(),
		SourceSession: options.sessionID,
		SourceRun:     options.runID,
		ForkTurn:      options.forkTurn,
		Model:         options.model,
		SystemPrompt:  options.systemPromptPath,
		Baseline:      baselineMetrics,
	}
	manager, err := worktree.NewManager(plan.Manifest.Git.Root, filepath.Join(plan.Manifest.Git.Root, ".cohort", "replay-worktrees"))
	if err != nil {
		return err
	}
	for trialIndex := 1; trialIndex <= options.repeat; trialIndex++ {
		trial := replay.TrialResult{Index: trialIndex}
		spec := worktree.Spec{
			ID:         fmt.Sprintf("%s-%d", experimentID, trialIndex),
			BaseCommit: plan.Manifest.Git.HeadCommit,
			Branch:     fmt.Sprintf("cohort/replay/%s-%d", experimentID, trialIndex),
			Path:       filepath.Join(plan.Manifest.Git.Root, ".cohort", "replay-worktrees", experimentID, fmt.Sprintf("trial-%d", trialIndex)),
		}
		if err := manager.Prepare(ctx, spec); err != nil {
			trial.Status = "setup_error"
			trial.Error = err.Error()
			report.Trials = append(report.Trials, trial)
			continue
		}
		remove := !options.keepWorktree
		if plan.Manifest.Git.Dirty {
			if err := replay.ApplyWorkspaceSnapshot(ctx, spec.Path, sourceBundleDir, plan.Manifest.Snapshot); err != nil {
				trial.Status = "snapshot_error"
				trial.Error = err.Error()
				report.Trials = append(report.Trials, trial)
				if remove {
					_ = manager.Remove(ctx, spec)
				}
				continue
			}
		}
		trialResult := executeForkTrial(ctx, cfg, plan, options, spec.Path, systemPrompt)
		trial = trialResult
		trial.Index = trialIndex
		if trial.SessionID != "" && trial.RunID != "" {
			trialRoot := filepath.Join(spec.Path, session.DefaultRootDir)
			manifest, frames, loadErr := replay.LoadBundle(trialRoot, trial.SessionID, trial.RunID)
			if loadErr != nil {
				trial.Error = loadErr.Error()
			} else {
				trial.Metrics = replay.Metrics(manifest, frames)
				trial.FirstDivergenceTurn = replay.FirstDivergenceTurn(plan.Frames, frames)
				archiveDir := filepath.Join(experimentDir, "trials", fmt.Sprintf("%d", trialIndex))
				if archiveErr := replay.ArchiveBundle(trialRoot, trial.SessionID, trial.RunID, archiveDir); archiveErr != nil {
					trial.Error = archiveErr.Error()
				}
			}
		}
		report.Trials = append(report.Trials, trial)
		if remove {
			_ = manager.Remove(ctx, spec)
		}
	}
	replay.FinalizeReport(&report)
	reportPath := filepath.Join(experimentDir, "report.json")
	if err := replay.WriteReport(reportPath, report); err != nil {
		return err
	}
	if options.jsonOutput {
		return writeReplayJSON(out, report)
	}
	fmt.Fprintf(out, "experiment: %s\n", report.ID)
	fmt.Fprintf(out, "source: %s/%s\nfork_turn: %d\n", report.SourceSession, report.SourceRun, report.ForkTurn)
	fmt.Fprintf(out, "trials: %d\nsuccessful: %d\nsuccess_rate: %.1f%%\n", len(report.Trials), report.Successful, report.SuccessRate*100)
	fmt.Fprintf(out, "mean_tokens: %.0f\nmean_duration: %s\n", report.MeanTokens, formatDurationMS(int64(report.MeanDurationMS)))
	fmt.Fprintf(out, "proof_hash: %s\nreport: %s\n", report.ProofHash, reportPath)
	for _, trial := range report.Trials {
		fmt.Fprintf(out, "trial_%d: status=%s divergence_turn=%d session=%s run=%s",
			trial.Index, trial.Status, trial.FirstDivergenceTurn, trial.SessionID, trial.RunID)
		if trial.Error != "" {
			fmt.Fprintf(out, " error=%q", trial.Error)
		}
		fmt.Fprintln(out)
	}
	return nil
}

func executeForkTrial(ctx context.Context, cfg app.Config, plan replay.ForkPlan, options replayCLIOptions, worktreePath, systemPrompt string) replay.TrialResult {
	originalCWD, err := os.Getwd()
	if err != nil {
		return replay.TrialResult{Status: "setup_error", Error: err.Error()}
	}
	if err := os.Chdir(worktreePath); err != nil {
		return replay.TrialResult{Status: "setup_error", Error: err.Error()}
	}
	defer os.Chdir(originalCWD)
	cfg = cfg.WithModelOverride(options.model)
	cfg.Workspace = worktreePath
	cfg.LogDir = filepath.Join(worktreePath, "temp", "logs")
	runner, err := app.NewForkRunner(cfg, plan, systemPrompt, nil)
	if err != nil {
		return replay.TrialResult{Status: "runner_error", Error: err.Error()}
	}
	defer runner.Close()
	result, runErr := runner.Run(ctx, plan.Input, agent.NewConsoleSink(io.Discard))
	trial := replay.TrialResult{
		SessionID: runner.SessionID(),
		RunID:     runner.LastRunID(),
		Status:    result.Status,
	}
	if runErr != nil {
		trial.Error = runErr.Error()
	}
	return trial
}

func writeReplayJSON(out io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func runTraceGraphCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort trace graph last|show <session_id> [--run id] [--out path] [--open] [--json]")
	}
	viewArgs := make([]string, 0, len(args))
	outputPath := ""
	openGraph := false
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--out":
			if index+1 >= len(args) {
				return errors.New("--out requires a path")
			}
			outputPath = args[index+1]
			index++
		case strings.HasPrefix(arg, "--out="):
			outputPath = strings.TrimPrefix(arg, "--out=")
		case arg == "--open":
			openGraph = true
		case arg == "--json":
			jsonOutput = true
		default:
			viewArgs = append(viewArgs, arg)
		}
	}
	view, err := loadTraceView(viewArgs)
	if err != nil {
		return err
	}
	graph := view.CausalGraph()
	if jsonOutput {
		data, err := json.MarshalIndent(graph, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	if strings.TrimSpace(outputPath) == "" {
		outputPath = filepath.Join(
			filepath.Dir(view.Path),
			"causal-graph-"+safeTraceFileID(view.RunID)+".html",
		)
	}
	outputPath = filepath.Clean(outputPath)
	graph, err = traceview.WriteGraphHTML(view, outputPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "causal_graph: %s\n", outputPath)
	fmt.Fprintf(out, "nodes: %d\n", graph.Summary.NodeCount)
	fmt.Fprintf(out, "edges: %d\n", graph.Summary.EdgeCount)
	fmt.Fprintf(out, "critical_path: %s\n", formatDurationMS(graph.CriticalPathMS))
	fmt.Fprintf(out, "bottlenecks: %d\n", len(graph.Bottlenecks))
	fmt.Fprintf(out, "anomalies: %d\n", len(graph.Anomalies))
	fmt.Fprintf(out, "file_changes: %d\n", graph.Summary.FileChanges)
	if openGraph {
		if err := exec.Command("open", outputPath).Start(); err != nil {
			return fmt.Errorf("open causal graph: %w", err)
		}
		fmt.Fprintln(out, "opened: true")
	}
	return nil
}

func safeTraceFileID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "latest"
	}
	var b strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
			b.WriteRune(char)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
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
	if summary.LastToolRouteMode != "" {
		fmt.Fprintf(out, "last_tool_route_mode: %s\n", summary.LastToolRouteMode)
		fmt.Fprintf(out, "last_full_tool_schema_count: %d\n", summary.LastFullSchemaCount)
		fmt.Fprintf(out, "last_tool_schema_bytes: %d\n", summary.LastSchemaBytes)
		fmt.Fprintf(out, "last_saved_schema_bytes: %d\n", summary.LastSavedSchemaBytes)
		fmt.Fprintf(out, "total_saved_schema_bytes: %d\n", summary.TotalSavedSchemaBytes)
		fmt.Fprintf(out, "adaptive_route_turns: %d\n", summary.AdaptiveRouteTurns)
		fmt.Fprintf(out, "tool_route_escalations: %d\n", summary.ToolRouteEscalations)
	}
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
