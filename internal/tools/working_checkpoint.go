package tools

import (
	"context"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

// UpdateWorkingCheckpoint 让模型把当前任务的关键约束写入短期工作记忆。
// 真正的状态更新由 Runner 根据工具参数完成，这里只提供 schema 和轻量确认结果。
type UpdateWorkingCheckpoint struct{}

func NewUpdateWorkingCheckpoint() *UpdateWorkingCheckpoint {
	return &UpdateWorkingCheckpoint{}
}

func (t *UpdateWorkingCheckpoint) Name() string { return ToolNameUpdateWorkingCheckpoint }

func (t *UpdateWorkingCheckpoint) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Update short-term working memory for the current task. Use after reading an SOP or Skill, before switching subtasks, or after repeated failures. key_info should be structured as: [任务] ... [关键约束] ... [禁止事项] ... [当前进度] ... [下一步] ... Keep it concise. Do not use at final completion.",
		Parameters: objectSchema(map[string]any{
			"key_info":      stringProp("Structured checkpoint: [任务] goal; [关键约束] SOP/Skill rules; [禁止事项] must-not-do; [当前进度] verified state; [下一步] immediate action."),
			"related_sop":   stringProp("Related SOP path or names to re-read when unsure, for example sops/browser_sop.md"),
			"related_skill": stringProp("Related Skill id to re-read when unsure, for example project/go-test"),
		}, "key_info"),
	}}
}

func (t *UpdateWorkingCheckpoint) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	keyInfo := asString(call.Args["key_info"])
	relatedSOP := asString(call.Args["related_sop"])
	relatedSkill := asString(call.Args["related_skill"])
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"updated":          true,
			"related_sop":      relatedSOP,
			"related_skill":    relatedSkill,
			"key_info_chars":   len([]rune(keyInfo)),
			"checkpoint_turn":  call.Turn,
			"checkpoint_index": call.Index,
		},
		NextPrompt: "\n",
	}, nil
}
