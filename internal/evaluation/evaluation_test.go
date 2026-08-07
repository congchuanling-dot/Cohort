package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/llm"
	"cohort/internal/observability"
	"cohort/internal/traceview"
)

func TestEnrichDiagnosticsEmbedsTraceAndActionItems_BitsUT(t *testing.T) {
	sessionRoot := t.TempDir()
	sessionID := "sess_diag"
	traceRunID := "run_diag"
	traceDir := filepath.Join(sessionRoot, sessionID)
	if err := os.MkdirAll(traceDir, 0755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	tracePath := filepath.Join(traceDir, traceview.ObservationLogFileName)
	writeEvalEvents(t, tracePath, []observability.Event{
		evalTestEvent(base, traceRunID, sessionID, observability.EventRunStarted, 0, observability.SeverityInfo, nil),
		evalTestEvent(base.Add(100*time.Millisecond), traceRunID, sessionID, observability.EventLLMResponseFinished, 1, observability.SeverityInfo, map[string]any{
			"duration_ms":     100,
			"tool_call_count": 1,
			"usage":           map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		}),
		evalTestEvent(base.Add(4200*time.Millisecond), traceRunID, sessionID, observability.EventToolFinished, 1, observability.SeverityWarn, map[string]any{
			"tool":        "file_read",
			"status":      "error",
			"duration_ms": 40,
			"error_code":  "not_found",
		}),
		evalTestEvent(base.Add(4300*time.Millisecond), traceRunID, sessionID, observability.EventRunFinished, 1, observability.SeverityInfo, map[string]any{
			"status":      "completed",
			"duration_ms": 4300,
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		}),
	})
	result := RunResult{
		RunID: "eval_diag", SuiteID: "stateful", SuiteName: "Stateful", StartedAt: base, TotalCases: 1, FailedCases: 1,
		Cases: []CaseResult{{
			CaseID: "create_config", Name: "Create", Passed: false, Score: 80, Attempts: 1,
			SessionID: sessionID, TraceRunID: traceRunID, TracePath: tracePath, ToolFailures: 1,
			AssertionResults: []AssertionResult{{Kind: "max_tool_calls", Expected: "3", Actual: "5", Passed: false}},
		}},
	}
	enriched := EnrichDiagnostics(result)
	c := enriched.Cases[0]
	if c.Trace == nil || c.Trace.EventCount != 4 || len(c.Trace.Timeline) != 4 {
		t.Fatalf("trace summary = %#v", c.Trace)
	}
	if len(c.ActionItems) < 2 {
		t.Fatalf("action items = %#v", c.ActionItems)
	}
	store := NewStore(t.TempDir())
	markdownPath, htmlPath, err := WriteReports(store, enriched)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "Action Items") {
		t.Fatalf("%s missing Action Items", markdownPath)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"收敛 Agent 执行轨迹", "ToolFinished"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("%s missing %q", htmlPath, want)
		}
	}
}

func evalTestEvent(at time.Time, runID string, sessionID string, eventType observability.EventType, turn int, severity observability.Severity, data map[string]any) observability.Event {
	return observability.Event{
		SchemaVersion: observability.SchemaVersion,
		EventID:       string(eventType) + "_" + runID,
		EventType:     eventType,
		Time:          at,
		RunID:         runID,
		SessionID:     sessionID,
		Turn:          turn,
		Workspace:     "/tmp/workspace",
		Source:        "runner",
		Severity:      severity,
		Data:          data,
	}
}

