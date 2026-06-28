package builtin

import (
	"context"
	"fmt"

	"cohort/internal/action"
	"cohort/internal/foundation"
	"cohort/internal/llm"
)

// WriteDesign 根据 PRD 撰写技术设计方案。
type WriteDesign struct {
	*action.BaseAction
}

const designSystemPrompt = `You are a senior System Architect with 15 years of experience designing large-scale systems.

Your task is to write a detailed Technical Design Document based on the PRD.

The design should include:
1. **Architecture Overview**: System architecture diagram (ASCII art), tech stack choices
2. **Component Design**: Each module/component with responsibilities and interfaces
3. **Data Flow**: How data moves through the system
4. **API Design**: Key API endpoints or function signatures
5. **Data Model**: Key data structures, schemas, or types
6. **Error Handling Strategy**: How errors are handled and propagated
7. **Project Structure**: Recommended file/directory layout with brief descriptions

Be specific and concrete. Output in markdown. Use Chinese for explanations.`

// NewWriteDesign 创建一个 WriteDesign Action。
func NewWriteDesign(client llm.Client) *WriteDesign {
	return &WriteDesign{
		BaseAction: action.NewBaseAction("WriteDesign", designSystemPrompt, client),
	}
}

// Run 执行技术设计。
func (a *WriteDesign) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
	prd := findLatestByAction(history, "WritePRD")
	if prd == "" {
		prd = "请根据上下文中的 PRD 做技术设计"
	}

	prompt := fmt.Sprintf(
		"Please write a detailed technical design based on the following PRD:\n\n%s\n\n"+
			"Design a complete, production-ready system. Be specific about the tech stack, "+
			"file structure, function signatures, and data models.",
		prd,
	)

	content, err := a.AskLLM(ctx, prompt, history)
	if err != nil {
		return nil, err
	}

	return &action.ActionOutput{Content: content}, nil
}

// findLatestByAction 从历史中查找最新一条由指定 action 产生的消息。
func findLatestByAction(history []*foundation.Message, causeBy string) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].CauseBy == causeBy {
			return history[i].Content
		}
	}
	return ""
}
