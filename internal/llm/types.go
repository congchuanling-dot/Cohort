package llm

import "context"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type ToolSchema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Raw       string
}

type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []ToolSchema
}

type EventType string

const (
	EventText  EventType = "text"
	EventDone  EventType = "done"
	EventError EventType = "error"
)

type Event struct {
	Type     EventType
	Text     string
	Response *Response
	Err      error
}

type Client interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan Event, error)
}
