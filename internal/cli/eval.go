package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/evaluation"
	"cohort/internal/llm"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

type evalRunOptions struct {
	SuitePath string
	CaseIDs   []string
	Tags      []string
	Workers   int
	Repeat    int
}

func runEvalCommand(ctx context.Context, cfg app.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort eval init|list|run|history|report")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	store := evaluation.NewStore(root)
	switch args[0] {
	case "init":
		return initEvalSuites(store, args[1:], out)
	case "list":
		return listEvalSuites(store, out)
	case "run":
		opts, err := parseEvalRunOptions(args[1:])
		if err != nil {
			return err
		}
		return executeEvalRun(ctx, cfg, store, opts, out)
	case "history":
		return printEvalHistory(store, out)
	case "report":
		return showEvalReport(store, args[1:], out)
	default:
		return fmt.Errorf("unknown eval command %q", args[0])
	}
}

func initEvalSuites(store evaluation.Store, args []string, out io.Writer) error {
	force := false
	for _, arg := range args {
		if arg == "--force" {
			force = true
		} else {
			return fmt.Errorf("unknown eval init option %q", arg)
		}
	}
	for _, suite := range evaluation.BuiltinSuites() {
		path := store.SuitePath(suite.ID)
		if _, err := os.Stat(path); err == nil && !force {
			fmt.Fprintf(out, "exists: %s\n", path)
			continue
		}
		if err := evaluation.SaveSuite(path, suite); err != nil {
			return err
		}
		fmt.Fprintf(out, "created: %s\n", path)
	}
	return nil
}

func listEvalSuites(store evaluation.Store, out io.Writer) error {
	entries, err := os.ReadDir(store.SuitesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no eval suites; run cohort eval init")
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(store.SuitesDir(), entry.Name())
		suite, err := evaluation.LoadSuite(path)
		if err != nil {
			fmt.Fprintf(out, "%s\tINVALID\t%v\n", path, err)
			continue
		}
		fmt.Fprintf(out, "%s\t%d cases\t%s\n", suite.ID, len(suite.Cases), suite.Name)
	}
	return nil
}

func parseEvalRunOptions(args []string) (evalRunOptions, error) {
	opts := evalRunOptions{SuitePath: "core", Workers: 1}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--case" || arg == "--tag" || arg == "--workers" || arg == "--repeat":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			value := args[i+1]
			i++
			switch arg {
			case "--case":
				opts.CaseIDs = append(opts.CaseIDs, splitCSV(value)...)
			case "--tag":
				opts.Tags = append(opts.Tags, splitCSV(value)...)
			case "--workers":
				workers, err := strconv.Atoi(value)
				if err != nil || workers <= 0 || workers > 16 {
					return opts, errors.New("--workers must be between 1 and 16")
				}
				opts.Workers = workers
			case "--repeat":
				repeat, err := strconv.Atoi(value)
				if err != nil || repeat <= 0 || repeat > 20 {
					return opts, errors.New("--repeat must be between 1 and 20")
				}
				opts.Repeat = repeat
			}
		case strings.HasPrefix(arg, "--case="):
			opts.CaseIDs = append(opts.CaseIDs, splitCSV(strings.TrimPrefix(arg, "--case="))...)
		case strings.HasPrefix(arg, "--tag="):
			opts.Tags = append(opts.Tags, splitCSV(strings.TrimPrefix(arg, "--tag="))...)
		case strings.HasPrefix(arg, "--workers="):
			workers, err := strconv.Atoi(strings.TrimPrefix(arg, "--workers="))
			if err != nil || workers <= 0 || workers > 16 {
				return opts, errors.New("--workers must be between 1 and 16")
			}
			opts.Workers = workers
		case strings.HasPrefix(arg, "--repeat="):
			repeat, err := strconv.Atoi(strings.TrimPrefix(arg, "--repeat="))
			if err != nil || repeat <= 0 || repeat > 20 {
				return opts, errors.New("--repeat must be between 1 and 20")
			}
			opts.Repeat = repeat
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown eval run option %q", arg)
		default:
			opts.SuitePath = arg
		}
	}
	return opts, nil
}

