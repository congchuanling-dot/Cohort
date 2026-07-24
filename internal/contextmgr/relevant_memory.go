package contextmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"cohert/internal/llm"
)

const (
	defaultGlobalMemoryPath = "memory/global.md"
	sopCandidateMemoryPath  = "memory/reflection/sop_candidates.md"
)

var (
	memoryPathPattern = regexp.MustCompile(`memory/[A-Za-z0-9._/\-]+`)
	taskKeywordHints  = []string{
		"飞书", "lark", "浏览器", "browser", "网页", "页面", "点击", "输入",
		"审批", "表单", "登录", "chrome", "snapshot", "selector", "element",
		"元素", "wait_for_stable", "wait_for_load", "wait_for_text", "cdp",
	}
)

type relevantMemoryMatch struct {
	relPath string
	title   string
	content string
	score   int
}

func loadRelevantLongTermMemory(memoryRoot string, indexText string, messages []llm.Message, cfg Config) (matches []relevantMemoryMatch, err error) {
	if strings.TrimSpace(memoryRoot) == "" || cfg.MaxRelevantMemoryEntries <= 0 || cfg.MaxRelevantMemoryChars <= 0 {
		return nil, nil
	}
	query := recentUserTaskText(messages)
	keywords := extractTaskKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}

	paths := candidateMemoryPaths(memoryRoot, indexText)
	scored := make([]relevantMemoryMatch, 0, len(paths))
	for _, relPath := range paths {
		content, ok, err := loadMemoryMarkdown(memoryRoot, relPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, entry := range splitMemoryEntries(content) {
			score := scoreRelevantMemory(relPath, entry.title, entry.content, keywords)
			if score <= 0 {
				continue
			}
			scored = append(scored, relevantMemoryMatch{
				relPath: relPath,
				title:   entry.title,
				content: entry.content,
				score:   score,
			})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].relPath < scored[j].relPath
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > cfg.MaxRelevantMemoryEntries {
		scored = scored[:cfg.MaxRelevantMemoryEntries]
	}
	return scored, nil
}

func buildRelevantLongTermMemoryMessage(matches []relevantMemoryMatch, cfg Config, stats *Stats) (llm.Message, bool) {
	if len(matches) == 0 {
		return llm.Message{}, false
	}
	var b strings.Builder
	b.WriteString("Matched by current user task keywords. Treat this as stable background; verify with tools when acting on external state.")
	for _, match := range matches {
		b.WriteString("\n\n[source: ")
		b.WriteString(match.relPath)
		if match.title != "" {
			b.WriteString(" | entry: ")
			b.WriteString(match.title)
		}
		b.WriteString("]\n")
		b.WriteString(strings.TrimSpace(match.content))
	}

	limited, truncated := limitRunes(b.String(), cfg.MaxRelevantMemoryChars)
	content := relevantLongTermMemoryNotice + "\n\n" + limited
	if truncated {
		content += "\n\n[Cohert relevant long-term memory truncated]"
		stats.RelevantMemoryTruncated = true
	}

	stats.InjectedRelevantMemory = true
	stats.RelevantMemoryChars = len([]rune(limited))
	stats.RelevantMemoryEntries = len(matches)
	return llm.Message{Role: llm.RoleAssistant, Content: content}, true
}

func recentUserTaskText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func extractTaskKeywords(text string) []string {
	lower := strings.ToLower(text)
	seen := map[string]bool{}
	var keywords []string
	for _, hint := range taskKeywordHints {
		key := strings.ToLower(hint)
		if strings.Contains(lower, key) && !seen[key] {
			seen[key] = true
			keywords = append(keywords, key)
		}
	}
	for _, word := range splitKeywordWords(lower) {
		if len([]rune(word)) < 3 || seen[word] {
			continue
		}
		seen[word] = true
		keywords = append(keywords, word)
	}
	return keywords
}

func splitKeywordWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		if r == '_' || r == '-' {
			return false
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func candidateMemoryPaths(memoryRoot string, indexText string) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = normalizeMemoryReference(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	add(defaultGlobalMemoryPath)
	add(sopCandidateMemoryPath)
	for _, match := range memoryPathPattern.FindAllString(indexText, -1) {
		add(match)
	}
	for _, path := range discoverProjectMemoryPaths(memoryRoot) {
		add(path)
	}
	return paths
}

func discoverProjectMemoryPaths(memoryRoot string) []string {
	pattern := filepath.Join(memoryRoot, "projects", "*", "project.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		rel, err := filepath.Rel(memoryRoot, match)
		if err != nil {
			continue
		}
		paths = append(paths, "memory/"+filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths
}

func normalizeMemoryReference(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\"：:，,。.)]}")
	path = strings.TrimPrefix(path, "./")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "memory/index.md" {
		return ""
	}
	if !strings.HasPrefix(path, "memory/") || !strings.HasSuffix(path, ".md") {
		return ""
	}
	if strings.Contains(path, "/../") || strings.HasPrefix(path, "../") {
		return ""
	}
	if strings.HasPrefix(path, "memory/raw_sessions/") || strings.HasPrefix(path, "memory/audit") {
		return ""
	}
	return path
}

func loadMemoryMarkdown(memoryRoot, relPath string) (text string, ok bool, err error) {
	relPath = normalizeMemoryReference(relPath)
	if relPath == "" {
		return "", false, nil
	}
	relUnderRoot := strings.TrimPrefix(relPath, "memory/")
	path := filepath.Join(memoryRoot, filepath.FromSlash(relUnderRoot))
	cleanRoot := filepath.Clean(memoryRoot)
	cleanPath := filepath.Clean(path)
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
		return "", false, fmt.Errorf("unsafe memory path %q", relPath)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	text = strings.TrimSpace(string(data))
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

type memoryEntry struct {
	title   string
	content string
}

func splitMemoryEntries(content string) []memoryEntry {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	var entries []memoryEntry
	var current strings.Builder
	currentTitle := ""
	flush := func() {
		text := strings.TrimSpace(current.String())
		if text == "" {
			return
		}
		entries = append(entries, memoryEntry{title: currentTitle, content: text})
		current.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Memory Entry:") || strings.HasPrefix(trimmed, "## SOP Candidate:") {
			flush()
			currentTitle = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "## Memory Entry:"), "## SOP Candidate:"))
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	flush()
	if len(entries) == 0 {
		return []memoryEntry{{content: content}}
	}
	if len(entries) == 1 && entries[0].title == "" {
		return entries
	}
	filtered := make([]memoryEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(strings.TrimSpace(entry.content), "# ") && entry.title == "" {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) == 0 {
		return []memoryEntry{{content: content}}
	}
	return filtered
}

func scoreRelevantMemory(relPath, title, content string, keywords []string) int {
	haystack := strings.ToLower(relPath + "\n" + title + "\n" + content)
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(haystack, keyword) {
			score += 10
		}
		if strings.Contains(extractMemoryField(content, "trigger_keywords"), keyword) {
			score += 20
		}
		if strings.Contains(strings.ToLower(extractMemoryField(content, "scene")), keyword) {
			score += 12
		}
	}
	if score > 0 && strings.Contains(relPath, "/projects/") {
		score += 3
	}
	return score
}

func extractMemoryField(content, field string) string {
	prefix := "- " + field + ":"
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		}
	}
	return ""
}
