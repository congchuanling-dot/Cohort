package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	LanguageGo         = "go"
	LanguageTypeScript = "typescript"
	LanguagePython     = "python"
	LanguageAll        = "all"
)

type Diagnostics struct {
	Root              string
	Timeout           time.Duration
	GoCommand         string
	TypeScriptCommand string
	PythonCommand     string
}

type LanguageDoctorResult struct {
	Language string `json:"language"`
	Command  string `json:"command"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

func NormalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "go", "golang":
		return LanguageGo
	case "ts", "typescript", "javascript", "js":
		return LanguageTypeScript
	case "py", "python":
		return LanguagePython
	case "all":
		return LanguageAll
	default:
		return strings.ToLower(strings.TrimSpace(language))
	}
}

func SupportedLanguages(language string) []string {
	switch NormalizeLanguage(language) {
	case LanguageAll:
		return []string{LanguageGo, LanguageTypeScript, LanguagePython}
	case LanguageGo:
		return []string{LanguageGo}
	case LanguageTypeScript:
		return []string{LanguageTypeScript}
	case LanguagePython:
		return []string{LanguagePython}
	default:
		return nil
	}
}

func (d Diagnostics) Doctor(ctx context.Context, language string) []LanguageDoctorResult {
	languages := SupportedLanguages(language)
	if len(languages) == 0 {
		return []LanguageDoctorResult{{
			Language: NormalizeLanguage(language),
			OK:       false,
			Error:    fmt.Sprintf("unsupported language %q", language),
		}}
	}
	results := make([]LanguageDoctorResult, 0, len(languages))
	for _, item := range languages {
		results = append(results, d.doctorOne(ctx, item))
	}
	return results
}

func (d Diagnostics) Check(ctx context.Context, language string, targets []string) (CheckResult, error) {
	switch NormalizeLanguage(language) {
	case LanguageGo:
		return Gopls{
			Command: d.GoCommand,
			Root:    d.Root,
			Timeout: d.Timeout,
		}.Check(ctx, targets)
	case LanguageTypeScript:
		return d.runTypeScript(ctx, targets)
	case LanguagePython:
		return d.runPython(ctx, targets)
	default:
		return CheckResult{Language: NormalizeLanguage(language), ExitCode: -1}, fmt.Errorf("unsupported LSP diagnostics language %q", language)
	}
}

func (d Diagnostics) doctorOne(ctx context.Context, language string) LanguageDoctorResult {
	command, args := d.commandAndVersionArgs(language)
	result := LanguageDoctorResult{Language: language, Command: command}
	path, err := exec.LookPath(command)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Path = path
	check, err := d.runCommand(ctx, language, command, args...)
	if err != nil {
		result.Error = err.Error()
		result.Version = strings.TrimSpace(check.Output)
		return result
	}
	result.OK = true
	result.Version = strings.TrimSpace(check.Output)
	return result
}

func (d Diagnostics) commandAndVersionArgs(language string) (string, []string) {
	switch language {
	case LanguageGo:
		return firstNonEmpty(d.GoCommand, "gopls"), []string{"version"}
	case LanguageTypeScript:
		return firstNonEmpty(d.TypeScriptCommand, "tsc"), []string{"--version"}
	case LanguagePython:
		return firstNonEmpty(d.PythonCommand, "pyright"), []string{"--version"}
	default:
		return language, nil
	}
}

func (d Diagnostics) runTypeScript(ctx context.Context, targets []string) (CheckResult, error) {
	args := []string{"--noEmit", "--pretty", "false"}
	clean := cleanExplicitTargets(targets)
	if len(clean) == 0 {
		if lspFileExists(filepath.Join(firstNonEmpty(d.Root, "."), "tsconfig.json")) {
			args = append(args, "--project", "tsconfig.json")
		}
	} else {
		args = append(args, clean...)
	}
	return d.runCommand(ctx, LanguageTypeScript, firstNonEmpty(d.TypeScriptCommand, "tsc"), args...)
}

func (d Diagnostics) runPython(ctx context.Context, targets []string) (CheckResult, error) {
	args := cleanExplicitTargets(targets)
	if len(args) == 0 {
		args = []string{"."}
	}
	return d.runCommand(ctx, LanguagePython, firstNonEmpty(d.PythonCommand, "pyright"), args...)
}

func (d Diagnostics) runCommand(ctx context.Context, language string, command string, args ...string) (CheckResult, error) {
	if _, err := exec.LookPath(command); err != nil {
		return CheckResult{Language: language, Command: append([]string{command}, args...), ExitCode: -1}, err
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, args...)
	if strings.TrimSpace(d.Root) != "" {
		cmd.Dir = filepath.Clean(d.Root)
	}
	output, err := cmd.CombinedOutput()
	result := CheckResult{
		Language: language,
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

func lspFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func cleanExplicitTargets(targets []string) []string {
	clean := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target != "" {
			clean = append(clean, target)
		}
	}
	return clean
}
