package tools

import (
	"context"
	"fmt"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

const (
	// ToolNameFileRead 读取文本文件。
	ToolNameFileRead = "file_read"
	// ToolNameFileWrite 创建或修改文本文件。
	ToolNameFileWrite = "file_write"
	// ToolNameFilePatch 替换文件中的唯一文本块。
	ToolNameFilePatch = "file_patch"
	// ToolNameCodeRun 在工作区执行 shell 命令。
	ToolNameCodeRun = "code_run"
	// ToolNameAskUser 在命令行向用户提问。
	ToolNameAskUser = "ask_user"
)

// Tool 是所有本地工具必须实现的接口。
// Runner 通过 Registry 调用工具，不直接依赖具体工具类型。
type Tool interface {
	Name() string
	Schema() llm.ToolSchema
	Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error)
}

// Registry 保存工具名到工具实例的映射，负责 schema 输出和工具分发。
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建空工具注册表。
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register 把一个工具注册到模型可调用列表中。
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Schemas 返回给模型看的工具定义。
// 固定顺序可以让输出更稳定，方便调试和测试。
func (r *Registry) Schemas() []llm.ToolSchema {
	schemas := make([]llm.ToolSchema, 0, len(r.tools))
	order := []string{ToolNameFileRead, ToolNameFileWrite, ToolNameFilePatch, ToolNameCodeRun, ToolNameAskUser}
	seen := map[string]bool{}
	for _, name := range order {
		if tool, ok := r.tools[name]; ok {
			schemas = append(schemas, tool.Schema())
			seen[name] = true
		}
	}
	for name, tool := range r.tools {
		if !seen[name] {
			schemas = append(schemas, tool.Schema())
		}
	}
	return schemas
}

// Run 根据模型返回的工具名找到具体工具并执行。
func (r *Registry) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tool, ok := r.tools[call.Name]
	if !ok {
		return agent.Outcome{
			Data: agent.NewToolError(
				"unknown_tool",
				"unknown tool: "+call.Name,
				"请改用当前可用工具之一："+ToolNameFileRead+"、"+ToolNameFileWrite+"、"+ToolNameFilePatch+"、"+ToolNameCodeRun+"、"+ToolNameAskUser,
			),
			NextPrompt: "未知工具 " + call.Name,
		}, fmt.Errorf("unknown tool %q", call.Name)
	}
	return tool.Run(ctx, call)
}

// objectSchema 生成工具参数的 JSON Schema object。
func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// stringProp/intProp/boolProp 是工具 schema 的小工具函数，减少重复 map 写法。
func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "default": def}
}

func boolProp(desc string, def bool) map[string]any {
	return map[string]any{"type": "boolean", "description": desc, "default": def}
}
