package explorer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

const explorerSearchTool = "explorer_search"

var explorerAllowedTools = map[string]bool{
	"file_read":       true,
	"lsp_diagnostics": true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_hover":       true,
	"lsp_symbols":     true,
}

// ReadOnlyToolRunner 把普通 Runner 收敛成 Explorer 的只读工具面。
//
// Explorer 不暴露通用 code_run。代码搜索通过结构化 explorer_search 直接执行 rg，
// 参数不经过 Shell，避免重定向、命令替换和 rg --pre 等逃逸路径。
type ReadOnlyToolRunner struct {
	Base      agent.ToolRunner
	Workspace string
}

func NewReadOnlyToolRunner(base agent.ToolRunner, workspace string) ReadOnlyToolRunner {
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	return ReadOnlyToolRunner{Base: base, Workspace: filepath.Clean(workspace)}
}

func (r ReadOnlyToolRunner) Schemas() []llm.ToolSchema {
	var schemas []llm.ToolSchema
	if r.Base != nil {
		for _, schema := range r.Base.Schemas() {
			if explorerAllowedTools[schema.Function.Name] {
				schemas = append(schemas, schema)
			}
		}
	}
	return append(schemas, llm.ToolSchema{
		Type: "function",
		Function: llm.FunctionSchema{
			Name:        explorerSearchTool,
			Description: "Search text in the Explorer workspace with ripgrep. This tool is read-only and does not invoke a shell.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     map[string]any{"type": "string", "description": "Literal or regular-expression search pattern"},
					"path":        map[string]any{"type": "string", "description": "Relative file or directory path; defaults to workspace root"},
					"glob":        map[string]any{"type": "string", "description": "Optional ripgrep glob such as *.go"},
					"max_results": map[string]any{"type": "integer", "description": "Maximum returned matching lines; default 200, max 1000"},
				},
				"required": []string{"pattern"},
			},
		},
	})
}

func (r ReadOnlyToolRunner) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if call.Name == explorerSearchTool {
		return r.runSearch(ctx, call.Args), nil
	}
	if r.Base == nil {
		return agent.Outcome{}, fmt.Errorf("explorer tool runner has no base runner")
	}
	if !explorerAllowedTools[call.Name] {
		return explorerDenied(call.Name, "tool is not available in read-only Explorer mode"), nil
	}
	return r.Base.Run(ctx, call)
}

func (r ReadOnlyToolRunner) runSearch(ctx context.Context, args map[string]any) agent.Outcome {
	pattern, _ := args["pattern"].(string)
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return explorerDenied(explorerSearchTool, "pattern is required")
	}
	relative, _ := args["path"].(string)
	target, err := resolveExplorerPath(r.Workspace, relative)
	if err != nil {
		return explorerDenied(explorerSearchTool, err.Error())
	}
	maxResults := explorerIntArg(args["max_results"], 200)
	if maxResults < 1 {
		maxResults = 200
	}
	if maxResults > 1000 {
		maxResults = 1000
	}
	argv := []string{"-n", "--no-heading", "--color", "never", "--max-filesize", "2M"}
	if glob, _ := args["glob"].(string); strings.TrimSpace(glob) != "" {
		argv = append(argv, "--glob", strings.TrimSpace(glob))
	}
	argv = append(argv, "--", pattern, target)
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "rg", argv...)
	cmd.Dir = r.Workspace
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	if runCtx.Err() != nil {
		return explorerDenied(explorerSearchTool, runCtx.Err().Error())
	}
	var exitErr *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1) {
		return explorerDenied(explorerSearchTool, strings.TrimSpace(output.String()))
	}
	lines := explorerNonEmptyLines(output.String())
	truncated := len(lines) > maxResults
	if truncated {
		lines = lines[:maxResults]
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":    agent.ToolStatusSuccess,
			"matches":   lines,
			"count":     len(lines),
			"truncated": truncated,
		},
		NextPrompt: "\n",
		Audit: map[string]any{
			"policy": "explorer:read_only",
			"tool":   explorerSearchTool,
			"status": agent.ToolStatusSuccess,
		},
	}
}

func resolveExplorerPath(workspace string, relative string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("explorer workspace is empty")
	}
	relative = strings.TrimSpace(relative)
	if relative == "" {
		relative = "."
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute paths are forbidden")
	}
	target := filepath.Clean(filepath.Join(workspace, relative))
	rel, err := filepath.Rel(workspace, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the Explorer workspace")
	}
	return target, nil
}

func explorerIntArg(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	}
	return fallback
}

func explorerNonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func explorerDenied(tool string, reason string) agent.Outcome {
	if strings.TrimSpace(reason) == "" {
		reason = "read-only Explorer operation failed"
	}
	return agent.Outcome{
		Data: agent.NewToolError(
			"explorer_read_only_policy",
			reason,
			"Use file_read, LSP tools, or explorer_search inside the current workspace.",
		),
		NextPrompt: "Explorer 是只读调查模式；请改用允许的读取、LSP 或结构化搜索操作。",
		Audit: map[string]any{
			"policy": "explorer:read_only",
			"tool":   tool,
			"status": agent.ToolStatusError,
		},
	}
}
