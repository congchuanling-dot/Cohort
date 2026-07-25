// Package mcp implements the subset of the Model Context Protocol that
// Cohert needs to discover and invoke externally provided tools.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ServerConfig describes one MCP server entry. It deliberately follows the
// Claude Code .mcp.json shape so users can reuse existing configuration.
type ServerConfig struct {
	Name    string            `json:"-"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Config is the JSON document stored in .mcp.json and user/local equivalents.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// ToolDefinition is a tool returned by MCP tools/list.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolResult is the normalized part of an MCP tools/call result Cohert exposes
// to the Agent. Raw protocol fields intentionally stay inside this package.
type ToolResult struct {
	Text    string
	IsError bool
}

// Client is one initialized MCP transport connection.
type Client interface {
	ListTools(ctx context.Context) ([]ToolDefinition, error)
	CallTool(ctx context.Context, name string, args map[string]any) (ToolResult, error)
	Close() error
}

// Validate normalizes defaults and rejects malformed server configuration.
func (c ServerConfig) Validate() (ServerConfig, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || !validName.MatchString(c.Name) {
		return ServerConfig{}, fmt.Errorf("invalid MCP server name %q", c.Name)
	}
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
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

// ToolName converts a server/tool pair to Cohert's snake_case tool namespace.
func ToolName(server, tool string) string {
	return "mcp_" + normalizeName(server) + "_" + normalizeName(tool)
}

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
