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
	// Role 是消息身份，例如 system、user、assistant 或 tool。
	Role string `json:"role"`
	// Content 是消息文本内容，工具调用消息可以为空。
	Content string `json:"content,omitempty"`
	// ToolCallID 是 tool 消息对应的 assistant tool call ID。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Name 是 tool 消息对应的工具名。
	Name string `json:"name,omitempty"`
	// ToolCalls 是 assistant 要求本地执行的工具调用列表。
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 表示模型要求本地执行的一次工具调用。
type ToolCall struct {
	// ID 是模型生成的工具调用标识，用于和后续 tool 结果配对。
	ID string `json:"id,omitempty"`
	// Type 是工具调用类型，OpenAI-compatible 协议里通常是 function。
	Type string `json:"type,omitempty"`
	// Function 保存具体函数名和 JSON 参数字符串。
	Function ToolFunction `json:"function"`
}

// ToolFunction 是工具调用里的函数名和 JSON 参数字符串。
type ToolFunction struct {
	// Name 是模型请求调用的工具函数名。
	Name string `json:"name,omitempty"`
	// Arguments 是模型生成的 JSON 参数字符串。
	Arguments string `json:"arguments,omitempty"`
}

// ToolSchema 是暴露给模型看的工具定义。
type ToolSchema struct {
	// Type 是 schema 类型，当前固定为 function。
	Type string `json:"type"`
	// Function 描述函数工具的名字、说明和参数 JSON Schema。
	Function FunctionSchema `json:"function"`
}

// FunctionSchema 描述一个工具的名字、用途和参数结构。
type FunctionSchema struct {
	// Name 是模型可调用的工具名。
	Name string `json:"name"`
	// Description 告诉模型工具用途和使用边界。
	Description string `json:"description"`
	// Parameters 是传给模型的 JSON Schema 参数定义。
	Parameters map[string]any `json:"parameters"`
}

// Response 是一次模型请求完成后的最终结果。
type Response struct {
	// Content 是模型最终生成的文本内容。
	Content string
	// ToolCalls 是模型在本轮请求中发起的工具调用。
	ToolCalls []ToolCall
	// Usage 保存供应商返回的 token 用量。
	Usage Usage
	// Raw 保存原始流式 payload，方便排查供应商协议问题。
	Raw string
}

// Usage 是一次模型响应的 token 用量摘要。
type Usage struct {
	// InputTokens 是提示词、历史消息和工具 schema 等输入 token。
	InputTokens int `json:"input_tokens,omitempty"`
	// OutputTokens 是模型生成内容和工具调用参数等输出 token。
	OutputTokens int `json:"output_tokens,omitempty"`
	// TotalTokens 是供应商返回的总 token；未返回时可由输入和输出推导。
	TotalTokens int `json:"total_tokens,omitempty"`
	// CacheCreationInputTokens 是 Anthropic 等供应商报告的新写入缓存输入 token。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// CacheReadInputTokens 是 Anthropic 等供应商报告的缓存命中输入 token。
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
}

// IsZero 判断供应商是否没有返回用量信息。
func (u Usage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.TotalTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.CacheReadInputTokens == 0
}

// NormalizedTotal 返回可用于展示和上报的总 token。
func (u Usage) NormalizedTotal() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.InputTokens + u.OutputTokens
}

// ChatRequest 是 Runner 调用 LLM Client 时传入的数据。
type ChatRequest struct {
	// System 是固定系统提示词，会被客户端放到消息列表最前面。
	System string
	// Messages 是本轮发给模型的可见对话上下文。
	Messages []Message
	// Tools 是本轮暴露给模型的工具 schema 列表。
	Tools []ToolSchema
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
	// Type 表示事件类型：文本片段、最终响应或错误。
	Type EventType
	// Text 是流式文本片段，仅 EventText 使用。
	Text string
	// Response 是请求完成后的完整响应，仅 EventDone 使用。
	Response *Response
	// Err 是请求失败原因，仅 EventError 使用。
	Err error
}

// Client 是模型客户端接口。Runner 只依赖这个接口，不关心底层供应商。
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan Event, error)
}
