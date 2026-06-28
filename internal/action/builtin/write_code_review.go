package builtin

import (
	"context"
	"fmt"
	"strings"

	"cohort/internal/action"
	"cohort/internal/foundation"
	"cohort/internal/llm"
)

// WriteCodeReview 对 PRD/设计文档进行评审和建议。
type WriteCodeReview struct {
	*action.BaseAction
}

const reviewSystemPrompt = `You are a senior Technical Reviewer with 15 years of experience.

Your task is to review the input document (PRD, design doc, or code) and provide:
1. **Summary**: Brief summary of what you reviewed
2. **Strengths**: What's well done (at least 2 points)
3. **Improvements**: Specific, actionable suggestions (at least 3 points)
4. **Risk Assessment**: Technical risks and mitigations
5. **Overall Assessment**: PASS / PASS_WITH_CHANGES / NEEDS_REWORK

Be constructive and specific. Use Chinese.`

// NewWriteCodeReview 创建一个 WriteCodeReview Action。
func NewWriteCodeReview(client llm.Client) *WriteCodeReview {
	return &WriteCodeReview{
		BaseAction: action.NewBaseAction("WriteCodeReview", reviewSystemPrompt, client),
	}
}

// Run 执行评审。
func (a *WriteCodeReview) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
	// 从历史中找到最新的非 UserRequirement 产出作为评审对象
	target := findLatestOutput(history)
	if target == "" {
		target = "请根据上下文做评审"
	}

	prompt := fmt.Sprintf("Please review the following:\n\n%s", target)

	content, err := a.AskLLM(ctx, prompt, history)
	if err != nil {
		return nil, err
	}

	return &action.ActionOutput{Content: content}, nil
}

// findLatestOutput 找到最新的非用户消息（即 Agent 产出）。
func findLatestOutput(history []*foundation.Message) string {
	var parts []string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].CauseBy != "UserRequirement" && history[i].Content != "" {
			parts = append(parts, history[i].Content)
			break // 只取最近一条
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}
