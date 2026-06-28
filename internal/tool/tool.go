// Package tool 提供 Agent 工具能力——不依赖 LLM 的基础操作。
//
// Tool 与 Action 的区别：
//   - Action 需要 LLM（AskLLM 是其核心方法）
//   - Tool 是纯函数，不调 LLM（ReadFile / WriteFile / RunCommand）
//
// 设计：
//   - 每个 Tool 实现 Tool 接口
//   - ToolRegistry 管理注册和调用
//   - 公有 Tool 注册在 Environment 级别（所有 Role 共享）
//   - 私有 Tool 注册在 Role 级别（只有该 Role 能用）
package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ==========================================
// Tool 接口
// ==========================================

// Tool 不依赖 LLM 的基础操作接口。
// 每个 Tool 必须有清晰的描述和参数说明——这些信息会注入到 LLM 的 system prompt 中，
// 让 LLM 知道有哪些工具可用，以及如何调用它们。
type Tool interface {
	// Name 返回工具名称（唯一标识），如 "ReadFile"、"WriteFile"、"GoFmt"。
	Name() string

	// Description 返回工具描述（给 LLM 看的）。
	// 描述应包含：工具做什么、何时使用、返回什么。
	Description() string

	// Parameters 返回参数说明，key=参数名, value=参数描述。
	Parameters() map[string]string

	// Run 执行工具。
	// args 是参数名→值的映射，返回值是给 LLM 看的执行结果文本。
	Run(ctx context.Context, args map[string]any) (string, error)
}

// ==========================================
// ToolRegistry 注册表
// ==========================================

// ToolRegistry 管理一组 Tool 的注册、查询和调用。
// 公有注册表挂在 Environment 上，私有注册表挂在 Role 上。
//
// 线程安全。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool // name → Tool
}

// NewRegistry 创建一个空的工具注册表。
func NewRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 注册一个工具。重名会 panic。
func (r *ToolRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool: %q already registered", t.Name()))
	}
	r.tools[t.Name()] = t
}

// RegisterAll 批量注册工具。
func (r *ToolRegistry) RegisterAll(tools ...Tool) {
	for _, t := range tools {
		r.Register(t)
	}
}

// Get 按名称获取工具。不存在返回 nil。
func (r *ToolRegistry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Call 按名称调用工具。不存在返回 error。
func (r *ToolRegistry) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	t := r.Get(name)
	if t == nil {
		return "", fmt.Errorf("tool %q not found", name)
	}
	return t.Run(ctx, args)
}

// List 返回所有已注册工具的名称列表（按字母排序）。
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Count 返回已注册工具的数量。
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Merge 将另一个注册表的所有工具合并到当前注册表。
// 用于把公有工具注入到 Role 的私有注册表中。
// 重名工具保留当前注册表的值（不覆盖）。
func (r *ToolRegistry) Merge(other *ToolRegistry) {
	if other == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, t := range other.tools {
		if _, exists := r.tools[name]; !exists {
			r.tools[name] = t
		}
	}
}

// ==========================================
// System Prompt 生成
// ==========================================

// ToolsInfo 生成给 LLM 看的工具列表文本。
// 可直接拼接到 system prompt 中，让 LLM 知道可用工具及用法。
//
// 输出格式：
//
//	## Available Tools
//	1. ReadFile: 读取文件内容
//	   Parameters: path (文件路径)
//	2. WriteFile: 将内容写入文件
//	   Parameters: path (文件路径), content (文件内容)
func (r *ToolRegistry) ToolsInfo() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.tools) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Available Tools\n")

	// 按名称排序保证输出稳定
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		t := r.tools[name]
		sb.WriteString(fmt.Sprintf("%d. **%s**: %s\n", i+1, t.Name(), t.Description()))

		params := t.Parameters()
		if len(params) > 0 {
			paramParts := make([]string, 0, len(params))
			for k, v := range params {
				paramParts = append(paramParts, fmt.Sprintf("%s (%s)", k, v))
			}
			sb.WriteString(fmt.Sprintf("   Parameters: %s\n", strings.Join(paramParts, ", ")))
		}
	}

	return sb.String()
}