func executeEvalRun(ctx context.Context, cfg app.Config, store evaluation.Store, opts evalRunOptions, out io.Writer) error {
	suite, err := evaluation.LoadSuite(store.SuitePath(opts.SuitePath))
	if err != nil {
		return err
	}
	suite, err = evaluation.FilterCases(suite, opts.CaseIDs, opts.Tags)
	if err != nil {
		return err
	}
	projectRoot := filepath.Dir(filepath.Dir(store.Root))
	evalCfg := cfg
	evalCfg.Workspace = projectRoot
	if len(suite.ToolGroups) > 0 {
		evalCfg.Tools.EnabledGroups = append([]string(nil), suite.ToolGroups...)
	}
	var outputMu sync.Mutex
	execute := func(caseCtx context.Context, request evaluation.ExecuteRequest) evaluation.Execution {
		outputMu.Lock()
		fmt.Fprintf(out, "[eval] start %s attempt=%d - %s\n", request.Case.ID, request.Attempt, request.Case.Name)
		outputMu.Unlock()
		caseCfg := evalCfg
		workspace, workspaceErr := prepareEvalWorkspace(projectRoot, store, request)
		if workspaceErr != nil {
			return evaluation.Execution{Status: "error", Error: workspaceErr.Error()}
		}
		caseCfg.Workspace = workspace
		execution := executeEvalCase(caseCtx, caseCfg, request.Case, filepath.Join(store.Root, "sessions"))
		outputMu.Lock()
		fmt.Fprintf(out, "[eval] finish %s attempt=%d status=%s duration=%s\n", request.Case.ID, request.Attempt, execution.Status, formatDurationMS(execution.DurationMS))
		outputMu.Unlock()
		return execution
	}
	result := evaluation.Run(ctx, suite, execute, evaluation.RunOptions{
		Workers: opts.Workers,
		Model:   evalCfg.LLM.Active().Model,
		Repeat:  opts.Repeat,
	})
	if previous, err := store.PreviousResult(suite.ID, result.StartedAt); err == nil {
		comparison := evaluation.Compare(result, previous)
		result.Baseline = &comparison
	}
	resultPath, err := store.SaveResult(result)
	if err != nil {
		return err
	}
	markdownPath, htmlPath, err := evaluation.WriteReports(store, result)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nrun: %s\npass_rate: %.1f%%\nscore: %.1f\npassed: %d\nfailed: %d\nduration: %s\ntokens: %d\nresult: %s\nmarkdown: %s\ndashboard: %s\n",
		result.RunID, result.PassRate, result.Score, result.PassedCases, result.FailedCases, formatDurationMS(result.DurationMS), result.TotalTokens, resultPath, markdownPath, htmlPath)
	if result.FailedCases > 0 {
		return fmt.Errorf("eval failed: %d/%d cases failed", result.FailedCases, result.TotalCases)
	}
	return nil
}