func writeEvalEvents(t *testing.T, path string, events []observability.Event) {
	t.Helper()
	var data []byte
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

type fakeJudgeClient struct {
	response string
}

func (c fakeJudgeClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	out := make(chan llm.Event, 1)
	out <- llm.Event{Type: llm.EventDone, Response: &llm.Response{Content: c.response}}
	close(out)
	return out, nil
}

func TestApplyLLMJudgesScoresCaseAndWritesArtifact_BitsUT(t *testing.T) {
	suite := Suite{
		SchemaVersion: SchemaVersion,
		ID:            "judge_suite",
		Name:          "Judge Suite",
		Cases: []Case{{
			ID: "judge_case", Name: "Judge Case", Prompt: "do it",
			Assertions: Assertions{Judge: &JudgeAssertion{
				Enabled: true, Mode: "llm", MinScore: 80,
				Rubric: []string{"must complete"},
			}},
		}},
	}
	result := RunResult{
		RunID: "eval_judge", SuiteID: suite.ID, SuiteName: suite.Name,
		Cases: []CaseResult{{
			CaseID: "judge_case", Name: "Judge Case", Passed: true, Score: 100, Status: "done", Output: "not enough",
			Attempts: 1, PassedAttempts: 1, StabilityRate: 100,
			AssertionResults: []AssertionResult{{Kind: "execution", Expected: "ok", Actual: "ok", Passed: true, Weight: 1}},
		}},
		TotalCases: 1, PassedCases: 1, PassRate: 100, Score: 100,
	}
	client := fakeJudgeClient{response: `{"score":45,"passed":false,"summary":"未完成任务","strengths":[],"weaknesses":["缺少状态证据"],"failure_category":"state_assertion","repair_hint":"补文件状态验证"}`}
	enriched := ApplyLLMJudges(context.Background(), result, suite, client, LLMJudgeOptions{
		Enabled: true, Mode: "llm", Profile: "fake", Model: "fake-model", ArtifactDir: t.TempDir(),
	})
	c := enriched.Cases[0]
	if c.Passed || c.Score >= 100 || c.Judge == nil || c.Judge.FailureCategory != "state_assertion" {
		t.Fatalf("judged case = %#v judge=%#v", c, c.Judge)
	}
	if c.Judge.RawPath == "" {
		t.Fatal("judge raw artifact path is empty")
	}
	if _, err := os.Stat(c.Judge.RawPath); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, assertion := range c.AssertionResults {
		if assertion.Kind == "judge_score" && !assertion.Passed {
			found = true
		}
	}
	if !found {
		t.Fatalf("judge_score assertion missing: %#v", c.AssertionResults)
	}
}

func TestSuiteValidationFilteringAndRoundTrip_BitsUT(t *testing.T) {
	suite := coreSuite()
	path := filepath.Join(t.TempDir(), "core.json")
	if err := SaveSuite(path, suite); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSuite(path)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := FilterCases(loaded, nil, []string{"codebase"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Cases) < 3 {
		t.Fatalf("codebase cases = %d, want at least 3", len(filtered.Cases))
	}
	loaded.Cases = append(loaded.Cases, loaded.Cases[0])
	if err := ValidateSuite(loaded); err == nil {
		t.Fatal("duplicate case id validation error = nil")
	}
}

func TestScoreCaseUsesOutputToolAndBudgetAssertions_BitsUT(t *testing.T) {
	c := Case{
		ID: "case", Name: "case",
		Assertions: Assertions{
			Status:            "done",
			OutputContains:    []string{"expected"},
			OutputNotContains: []string{"forbidden"},
			RequiredTools:     []string{"file_read"},
			ForbiddenTools:    []string{"file_write"},
			MaxTurns:          3,
			MaxDurationMS:     1000,
			MaxToolFailures:   0,
		},
	}
	passed := ScoreCase(c, Execution{
		Status: "done", Output: "EXPECTED result", Tools: []string{"file_read"}, Turns: 2, DurationMS: 500,
	})
	if !passed.Passed || passed.Score != 100 {
		t.Fatalf("passed result = %#v", passed)
	}
	failed := ScoreCase(c, Execution{
		Status: "done", Output: "forbidden", Tools: []string{"file_write"}, Turns: 4, DurationMS: 1500, ToolFailures: 1,
	})
	if failed.Passed || failed.Score >= 100 {
		t.Fatalf("failed result = %#v", failed)
	}
	if len(failed.AssertionResults) < 7 {
		t.Fatalf("assertion count = %d", len(failed.AssertionResults))
	}
}

func TestRunCompareStoreAndDashboard_BitsUT(t *testing.T) {
	suite := Suite{
		SchemaVersion: SchemaVersion,
		ID:            "stable", Name: "Stable",
		Cases: []Case{
			{ID: "pass", Name: "Pass", Prompt: "x", Tags: []string{"core"}, Assertions: Assertions{OutputContains: []string{"ok"}}},
			{ID: "fail", Name: "Fail", Prompt: "x", Tags: []string{"core", "tool"}, Assertions: Assertions{RequiredTools: []string{"file_read"}}},
		},
	}
	baseline := Run(context.Background(), suite, func(context.Context, ExecuteRequest) Execution {
		return Execution{Status: "done", Output: "ok", Tools: []string{"file_read"}, DurationMS: 10}
	}, RunOptions{Workers: 2, Model: "fake"})
	if baseline.FailedCases != 0 || baseline.PassRate != 100 {
		t.Fatalf("baseline = %#v", baseline)
	}
	current := Run(context.Background(), suite, func(_ context.Context, request ExecuteRequest) Execution {
		if request.Case.ID == "fail" {
			return Execution{Status: "done", Output: "ok", DurationMS: 20}
		}
		return Execution{Status: "done", Output: "ok", DurationMS: 20}
	}, RunOptions{Workers: 2, Model: "fake"})
	comparison := Compare(current, baseline)
	current.Baseline = &comparison
	if len(comparison.RegressedCases) != 1 || comparison.RegressedCases[0] != "fail" {
		t.Fatalf("comparison = %#v", comparison)
	}

	store := NewStore(t.TempDir())
	baseline.StartedAt = time.Now().Add(-time.Minute)
	if _, err := store.SaveResult(baseline); err != nil {
		t.Fatal(err)
	}
	current.RunID = "eval_current"
	current.StartedAt = time.Now()
	if _, err := store.SaveResult(current); err != nil {
		t.Fatal(err)
	}
	markdownPath, htmlPath, err := WriteReports(store, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{markdownPath, htmlPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 500 {
			t.Fatalf("%s too small: %d", path, len(data))
		}
	}
	htmlData, _ := os.ReadFile(htmlPath)
	html := string(htmlData)
	for _, want := range []string{"Cohort Evaluation", "历史趋势", "标签通过率", "eval_current", "fail"} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
}

func TestCaseTimeout_BitsUT(t *testing.T) {
	suite := Suite{
		SchemaVersion: SchemaVersion, ID: "timeout", Name: "Timeout",
		Cases: []Case{{ID: "slow", Name: "Slow", Prompt: "x", TimeoutSec: 1}},
	}
	result := Run(context.Background(), suite, func(ctx context.Context, _ ExecuteRequest) Execution {
		<-ctx.Done()
		return Execution{Status: "cancelled"}
	}, RunOptions{Workers: 1})
	if result.Cases[0].Status != "timeout" || result.Cases[0].Passed {
		t.Fatalf("timeout result = %#v", result.Cases[0])
	}
}

func TestStateAndTrajectoryAssertions_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "result.txt"), []byte("status=ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := Case{
		ID: "state", Name: "state",
		Assertions: Assertions{
			ToolSequence:        []string{"file_read", "file_write"},
			NoConsecutiveRepeat: true,
			MaxToolCalls:        3,
			FilesExist:          []string{"result.txt"},
			FilesNotExist:       []string{"unexpected.txt"},
			FileContains:        map[string][]string{"result.txt": {"status=ready"}},
			FileNotContains:     map[string][]string{"result.txt": {"status=broken"}},
		},
	}
	result := ScoreCase(c, Execution{
		Status: "done", Workspace: workspace, Tools: []string{"file_read", "code_run", "file_write"},
	})
	if !result.Passed || result.Score != 100 {
		t.Fatalf("state/trajectory result = %#v", result)
	}
	result = ScoreCase(c, Execution{
		Status: "done", Workspace: workspace, Tools: []string{"file_write", "file_write", "file_read"},
	})
	if result.Passed {
		t.Fatalf("bad trajectory passed: %#v", result)
	}
}

func TestRepeatedRunReportsStability_BitsUT(t *testing.T) {
	suite := Suite{
		SchemaVersion: 1, ID: "repeat", Name: "repeat", DefaultRepeat: 3,
		Cases: []Case{{ID: "flaky", Name: "flaky", Prompt: "x", Assertions: Assertions{OutputContains: []string{"ok"}}}},
	}
	result := Run(context.Background(), suite, func(_ context.Context, request ExecuteRequest) Execution {
		output := "ok"
		if request.Attempt == 2 {
			output = "bad"
		}
		return Execution{Status: "done", Output: output, DurationMS: 10, TotalTokens: 5}
	}, RunOptions{Workers: 3})
	c := result.Cases[0]
	if c.Passed || c.Attempts != 3 || c.PassedAttempts != 2 {
		t.Fatalf("repeat case = %#v", c)
	}
	if c.StabilityRate < 66 || c.StabilityRate > 67 {
		t.Fatalf("stability rate = %f", c.StabilityRate)
	}
	if result.TotalTokens != 15 || len(c.AttemptResults) != 3 {
		t.Fatalf("repeat aggregation = %#v", result)
	}
}

func TestEvalV3AssertionsJudgeAndGate_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.json"), []byte(`{"enabled":true,"retries":3,"name":"cohort-eval"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("owner=cohort\nstatus=ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := Case{
		ID:      "v3",
		Name:    "v3",
		Fixture: Fixture{Mode: "temp", Files: map[string]string{"state.txt": "owner=cohort\nstatus=old\n"}},
		Assertions: Assertions{
			Status:           "done",
			FileJSONEquals:   map[string]json.RawMessage{"app.json": json.RawMessage(`{"name":"cohort-eval","enabled":true,"retries":3}`)},
			FileDiffContains: map[string][]string{"state.txt": {"+status=ready", "-status=old"}},
			CommandAssertions: []CommandAssertion{{
				Name:              "json check",
				Command:           "test -f app.json && grep -q cohort-eval app.json",
				ExitCode:          0,
				OutputNotContains: []string{"FAIL"},
			}},
			Judge: &JudgeAssertion{Enabled: true, Mode: "heuristic", MinScore: 90, MaxOutputChars: 40, MaxToolCalls: 2, RequireNoToolOveruse: true},
		},
	}
	result := ScoreCase(c, Execution{
		Status: "done", Output: "ok", Workspace: workspace, Tools: []string{"file_read", "file_write"},
	})
	if !result.Passed || result.Judge == nil || result.Judge.Score != 100 {
		t.Fatalf("v3 assertions should pass: %#v", result)
	}
	failed := ScoreCase(c, Execution{
		Status: "done", Output: strings.Repeat("verbose ", 20), Workspace: workspace, Tools: []string{"a", "b", "c"},
	})
	if failed.Passed || failed.Judge == nil || failed.Judge.Score >= 90 {
		t.Fatalf("judge/tool overuse should fail: %#v", failed)
	}
	run := RunResult{
		Score:       89,
		PassRate:    100,
		TotalCases:  1,
		FailedCases: 0,
		Cases:       []CaseResult{{StabilityRate: 50}},
	}
	gate := EvaluateGate(run, GateConfig{MinScore: 90, MinPassRate: 100, MinStability: 80, MaxRegressions: 0})
	if gate.Passed || len(gate.Violations) != 2 {
		t.Fatalf("gate = %#v, want score and stability violations", gate)
	}
}

