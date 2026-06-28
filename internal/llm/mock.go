package llm

import (
	"context"
	"fmt"
)

// ==========================================
// Mock Client —— 测试用，不需要真实 API Key
// ==========================================

// MockClient 实现 Client 接口的内存测试桩。
//
// 典型用法：
//
//	mock := llm.NewMockClient()
//	mock.SetResponse("Hello, world!")          // 预设 Chat 返回内容
//	mock.SetStreamChunks([]string{"He", "llo"}) // 预设流式 token
//	mock.SetError(fmt.Errorf("simulated"))      // 预设错误
//	mock.SetTokenCount(42)                      // 预设 token 计数
type MockClient struct {
	name         string
	response     string
	finishReason string
	usage        *TokenUsage
	streamChunks []string
	err          error
	tokenCount   int
}

// NewMockClient 创建一个 Mock 客户端。
func NewMockClient() *MockClient {
	return &MockClient{
		name:         "mock/gpt-mock",
		finishReason: "stop",
		usage: &TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

// SetResponse 预设 Chat 的返回内容。
func (m *MockClient) SetResponse(content string) *MockClient {
	m.response = content
	return m
}

// SetFinishReason 预设 Chat 的 finish_reason。
func (m *MockClient) SetFinishReason(reason string) *MockClient {
	m.finishReason = reason
	return m
}

// SetUsage 预设 token 用量统计。
func (m *MockClient) SetUsage(u *TokenUsage) *MockClient {
	m.usage = u
	return m
}

// SetStreamChunks 预设流式输出的 token 序列。
func (m *MockClient) SetStreamChunks(chunks []string) *MockClient {
	m.streamChunks = chunks
	return m
}

// SetError 预设错误（Chat 和 ChatStream 都会返回此错误）。
func (m *MockClient) SetError(err error) *MockClient {
	m.err = err
	return m
}

// SetTokenCount 预设 CountTokens 的返回值。
func (m *MockClient) SetTokenCount(count int) *MockClient {
	m.tokenCount = count
	return m
}

// SetName 预设 Name() 的返回值。
func (m *MockClient) SetName(name string) *MockClient {
	m.name = name
	return m
}

// ==========================================
// Client 接口实现
// ==========================================

// Chat 返回预设的响应或错误。
func (m *MockClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ChatResponse{
		Content:      m.response,
		FinishReason: m.finishReason,
		Usage:        m.usage,
	}, nil
}

// ChatStream 将预设的 streamChunks 逐条推入 channel。
func (m *MockClient) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}

	ch := make(chan *StreamChunk, len(m.streamChunks)+1)

	go func() {
		defer close(ch)
		for i, content := range m.streamChunks {
			select {
			case <-ctx.Done():
				ch <- &StreamChunk{Done: true, Error: ctx.Err()}
				return
			case ch <- &StreamChunk{
				Content: content,
				Done:    i == len(m.streamChunks)-1,
			}:
			}
		}
		// 如果没有预设 streamChunks，直接发 Done
		if len(m.streamChunks) == 0 {
			ch <- &StreamChunk{Done: true}
		}
	}()

	return ch, nil
}

// CountTokens 返回预设的 token 数，如果未设置则用 estimateTokens 估算。
func (m *MockClient) CountTokens(messages []ChatMessage) int {
	if m.tokenCount > 0 {
		return m.tokenCount
	}
	return estimateTokens(messages)
}

// Name 返回客户端标识。
func (m *MockClient) Name() string {
	return m.name
}

// ==========================================
// 便捷构造函数（常见测试场景）
// ==========================================

// NewEchoClient 创建一个回显客户端：Chat 返回最后一条用户消息的内容。
// 用于测试 Action 的 prompt 构建逻辑。
func NewEchoClient() *MockClient {
	m := NewMockClient()
	m.name = "mock/echo"
	// response 在 Chat 中动态设置太复杂，这里留空让调用方自行 SetResponse
	// 如果需要动态 echo，使用 EchoResponder 包装
	return m
}

// EchoResponder 实现 Client 接口，Chat 返回最后一条 user 消息的内容。
// 适合验证 Action 是否正确构建了 prompt。
type EchoResponder struct {
	name string
}

// NewEchoResponder 创建回显响应器。
func NewEchoResponder() *EchoResponder {
	return &EchoResponder{name: "echo"}
}

func (e *EchoResponder) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	content := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			content = messages[i].Content
			break
		}
	}
	return &ChatResponse{
		Content:      fmt.Sprintf("echo: %s", content),
		FinishReason: "stop",
		Usage:        &TokenUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func (e *EchoResponder) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan *StreamChunk, error) {
	resp, err := e.Chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	ch := make(chan *StreamChunk, 1)
	ch <- &StreamChunk{Content: resp.Content, Done: true}
	close(ch)
	return ch, nil
}

func (e *EchoResponder) CountTokens(messages []ChatMessage) int {
	return estimateTokens(messages)
}

func (e *EchoResponder) Name() string {
	return e.name
}
