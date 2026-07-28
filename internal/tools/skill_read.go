package tools

import (
	"context"

	"cohort/internal/agent"
	"cohort/internal/llm"
	"cohort/internal/skill"
)

// SkillRead 读取已经发现的 Skill 正文。
// 它只接受 Skill ID 或唯一 alias，不接受任意文件路径，避免绕过 Skill Store 的扫描边界。
type SkillRead struct {
	store *skill.Store
}

func NewSkillRead(store *skill.Store) *SkillRead {
	return &SkillRead{store: store}
}

func (t *SkillRead) Name() string { return ToolNameSkillRead }

func (t *SkillRead) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read the full SKILL.md for a discovered Cohort Skill. Use this after the Skill Index suggests a matching workflow. skill_id must be an id from the Skill Index, such as project/go-test or user/release-checks; an unambiguous alias is also accepted.",
		Parameters: objectSchema(map[string]any{
			"skill_id": stringProp("Skill id from the Skill Index or /skill list, for example project/go-test."),
		}, "skill_id"),
	}}
}

func (t *SkillRead) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	result, err := t.store.Read(asString(call.Args["skill_id"]))
	if err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"skill_read_failed",
				err.Error(),
				"请先查看 Skill Index 或使用 /skill list，传入明确的 skill_id，例如 project/name 或 user/name。",
			),
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":    agent.ToolStatusSuccess,
			"skill":     result.Skill,
			"content":   result.Content,
			"truncated": result.Truncated,
		},
		NextPrompt: "\n[SYSTEM HINT] 你刚读取了 Skill。如果决定按它执行，请调用 update_working_checkpoint，把关键约束、禁止事项、当前进度、下一步和 related_skill 存入工作记忆。\n",
	}, nil
}
