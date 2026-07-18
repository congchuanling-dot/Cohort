package tools

import (
	"context"
	"fmt"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

type Tool interface {
	Name() string
	Schema() llm.ToolSchema
	Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *Registry) Schemas() []llm.ToolSchema {
	schemas := make([]llm.ToolSchema, 0, len(r.tools))
	order := []string{"file_read", "file_write", "file_patch", "code_run", "ask_user"}
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

func (r *Registry) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tool, ok := r.tools[call.Name]
	if !ok {
		return agent.Outcome{
			Data:       map[string]any{"status": "error", "msg": "unknown tool: " + call.Name},
			NextPrompt: "未知工具 " + call.Name,
		}, fmt.Errorf("unknown tool %q", call.Name)
	}
	return tool.Run(ctx, call)
}

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

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "default": def}
}

func boolProp(desc string, def bool) map[string]any {
	return map[string]any{"type": "boolean", "description": desc, "default": def}
}
