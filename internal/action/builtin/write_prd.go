package builtin

import (
	"context"
	"fmt"

	"cohort/internal/action"
	"cohort/internal/foundation"
	"cohort/internal/llm"
)

// WritePRD 撰写产品需求文档。
type WritePRD struct {
	*action.BaseAction
}

const prdSystemPrompt = `You are a professional Product Manager with 10 years of experience.

Your task is to write a comprehensive Product Requirement Document (PRD) based on the user's requirements.

The PRD should include:
1. **Product Overview**: What is this product? What problem does it solve?
2. **Target Users**: Who will use this product?
3. **Core Features**: List all features with priority (P0, P1, P2)
4. **User Stories**: "As a [user], I want [feature] so that [benefit]"
5. **Non-Functional Requirements**: Performance, security, accessibility
6. **Success Metrics**: How to measure success (KPIs)

Output in clear, professional markdown. Use Chinese for the response.`

// NewWritePRD 创建一个 WritePRD Action。
func NewWritePRD(client llm.Client) *WritePRD {
	return &WritePRD{
		BaseAction: action.NewBaseAction("WritePRD", prdSystemPrompt, client),
	}
}

// Run 执行 PRD 撰写。
func (a *WritePRD) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
	// 从历史中提取用户需求
	userReq := extractUserRequirement(history)
	if userReq == "" {
		userReq = "请根据上下文写一份产品需求文档"
	}

	prompt := fmt.Sprintf("Please write a detailed PRD based on the following requirement:\n\n%s", userReq)

	content, err := a.AskLLM(ctx, prompt, history)
	if err != nil {
		return nil, err
	}

	return &action.ActionOutput{Content: content}, nil
}

// extractUserRequirement 从历史消息中提取用户需求。
func extractUserRequirement(history []*foundation.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].CauseBy == "UserRequirement" {
			return history[i].Content
		}
	}
	return ""
}
