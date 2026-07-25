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

const protocolVersion = "2025-03-26"

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializedClient struct {
	request func(context.Context, string, any, any) error
	close   func() error
}

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

func (c *initializedClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	var result struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

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

func (c *initializedClient) Close() error {
	if c == nil || c.close == nil {
		return nil
	}
	return c.close()
}

type stdioClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	decoder *json.Decoder
	mu      sync.Mutex
	nextID  int
}

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

func writeRPC(writer io.Writer, request rpcRequest) error {
	content, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(content, '\n'))
	return err
}

type httpClient struct {
	url       string
	headers   map[string]string
	client    *http.Client
	sessionID string
	mu        sync.Mutex
	nextID    int
}

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

func decodeRPCResult(response rpcResponse, target any) error {
	if response.Error != nil {
		return fmt.Errorf("MCP RPC %d: %s", response.Error.Code, response.Error.Message)
	}
	if target == nil || len(response.Result) == 0 {
		return nil
	}
	return json.Unmarshal(response.Result, target)
}

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

func expandEnvironment(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	for key, value := range extra {
		env = append(env, key+"="+expandEnv(value))
	}
	return env
}

func expandEnv(value string) string {
	return os.ExpandEnv(value)
}
