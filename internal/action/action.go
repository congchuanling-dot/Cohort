// Package action 定义 Agent 可执行的原子操作。
// 每个 Action（WritePRD、WriteCode、WriteTest 等）都实现 Action 接口，
// BaseAction 提供 LLM 调用的通用能力。
package action

import (
	"context"
	"fmt"

	"cohort/internal/foundation"
	"cohort/internal/llm"
	"cohort/internal/tool"
)

// Action Agent 可执行的原子操作接口。
// 这是整个框架中"动作"的抽象 —— Role 通过执行 Action 来完成工作。
// 每个 Action 的 Name() 同时也是 Message.CauseBy 的值，用于消息路由。
type Action interface {
	// Name 返回 Action 名称，也是 Message.CauseBy 的值。
	// 例如："WritePRD"、"WriteCode"、"WriteTest"。
	Name() string

	// Run 执行动作，接收历史消息，返回执行结果。
	Run(ctx context.Context, history []*foundation.Message) (*ActionOutput, error)
}

// ActionOutput Action 的执行结果。
type ActionOutput struct {
	Content         string `json:"content"`          // 自然语言输出（给其他 Agent 读的）
	InstructContent any    `json:"instruct_content"` // 结构化输出（PRD 字段、代码等）
}

// BaseAction 提供 LLM 调用的通用能力。
// 所有具体 Action（WritePRD、WriteCode 等）嵌入此结构体，
// 通过 AskLLM/AskLLMStream 方法便捷地调用 LLM。
//
// 设计要点：BaseAction 是 Action 接口的部分实现 —— 它实现了 Name()，
// 子 Action 只需实现 Run()。
type BaseAction struct {
	name   string     // Action 名称
	prefix string     // 系统提示词前缀（传给 LLM 的 system prompt）
	client llm.Client // LLM 客户端
	node   *ActionNode // 可选：结构化输出解析器
	tools  *tool.ToolRegistry // 私有工具注册表（Role 注册时注入）
}

// NewBaseAction 创建一个 BaseAction。
//
// 参数：
//   - name: Action 名称，如 "WritePRD"
//   - prefix: 系统提示词，如 "You are a senior Product Manager..."
//   - client: LLM 客户端（由 LLMResolver 按三级配置解析得到）
func NewBaseAction(name, prefix string, client llm.Client) *BaseAction {
	return &BaseAction{
		name:   name,
		prefix: prefix,
		client: client,
		tools:  tool.NewRegistry(), // 空注册表，等待 Role 注入
	}
}

// Name 返回 Action 名称。
func (a *BaseAction) Name() string {
	return a.name
}

// SetPrefix 修改系统提示词（允许运行时动态调整）。
func (a *BaseAction) SetPrefix(prefix string) {
	a.prefix = prefix
}

// SetNode 设置结构化输出解析器。
// 传入 ActionNode 后，AskLLM 的返回值可以自动提取结构化字段。
func (a *BaseAction) SetNode(node *ActionNode) {
	a.node = node
}

// SetTools 注入工具注册表（由 Role 在初始化时调用）。
// 会将 Role 的公有 + 私有工具合并后注入到每个 Action。
func (a *BaseAction) SetTools(tools *tool.ToolRegistry) {
	a.tools = tools
}

// Tools 返回工具注册表。
func (a *BaseAction) Tools() *tool.ToolRegistry {
	return a.tools
}

// CallTool 调用一个已注册的工具。
// 便捷方法，等价于 a.tools.Call(ctx, name, args)。
func (a *BaseAction) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	return a.tools.Call(ctx, name, args)
}

// AskLLM 向 LLM 发送请求。
//
// 自动完成以下步骤：
//  1. 构建 system prompt：a.prefix + 可用工具列表
//  2. 将框架 Message 转为 LLM ChatMessage
//  3. 附加当前 prompt（user 角色）
//  4. 调用 LLM Chat
func (a *BaseAction) AskLLM(ctx context.Context, prompt string, history []*foundation.Message) (string, error) {
	// 1. 构建 system prompt，附带可用工具信息
	systemPrompt := a.prefix
	if toolsInfo := a.tools.ToolsInfo(); toolsInfo != "" {
		systemPrompt += toolsInfo
	}

	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	// 2. 将框架内的 Message 转换为 LLM 的 ChatMessage
	historyMsgs := a.frameToLLMMessages(history)
	messages = append(messages, historyMsgs...)

	// 3. 附加当前 prompt
	messages = append(messages, llm.ChatMessage{
		Role:    "user",
		Content: prompt,
	})

	// 4. 调用 LLM
	resp, err := a.client.Chat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("ask llm (%s): %w", a.name, err)
	}

	return resp.Content, nil
}

// AskLLMStream 流式版本的 AskLLM。
func (a *BaseAction) AskLLMStream(ctx context.Context, prompt string, history []*foundation.Message) (<-chan *llm.StreamChunk, error) {
	systemPrompt := a.prefix
	if toolsInfo := a.tools.ToolsInfo(); toolsInfo != "" {
		systemPrompt += toolsInfo
	}

	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	historyMsgs := a.frameToLLMMessages(history)
	messages = append(messages, historyMsgs...)
	messages = append(messages, llm.ChatMessage{
		Role: "user", Content: prompt,
	})

	return a.client.ChatStream(ctx, messages)
}

// frameToLLMMessages 将框架的 foundation.Message 转换为 LLM 的 ChatMessage。
//
// 转换规则：
//   - Role 为空 → 默认 "user"
//   - 其他字段直接映射
func (a *BaseAction) frameToLLMMessages(msgs []*foundation.Message) []llm.ChatMessage {
	result := make([]llm.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		role := msg.Role
		if role == "" {
			role = "user"
		}
		result = append(result, llm.ChatMessage{
			Role:    role,
			Content: msg.Content,
		})
	}
	return result
}
