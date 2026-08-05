package lsp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	QueryDefinition = "definition"
	QueryReferences = "references"
	QueryHover      = "hover"
	QuerySymbols    = "symbols"
)

type QueryOptions struct {
	Language           string
	Kind               string
	Position           string
	Target             string
	IncludeDeclaration bool
}

func (d Diagnostics) Query(ctx context.Context, opts QueryOptions) (QueryResult, error) {
	language := NormalizeLanguage(opts.Language)
	kind := strings.ToLower(strings.TrimSpace(opts.Kind))
	switch language {
	case LanguageGo:
		gopls := Gopls{Command: d.GoCommand, Root: d.Root, Timeout: d.Timeout}
		switch kind {
		case QueryDefinition:
			return gopls.Definition(ctx, opts.Position)
		case QueryReferences:
			return gopls.References(ctx, opts.Position, opts.IncludeDeclaration)
		case QueryHover:
			return gopls.Hover(ctx, opts.Position)
		case QuerySymbols:
			return gopls.Symbols(ctx, firstNonEmpty(opts.Target, opts.Position, "."))
		default:
			return QueryResult{Language: language, Kind: kind, ExitCode: -1}, fmt.Errorf("unsupported lsp query kind %q", kind)
		}
	case LanguageTypeScript, LanguagePython:
		return d.symbolScanQuery(language, kind, opts)
	default:
		return QueryResult{Language: language, Kind: kind, ExitCode: -1}, fmt.Errorf("unsupported lsp query language %q", opts.Language)
	}
}

func (d Diagnostics) symbolScanQuery(language string, kind string, opts QueryOptions) (QueryResult, error) {
	root := filepath.Clean(firstNonEmpty(d.Root, "."))
	position := strings.TrimSpace(opts.Position)
	target := strings.TrimSpace(opts.Target)
	if target == "" && position != "" {
		if parsed, err := parseSourcePosition(position); err == nil {
			target = parsed.File
		}
	}
	result := QueryResult{
		Language: language,
		Kind:     kind,
		Position: position,
		Engine:   "symbol_scan",
		Command:  []string{"symbol-scan", "--language", language, kind, firstNonEmpty(position, target)},
		ExitCode: 0,
	}
	switch kind {
	case QueryDefinition, QueryReferences, QueryHover:
		symbol, err := symbolAtPosition(root, position)
		if err != nil {
			result.ExitCode = -1
			return result, err
		}
		output, err := scanSymbol(root, language, kind, symbol, opts.IncludeDeclaration)
		result.Output = output
		result.OK = err == nil
		if err != nil {
			result.ExitCode = -1
		}
		return result, err
	case QuerySymbols:
		output, err := scanSymbols(root, language, target)
		result.Output = output
		result.OK = err == nil
		if err != nil {
			result.ExitCode = -1
		}
		return result, err
	default:
		result.ExitCode = -1
		return result, fmt.Errorf("unsupported lsp query kind %q", kind)
	}
}

type sourcePosition struct {
	File   string
	Line   int
	Column int
}

func parseSourcePosition(position string) (sourcePosition, error) {
	position = strings.TrimSpace(position)
	parts := strings.Split(position, ":")
	if len(parts) < 3 {
		return sourcePosition{}, errors.New("position must be file:line:column")
	}
	line := atoi(parts[len(parts)-2])
	column := atoi(parts[len(parts)-1])
	file := strings.Join(parts[:len(parts)-2], ":")
	if strings.TrimSpace(file) == "" || line <= 0 || column <= 0 {
		return sourcePosition{}, errors.New("position must be file:line:column")
	}
	return sourcePosition{File: file, Line: line, Column: column}, nil
}

func symbolAtPosition(root string, position string) (string, error) {
	parsed, err := parseSourcePosition(position)
	if err != nil {
		return "", err
	}
	path := parsed.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if parsed.Line > len(lines) {
		return "", fmt.Errorf("line %d is outside %s", parsed.Line, parsed.File)
	}
	line := []rune(lines[parsed.Line-1])
	if parsed.Column > len(line)+1 {
		return "", fmt.Errorf("column %d is outside line %d", parsed.Column, parsed.Line)
	}
	index := parsed.Column - 1
	if index >= len(line) {
		index = len(line) - 1
	}
	if index < 0 {
		return "", errors.New("empty source line")
	}
	start, end := index, index
	for start > 0 && isIdentRune(line[start-1]) {
		start--
	}
	for end < len(line) && isIdentRune(line[end]) {
		end++
	}
	symbol := strings.TrimSpace(string(line[start:end]))
	if symbol == "" {
		return "", fmt.Errorf("no symbol at %s", position)
	}
	return symbol, nil
}

