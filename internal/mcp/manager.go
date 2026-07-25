package mcp

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// RegisteredTool 将 MCP 发现结果与 Cohert 内部唯一工具名绑定。
type RegisteredTool struct {
	// Server 是提供该工具且已校验过的服务器配置。
	Server ServerConfig
	// Tool 是服务器通过 tools/list 上报的原始定义。
	Tool ToolDefinition
	// CohertID 是注册到本地工具表后的无冲突名称。
	CohertID string
}

// ServerStatus 是用于 CLI 诊断的无敏感信息摘要。
type ServerStatus struct {
	// Name 是配置中的服务器名。
	Name string
	// Transport 是实际使用的传输方式。
	Transport string
	// Available 表示握手和工具发现均成功。
	Available bool
	// Error 是不可用原因，不包含环境变量或请求正文。
	Error string
	// Tools 是成功挂载到 Cohert 的工具数量。
	Tools int
}

// Manager 持有已打开的 MCP 连接、发现的工具和每个服务器的状态。
type Manager struct {
	// mu 保护三个映射，使加载、枚举和调用可在不同 goroutine 中安全交错。
	mu sync.RWMutex
	// clients 按服务器名保存已完成握手的传输连接。
	clients map[string]Client
	// tools 按 CohertID 保存可供 Agent 调用的工具。
	tools map[string]RegisteredTool
	// statuses 保存成功与失败服务器的诊断结果。
	statuses map[string]ServerStatus
}

// NewManager 创建空管理器；服务器连接在 Load 阶段才建立。
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

// loadOne 校验、打开并发现单个服务器。
//
// 失败只记录状态，不返回给批量加载调用方，因为一个可选 MCP 服务器故障
// 不应阻止本地文件、命令或其他 MCP 工具启动。
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

// Tools 返回按 CohertID 排序的发现结果，使 CLI 与模型 schema 顺序稳定。
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

// Statuses 返回按服务器名排序的状态副本，供诊断而不暴露内部 map。
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

// CallTool 将本地 CohertID 映射回原始 MCP 名称，再转发给所属客户端。
// 读取注册信息时只持有读锁，网络调用期间不阻塞其他工具枚举。
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

// Close 关闭所有已打开客户端并清空客户端表。
// 即使某个关闭失败也会继续关闭其余连接，只返回遇到的第一个错误。
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

// setStatus 在锁保护下覆盖同名服务器的最新加载结果。
func (m *Manager) setStatus(status ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[status.Name] = status
}
