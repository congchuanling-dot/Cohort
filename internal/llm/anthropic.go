package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	defaultAnthropicAPIBase = "https://api.anthropic.com"
	defaultAnthropicVersion = "2023-06-01"
	defaultAnthropicTokens  = 4096
)

// AnthropicConfig 是 Anthropic Messages API 的连接配置。
type AnthropicConfig struct {
	// Name 是本配置的可读名称，主要用于展示和排查。
	Name string
	// APIKey 是访问 Anthropic 服务的鉴权密钥。
	APIKey string
	// APIBase 是 Anthropic API 基础地址，可以为空、/v1 或完整 /v1/messages。
	APIBase string
	// Model 是本次请求使用的模型名称。
	Model string
	// Stream 控制是否使用 SSE 流式响应。
	Stream bool
	// ConnectTimeout 是连接建立阶段的超时预算。
	ConnectTimeout time.Duration
	// ReadTimeout 是读取响应阶段的超时预算。
	ReadTimeout time.Duration
	// MaxRetries 是遇到可重试错误时的最大重试次数。
	MaxRetries int
}

// AnthropicClient 实现 Client 接口，负责调用 /v1/messages。
type AnthropicClient struct {
	cfg    AnthropicConfig
	client *http.Client
}

// NewAnthropicClient 创建 Anthropic Messages API 客户端。
func NewAnthropicClient(cfg AnthropicConfig) *AnthropicClient {
	timeout := cfg.ConnectTimeout + cfg.ReadTimeout
	if timeout <= 0 {
		timeout = 130 * time.Second
	}
	return &AnthropicClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Chat 启动一次模型请求，并用 channel 向 Runner 返回流式事件。
func (c *AnthropicClient) Chat(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		var lastErr error
		for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					out <- Event{Type: EventError, Err: ctx.Err()}
					return
				case <-time.After(time.Duration(attempt) * 600 * time.Millisecond):
				}
			}
			resp, err := c.doChat(ctx, req, out)
			if err == nil {
				out <- Event{Type: EventDone, Response: resp}
				return
			}
			lastErr = err
			if !isRetryable(err) {
				break
			}
		}
		out <- Event{Type: EventError, Err: lastErr}
	}()
	return out, nil
}

func (c *AnthropicClient) doChat(ctx context.Context, req ChatRequest, out chan<- Event) (*Response, error) {
	system, messages, err := buildAnthropicMessages(req.System, req.Messages)
	if err != nil {
		return nil, err
	}
	body := anthropicRequest{
		Model:     c.cfg.Model,
		System:    system,
		Messages:  messages,
		MaxTokens: defaultAnthropicTokens,
		Stream:    c.cfg.Stream,
		Tools:     buildAnthropicTools(req.Tools),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL(c.cfg.APIBase), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", defaultAnthropicVersion)

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, httpStatusError{Code: httpResp.StatusCode, Body: string(data)}
	}

	if !c.cfg.Stream {
		return parseAnthropicJSON(httpResp.Body)
	}
	return parseAnthropicSSE(httpResp.Body, out)
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

func buildAnthropicMessages(system string, messages []Message) (string, []anthropicMessage, error) {
	systemParts := []string{}
	if strings.TrimSpace(system) != "" {
		systemParts = append(systemParts, strings.TrimSpace(system))
	}
	result := make([]anthropicMessage, 0, len(messages))
	pendingToolResults := []anthropicContentBlock{}
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		result = append(result, anthropicMessage{Role: RoleUser, Content: pendingToolResults})
		pendingToolResults = nil
	}

	for _, message := range messages {
		switch message.Role {
		case RoleSystem:
			if strings.TrimSpace(message.Content) != "" {
				systemParts = append(systemParts, strings.TrimSpace(message.Content))
			}
		case RoleTool:
			pendingToolResults = append(pendingToolResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Content:   message.Content,
			})
		case RoleAssistant:
			flushToolResults()
			blocks := make([]anthropicContentBlock, 0, 1+len(message.ToolCalls))
			if message.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				input, err := parseAnthropicToolInput(call.Function.Arguments)
				if err != nil {
					return "", nil, fmt.Errorf("convert tool call %q arguments for Anthropic: %w", call.Function.Name, err)
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Function.Name,
					Input: input,
				})
			}
			if len(blocks) > 0 {
				result = append(result, anthropicMessage{Role: RoleAssistant, Content: blocks})
			}
		default:
			flushToolResults()
			if message.Content != "" {
				result = append(result, anthropicMessage{
					Role:    RoleUser,
					Content: []anthropicContentBlock{{Type: "text", Text: message.Content}},
				})
			}
		}
	}
	flushToolResults()
	return strings.Join(systemParts, "\n\n"), result, nil
}

