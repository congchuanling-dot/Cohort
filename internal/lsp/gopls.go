package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const DefaultTimeout = 30 * time.Second

type Gopls struct {
	Command string
	Root    string
	Timeout time.Duration
}

type DoctorResult struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
}

type CheckResult struct {
	Language string   `json:"language,omitempty"`
	Command  []string `json:"command"`
	Output   string   `json:"output"`
	OK       bool     `json:"ok"`
	ExitCode int      `json:"exit_code"`
}

type QueryResult struct {
	Language       string   `json:"language,omitempty"`
	Kind           string   `json:"kind"`
	Position       string   `json:"position"`
	Engine         string   `json:"engine,omitempty"`
	FallbackReason string   `json:"fallback_reason,omitempty"`
	Command        []string `json:"command"`
	Output         string   `json:"output"`
	OK             bool     `json:"ok"`
	ExitCode       int      `json:"exit_code"`
}

func (g Gopls) Doctor(ctx context.Context) (DoctorResult, error) {
	command := firstNonEmpty(g.Command, "gopls")
	path, err := exec.LookPath(command)
	if err != nil {
		return DoctorResult{Command: command}, fmt.Errorf("gopls not found in PATH: %w", err)
	}
	output, err := g.run(ctx, "version")
	if err != nil {
		return DoctorResult{Command: command, Path: path}, err
	}
	return DoctorResult{
		Command: command,
		Path:    path,
		Version: strings.TrimSpace(output.Output),
		OK:      output.OK,
	}, nil
}

func (g Gopls) Check(ctx context.Context, targets []string) (CheckResult, error) {
	if len(targets) == 0 {
		targets = []string{"./..."}
	}
	expanded, err := g.expandCheckTargets(ctx, cleanTargets(targets))
	if err != nil {
		return CheckResult{Language: LanguageGo, Command: append([]string{firstNonEmpty(g.Command, "gopls"), "check"}, targets...), ExitCode: -1}, err
	}
	args := append([]string{"check"}, expanded...)
	return g.run(ctx, args...)
}

func (g Gopls) Definition(ctx context.Context, position string) (QueryResult, error) {
	position = strings.TrimSpace(position)
	if position == "" {
		return QueryResult{Language: LanguageGo, Kind: "definition", ExitCode: -1}, errors.New("definition position is required")
	}
	result, err := g.run(ctx, "definition", position)
	return queryResultFromCheck("definition", position, result, err)
}

func (g Gopls) References(ctx context.Context, position string, includeDeclaration bool) (QueryResult, error) {
	position = strings.TrimSpace(position)
	if position == "" {
		return QueryResult{Language: LanguageGo, Kind: "references", ExitCode: -1}, errors.New("references position is required")
	}
	args := []string{"references"}
	if includeDeclaration {
		args = append(args, "-d")
	}
	args = append(args, position)
	result, err := g.run(ctx, args...)
	return queryResultFromCheck("references", position, result, err)
}

func (g Gopls) Hover(ctx context.Context, position string) (QueryResult, error) {
	position = strings.TrimSpace(position)
	if position == "" {
		return QueryResult{Language: LanguageGo, Kind: "hover", ExitCode: -1}, errors.New("hover position is required")
	}
	result, err := g.run(ctx, "hover", position)
	return queryResultFromCheck("hover", position, result, err)
}

func (g Gopls) Symbols(ctx context.Context, target string) (QueryResult, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "."
	}
	result, err := g.run(ctx, "symbols", target)
	return queryResultFromCheck("symbols", target, result, err)
}

func (g Gopls) expandCheckTargets(ctx context.Context, targets []string) ([]string, error) {
	root := strings.TrimSpace(g.Root)
	if root == "" {
		root = "."
	}
	root = filepath.Clean(root)
	expanded := make([]string, 0, len(targets))
	for _, target := range targets {
		if needsGoListExpansion(root, target) {
			files, err := goListFiles(ctx, root, target)
			if err != nil {
				return nil, err
			}
			expanded = append(expanded, files...)
			continue
		}
		expanded = append(expanded, target)
	}
	if len(expanded) == 0 {
		return nil, errors.New("gopls check has no Go files to inspect")
	}
	return expanded, nil
}

func (g Gopls) run(ctx context.Context, args ...string) (CheckResult, error) {
	command := firstNonEmpty(g.Command, "gopls")
	if _, err := exec.LookPath(command); err != nil {
		return CheckResult{Language: LanguageGo, Command: append([]string{command}, args...), ExitCode: -1}, err
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, args...)
	if strings.TrimSpace(g.Root) != "" {
		cmd.Dir = filepath.Clean(g.Root)
	}
	output, err := cmd.CombinedOutput()
	result := CheckResult{
		Language: LanguageGo,
		Command:  append([]string{command}, args...),
		Output:   strings.TrimSpace(string(output)),
		OK:       err == nil,
		ExitCode: exitCode(err),
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	return result, err
}

func cleanTargets(targets []string) []string {
	clean := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target != "" {
			clean = append(clean, target)
		}
	}
	if len(clean) == 0 {
		return []string{"./..."}
	}
	return clean
}

func needsGoListExpansion(root string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return true
	}
	if strings.Contains(target, "...") {
		return true
	}
	candidate := target
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	info, err := os.Stat(candidate)
	return err == nil && info.IsDir()
}

func goListFiles(ctx context.Context, root string, pattern string) ([]string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = "./..."
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-f", `{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}`, pattern)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("expand gopls targets with go list %s: %w: %s", pattern, err, strings.TrimSpace(string(data)))
	}
	lines := strings.Split(string(data), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("go list %s returned no Go files", pattern)
	}
	return files, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func queryResultFromCheck(kind string, position string, result CheckResult, err error) (QueryResult, error) {
	query := QueryResult{
		Language: result.Language,
		Kind:     kind,
		Position: position,
		Engine:   "gopls",
		Command:  result.Command,
		Output:   result.Output,
		OK:       result.OK,
		ExitCode: result.ExitCode,
	}
	return query, err
}
