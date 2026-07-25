package mcp

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// RegisteredTool combines a discovered MCP tool with its Cohert-safe name.
type RegisteredTool struct {
	Server   ServerConfig
	Tool     ToolDefinition
	CohertID string
}

// ServerStatus is intentionally small so CLI diagnostics can explain whether
// a configured server was loaded without exposing credentials.
type ServerStatus struct {
	Name      string
	Transport string
	Available bool
	Error     string
	Tools     int
}

// Manager owns opened MCP clients and their discovered tools.
type Manager struct {
	mu       sync.RWMutex
	clients  map[string]Client
	tools    map[string]RegisteredTool
	statuses map[string]ServerStatus
}

func NewManager() *Manager {
	return &Manager{
		clients:  map[string]Client{},
		tools:    map[string]RegisteredTool{},
		statuses: map[string]ServerStatus{},
	}
}

// Load opens every configured server. A broken optional server is recorded in
// Statuses but does not prevent Cohert's local tools from starting.
func (m *Manager) Load(ctx context.Context, configs []ServerConfig) {
	for _, config := range configs {
		m.loadOne(ctx, config)
	}
}

func (m *Manager) loadOne(ctx context.Context, config ServerConfig) {
	validated, err := config.Validate()
	if err != nil {
		m.setStatus(ServerStatus{Name: config.Name, Transport: config.Type, Error: err.Error()})
		return
	}
	client, err := Open(ctx, validated)
	if err != nil {
		m.setStatus(ServerStatus{Name: validated.Name, Transport: validated.Type, Error: err.Error()})
		return
	}
	discovered, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		m.setStatus(ServerStatus{Name: validated.Name, Transport: validated.Type, Error: err.Error()})
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.clients[validated.Name]; ok {
		_ = existing.Close()
	}
	count := 0
	for _, tool := range discovered {
		if tool.Name == "" {
			continue
		}
		cohertID := ToolName(validated.Name, tool.Name)
		if _, conflict := m.tools[cohertID]; conflict {
			continue
		}
		m.tools[cohertID] = RegisteredTool{
			Server:   validated,
			Tool:     tool,
			CohertID: cohertID,
		}
		count++
	}
	m.clients[validated.Name] = client
	m.statuses[validated.Name] = ServerStatus{
		Name:      validated.Name,
		Transport: validated.Type,
		Available: true,
		Tools:     count,
	}
}

func (m *Manager) Tools() []RegisteredTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]RegisteredTool, 0, len(names))
	for _, name := range names {
		result = append(result, m.tools[name])
	}
	return result
}

func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.statuses))
	for name := range m.statuses {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ServerStatus, 0, len(names))
	for _, name := range names {
		result = append(result, m.statuses[name])
	}
	return result
}

func (m *Manager) CallTool(ctx context.Context, cohertID string, args map[string]any) (ToolResult, RegisteredTool, error) {
	m.mu.RLock()
	registered, ok := m.tools[cohertID]
	if !ok {
		m.mu.RUnlock()
		return ToolResult{}, RegisteredTool{}, fmt.Errorf("unknown MCP tool %q", cohertID)
	}
	client := m.clients[registered.Server.Name]
	m.mu.RUnlock()
	if client == nil {
		return ToolResult{}, RegisteredTool{}, fmt.Errorf("MCP server %q is unavailable", registered.Server.Name)
	}
	result, err := client.CallTool(ctx, registered.Tool.Name, args)
	return result, registered, err
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for name, client := range m.clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close MCP server %s: %w", name, err)
		}
	}
	m.clients = map[string]Client{}
	return firstErr
}

func (m *Manager) setStatus(status ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[status.Name] = status
}
