package evaluation

import (
	"context"
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
	baseline := Run(context.Background(), suite, func(context.Context, Case) Execution {
		return Execution{Status: "done", Output: "ok", Tools: []string{"file_read"}, DurationMS: 10}
	}, RunOptions{Workers: 2, Model: "fake"})
	if baseline.FailedCases != 0 || baseline.PassRate != 100 {
		t.Fatalf("baseline = %#v", baseline)
	}
	current := Run(context.Background(), suite, func(_ context.Context, c Case) Execution {
		if c.ID == "fail" {
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
	result := Run(context.Background(), suite, func(ctx context.Context, _ Case) Execution {
		<-ctx.Done()
		return Execution{Status: "cancelled"}
	}, RunOptions{Workers: 1})
	if result.Cases[0].Status != "timeout" || result.Cases[0].Passed {
		t.Fatalf("timeout result = %#v", result.Cases[0])
	}
}
