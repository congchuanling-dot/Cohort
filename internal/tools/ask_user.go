package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

type AskUser struct{}

func NewAskUser() *AskUser {
	return &AskUser{}
}

func (t *AskUser) Name() string { return "ask_user" }

func (t *AskUser) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Ask the user for missing information or a decision.",
		Parameters: objectSchema(map[string]any{
			"question": stringProp("Question for the user"),
		}, "question"),
	}}
}

func (t *AskUser) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	question := asString(call.Args["question"])
	if question == "" {
		question = "请提供输入："
	}
	fmt.Printf("\n%s\n> ", question)
	answerCh := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		text, _ := reader.ReadString('\n')
		answerCh <- strings.TrimSpace(text)
	}()
	select {
	case <-ctx.Done():
		return agent.Outcome{}, ctx.Err()
	case answer := <-answerCh:
		return agent.Outcome{
			Data:       map[string]any{"answer": answer},
			NextPrompt: "\n",
		}, nil
	}
}
