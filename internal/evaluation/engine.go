package evaluation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type ExecuteCaseFunc func(ctx context.Context, c Case) Execution

type RunOptions struct {
	Workers int
	Model   string
}

func Run(ctx context.Context, suite Suite, execute ExecuteCaseFunc, opts RunOptions) RunResult {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.Workers > len(suite.Cases) {
		opts.Workers = len(suite.Cases)
	}
	started := time.Now().UTC()
	result := RunResult{
		SchemaVersion: SchemaVersion,
		RunID:         fmt.Sprintf("eval_%s", started.Format("20060102T150405.000000000")),
		SuiteID:       suite.ID,
		SuiteName:     suite.Name,
		Model:         opts.Model,
		StartedAt:     started,
		TotalCases:    len(suite.Cases),
	}
	type job struct {
		index int
		c     Case
	}
	jobs := make(chan job)
	results := make(chan struct {
		index  int
		result CaseResult
	}, len(suite.Cases))
	var workers sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				execution := executeCaseWithTimeout(ctx, item.c, execute)
				results <- struct {
					index  int
					result CaseResult
				}{index: item.index, result: ScoreCase(item.c, execution)}
			}
		}()
	}
	go func() {
		for index, c := range suite.Cases {
			jobs <- job{index: index, c: c}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	indexed := make([]struct {
		index  int
		result CaseResult
	}, 0, len(suite.Cases))
	for item := range results {
		indexed = append(indexed, item)
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].index < indexed[j].index })
	for _, item := range indexed {
		result.Cases = append(result.Cases, item.result)
		if item.result.Passed {
			result.PassedCases++
		} else {
			result.FailedCases++
		}
		result.Score += item.result.Score
		result.TotalTokens += item.result.TotalTokens
		result.InputTokens += item.result.InputTokens
		result.OutputTokens += item.result.OutputTokens
	}
	if result.TotalCases > 0 {
		result.PassRate = float64(result.PassedCases) / float64(result.TotalCases) * 100
		result.Score /= float64(result.TotalCases)
	}
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	return result
}

func executeCaseWithTimeout(parent context.Context, c Case, execute ExecuteCaseFunc) Execution {
	timeout := time.Duration(c.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	done := make(chan Execution, 1)
	go func() {
		done <- execute(ctx, c)
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
		old, ok := oldCases[c.CaseID]
		if !ok {
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
