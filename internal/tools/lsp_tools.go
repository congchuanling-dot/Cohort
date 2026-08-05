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

type LSPDefinition struct {
	workspace string
}

func NewLSPDefinition(workspace string) *LSPDefinition {
	return &LSPDefinition{workspace: workspace}
}

func (t *LSPDefinition) Name() string { return ToolNameLSPDefinition }

func (t *LSPDefinition) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find a symbol definition at a 1-indexed position. Go uses gopls; TypeScript/Python use read-only symbol_scan fallback. Read-only.",
		Parameters: objectSchema(map[string]any{
			"language": stringProp("Language: go (default), typescript, or python."),
			"position": stringProp("Required source position in file:line:column form."),
		}),
	}}
}

func (t *LSPDefinition) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	language := lsp.NormalizeLanguage(asString(call.Args["language"]))
	if language == "" {
		language = lsp.LanguageGo
	}
	position := strings.TrimSpace(asString(call.Args["position"]))
	result, err := (lsp.Diagnostics{Root: t.workspace}).Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryDefinition, Position: position})
	return lspQueryOutcome(result, err, "definition"), nil
}

type LSPReferences struct {
	workspace string
}

func NewLSPReferences(workspace string) *LSPReferences {
	return &LSPReferences{workspace: workspace}
}

func (t *LSPReferences) Name() string { return ToolNameLSPReferences }

func (t *LSPReferences) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find symbol references at a 1-indexed position. Go uses gopls; TypeScript/Python use read-only symbol_scan fallback. Read-only.",
		Parameters: objectSchema(map[string]any{
			"language":            stringProp("Language: go (default), typescript, or python."),
			"position":            stringProp("Required source position in file:line:column form."),
			"include_declaration": boolProp("Include the declaration in the reference result.", false),
		}),
	}}
}

func (t *LSPReferences) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	language := lsp.NormalizeLanguage(asString(call.Args["language"]))
	if language == "" {
		language = lsp.LanguageGo
	}
	position := strings.TrimSpace(asString(call.Args["position"]))
	includeDeclaration := asBool(call.Args["include_declaration"], false)
	result, err := (lsp.Diagnostics{Root: t.workspace}).Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryReferences, Position: position, IncludeDeclaration: includeDeclaration})
	return lspQueryOutcome(result, err, "references"), nil
}

type LSPHover struct {
	workspace string
}

func NewLSPHover(workspace string) *LSPHover {
	return &LSPHover{workspace: workspace}
}

func (t *LSPHover) Name() string { return ToolNameLSPHover }

func (t *LSPHover) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read hover/context information for a symbol position. Go uses gopls; TypeScript/Python use read-only symbol_scan fallback.",
		Parameters: objectSchema(map[string]any{
			"language": stringProp("Language: go (default), typescript, or python."),
			"position": stringProp("Required source position in file:line:column form."),
		}),
	}}
}

func (t *LSPHover) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	language := lsp.NormalizeLanguage(asString(call.Args["language"]))
	if language == "" {
		language = lsp.LanguageGo
	}
	position := strings.TrimSpace(asString(call.Args["position"]))
	result, err := (lsp.Diagnostics{Root: t.workspace}).Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QueryHover, Position: position})
	return lspQueryOutcome(result, err, "hover"), nil
}

type LSPSymbols struct {
	workspace string
}

func NewLSPSymbols(workspace string) *LSPSymbols {
	return &LSPSymbols{workspace: workspace}
}

func (t *LSPSymbols) Name() string { return ToolNameLSPSymbols }

func (t *LSPSymbols) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "List symbols in a file or directory. Go uses gopls; TypeScript/Python use read-only symbol_scan fallback.",
		Parameters: objectSchema(map[string]any{
			"language": stringProp("Language: go (default), typescript, or python."),
			"target":   stringProp("Optional file or directory target. Defaults to workspace root."),
		}),
	}}
}

func (t *LSPSymbols) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	language := lsp.NormalizeLanguage(asString(call.Args["language"]))
	if language == "" {
		language = lsp.LanguageGo
	}
	target := strings.TrimSpace(asString(call.Args["target"]))
	result, err := (lsp.Diagnostics{Root: t.workspace}).Query(ctx, lsp.QueryOptions{Language: language, Kind: lsp.QuerySymbols, Target: target})
	return lspQueryOutcome(result, err, "symbols"), nil
}

func lspQueryOutcome(result lsp.QueryResult, err error, kind string) agent.Outcome {
	data := map[string]any{
		"status":    agent.ToolStatusSuccess,
		"language":  result.Language,
		"kind":      result.Kind,
		"engine":    result.Engine,
		"position":  result.Position,
		"command":   result.Command,
		"exit_code": result.ExitCode,
		"output":    result.Output,
	}
	if err != nil {
		data["status"] = agent.ToolStatusError
		data["error"] = err.Error()
		data["hint"] = "Install the relevant language backend or pass a valid source position. CLI equivalent: cohort lsp " + kind + " --language <go|typescript|python> <file:line:column>"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}
}
