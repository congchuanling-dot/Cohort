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

// AskUser 让模型在缺少关键信息时，通过命令行向用户提问。
type AskUser struct{}

func NewAskUser() *AskUser {
	return &AskUser{}
}

func (t *AskUser) Name() string { return "ask_user" }

// Schema 只要求一个 question 字段。
func (t *AskUser) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Ask the user for missing information or a decision.",
		Parameters: objectSchema(map[string]any{
			"question": stringProp("Question for the user"),
		}, "question"),
	}}
}

// Run 在终端阻塞等待用户输入，并把答案作为工具结果返回给模型。
func (t *AskUser) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	question := asString(call.Args["question"])
	if question == "" {
		question = "请提供输入："
	}
	fmt.Printf("\n%s\n> ", question)
	answerCh := make(chan string, 1)
	go func() {
		// stdin 读取本身不容易被 ctx 中断，所以放到 goroutine 里配合 select。
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
