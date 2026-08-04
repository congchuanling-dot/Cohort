package tools

import (
	"context"
	"strings"

	"cohort/internal/agent"
	"cohort/internal/llm"
	"cohort/internal/lsp"
)

type LSPDiagnostics struct {
	workspace string
}

func NewLSPDiagnostics(workspace string) *LSPDiagnostics {
	return &LSPDiagnostics{workspace: workspace}
}

func (t *LSPDiagnostics) Name() string { return ToolNameLSPDiagnostics }

func (t *LSPDiagnostics) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Run read-only language diagnostics. Supports Go via gopls, TypeScript via tsc --noEmit, and Python via pyright. Use before claiming code is diagnostically clean when the relevant checker is available.",
		Parameters: objectSchema(map[string]any{
			"language": stringProp("Language to check: go (default), typescript, or python."),
			"targets": map[string]any{
				"type":        "array",
				"description": "Optional package patterns, files, or directories. Go defaults to ./..., Python defaults to ., TypeScript defaults to tsconfig.json when present.",
				"items":       stringProp("Package pattern, file path, or directory path"),
			},
		}),
	}}
}

func (t *LSPDiagnostics) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	language := lsp.NormalizeLanguage(asString(call.Args["language"]))
	if language == "" {
		language = lsp.LanguageGo
	}
	targets := asStringSlice(call.Args["targets"])
	result, err := (lsp.Diagnostics{Root: t.workspace}).Check(ctx, language, targets)
	data := map[string]any{
		"status":    agent.ToolStatusSuccess,
		"language":  result.Language,
		"command":   result.Command,
		"exit_code": result.ExitCode,
		"output":    result.Output,
		"clean":     result.OK && strings.TrimSpace(result.Output) == "",
	}
	if err != nil {
		data["status"] = agent.ToolStatusError
		data["error"] = err.Error()
		data["hint"] = "Install the relevant checker (gopls, tsc, or pyright) or fix reported diagnostics. CLI equivalent: cohort lsp diagnostics --language " + language
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return cleanStringSlice(x)
	case []any:
		values := make([]string, 0, len(x))
		for _, item := range x {
			value := strings.TrimSpace(asString(item))
			if value != "" {
				values = append(values, value)
			}
		}
		return values
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		return []string{strings.TrimSpace(x)}
	default:
		return nil
	}
}

func cleanStringSlice(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			clean = append(clean, value)
		}
	}
	return clean
}
