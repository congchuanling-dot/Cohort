package tools

import (
	"context"
	"fmt"
	"os"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

type FileWrite struct {
	workspaceTool
}

func NewFileWrite(workspace string) *FileWrite {
	return &FileWrite{workspaceTool: newWorkspaceTool(workspace)}
}

func (t *FileWrite) Name() string { return "file_write" }

func (t *FileWrite) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Create, overwrite, append, or prepend a text file.",
		Parameters: objectSchema(map[string]any{
			"path":    stringProp("Relative or absolute file path"),
			"content": stringProp("Content to write"),
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"overwrite", "append", "prepend"},
				"description": "Write mode",
				"default":     "overwrite",
			},
		}, "path", "content"),
	}}
}

func (t *FileWrite) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	_ = ctx
	path := t.resolve(asString(call.Args["path"]))
	content := asString(call.Args["content"])
	mode := asString(call.Args["mode"])
	if mode == "" {
		mode = "overwrite"
	}
	if err := ensureParent(path); err != nil {
		return agent.Outcome{}, err
	}

	switch mode {
	case "overwrite":
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return agent.Outcome{}, err
		}
	case "append":
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return agent.Outcome{}, err
		}
		_, err = f.WriteString(content)
		closeErr := f.Close()
		if err != nil {
			return agent.Outcome{}, err
		}
		if closeErr != nil {
			return agent.Outcome{}, closeErr
		}
	case "prepend":
		old, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append([]byte(content), old...), 0644); err != nil {
			return agent.Outcome{}, err
		}
	default:
		return agent.Outcome{}, fmt.Errorf("unsupported write mode %q", mode)
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":        "success",
			"path":          path,
			"written_bytes": len(content),
			"mode":          mode,
		},
		NextPrompt: "\n",
	}, nil
}