func TestStabilityIndexAndReports_BitsUT(t *testing.T) {
	now := time.Now()
	results := []RunResult{
		{
			RunID: "r1", SuiteID: "stateful", SuiteName: "Stateful", Profile: "deepseek", Model: "model-a", StartedAt: now.Add(-time.Hour),
			PassRate: 100, Score: 100, TotalCases: 1,
			Cases: []CaseResult{{CaseID: "create_config", Name: "Create", Passed: true, Score: 100, StabilityRate: 100}},
		},
		{
			RunID: "r2", SuiteID: "stateful", SuiteName: "Stateful", Profile: "deepseek", Model: "model-a", StartedAt: now,
			PassRate: 0, Score: 80, FailedCases: 1, TotalCases: 1,
			Cases: []CaseResult{{
				CaseID: "create_config", Name: "Create", Passed: false, Score: 80, StabilityRate: 50,
				AssertionResults: []AssertionResult{{Kind: "max_tool_calls", Expected: "3", Actual: "4", Passed: false}},
				TracePath:        "/tmp/run.log.jsonl", TraceRunID: "run_2",
			}},
		},
	}
	index := BuildStabilityIndex(results, StabilityOptions{Window: 20, SuiteID: "stateful", Profile: "deepseek"})
	if index.Summary.Runs != 2 || index.Summary.FlakyCases != 1 || index.Summary.Regressions != 1 || len(index.FailureSignatures) != 1 {
		t.Fatalf("index summary = %#v signatures=%#v", index.Summary, index.FailureSignatures)
	}
	if len(index.Cases) != 1 || !index.Cases[0].Flaky || index.Cases[0].LatestTraceRunID != "run_2" {
		t.Fatalf("case metrics = %#v", index.Cases)
	}
	store := NewStore(t.TempDir())
	indexPath, markdownPath, htmlPath, err := WriteStabilityReports(store, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{indexPath, markdownPath, htmlPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "create_config") {
			t.Fatalf("%s missing case id", path)
		}
	}
}
