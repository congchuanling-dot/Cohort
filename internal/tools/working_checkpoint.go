package tools

import (
	"context"

	"cohert/internal/agent"
	"cohert/internal/llm"
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
		Description: "Update short-term working memory for the current task. Use after reading an SOP, before switching subtasks, or after repeated failures. Store key constraints, pitfalls, important paths, progress, next step, and related_sop. Do not use at final completion.",
		Parameters: objectSchema(map[string]any{
			"key_info":    stringProp("Current task constraints, pitfalls, key findings, progress, and next step. Keep it concise."),
			"related_sop": stringProp("Related SOP path or names to re-read when unsure, for example sops/browser_sop.md"),
		}, "key_info"),
	}}
}

func (t *UpdateWorkingCheckpoint) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	keyInfo := asString(call.Args["key_info"])
	relatedSOP := asString(call.Args["related_sop"])
	return agent.Outcome{
		Data: map[string]any{
			"status":      agent.ToolStatusSuccess,
			"key_info":    keyInfo,
			"related_sop": relatedSOP,
			"turn":        call.Turn,
		},
		NextPrompt: "\n",
	}, nil
}
