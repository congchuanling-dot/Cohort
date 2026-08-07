package evaluation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type ExecuteRequest struct {
	RunID   string
	Case    Case
	Attempt int
}

type ExecuteCaseFunc func(ctx context.Context, request ExecuteRequest) Execution

type RunOptions struct {
	Workers int
	Profile string
	Model   string
	Repeat  int
}

func Run(ctx context.Context, suite Suite, execute ExecuteCaseFunc, opts RunOptions) RunResult {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	started := time.Now().UTC()
	result := RunResult{
		SchemaVersion: SchemaVersion,
		RunID:         fmt.Sprintf("eval_%s", started.Format("20060102T150405.000000000")),
		SuiteID:       suite.ID,
		SuiteName:     suite.Name,
		Profile:       opts.Profile,
		Model:         opts.Model,
		StartedAt:     started,
		TotalCases:    len(suite.Cases),
	}
	type job struct {
		caseIndex int
		attempt   int
		c         Case
	}
	totalAttempts := 0
	for _, c := range suite.Cases {
		totalAttempts += resolveRepeat(suite, c, opts)
	}
	if opts.Workers > totalAttempts {
		opts.Workers = totalAttempts
	}
	jobs := make(chan job)
	results := make(chan struct {
		caseIndex int
		attempt   int
		result    CaseResult
	}, totalAttempts)
	var workers sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				request := ExecuteRequest{RunID: result.RunID, Case: item.c, Attempt: item.attempt}
				execution := executeCaseWithTimeout(ctx, request, execute)
				results <- struct {
					caseIndex int
					attempt   int
					result    CaseResult
				}{caseIndex: item.caseIndex, attempt: item.attempt, result: ScoreCase(item.c, execution)}
			}
		}()
	}
	go func() {
		for index, c := range suite.Cases {
			for attempt := 1; attempt <= resolveRepeat(suite, c, opts); attempt++ {
				jobs <- job{caseIndex: index, attempt: attempt, c: c}
			}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	indexed := make([]struct {
		caseIndex int
		attempt   int
		result    CaseResult
	}, 0, totalAttempts)
	for item := range results {
		indexed = append(indexed, item)
	}
	sort.Slice(indexed, func(i, j int) bool {
		if indexed[i].caseIndex != indexed[j].caseIndex {
			return indexed[i].caseIndex < indexed[j].caseIndex
		}
		return indexed[i].attempt < indexed[j].attempt
	})
	grouped := make([][]struct {
		caseIndex int
		attempt   int
		result    CaseResult
	}, len(suite.Cases))
	for _, item := range indexed {
		grouped[item.caseIndex] = append(grouped[item.caseIndex], item)
	}
	for index, attempts := range grouped {
		caseResult := aggregateAttempts(suite.Cases[index], attempts)
		result.Cases = append(result.Cases, caseResult)
		if caseResult.Skipped {
			result.SkippedCases++
		} else if caseResult.Passed {
			result.PassedCases++
		} else {
			result.FailedCases++
		}
		result.Score += caseResult.Score
		result.TotalTokens += caseResult.TotalTokens
		result.InputTokens += caseResult.InputTokens
		result.OutputTokens += caseResult.OutputTokens
	}
	executedCases := result.TotalCases - result.SkippedCases
	if executedCases > 0 {
		result.PassRate = float64(result.PassedCases) / float64(executedCases) * 100
		result.Score /= float64(executedCases)
	}
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	return result
}

func executeCaseWithTimeout(parent context.Context, request ExecuteRequest, execute ExecuteCaseFunc) Execution {
	timeout := time.Duration(request.Case.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	done := make(chan Execution, 1)
	go func() {
		done <- execute(ctx, request)
	}()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		return Execution{
			Status:     "timeout",
			Error:      fmt.Sprintf("case exceeded timeout %s", timeout),
			DurationMS: timeout.Milliseconds(),
		}
	}
}

func resolveRepeat(suite Suite, c Case, opts RunOptions) int {
	if opts.Repeat > 0 {
		return opts.Repeat
	}
	if c.Repeat > 0 {
		return c.Repeat
	}
	if suite.DefaultRepeat > 0 {
		return suite.DefaultRepeat
	}
	return 1
}

func aggregateAttempts(c Case, attempts []struct {
	caseIndex int
	attempt   int
	result    CaseResult
}) CaseResult {
	result := CaseResult{
		CaseID:   c.ID,
		Name:     c.Name,
		Tags:     append([]string(nil), c.Tags...),
		Attempts: len(attempts),
		Passed:   len(attempts) > 0,
	}
	representative := -1
	executedAttempts := 0
	for index, item := range attempts {
		scored := item.result
		attempt := AttemptResult{
			Attempt:          item.attempt,
			Passed:           scored.Passed,
			Skipped:          scored.Skipped,
			SkipReason:       scored.SkipReason,
			Score:            scored.Score,
			Status:           scored.Status,
			Error:            scored.Error,
			Output:           scored.Output,
			SessionID:        scored.SessionID,
			TraceRunID:       scored.TraceRunID,
			TracePath:        scored.TracePath,
			Workspace:        scored.Workspace,
			DurationMS:       scored.DurationMS,
			Turns:            scored.Turns,
			Tools:            append([]string(nil), scored.Tools...),
			ToolFailures:     scored.ToolFailures,
			TotalTokens:      scored.TotalTokens,
			InputTokens:      scored.InputTokens,
			OutputTokens:     scored.OutputTokens,
			Judge:            cloneJudge(scored.Judge),
			AssertionResults: append([]AssertionResult(nil), scored.AssertionResults...),
		}
		result.AttemptResults = append(result.AttemptResults, attempt)
		result.Score += scored.Score
		result.DurationMS += scored.DurationMS
		result.TotalTokens += scored.TotalTokens
		result.InputTokens += scored.InputTokens
		result.OutputTokens += scored.OutputTokens
		if scored.Skipped {
			if representative < 0 {
				representative = index
			}
		} else if scored.Passed {
			executedAttempts++
			result.PassedAttempts++
		} else {
			executedAttempts++
			result.Passed = false
			if representative < 0 {
				representative = index
			}
		}
	}
	if executedAttempts > 0 {
		result.Score /= float64(executedAttempts)
		result.DurationMS /= int64(len(attempts))
		result.StabilityRate = float64(result.PassedAttempts) / float64(executedAttempts) * 100
	} else if len(attempts) > 0 {
		result.Skipped = true
		result.Passed = false
		result.Score = 0
	}
	if representative < 0 && len(attempts) > 0 {
		representative = 0
	}
	if representative >= 0 {
		scored := attempts[representative].result
		result.Skipped = scored.Skipped
		result.SkipReason = scored.SkipReason
		result.Status = scored.Status
		result.Error = scored.Error
		result.Output = scored.Output
		result.SessionID = scored.SessionID
		result.TraceRunID = scored.TraceRunID
		result.TracePath = scored.TracePath
		result.Turns = scored.Turns
		result.Tools = append([]string(nil), scored.Tools...)
		result.ToolFailures = scored.ToolFailures
		result.Judge = cloneJudge(scored.Judge)
		result.AssertionResults = append([]AssertionResult(nil), scored.AssertionResults...)
	}
	return result
}

func Compare(current RunResult, baseline RunResult) Comparison {
	comparison := Comparison{
		RunID:           baseline.RunID,
		ScoreDelta:      current.Score - baseline.Score,
		PassRateDelta:   current.PassRate - baseline.PassRate,
		DurationDeltaMS: current.DurationMS - baseline.DurationMS,
		TokenDelta:      current.TotalTokens - baseline.TotalTokens,
	}
	oldCases := map[string]CaseResult{}
	for _, c := range baseline.Cases {
		oldCases[c.CaseID] = c
	}
	for _, c := range current.Cases {
		if c.Skipped {
			continue
		}
		old, ok := oldCases[c.CaseID]
		if !ok || old.Skipped {
			continue
		}
		if old.Passed && !c.Passed {
			comparison.RegressedCases = append(comparison.RegressedCases, c.CaseID)
		}
		if !old.Passed && c.Passed {
			comparison.ImprovedCases = append(comparison.ImprovedCases, c.CaseID)
		}
	}
	return comparison
}

func EvaluateGate(result RunResult, cfg GateConfig) GateResult {
	gate := GateResult{Passed: true}
	if cfg.AllowFailures {
		return gate
	}
	if result.TotalCases > 0 && result.SkippedCases == result.TotalCases {
		gate.Violations = append(gate.Violations, "all eval cases were skipped because environment requirements were not met")
	}
	if cfg.MinScore > 0 && result.Score < cfg.MinScore {
		gate.Violations = append(gate.Violations, fmt.Sprintf("score %.1f < %.1f", result.Score, cfg.MinScore))
	}
	if cfg.MinPassRate > 0 && result.PassRate < cfg.MinPassRate {
		gate.Violations = append(gate.Violations, fmt.Sprintf("pass_rate %.1f%% < %.1f%%", result.PassRate, cfg.MinPassRate))
	}
	if cfg.MinStability > 0 {
		stability := averageCaseStability(result.Cases)
		if stability < cfg.MinStability {
			gate.Violations = append(gate.Violations, fmt.Sprintf("stability %.1f%% < %.1f%%", stability, cfg.MinStability))
		}
	}
	if cfg.MaxRegressions >= 0 && result.Baseline != nil && len(result.Baseline.RegressedCases) > cfg.MaxRegressions {
		gate.Violations = append(gate.Violations, fmt.Sprintf("regressions %d > %d", len(result.Baseline.RegressedCases), cfg.MaxRegressions))
	}
	if result.FailedCases > 0 && cfg.MinPassRate <= 0 {
		gate.Violations = append(gate.Violations, fmt.Sprintf("failed_cases %d > 0", result.FailedCases))
	}
	gate.Passed = len(gate.Violations) == 0
	return gate
}

func averageCaseStability(cases []CaseResult) float64 {
	var total float64
	count := 0
	for _, c := range cases {
		if c.Skipped {
			continue
		}
		total += c.StabilityRate
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func cloneJudge(judge *JudgeResult) *JudgeResult {
	if judge == nil {
		return nil
	}
	cloned := *judge
	cloned.Reasons = append([]string(nil), judge.Reasons...)
	return &cloned
}
