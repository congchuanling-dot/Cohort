package replayexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/replay"
	"cohort/internal/session"
	"cohort/internal/worktree"
)

type Config struct {
	AppConfig         app.Config
	SessionRoot       string
	SessionID         string
	RunID             string
	ForkTurn          int
	Repeat            int
	ModelOverride     string
	ProfileID         string
	SystemPrompt      string
	SystemPromptLabel string
	KeepWorktrees     bool
}

type Result struct {
	Report     replay.ExperimentReport `json:"report"`
	ReportPath string                  `json:"report_path"`
}

func RunFork(ctx context.Context, cfg Config) (Result, error) {
	cfg.SessionRoot = filepath.Clean(strings.TrimSpace(cfg.SessionRoot))
	if cfg.SessionRoot == "." || !filepath.IsAbs(cfg.SessionRoot) {
		return Result{}, errors.New("absolute session root is required")
	}
	if cfg.Repeat <= 0 {
		cfg.Repeat = 1
	}
	if cfg.Repeat > 20 {
		return Result{}, errors.New("repeat must not exceed 20")
	}
	plan, err := replay.BuildForkPlan(cfg.SessionRoot, cfg.SessionID, cfg.RunID, cfg.ForkTurn)
	if err != nil {
		return Result{}, err
	}
	if plan.Manifest.Replayability != replay.ReplayabilityForkable {
		return Result{}, fmt.Errorf("run is not forkable: %s", plan.Manifest.ReplayBlockReason)
	}
	runConfig := cfg.AppConfig
	if cfg.ProfileID != "" {
		runConfig, err = runConfig.WithActiveProfile(cfg.ProfileID)
		if err != nil {
			return Result{}, err
		}
	}
	runConfig = runConfig.WithModelOverride(cfg.ModelOverride)

	baselineMetrics := replay.Metrics(plan.Manifest, plan.Frames)
	experimentID := fmt.Sprintf("fork-%s-%d", safeID(cfg.RunID), time.Now().UTC().UnixNano())
	sourceBundleDir := filepath.Join(cfg.SessionRoot, cfg.SessionID, replay.ReplayDirName, cfg.RunID)
	experimentDir := filepath.Join(sourceBundleDir, "experiments", experimentID)
	report := replay.ExperimentReport{
		SchemaVersion: 1,
		ID:            experimentID,
		CreatedAt:     time.Now().UTC(),
		SourceSession: cfg.SessionID,
		SourceRun:     cfg.RunID,
		ForkTurn:      cfg.ForkTurn,
		Model:         firstNonEmpty(cfg.ProfileID, cfg.ModelOverride),
		SystemPrompt:  cfg.SystemPromptLabel,
		Baseline:      baselineMetrics,
	}
	projectRoot := plan.Manifest.Git.Root
	manager, err := worktree.NewManager(projectRoot, filepath.Join(projectRoot, ".cohort", "replay-worktrees"))
	if err != nil {
		return Result{}, err
	}
	for trialIndex := 1; trialIndex <= cfg.Repeat; trialIndex++ {
		trial := replay.TrialResult{Index: trialIndex}
		spec := worktree.Spec{
			ID:         fmt.Sprintf("%s-%d", experimentID, trialIndex),
			BaseCommit: plan.Manifest.Git.HeadCommit,
			Branch:     fmt.Sprintf("cohort/replay/%s-%d", experimentID, trialIndex),
			Path:       filepath.Join(projectRoot, ".cohort", "replay-worktrees", experimentID, fmt.Sprintf("trial-%d", trialIndex)),
		}
		if err := manager.Prepare(ctx, spec); err != nil {
			trial.Status = "setup_error"
			trial.Error = err.Error()
			report.Trials = append(report.Trials, trial)
			continue
		}
		remove := !cfg.KeepWorktrees
		if plan.Manifest.Git.Dirty {
			if err := replay.ApplyWorkspaceSnapshot(ctx, spec.Path, sourceBundleDir, plan.Manifest.Snapshot); err != nil {
				trial.Status = "snapshot_error"
				trial.Error = err.Error()
				report.Trials = append(report.Trials, trial)
				if remove {
					_ = manager.Discard(ctx, spec)
				}
				continue
			}
		}
		trial = executeTrial(ctx, runConfig, plan, spec.Path, cfg.SystemPrompt)
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
			_ = manager.Discard(ctx, spec)
		}
	}
	replay.FinalizeReport(&report)
	reportPath := filepath.Join(experimentDir, "report.json")
	if err := replay.WriteReport(reportPath, report); err != nil {
		return Result{}, err
	}
	result := Result{Report: report, ReportPath: reportPath}
	if report.Successful == 0 {
		return result, errors.New("all fork replay trials failed; inspect the persisted report")
	}
	return result, nil
}

func executeTrial(ctx context.Context, cfg app.Config, plan replay.ForkPlan, worktreePath, systemPrompt string) replay.TrialResult {
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

func safeID(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '-',
			char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
