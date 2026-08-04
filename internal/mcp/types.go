// Package mcp 实现 Cohort 发现和调用外部工具所需的 Model Context Protocol 子集。
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

const (
	// TransportStdio 表示 Cohort 通过子进程标准输入输出与服务器通信。
	TransportStdio = "stdio"
	// TransportHTTP 表示 Cohort 通过 Streamable HTTP 或 JSON-RPC HTTP 与服务器通信。
	TransportHTTP = "http"
	// TransportSSE 是旧配置里常见的 SSE 传输名；当前实现按 HTTP/SSE 响应兼容处理。
	TransportSSE = "sse"
)

// validName 限制服务器名称能安全用作配置键和 Cohort 工具命名空间的一部分。
var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ServerConfig 描述一个 MCP 服务器配置项。
//
// 字段形状刻意兼容 Claude Code 的 .mcp.json，使已有配置可直接复用。
type ServerConfig struct {
	// Name 是配置映射中的服务器名，序列化时由 map 键承载。
	Name string `json:"-"`
	// Type 是传输方式，支持 stdio 和 http；缺省时依据 URL 推断。
	Type string `json:"type,omitempty"`
	// Command 是 stdio 服务器的可执行命令。
	Command string `json:"command,omitempty"`
	// Args 是传给 Command 的顺序参数。
	Args []string `json:"args,omitempty"`
	// Env 是启动 stdio 子进程时额外覆盖或补充的环境变量。
	Env map[string]string `json:"env,omitempty"`
	// URL 是 HTTP MCP 服务端点。
	URL string `json:"url,omitempty"`
	// Headers 是 HTTP 请求的额外头，可使用 ${VAR} 引用环境变量。
	Headers map[string]string `json:"headers,omitempty"`
}

// Config 是存储于 .mcp.json、用户级或本地级文件中的 JSON 文档。
type Config struct {
	// Servers 以服务器名为键，便于按 scope 合并和覆盖。
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// ToolDefinition 是 MCP tools/list 返回的单个工具描述。
type ToolDefinition struct {
	// Name 是 MCP 服务器内部使用的工具名。
	Name string `json:"name"`
	// Description 是给模型和诊断命令展示的工具用途说明。
	Description string `json:"description,omitempty"`
	// InputSchema 是 JSON Schema 形式的输入参数定义。
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolResult 是 Cohort 暴露给 Agent 的 MCP tools/call 规范化结果。
// 原始协议字段刻意留在本包，避免外部工具格式泄漏到 Agent 主循环。
type ToolResult struct {
	// Text 汇总 MCP 文本内容块；非文本内容会被安全省略。
	Text string
	// IsError 是服务器声明的工具级错误状态。
	IsError bool
}

// Client 是已经完成 initialize 握手的一条 MCP 传输连接。
type Client interface {
	ListTools(ctx context.Context) ([]ToolDefinition, error)
	CallTool(ctx context.Context, name string, args map[string]any) (ToolResult, error)
	Close() error
}

// Validate 规范化默认值并拒绝缺少传输所需字段的服务器配置。
func (c ServerConfig) Validate() (ServerConfig, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || !validName.MatchString(c.Name) {
		return ServerConfig{}, fmt.Errorf("invalid MCP server name %q", c.Name)
	}
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	if c.Type == TransportSSE {
		c.Type = TransportHTTP
	}
	if c.Type == "" {
		if strings.TrimSpace(c.URL) != "" {
			c.Type = TransportHTTP
		} else {
			c.Type = TransportStdio
		}
	}
	switch c.Type {
	case TransportStdio:
		if strings.TrimSpace(c.Command) == "" {
			return ServerConfig{}, fmt.Errorf("MCP stdio server %q requires command", c.Name)
		}
	case TransportHTTP:
		if strings.TrimSpace(c.URL) == "" {
			return ServerConfig{}, fmt.Errorf("MCP HTTP server %q requires url", c.Name)
		}
	default:
		return ServerConfig{}, fmt.Errorf("MCP server %q has unsupported transport %q", c.Name, c.Type)
	}
	return c, nil
}

// ToolName 将服务器名和 MCP 工具名转换为 Cohort 的 snake_case 命名空间。
func ToolName(server, tool string) string {
	return "mcp_" + normalizeName(server) + "_" + normalizeName(tool)
}

// normalizeName 将协议允许的多种分隔符统一为单个下划线。
func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	underscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
