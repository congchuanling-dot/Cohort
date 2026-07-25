package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// protocolVersion 是本客户端实现并声明给服务器的 MCP 协议版本。
const protocolVersion = "2025-03-26"

// rpcRequest 是所有 MCP 传输共用的 JSON-RPC 请求形状。
type rpcRequest struct {
	// JSONRPC 固定为 "2.0"。
	JSONRPC string `json:"jsonrpc"`
	// ID 关联请求与响应；通知请求不携带它。
	ID int `json:"id,omitempty"`
	// Method 是 MCP 方法名，例如 tools/list。
	Method string `json:"method"`
	// Params 是与具体 Method 对应的动态参数对象。
	Params any `json:"params,omitempty"`
}

// rpcResponse 是传输层解析后等待进一步解码的 JSON-RPC 响应。
type rpcResponse struct {
	// JSONRPC 是服务端声明的协议版本。
	JSONRPC string `json:"jsonrpc"`
	// ID 兼容部分服务器把数字响应 ID 编成字符串的情况。
	ID any `json:"id"`
	// Result 保留原始 JSON，等调用方知道结果类型后再解码。
	Result json.RawMessage `json:"result"`
	// Error 非空表示服务器返回协议错误而非业务结果。
	Error *rpcError `json:"error"`
}

// rpcError 是 JSON-RPC error 对象中本客户端需要的最小字段集。
type rpcError struct {
	// Code 是协议或服务器定义的错误码。
	Code int `json:"code"`
	// Message 是服务器给出的错误说明。
	Message string `json:"message"`
}

// initializedClient 将具体传输适配为已经握手的 MCP Client。
// 用函数字段隔离 stdio/HTTP 的请求与关闭细节，公共方法只处理 MCP 语义。
type initializedClient struct {
	request func(context.Context, string, any, any) error
	close   func() error
}

// Open 校验配置、打开对应传输，并完成 MCP initialize 握手。
func Open(ctx context.Context, config ServerConfig) (Client, error) {
	validated, err := config.Validate()
	if err != nil {
		return nil, err
	}
	var client *initializedClient
	switch validated.Type {
	case TransportStdio:
		client, err = openStdio(ctx, validated)
	case TransportHTTP:
		client, err = openHTTP(ctx, validated)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", validated.Type)
	}
	if err != nil {
		return nil, err
	}
	if err := client.initialize(ctx); err != nil {
		_ = client.close()
		return nil, err
	}
	return client, nil
}

// initialize 发送协议握手及初始化完成通知。
// 后者是 notification，依规范不等待 JSON-RPC 响应。
func (c *initializedClient) initialize(ctx context.Context) error {
	var result map[string]any
	if err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "cohert",
			"version": "0.1.0",
		},
	}, &result); err != nil {
		return fmt.Errorf("initialize MCP server: %w", err)
	}
	// initialized is a notification in the protocol. Clients that do not need
	// it ignore this normal JSON-RPC notification.
	return c.request(ctx, "notifications/initialized", map[string]any{}, nil)
}

// ListTools 请求服务器列出可被 Cohert 注册的工具定义。
func (c *initializedClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	var result struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool 调用远端工具并将多种 MCP content block 规范化为安全的文本结果。
// 当前仅把 text 块传给 Agent，其他类型保留类型提示而不泄漏二进制或未知载荷。
func (c *initializedClient) CallTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
		return ToolResult{}, err
	}
	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		switch item.Type {
		case "", "text":
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		default:
			parts = append(parts, fmt.Sprintf("[%s MCP content omitted]", item.Type))
		}
	}
	return ToolResult{Text: strings.Join(parts, "\n"), IsError: result.IsError}, nil
}

// Close 关闭底层传输；空客户端用于错误清理路径时视为已关闭。
func (c *initializedClient) Close() error {
	if c == nil || c.close == nil {
		return nil
	}
	return c.close()
}

// stdioClient 管理单个 MCP 子进程及其串行 JSON-RPC 对话。
type stdioClient struct {
	// cmd 是正在运行的 MCP server 子进程。
	cmd *exec.Cmd
	// stdin 是向子进程写 JSON-RPC 行的通道。
	stdin io.WriteCloser
	// decoder 从子进程 stdout 持续解码响应。
	decoder *json.Decoder
	// mu 让一次请求完整占有 stdin/decoder，避免响应与请求 ID 交叉。
	mu sync.Mutex
	// nextID 递增生成当前连接内的请求 ID。
	nextID int
}

