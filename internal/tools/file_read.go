package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

// FileRead 读取文本文件，并可按行号截取内容。
type FileRead struct {
	// workspaceTool 提供 file_read 的路径解析能力。
	workspaceTool
}

func NewFileRead(workspace string) *FileRead {
	return &FileRead{workspaceTool: newWorkspaceTool(workspace)}
}

func (t *FileRead) Name() string { return ToolNameFileRead }

// Schema 告诉模型 file_read 支持哪些参数。
func (t *FileRead) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read a text file. Use before modifying files.",
		Parameters: objectSchema(map[string]any{
			"path":         stringProp("Relative or absolute file path"),
			"start":        intProp("Start line number, 1-based", 1),
			"count":        intProp("Number of lines to read", 200),
			"show_linenos": boolProp("Show line numbers", true),
		}, "path"),
	}}
}

// Run 执行文件读取。读取失败会作为工具结果返回给模型，避免整个 Agent 直接中断。
func (t *FileRead) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	rawPath := asString(call.Args["path"])
	path := t.resolve(rawPath)
	start := asInt(call.Args["start"], 1)
	count := asInt(call.Args["count"], 200)
	showLineNos := asBool(call.Args["show_linenos"], true)
	if start < 1 {
		start = 1
	}
	if count <= 0 {
		count = 200
	}

	file, err := os.Open(path)
	if err != nil {
		if fallback, ok := t.resolveSOPReadFallback(rawPath); ok {
			path = fallback
			file, err = os.Open(path)
		}
		if err != nil {
			return agent.Outcome{Data: fmt.Sprintf("Error: %v", err), NextPrompt: "\n"}, nil
		}
	}
	defer file.Close()

	var b strings.Builder
	scanner := bufio.NewScanner(file)
	lineNo := 0
	written := 0
	for scanner.Scan() {
		// 支持 ctx 取消，后续接入超时或用户中断时可以及时退出。
		select {
		case <-ctx.Done():
			return agent.Outcome{}, ctx.Err()
		default:
		}
		lineNo++
		if lineNo < start {
			continue
		}
		if written >= count {
			break
		}
		if showLineNos {
			fmt.Fprintf(&b, "%d|%s\n", lineNo, scanner.Text())
		} else {
			b.WriteString(scanner.Text())
			b.WriteByte('\n')
		}
		written++
	}
	if err := scanner.Err(); err != nil {
		return agent.Outcome{}, err
	}
	nextPrompt := "\n"
	if looksLikeSOPPath(path) {
		nextPrompt = "\n[SYSTEM HINT] 你刚读取了 SOP。如果决定按它执行，请调用 update_working_checkpoint，把关键约束和 related_sop 存入工作记忆。\n"
	}
	return agent.Outcome{Data: b.String(), NextPrompt: nextPrompt}, nil
}

func looksLikeSOPPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	base := strings.ToLower(filepath.Base(clean))
	lower := strings.ToLower(clean)
	return strings.Contains(lower, "/sops/") || strings.Contains(base, "sop")
}