func scanSymbol(root string, language string, kind string, symbol string, includeDeclaration bool) (string, error) {
	files, err := sourceFiles(root, language, "")
	if err != nil {
		return "", err
	}
	var matches []string
	defPattern := definitionPattern(language, symbol)
	refPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol) + `\b`)
	for _, file := range files {
		if err := scanFileLines(file, func(lineNo int, line string) {
			rel := relPath(root, file)
			switch kind {
			case QueryDefinition:
				if defPattern.MatchString(line) {
					matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, lineNo, strings.TrimSpace(line)))
				}
			case QueryReferences:
				if refPattern.MatchString(line) {
					if includeDeclaration || !defPattern.MatchString(line) {
						matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, lineNo, strings.TrimSpace(line)))
					}
				}
			case QueryHover:
				if refPattern.MatchString(line) {
					matches = append(matches, fmt.Sprintf("symbol: %s\n%s:%d:%s", symbol, rel, lineNo, strings.TrimSpace(line)))
				}
			}
		}); err != nil {
			return "", err
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("symbol %q not found", symbol)
	}
	if kind == QueryHover {
		return matches[0], nil
	}
	return strings.Join(matches, "\n"), nil
}

func scanSymbols(root string, language string, target string) (string, error) {
	files, err := sourceFiles(root, language, target)
	if err != nil {
		return "", err
	}
	patterns := symbolPatterns(language)
	var matches []string
	for _, file := range files {
		if err := scanFileLines(file, func(lineNo int, line string) {
			for _, pattern := range patterns {
				if match := pattern.FindStringSubmatch(line); len(match) >= 2 {
					matches = append(matches, fmt.Sprintf("%s:%d:%s", relPath(root, file), lineNo, strings.TrimSpace(line)))
					return
				}
			}
		}); err != nil {
			return "", err
		}
	}
	if len(matches) == 0 {
		return "", errors.New("no symbols found")
	}
	return strings.Join(matches, "\n"), nil
}

func sourceFiles(root string, language string, target string) ([]string, error) {
	root = filepath.Clean(root)
	start := root
	if strings.TrimSpace(target) != "" {
		start = target
		if !filepath.IsAbs(start) {
			start = filepath.Join(root, start)
		}
	}
	extensions := map[string]bool{}
	switch language {
	case LanguageTypeScript:
		extensions[".ts"] = true
		extensions[".tsx"] = true
		extensions[".js"] = true
		extensions[".jsx"] = true
	case LanguagePython:
		extensions[".py"] = true
	default:
		return nil, fmt.Errorf("unsupported symbol scan language %q", language)
	}
	info, err := os.Stat(start)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if extensions[strings.ToLower(filepath.Ext(start))] {
			return []string{filepath.Clean(start)}, nil
		}
		return nil, fmt.Errorf("target %s is not a %s source file", target, language)
	}
	var files []string
	err = filepath.WalkDir(start, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".venv", "venv", "__pycache__":
				if path != start {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if extensions[strings.ToLower(filepath.Ext(path))] {
			files = append(files, filepath.Clean(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s source files under %s", language, relPath(root, start))
	}
	return files, nil
}

func scanFileLines(path string, fn func(lineNo int, line string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		fn(lineNo, scanner.Text())
	}
	return scanner.Err()
}

func definitionPattern(language string, symbol string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	switch language {
	case LanguagePython:
		return regexp.MustCompile(`^\s*(def|class)\s+` + quoted + `\b|^\s*` + quoted + `\s*=`)
	default:
		return regexp.MustCompile(`\b(function|class|interface|type|const|let|var)\s+` + quoted + `\b|\b` + quoted + `\s*[:=]\s*`)
	}
}

func symbolPatterns(language string) []*regexp.Regexp {
	if language == LanguagePython {
		return []*regexp.Regexp{
			regexp.MustCompile(`^\s*(def|class)\s+[A-Za-z_][A-Za-z0-9_]*\b`),
		}
	}
	return []*regexp.Regexp{
		regexp.MustCompile(`\b(function|class|interface|type|const|let|var)\s+[A-Za-z_$][A-Za-z0-9_$]*\b`),
	}
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func atoi(value string) int {
	n := 0
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func relPath(root string, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
