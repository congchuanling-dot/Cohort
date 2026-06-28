package llm

import "context"

// ==========================================
// Client 接口 —— 上层（Action/Role）唯一依赖
// ==========================================

// Client 是 LLM 调用的统一接口。
// 所有 Provider（OpenAI、DeepSeek、Anthropic、Ollama、Custom）
// 都必须实现此接口。上层代码只 import 这个接口，不依赖任何具体 Provider。
type Client interface {
	// Chat 同步对话：发送消息列表，返回完整响应。
	Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error)

	// ChatStream 流式对话：返回一个 token 通道，实时接收生成内容。
	// 通道会在生成完成或出错时关闭。
	ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error)

	// CountTokens 估算消息列表的 token 数量。
	// 用于上下文窗口管理和成本预估。
	CountTokens(messages []ChatMessage) int

	// Name 返回此客户端的标识（Provider名称/模型名），用于日志和调试。
	Name() string
}

// ==========================================
// 框架内部统一类型 —— 所有 Provider 都翻译成这个格式
// ==========================================
// 注意：这些不是 OpenAI 的格式！是框架自己的抽象。
// 每个 Provider 负责把自己的 API 格式翻译成这个内部格式。

// ChatMessage 框架内部的对话消息格式。
// 与 OpenAI ChatMessage 类似但独立定义，
// 避免上层代码和任何具体 API 格式耦合。
type ChatMessage struct {
	Role    string `json:"role"`    // system / user / assistant
	Content string `json:"content"` // 消息正文
}

// ChatResponse 框架内部的 LLM 响应格式。
// 无论调用哪个 Provider，上层拿到的都是这个结构体。
type ChatResponse struct {
	Content      string      `json:"content"`                 // 模型回复正文
	FinishReason string      `json:"finish_reason"`           // stop / length / tool_calls
	Usage        *TokenUsage `json:"usage,omitempty"`         // token 使用统计
}

// TokenUsage token 使用统计。
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`     // 输入消耗
	CompletionTokens int `json:"completion_tokens"` // 输出消耗
	TotalTokens      int `json:"total_tokens"`      // 总计
}

// StreamChunk 流式输出的一个 token 片段。
// ChatStream 返回的通道中，每个元素都是这个结构体。
type StreamChunk struct {
	Content string `json:"content"` // 本次推送的文本片段（累积或增量）
	Done    bool   `json:"done"`    // true 表示流式输出结束
	Error   error  `json:"-"`       // 流式过程中的错误（仅在 Done=true 时可能有值）
}
