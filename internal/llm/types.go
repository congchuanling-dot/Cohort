package llm

import "context"

const (
	// RoleSystem/RoleUser/RoleAssistant/RoleTool 对应 OpenAI-compatible 对话协议里的消息身份。
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message 是发给模型的对话消息，也用于保存 Runner 的会话历史。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 表示模型要求本地执行的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 是工具调用里的函数名和 JSON 参数字符串。
type ToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolSchema 是暴露给模型看的工具定义。
type ToolSchema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

// FunctionSchema 描述一个工具的名字、用途和参数结构。
type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Response 是一次模型请求完成后的最终结果。
type Response struct {
	Content   string
	ToolCalls []ToolCall
	Raw       string
}

// ChatRequest 是 Runner 调用 LLM Client 时传入的数据。
type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []ToolSchema
}

type EventType string

const (
	// EventText 是流式文本片段，EventDone 携带最终响应，EventError 表示请求失败。
	EventText  EventType = "text"
	EventDone  EventType = "done"
	EventError EventType = "error"
)

// Event 是 LLM Client 输出给 Runner 的流式事件。
type Event struct {
	Type     EventType
	Text     string
	Response *Response
	Err      error
}

// Client 是模型客户端接口。Runner 只依赖这个接口，不关心底层供应商。
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan Event, error)
}
