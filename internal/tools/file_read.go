package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

type FileRead struct {
	workspaceTool
}

func NewFileRead(workspace string) *FileRead {
	return &FileRead{workspaceTool: newWorkspaceTool(workspace)}
}

func (t *FileRead) Name() string { return "file_read" }

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

func (t *FileRead) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	path := t.resolve(asString(call.Args["path"]))
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
		return agent.Outcome{Data: fmt.Sprintf("Error: %v", err), NextPrompt: "\n"}, nil
	}
	defer file.Close()

	var b strings.Builder
	scanner := bufio.NewScanner(file)
	lineNo := 0
	written := 0
	for scanner.Scan() {
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
	return agent.Outcome{Data: b.String(), NextPrompt: "\n"}, nil
}
