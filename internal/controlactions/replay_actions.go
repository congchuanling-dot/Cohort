package controlactions

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"cohort/internal/app"
	"cohort/internal/controlplane"
	"cohort/internal/replayexec"
	"cohort/internal/session"
)

func replayActions(configPath string) []controlplane.ActionSpec {
	sessionID := controlplane.InputField{
		Name: "session_id", Label: "Session", Type: controlplane.FieldEntity, Required: true,
		Entity: &controlplane.EntitySelector{Kind: controlplane.EntitySession, RecentFirst: true},
	}
	runID := controlplane.InputField{
		Name: "run_id", Label: "Replayable Run", Type: controlplane.FieldEntity, Required: true,
		Entity: &controlplane.EntitySelector{
			Kind: controlplane.EntityReplayBundle, Status: []string{"forkable"}, RecentFirst: true,
			DependsOn: map[string]string{"session_id": "session_id"},
		},
	}
	return []controlplane.ActionSpec{{
		ID:          "replay.fork",
		Category:    "replay",
		Label:       "分叉并验证 Run",
		Description: "在选定 Turn 前回放历史证据，从该 Turn 起在隔离 Worktree 中执行反事实 Trial。",
		Keywords:    []string{"replay", "fork", "time machine", "回放", "分叉", "时光机"},
		Risk:        controlplane.RiskExecute,
		Async:       true,
		Inputs: []controlplane.InputField{
			sessionID,
			runID,
			{Name: "fork_turn", Label: "从哪个 Turn 分叉", Type: controlplane.FieldInteger, Required: true, Default: 1},
			{Name: "repeat", Label: "重复 Trial 数", Type: controlplane.FieldInteger, Required: true, Default: 3},
			{
				Name: "profile_id", Label: "候选模型 Profile", Type: controlplane.FieldEntity,
				Entity:      &controlplane.EntitySelector{Kind: controlplane.EntityModelProfile},
				Description: "留空时使用当前激活模型。",
			},
			{Name: "system_prompt", Label: "候选 System Prompt", Type: controlplane.FieldText, Description: "留空时使用录制的 System Prompt。"},
			{Name: "keep_worktrees", Label: "保留 Trial Worktree", Type: controlplane.FieldBoolean, Default: false},
		},
		Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
			forkTurn, ok := request.Input["fork_turn"].(int)
			if !ok || forkTurn <= 0 {
				return controlplane.ActionResult{}, errors.New("fork_turn must be positive")
			}
			repeat, ok := request.Input["repeat"].(int)
			if !ok || repeat <= 0 || repeat > 20 {
				return controlplane.ActionResult{}, errors.New("repeat must be between 1 and 20")
			}
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return controlplane.ActionResult{}, err
			}
			prompt := textInput(request, "system_prompt")
			result, err := replayexec.RunFork(ctx, replayexec.Config{
				AppConfig:         cfg,
				SessionRoot:       filepath.Join(request.ProjectRoot, session.DefaultRootDir),
				SessionID:         textInput(request, "session_id"),
				RunID:             textInput(request, "run_id"),
				ForkTurn:          forkTurn,
				Repeat:            repeat,
				ProfileID:         textInput(request, "profile_id"),
				SystemPrompt:      prompt,
				SystemPromptLabel: replayPromptLabel(prompt),
				KeepWorktrees:     boolInput(request, "keep_worktrees"),
			})
			return controlplane.ActionResult{
				Summary: "counterfactual replay experiment finished",
				Data:    result,
			}, err
		},
	}}
}

func replayPromptLabel(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return "recorded"
	}
	return "candidate"
}