func executeEvalCase(ctx context.Context, cfg app.Config, c evaluation.Case, sessionRoot string) evaluation.Execution {
	started := time.Now()
	runner, err := app.NewRunner(cfg)
	if err != nil {
		return evaluation.Execution{Status: "error", Error: err.Error(), DurationMS: time.Since(started).Milliseconds()}
	}
	defer runner.Close()
	evalSessionStore := session.NewStore(sessionRoot)
	runner.SessionStore = &evalSessionStore
	runner.DisableLongTermMemoryReview = true
	runner.DisableCapabilityGapRecording = true
	runner.SystemPrompt += "\n[EVALUATION MODE] 这是隔离评测运行。只能调用本次请求实际提供的工具；不要调用长期记忆、checkpoint、ask_user 或未出现在 tool schema 中的工具。完成用户任务后直接给出答案。"
	sink := &evalSink{}
	runResult, runErr := runner.Run(ctx, c.Prompt, sink)
	execution := evaluation.Execution{
		Status:     runResult.Status,
		Output:     cleanEvalOutput(sink.text.String()),
		SessionID:  runner.SessionID(),
		Workspace:  cfg.Workspace,
		DurationMS: time.Since(started).Milliseconds(),
		Tools:      append([]string(nil), sink.tools...),
	}
	if runErr != nil {
		execution.Error = runErr.Error()
		if execution.Status == "" {
			execution.Status = "error"
		}
	}
	if execution.SessionID != "" {
		if view, err := traceview.LoadSessionRun(sessionRoot, execution.SessionID, ""); err == nil {
			summary := view.Summary()
			execution.DurationMS = summary.DurationMS
			execution.Turns = summary.TurnCount
			execution.ToolFailures = summary.ToolFailures
			execution.TotalTokens = summary.TotalTokens
			execution.InputTokens = summary.InputTokens
			execution.OutputTokens = summary.OutputTokens
			execution.Tools = execution.Tools[:0]
			for _, tool := range summary.Tools {
				execution.Tools = append(execution.Tools, tool.Name)
			}
		}
	}
	return execution
}

func prepareEvalWorkspace(projectRoot string, store evaluation.Store, request evaluation.ExecuteRequest) (string, error) {
	mode := strings.TrimSpace(request.Case.Fixture.Mode)
	if mode == "" || mode == "project" {
		return projectRoot, nil
	}
	workspace := filepath.Join(store.RunDir(request.RunID), "workspaces", request.Case.ID, fmt.Sprintf("attempt-%02d", request.Attempt))
	if err := os.RemoveAll(workspace); err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return "", err
	}
	for relativePath, content := range request.Case.Fixture.Files {
		target := filepath.Join(workspace, filepath.Clean(relativePath))
		relative, err := filepath.Rel(workspace, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("fixture path escapes workspace: %s", relativePath)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, []byte(content), 0644); err != nil {
			return "", err
		}
	}
	return workspace, nil
}

type evalSink struct {
	mu    sync.Mutex
	text  strings.Builder
	tools []string
}

func (s *evalSink) WriteText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text.WriteString(text)
}
func (s *evalSink) WriteToolCall(call llm.ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, call.Function.Name)
}
func (s *evalSink) WriteToolResult(string, string) {}
func (s *evalSink) WriteError(error)               {}

func printEvalHistory(store evaluation.Store, out io.Writer) error {
	results, err := store.ListResults()
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Fprintln(out, "no eval runs")
		return nil
	}
	fmt.Fprintln(out, "RUN\tSUITE\tPASS RATE\tSCORE\tCASES\tDURATION\tMODEL")
	for _, result := range results {
		fmt.Fprintf(out, "%s\t%s\t%.1f%%\t%.1f\t%d\t%s\t%s\n", result.RunID, result.SuiteID, result.PassRate, result.Score, result.TotalCases, formatDurationMS(result.DurationMS), result.Model)
	}
	return nil
}

func showEvalReport(store evaluation.Store, args []string, out io.Writer) error {
	id := "latest"
	open := false
	for _, arg := range args {
		if arg == "--open" {
			open = true
		} else if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unknown eval report option %q", arg)
		} else {
			id = arg
		}
	}
	result, err := store.LoadResult(id)
	if err != nil {
		return err
	}
	_, htmlPath, err := evaluation.WriteReports(store, result)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "dashboard: %s\n", htmlPath)
	if open {
		if err := exec.Command("open", htmlPath).Start(); err != nil {
			return fmt.Errorf("open dashboard: %w", err)
		}
		fmt.Fprintln(out, "opened: true")
	}
	return nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

var _ agent.OutputSink = (*evalSink)(nil)

var evalRunnerStatusLine = regexp.MustCompile(`(?m)^\s*LLM Running \(Turn \d+\) \.\.\.\s*$`)

func cleanEvalOutput(value string) string {
	value = evalRunnerStatusLine.ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}
