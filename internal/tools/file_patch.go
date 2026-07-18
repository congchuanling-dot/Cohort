package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

// FilePatch 用精确文本块替换文件内容，适合小范围修改。
type FilePatch struct {
	workspaceTool
}

func NewFilePatch(workspace string) *FilePatch {
	return &FilePatch{workspaceTool: newWorkspaceTool(workspace)}
}

func (t *FilePatch) Name() string { return "file_patch" }

// Schema 要求模型提供 path、old_content、new_content。
func (t *FilePatch) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Replace a unique old_content block with new_content. Exact match required.",
		Parameters: objectSchema(map[string]any{
			"path":        stringProp("Relative or absolute file path"),
			"old_content": stringProp("Original text block, must be unique"),
			"new_content": stringProp("Replacement text"),
		}, "path", "old_content", "new_content"),
	}}
}

// Run 只允许 old_content 唯一匹配时写入，避免误改多个位置。
func (t *FilePatch) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	_ = ctx
	path := t.resolve(asString(call.Args["path"]))
	oldContent := asString(call.Args["old_content"])
	newContent := asString(call.Args["new_content"])
	if oldContent == "" {
		return agent.Outcome{}, fmt.Errorf("old_content is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.Outcome{}, err
	}
	text := string(data)
	count := strings.Count(text, oldContent)
	// 找不到或匹配多次都返回给模型，让模型重新读取文件后再修正 patch。
	if count == 0 {
		return agent.Outcome{
			Data:       map[string]any{"status": "error", "msg": "old_content not found"},
			NextPrompt: "\n",
		}, nil
	}
	if count > 1 {
		return agent.Outcome{
			Data:       map[string]any{"status": "error", "msg": fmt.Sprintf("old_content matched %d times", count)},
			NextPrompt: "\n",
		}, nil
	}
	updated := strings.Replace(text, oldContent, newContent, 1)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return agent.Outcome{}, err
	}
	return agent.Outcome{
		Data:       map[string]any{"status": "success", "path": path},
		NextPrompt: "\n",
	}, nil
}
