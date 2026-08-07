package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/app"
	"cohort/internal/evaluation"
	"cohort/internal/llm"
)

type judgeCalibrationSample struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Prompt          string   `json:"prompt"`
	Output          string   `json:"output"`
	Rubric          []string `json:"rubric,omitempty"`
	ExpectedMin     float64  `json:"expected_min"`
	ExpectedMax     float64  `json:"expected_max"`
	FailureCategory string   `json:"failure_category,omitempty"`
}

func runEvalJudgeCommand(ctx context.Context, cfg app.Config, store evaluation.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort eval judge run <result-id>|calibrate")
	}
	switch args[0] {
	case "run":
		id := "latest"
		profile := ""
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--profile=") {
				profile = strings.TrimSpace(strings.TrimPrefix(arg, "--profile="))
			} else if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown eval judge run option %q", arg)
			} else {
				id = arg
			}
		}
		return runEvalJudgeOnResult(ctx, cfg, store, id, profile, out)
	case "calibrate":
		profile := ""
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--profile=") {
				profile = strings.TrimSpace(strings.TrimPrefix(arg, "--profile="))
			} else {
				return fmt.Errorf("unknown eval judge calibrate option %q", arg)
			}
		}
		return runEvalJudgeCalibration(ctx, cfg, store, profile, out)
	default:
		return fmt.Errorf("unknown eval judge command %q", args[0])
	}
}

func runEvalJudgeOnResult(ctx context.Context, cfg app.Config, store evaluation.Store, id string, profile string, out io.Writer) error {
	result, err := store.LoadResult(id)
	if err != nil {
		return err
	}
	suite, err := evaluation.LoadSuite(store.SuitePath(result.SuiteID))
	if err != nil {
		return err
	}
	client, judgeProfile, err := buildEvalJudgeClient(cfg, profile)
	if err != nil {
		return err
	}
	result = evaluation.ApplyLLMJudges(ctx, result, suite, client, evaluation.LLMJudgeOptions{
		Enabled:     true,
		Mode:        "llm",
		Profile:     judgeProfile.ID,
		Model:       judgeProfile.Model,
		ArtifactDir: filepath.Join(store.RunDir(result.RunID), "judge"),
	})
	if _, err := store.SaveResult(result); err != nil {
		return err
	}
	markdownPath, htmlPath, err := evaluation.WriteReports(store, result)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "judge: completed\nrun: %s\nprofile: %s\nmodel: %s\nmarkdown: %s\ndashboard: %s\n", result.RunID, judgeProfile.ID, judgeProfile.Model, markdownPath, htmlPath)
	return nil
}

func runEvalJudgeCalibration(ctx context.Context, cfg app.Config, store evaluation.Store, profile string, out io.Writer) error {
	samples, path, err := loadOrCreateJudgeCalibration(store)
	if err != nil {
		return err
	}
	client, judgeProfile, err := buildEvalJudgeClient(cfg, profile)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "calibration: %s\nprofile: %s\nmodel: %s\n", path, judgeProfile.ID, judgeProfile.Model)
	failures := 0
	for _, sample := range samples {
		suite := evaluation.Suite{
			SchemaVersion: evaluation.SchemaVersion,
			ID:            "judge_calibration",
			Name:          "Judge Calibration",
			Cases: []evaluation.Case{{
				ID:     sample.ID,
				Name:   sample.Name,
				Prompt: sample.Prompt,
				Assertions: evaluation.Assertions{Judge: &evaluation.JudgeAssertion{
					Enabled:          true,
					Mode:             "llm",
					MinScore:         sample.ExpectedMin,
					Rubric:           sample.Rubric,
					ExpectedBehavior: "按样例预期判断 Agent 输出质量，不要因为输出流畅就放过未完成任务。",
				}},
			}},
		}
		result := evaluation.RunResult{
			RunID:     "judge_calibration_" + sample.ID,
			SuiteID:   suite.ID,
			SuiteName: suite.Name,
			StartedAt: time.Now().UTC(),
			Cases: []evaluation.CaseResult{{
				CaseID: sample.ID, Name: sample.Name, Passed: true, Score: 100, Status: "done", Output: sample.Output,
				Attempts: 1, PassedAttempts: 1, StabilityRate: 100,
				AssertionResults: []evaluation.AssertionResult{{Kind: "execution", Expected: "sample", Actual: "sample", Passed: true, Weight: 1}},
			}},
			TotalCases: 1, PassedCases: 1, PassRate: 100, Score: 100,
		}
		result = evaluation.ApplyLLMJudges(ctx, result, suite, client, evaluation.LLMJudgeOptions{
			Enabled:     true,
			Mode:        "llm",
			Profile:     judgeProfile.ID,
			Model:       judgeProfile.Model,
			ArtifactDir: filepath.Join(store.Root, "judge_calibration", "artifacts"),
		})
		judge := result.Cases[0].Judge
		ok := judge != nil && judge.Error == "" && judge.Score >= sample.ExpectedMin && judge.Score <= sample.ExpectedMax
		if !ok {
			failures++
		}
		score := 0.0
		summary := ""
		if judge != nil {
			score = judge.Score
			summary = judge.Summary
		}
		fmt.Fprintf(out, "%s\tok=%t\tscore=%.1f\texpected=%.1f..%.1f\t%s\n", sample.ID, ok, score, sample.ExpectedMin, sample.ExpectedMax, summary)
	}
	if failures > 0 {
		return fmt.Errorf("judge calibration failed: %d samples out of range", failures)
	}
	fmt.Fprintln(out, "judge calibration: PASS")
	return nil
}

