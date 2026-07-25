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

// AskUser 让模型在缺少关键信息或高风险操作确认时，通过命令行向用户提问。
type AskUser struct {
	confirmations *ConfirmationStore
}

// NewAskUser 创建询问工具。传入确认存储时，可为受控桌面动作签发一次性确认令牌。
func NewAskUser(confirmations ...*ConfirmationStore) *AskUser {
	var store *ConfirmationStore
	if len(confirmations) > 0 {
		store = confirmations[0]
	}
	return &AskUser{confirmations: store}
}

func (t *AskUser) Name() string { return ToolNameAskUser }

// Schema 只要求一个 question 字段。
func (t *AskUser) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Ask the user for missing information or a decision.",
		Parameters: objectSchema(map[string]any{
			"question": stringProp("Question for the user"),
			"approval": objectProp("Optional confirmation binding for a high-risk action. Supported operations: desktop_ax_press/desktop_click with operation, pid, node_id, reason; desktop_press_key with operation, pid, key, reason. A positive user answer returns a one-time confirmation_token."),
		}, "question"),
	}}
}

// Run 在终端阻塞等待用户输入，并把答案作为工具结果返回给模型。
func (t *AskUser) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	question := asString(call.Args["question"])
	if question == "" {
		question = "请提供输入："
	}
	approval, approvalErr := parseAskUserApproval(call.Args)
	if approvalErr != nil {
		return agent.Outcome{Data: *approvalErr, NextPrompt: "\n"}, nil
	}
	if approval != nil {
		question += "\n\n" + approvalPrompt(*approval) + "\n请输入“确认”继续，其他回答均视为拒绝。"
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
		data := map[string]any{"answer": answer}
		if approval != nil {
			approved := approvalAnswerAccepted(answer)
			data["approved"] = approved
			if approved {
				if t.confirmations == nil {
					return agent.Outcome{
						Data: agent.NewToolError(
							"confirmation_unavailable",
							"ask_user cannot issue an approval token because the confirmation store is unavailable",
							"请重新初始化 Cohert 工具注册表后重试。",
						),
						NextPrompt: "\n",
					}, nil
				}
				token, err := t.confirmations.Issue(*approval)
				if err != nil {
					return agent.Outcome{
						Data: agent.NewToolError(
							"confirmation_issue_failed",
							fmt.Sprintf("cannot issue confirmation token: %v", err),
							"请重新发起确认；不要尝试手工构造 confirmation_token。",
						),
						NextPrompt: "\n",
					}, nil
				}
				data["confirmation_token"] = token
			}
		}
		return agent.Outcome{
			Data:       data,
			NextPrompt: "\n",
		}, nil
	}
}

func parseAskUserApproval(args map[string]any) (*ActionApproval, *agent.ToolErrorData) {
	raw, present := args["approval"]
	if !present || raw == nil {
		return nil, nil
	}
	approval := asObject(raw)
	if len(approval) == 0 {
		err := agent.NewToolError(
			"confirmation_bad_request",
			"ask_user approval must be an object",
			"请提供 operation、pid、node_id 和 reason，或省略 approval 进行普通提问。",
		)
		return nil, &err
	}
	value := ActionApproval{
		Operation: strings.TrimSpace(asString(approval["operation"])),
		PID:       asInt(approval["pid"], 0),
		NodeID:    strings.TrimSpace(asString(approval["node_id"])),
		Key:       strings.TrimSpace(asString(approval["key"])),
		Reason:    strings.TrimSpace(asString(approval["reason"])),
	}
	if !validActionApproval(value) {
		err := agent.NewToolError(
			"confirmation_bad_request",
			"ask_user approval only supports desktop_ax_press/desktop_click with pid/node_id/reason or desktop_press_key with pid/key/reason",
			"请使用工具返回的 approval_request 原样请求用户确认，不要手写或复用旧确认。",
		)
		return nil, &err
	}
	return &value, nil
}

func validActionApproval(value ActionApproval) bool {
	if value.PID <= 0 || value.Reason == "" {
		return false
	}
	switch value.Operation {
	case desktopAXPressOperation, desktopClickOperation:
		return value.NodeID != "" && value.Key == ""
	case desktopPressKeyOperation:
		return value.Key != "" && value.NodeID == ""
	default:
		return false
	}
}

func approvalPrompt(value ActionApproval) string {
	switch value.Operation {
	case desktopAXPressOperation:
		return fmt.Sprintf(
			"该回答将只授权一次桌面 AXPress 操作：pid=%d，node_id=%s，原因=%s。",
			value.PID,
			value.NodeID,
			value.Reason,
		)
	case desktopClickOperation:
		return fmt.Sprintf(
			"该回答将只授权一次桌面物理点击操作：pid=%d，node_id=%s，原因=%s。",
			value.PID,
			value.NodeID,
			value.Reason,
		)
	case desktopPressKeyOperation:
		return fmt.Sprintf(
			"该回答将只授权一次桌面按键操作：pid=%d，key=%s，原因=%s。",
			value.PID,
			value.Key,
			value.Reason,
		)
	default:
		return "该回答将只授权一次受控桌面操作。"
	}
}
