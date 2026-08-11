package controlactions

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/app"
	"cohort/internal/delivery"
	"cohort/internal/evaluation"
	"cohort/internal/evolution"
	"cohort/internal/explorer"
	"cohort/internal/hermes"
	"cohort/internal/session"
)

type DashboardSnapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Project     ProjectSummary    `json:"project"`
	Model       ModelSummary      `json:"model"`
	Counts      ResourceCounts    `json:"counts"`
	Delivery    DeliverySummary   `json:"delivery"`
	Hermes      HermesSummary     `json:"hermes"`
	Evaluation  EvaluationSummary `json:"evaluation"`
	Reflection  any               `json:"reflection"`
}

type ProjectSummary struct {
	Root   string `json:"root"`
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Dirty  bool   `json:"dirty"`
}

type ModelSummary struct {
	ConfigPath    string `json:"config_path"`
	Profile       string `json:"profile"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	APIKeyPresent bool   `json:"api_key_present"`
}

type ResourceCounts struct {
	Sessions   int `json:"sessions"`
	Deliveries int `json:"deliveries"`
	Explorers  int `json:"explorers"`
	EvalRuns   int `json:"eval_runs"`
}

type DeliverySummary struct {
	Active   int                             `json:"active"`
	Verified int                             `json:"verified"`
	Failed   int                             `json:"failed"`
	Latest   *delivery.Delivery              `json:"latest,omitempty"`
	ByStatus map[delivery.DeliveryStatus]int `json:"by_status"`
}

type HermesSummary struct {
	Running         bool   `json:"running"`
	OpenActions     int    `json:"open_actions"`
	CriticalActions int    `json:"critical_actions"`
	RunningJobs     int    `json:"running_jobs"`
	RunningRepairs  int    `json:"running_repairs"`
	LastError       string `json:"last_error,omitempty"`
}

type EvaluationSummary struct {
	LatestRunID string  `json:"latest_run_id,omitempty"`
	PassRate    float64 `json:"pass_rate"`
	Score       float64 `json:"score"`
	Regressions int     `json:"regressions"`
}

func SnapshotProvider(configPath string) func(context.Context, string) (any, error) {
	return func(ctx context.Context, projectRoot string) (any, error) {
		project, err := inspectProject(ctx, projectRoot)
		if err != nil {
			return nil, err
		}
		snapshot := DashboardSnapshot{
			GeneratedAt: time.Now().UTC(),
			Project:     project,
			Delivery: DeliverySummary{
				ByStatus: map[delivery.DeliveryStatus]int{},
			},
		}
		cfg, configErr := app.LoadConfig(configPath)
		if configErr == nil {
			active := cfg.LLM.Active()
			snapshot.Model = ModelSummary{
				ConfigPath: configPath, Profile: cfg.LLM.ActiveProfile,
				Provider: active.Provider, Model: active.Model,
				APIKeyPresent: strings.TrimSpace(active.APIKey) != "",
			}
		} else {
			snapshot.Model.ConfigPath = configPath
		}

		deliveries, err := delivery.NewStore(projectRoot).List()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		snapshot.Counts.Deliveries = len(deliveries)
		for index := range deliveries {
			item := deliveries[index]
			snapshot.Delivery.ByStatus[item.Status]++
			switch item.Status {
			case delivery.StatusVerified:
				snapshot.Delivery.Verified++
			case delivery.StatusFailed, delivery.StatusBudgetExhausted:
				snapshot.Delivery.Failed++
			case delivery.StatusCancelled:
			default:
				snapshot.Delivery.Active++
			}
		}
		if len(deliveries) > 0 {
			latest := deliveries[0]
			snapshot.Delivery.Latest = &latest
		}
		sessions, sessionErr := session.NewStore(filepath.Join(projectRoot, session.DefaultRootDir)).List()
		if sessionErr == nil {
			snapshot.Counts.Sessions = len(sessions)
		}
		explorers, explorerErr := explorer.NewStore(projectRoot).List()
		if explorerErr == nil {
			snapshot.Counts.Explorers = len(explorers)
		}
		evalRuns, evalErr := evaluation.NewStore(projectRoot).ListResults()
		if evalErr == nil {
			snapshot.Counts.EvalRuns = len(evalRuns)
			if len(evalRuns) > 0 {
				latest := evalRuns[0]
				snapshot.Evaluation.LatestRunID = latest.RunID
				snapshot.Evaluation.PassRate = latest.PassRate
				snapshot.Evaluation.Score = latest.Score
				if latest.Baseline != nil {
					snapshot.Evaluation.Regressions = len(latest.Baseline.RegressedCases)
				}
			}
		}
		hermesStore := hermes.NewStore(projectRoot)
		if _, statErr := os.Stat(hermesStore.StatusPath()); statErr == nil {
			hermesStatus, hermesErr := hermesStore.LoadStatus()
			if hermesErr == nil {
				snapshot.Hermes = HermesSummary{
					Running: hermesStatus.Running, OpenActions: hermesStatus.OpenActions,
					CriticalActions: hermesStatus.CriticalActions,
					RunningJobs:     len(hermesStatus.RunningJobs), RunningRepairs: len(hermesStatus.RunningRepairs),
					LastError: hermesStatus.LastError,
				}
			}
		}
		reflectionQueue := evolution.NewReflectionQueue(projectRoot)
		if _, statErr := os.Stat(reflectionQueue.RootDir); statErr == nil {
			reflectionStatus, reflectionErr := reflectionQueue.Status()
			if reflectionErr == nil {
				snapshot.Reflection = reflectionStatus
			}
		}
		return snapshot, nil
	}
}

func inspectProject(ctx context.Context, root string) (ProjectSummary, error) {
	branch, err := gitOutput(ctx, root, "branch", "--show-current")
	if err != nil {
		return ProjectSummary{}, err
	}
	head, err := gitOutput(ctx, root, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return ProjectSummary{}, err
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return ProjectSummary{}, err
	}
	return ProjectSummary{
		Root: root, Name: filepath.Base(root), Branch: branch, Head: head, Dirty: status != "",
	}, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