func buildEvalJudgeClient(cfg app.Config, profile string) (llm.Client, app.LLMProfile, error) {
	judgeCfg := cfg
	if strings.TrimSpace(profile) != "" {
		if err := applyEvalProfile(&judgeCfg, profile); err != nil {
			return nil, app.LLMProfile{}, err
		}
	}
	active := judgeCfg.LLM.Active()
	active.Stream = false
	if strings.TrimSpace(active.APIKey) == "" {
		return nil, app.LLMProfile{}, fmt.Errorf("missing API key for judge profile %q", active.ID)
	}
	client, err := llm.NewClient(llm.ProviderConfig{
		ProfileID:      active.ID,
		Provider:       active.Provider,
		Name:           active.Name,
		APIKey:         active.APIKey,
		APIBase:        active.APIBase,
		Model:          active.Model,
		Stream:         false,
		ConnectTimeout: time.Duration(active.ConnectTimeoutSeconds) * time.Second,
		ReadTimeout:    time.Duration(active.ReadTimeoutSeconds) * time.Second,
		MaxRetries:     active.MaxRetries,
	})
	if err != nil {
		return nil, app.LLMProfile{}, err
	}
	return client, active, nil
}

func loadOrCreateJudgeCalibration(store evaluation.Store) ([]judgeCalibrationSample, string, error) {
	dir := filepath.Join(store.Root, "judge_calibration")
	path := filepath.Join(dir, "samples.json")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeJudgeCalibrationSamples(path, defaultJudgeCalibrationSamples()); err != nil {
			return nil, "", err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var samples []judgeCalibrationSample
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, "", err
	}
	if len(samples) == 0 {
		return nil, "", errors.New("judge calibration samples are empty")
	}
	return samples, path, nil
}

func writeJudgeCalibrationSamples(path string, samples []judgeCalibrationSample) error {
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func defaultJudgeCalibrationSamples() []judgeCalibrationSample {
	return []judgeCalibrationSample{
		{
			ID:          "good_concise_completion",
			Name:        "好答案",
			Prompt:      "修复 Go 测试失败并简洁说明。",
			Output:      "已修复 calc.go 中 Add 的边界逻辑，并运行 go test ./... 通过。",
			Rubric:      []string{"必须完成任务", "必须提到验证结果", "回答简洁"},
			ExpectedMin: 80, ExpectedMax: 100,
		},
		{
			ID:          "claims_without_work",
			Name:        "嘴硬未完成",
			Prompt:      "创建 config/app.json。",
			Output:      "我已经完成了配置创建，但没有任何文件证据，也没有说明内容。",
			Rubric:      []string{"不能在缺少状态证据时声称完成", "必须指出缺失或失败"},
			ExpectedMin: 0, ExpectedMax: 55, FailureCategory: "state_assertion",
		},
		{
			ID:          "verbose_overwork",
			Name:        "冗余绕路",
			Prompt:      "读取 Go 版本。",
			Output:      strings.Repeat("我会先进行大量无关分析。", 20) + "最终答案：go 1.21。",
			Rubric:      []string{"简单任务应直接回答", "冗余和绕路需要扣分"},
			ExpectedMin: 30, ExpectedMax: 75, FailureCategory: "trajectory",
		},
	}
}