// openStdio 启动 MCP server 子进程，并包装其标准输入输出为初始化前客户端。
func openStdio(ctx context.Context, config ServerConfig) (*initializedClient, error) {
	cmd := exec.CommandContext(ctx, config.Command, config.Args...)
	cmd.Env = expandEnvironment(config.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	raw := &stdioClient{
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(bufio.NewReaderSize(stdout, 1<<20)),
	}
	return &initializedClient{
		request: raw.request,
		close: func() error {
			_ = stdin.Close()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil && stderr.Len() > 0 {
					return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
				}
				return err
			case <-time.After(time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-done
				return nil
			}
		},
	}, nil
}

// request 串行发送一条 stdio JSON-RPC 请求并等待同 ID 响应。
//
// decoder 循环会跳过通知或其他 ID 的消息，兼容服务器在响应间插入事件；
// 调用取消后不强杀共享子进程，后续请求仍可继续使用连接。
func (c *stdioClient) request(ctx context.Context, method string, params any, target any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if method == "notifications/initialized" {
		return writeRPC(c.stdin, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	}
	c.nextID++
	id := c.nextID
	if err := writeRPC(c.stdin, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	responseCh := make(chan error, 1)
	go func() {
		for {
			var response rpcResponse
			if err := c.decoder.Decode(&response); err != nil {
				responseCh <- err
				return
			}
			if response.ID == nil || rpcResponseID(response.ID) != id {
				continue
			}
			responseCh <- decodeRPCResult(response, target)
			return
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-responseCh:
		return err
	}
}

// writeRPC 将一条 JSON-RPC 消息编码为 JSON Lines，stdio MCP 以换行分隔消息。
func writeRPC(writer io.Writer, request rpcRequest) error {
	content, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(content, '\n'))
	return err
}

// httpClient 管理 HTTP MCP 会话 ID、请求序号与额外认证头。
type httpClient struct {
	// url 是 MCP HTTP 服务端点。
	url string
	// headers 是配置提供的额外请求头。
	headers map[string]string
	// client 执行 HTTP 请求并施加传输级超时。
	client *http.Client
	// sessionID 是服务器在握手后返回、后续请求需要回传的会话标识。
	sessionID string
	// mu 串行化 nextID 和 sessionID 的读写。
	mu sync.Mutex
	// nextID 为需要响应的请求生成递增 ID。
	nextID int
}

// openHTTP 构造 HTTP 传输；握手由 Open 中的 initialize 统一完成。
func openHTTP(_ context.Context, config ServerConfig) (*initializedClient, error) {
	timeout := 30 * time.Second
	raw := &httpClient{
		url:     config.URL,
		headers: config.Headers,
		client:  &http.Client{Timeout: timeout},
	}
	return &initializedClient{
		request: raw.request,
		close:   func() error { return nil },
	}, nil
}

// request 以 HTTP POST 发送 JSON-RPC，并兼容 JSON 与 text/event-stream 响应。
func (c *httpClient) request(ctx context.Context, method string, params any, target any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	request := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if method != "notifications/initialized" {
		c.nextID++
		request.ID = c.nextID
	}
	content, err := json.Marshal(request)
	if err != nil {
		return err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(content))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	httpRequest.Header.Set("MCP-Protocol-Version", protocolVersion)
	for key, value := range c.headers {
		httpRequest.Header.Set(key, expandEnv(value))
	}
	if c.sessionID != "" {
		httpRequest.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		c.sessionID = sessionID
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("MCP HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if method == "notifications/initialized" {
		return nil
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return decodeSSEHTTPResponse(response.Body, target)
	}
	var rpc rpcResponse
	if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil {
		return err
	}
	return decodeRPCResult(rpc, target)
}

// decodeSSEHTTPResponse 从 Streamable HTTP 响应中读取第一条 JSON-RPC data 事件。
func decodeSSEHTTPResponse(reader io.Reader, target any) error {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var response rpcResponse
		if err := json.Unmarshal([]byte(content), &response); err != nil {
			return err
		}
		return decodeRPCResult(response, target)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("MCP HTTP event stream ended without a response")
}

// decodeRPCResult 将协议错误转换为 Go error，或把成功 result 解码到目标结构。
func decodeRPCResult(response rpcResponse, target any) error {
	if response.Error != nil {
		return fmt.Errorf("MCP RPC %d: %s", response.Error.Code, response.Error.Message)
	}
	if target == nil || len(response.Result) == 0 {
		return nil
	}
	return json.Unmarshal(response.Result, target)
}

// rpcResponseID 将兼容服务器使用的数字或字符串 ID 归一化为 int。
func rpcResponseID(value any) int {
	switch id := value.(type) {
	case float64:
		return int(id)
	case string:
		parsed, _ := strconv.Atoi(id)
		return parsed
	default:
		return 0
	}
}

// expandEnvironment 复制当前环境后叠加配置变量，避免修改父进程环境。
func expandEnvironment(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	for key, value := range extra {
		env = append(env, key+"="+expandEnv(value))
	}
	return env
}

// expandEnv 支持配置中使用 ${TOKEN} 等标准环境变量占位符。
func expandEnv(value string) string {
	return os.ExpandEnv(value)
}