func parseAnthropicToolInput(arguments string) (map[string]any, error) {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return map[string]any{}, nil
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return nil, err
	}
	if input == nil {
		input = map[string]any{}
	}
	return input, nil
}

func buildAnthropicTools(tools []ToolSchema) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return result
}

func anthropicMessagesURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = defaultAnthropicAPIBase
	}
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
}

func parseAnthropicJSON(r io.Reader) (*Response, error) {
	var data anthropicResponse
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, err
	}
	var content strings.Builder
	calls := []ToolCall{}
	for _, block := range data.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			args := strings.TrimSpace(string(block.Input))
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: ToolFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}
	return &Response{Content: content.String(), ToolCalls: normalizeToolCalls(calls), Usage: data.Usage.toUsage()}, nil
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	ContentBlock *struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

type anthropicStreamBlock struct {
	Type  string
	ID    string
	Name  string
	Text  strings.Builder
	Input strings.Builder
}

func parseAnthropicSSE(r io.Reader, out chan<- Event) (*Response, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 1024*1024*8)

	blocks := map[int]*anthropicStreamBlock{}
	var usage Usage
	var raw strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		raw.WriteString(payload)
		raw.WriteByte('\n')

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			if anthropicHasProgress(blocks) {
				return nil, markModelProgress(err)
			}
			return nil, err
		}
		if event.Message != nil {
			usage.merge(event.Message.Usage.toUsage())
		}
		usage.merge(event.Usage.toUsage())
		if event.Type == "error" {
			errType := "unknown"
			errMessage := payload
			if event.Error != nil {
				errType = event.Error.Type
				errMessage = event.Error.Message
			}
			err := fmt.Errorf("anthropic error: %s: %s", errType, errMessage)
			if anthropicHasProgress(blocks) {
				return nil, markModelProgress(err)
			}
			return nil, err
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock == nil {
				continue
			}
			block := &anthropicStreamBlock{
				Type: event.ContentBlock.Type,
				ID:   event.ContentBlock.ID,
				Name: event.ContentBlock.Name,
			}
			if event.ContentBlock.Text != "" {
				block.Text.WriteString(event.ContentBlock.Text)
				out <- Event{Type: EventText, Text: event.ContentBlock.Text}
			}
			if len(event.ContentBlock.Input) > 0 && string(event.ContentBlock.Input) != "{}" {
				block.Input.Write(event.ContentBlock.Input)
			}
			blocks[event.Index] = block
		case "content_block_delta":
			if event.Delta == nil {
				continue
			}
			block := blocks[event.Index]
			if block == nil {
				block = &anthropicStreamBlock{}
				blocks[event.Index] = block
			}
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					block.Type = "text"
					block.Text.WriteString(event.Delta.Text)
					out <- Event{Type: EventText, Text: event.Delta.Text}
				}
			case "input_json_delta":
				if event.Delta.PartialJSON != "" {
					if block.Type == "" {
						block.Type = "tool_use"
					}
					block.Input.WriteString(event.Delta.PartialJSON)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if anthropicHasProgress(blocks) {
			return nil, markModelProgress(err)
		}
		return nil, err
	}

	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	var content strings.Builder
	calls := []ToolCall{}
	for _, index := range indexes {
		block := blocks[index]
		switch block.Type {
		case "text":
			content.WriteString(block.Text.String())
		case "tool_use":
			args := strings.TrimSpace(block.Input.String())
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: ToolFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}
	return &Response{Content: content.String(), ToolCalls: normalizeToolCalls(calls), Usage: usage, Raw: raw.String()}, nil
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u anthropicUsage) toUsage() Usage {
	usage := Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func (u *Usage) merge(next Usage) {
	if next.InputTokens != 0 {
		u.InputTokens = next.InputTokens
	}
	if next.OutputTokens != 0 {
		u.OutputTokens = next.OutputTokens
	}
	if next.TotalTokens != 0 {
		u.TotalTokens = next.TotalTokens
	}
	if next.CacheCreationInputTokens != 0 {
		u.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	if next.CacheReadInputTokens != 0 {
		u.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
}

func anthropicHasProgress(blocks map[int]*anthropicStreamBlock) bool {
	for _, block := range blocks {
		if block == nil {
			continue
		}
		if block.Text.Len() > 0 || block.Input.Len() > 0 || block.Type == "tool_use" {
			return true
		}
	}
	return false
}
