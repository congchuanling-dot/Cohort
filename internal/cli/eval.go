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
	Profiles  []string
	Gate      evaluation.GateConfig
}

func runEvalCommand(ctx context.Context, cfg app.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort eval init|list|run|history|report|stability")
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
	case "stability":
		return runEvalStabilityCommand(store, args[1:], out)
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
	opts := evalRunOptions{SuitePath: "core", Workers: 1, Gate: evaluation.GateConfig{MaxRegressions: 0}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--case" || arg == "--tag" || arg == "--workers" || arg == "--repeat" || arg == "--profile" || arg == "--min-score" || arg == "--min-pass-rate" || arg == "--min-stability" || arg == "--max-regressions":
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
			case "--profile":
				opts.Profiles = append(opts.Profiles, splitCSV(value)...)
			case "--min-score":
				minScore, err := parsePercentValue(value, "--min-score")
				if err != nil {
					return opts, err
				}
				opts.Gate.MinScore = minScore
			case "--min-pass-rate":
				minPassRate, err := parsePercentValue(value, "--min-pass-rate")
				if err != nil {
					return opts, err
				}
				opts.Gate.MinPassRate = minPassRate
			case "--min-stability":
				minStability, err := parsePercentValue(value, "--min-stability")
				if err != nil {
					return opts, err
				}
				opts.Gate.MinStability = minStability
			case "--max-regressions":
				maxRegressions, err := strconv.Atoi(value)
				if err != nil || maxRegressions < 0 {
					return opts, errors.New("--max-regressions must be >= 0")
				}
				opts.Gate.MaxRegressions = maxRegressions
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
		case strings.HasPrefix(arg, "--profile="):
			opts.Profiles = append(opts.Profiles, splitCSV(strings.TrimPrefix(arg, "--profile="))...)
		case strings.HasPrefix(arg, "--min-score="):
			minScore, err := parsePercentValue(strings.TrimPrefix(arg, "--min-score="), "--min-score")
			if err != nil {
				return opts, err
			}
			opts.Gate.MinScore = minScore
		case strings.HasPrefix(arg, "--min-pass-rate="):
			minPassRate, err := parsePercentValue(strings.TrimPrefix(arg, "--min-pass-rate="), "--min-pass-rate")
			if err != nil {
				return opts, err
			}
			opts.Gate.MinPassRate = minPassRate
		case strings.HasPrefix(arg, "--min-stability="):
			minStability, err := parsePercentValue(strings.TrimPrefix(arg, "--min-stability="), "--min-stability")
			if err != nil {
				return opts, err
			}
			opts.Gate.MinStability = minStability
		case strings.HasPrefix(arg, "--max-regressions="):
			maxRegressions, err := strconv.Atoi(strings.TrimPrefix(arg, "--max-regressions="))
			if err != nil || maxRegressions < 0 {
				return opts, errors.New("--max-regressions must be >= 0")
			}
			opts.Gate.MaxRegressions = maxRegressions
		case arg == "--allow-failures":
			opts.Gate.AllowFailures = true
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown eval run option %q", arg)
		default:
			opts.SuitePath = arg
		}
	}
	return opts, nil
}

func executeEvalRun(ctx context.Context, cfg app.Config, store evaluation.Store, opts evalRunOptions, out io.Writer) error {
	profiles := opts.Profiles
	if len(profiles) == 0 {
		profiles = []string{cfg.LLM.Active().ID}
	}
	var failures []string
	for _, profile := range profiles {
		profileCfg := cfg
		if err := applyEvalProfile(&profileCfg, profile); err != nil {
			return err
		}
		if len(profiles) > 1 {
			fmt.Fprintf(out, "\n[eval] matrix profile=%s model=%s\n", profileCfg.LLM.Active().ID, profileCfg.LLM.Active().Model)
		}
		if err := executeEvalRunOnce(ctx, profileCfg, store, opts, out); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("eval matrix failed:\n%s", strings.Join(failures, "\n"))
	}
	return nil
}

func executeEvalRunOnce(ctx context.Context, cfg app.Config, store evaluation.Store, opts evalRunOptions, out io.Writer) error {
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
		Profile: evalCfg.LLM.Active().ID,
		Model:   evalCfg.LLM.Active().Model,
		Repeat:  opts.Repeat,
	})
	if previous, err := previousComparableEvalResult(store, result); err == nil {
		comparison := evaluation.Compare(result, previous)
		result.Baseline = &comparison
	}
	gate := evaluation.EvaluateGate(result, opts.Gate)
	result.Gate = &gate
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
	if result.Gate != nil && !result.Gate.Passed {
		fmt.Fprintf(out, "gate: FAIL (%s)\n", strings.Join(result.Gate.Violations, "; "))
		return fmt.Errorf("eval gate failed for %s: %s", result.RunID, strings.Join(result.Gate.Violations, "; "))
	}
	fmt.Fprintln(out, "gate: PASS")
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
			execution.TraceRunID = view.RunID
			execution.TracePath = view.Path
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

func applyEvalProfile(cfg *app.Config, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == cfg.LLM.Active().ID {
		return nil
	}
	if len(cfg.LLM.Profiles) == 0 {
		return fmt.Errorf("--profile requires llm.profiles; got %q", profile)
	}
	if _, ok := cfg.LLM.Profiles[profile]; !ok {
		return fmt.Errorf("llm profile %q does not exist", profile)
	}
	cfg.LLM.ActiveProfile = profile
	return nil
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
	if request.Case.Assertions.GitStatus != nil {
		if err := initEvalGitBaseline(workspace); err != nil {
			return "", err
		}
	}
	return workspace, nil
}

func initEvalGitBaseline(workspace string) error {
	commands := [][]string{
		{"git", "init"},
		{"git", "add", "."},
		{"git", "-c", "user.email=cohort-eval@example.invalid", "-c", "user.name=Cohort Eval", "commit", "-m", "baseline"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workspace
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return nil
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

func parsePercentValue(value string, name string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "%"), 64)
	if err != nil || parsed < 0 || parsed > 100 {
		return 0, fmt.Errorf("%s must be between 0 and 100", name)
	}
	return parsed, nil
}

func previousComparableEvalResult(store evaluation.Store, current evaluation.RunResult) (evaluation.RunResult, error) {
	results, err := store.ListResults()
	if err != nil {
		return evaluation.RunResult{}, err
	}
	for _, result := range results {
		if result.RunID == current.RunID {
			continue
		}
		if result.SuiteID == current.SuiteID && result.Model == current.Model && result.Profile == current.Profile && result.StartedAt.Before(current.StartedAt) {
			return result, nil
		}
	}
	return evaluation.RunResult{}, errors.New("no previous comparable eval result")
}

var _ agent.OutputSink = (*evalSink)(nil)

var evalRunnerStatusLine = regexp.MustCompile(`(?m)^\s*LLM Running \(Turn \d+\) \.\.\.\s*$`)

func cleanEvalOutput(value string) string {
	value = evalRunnerStatusLine.ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}
