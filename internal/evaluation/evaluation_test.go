package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
