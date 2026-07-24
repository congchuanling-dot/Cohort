package contextmgr

import (
	"errors"
	"fmt"
	"hash/fnv"
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
	entryID string
	title   string
	content string
	score   int
	reasons []string
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
			if score.score <= 0 {
				continue
			}
			scored = append(scored, relevantMemoryMatch{
				relPath: relPath,
				entryID: stableMemoryEntryID(entry.title, entry.content),
				title:   entry.title,
				content: entry.content,
				score:   score.score,
				reasons: score.reasons,
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
		if match.entryID != "" {
			b.WriteString(" | id: ")
			b.WriteString(match.entryID)
		}
		if match.title != "" {
			b.WriteString(" | entry: ")
			b.WriteString(match.title)
		}
		b.WriteString(" | score: ")
		b.WriteString(fmt.Sprint(match.score))
		if len(match.reasons) > 0 {
			b.WriteString(" | why: ")
			b.WriteString(strings.Join(match.reasons, "; "))
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
	stats.RelevantMemoryHitLogs = relevantMemoryHitLogs(matches)
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

type relevantMemoryScore struct {
	score   int
	reasons []string
}

func scoreRelevantMemory(relPath, title, content string, keywords []string) relevantMemoryScore {
	fields := []struct {
		name   string
		text   string
		weight int
	}{
		{name: "trigger_keywords", text: extractMemoryField(content, "trigger_keywords"), weight: 50},
		{name: "scene", text: extractMemoryField(content, "scene"), weight: 35},
		{name: "lesson", text: extractMemorySection(content, "Lesson") + "\n" + extractMemorySection(content, "Why This Should Become SOP"), weight: 22},
		{name: "recommended_steps", text: extractMemorySection(content, "Recommended Steps") + "\n" + extractMemorySection(content, "Draft Steps"), weight: 14},
		{name: "title", text: title, weight: 10},
		{name: "path", text: relPath, weight: 4},
	}
	score := relevantMemoryScore{}
	seenReasons := map[string]bool{}
	for _, keyword := range keywords {
		for _, field := range fields {
			if !strings.Contains(strings.ToLower(field.text), keyword) {
				continue
			}
			score.score += field.weight
			reason := fmt.Sprintf("%s matched %q (+%d)", field.name, keyword, field.weight)
			if !seenReasons[reason] {
				score.reasons = append(score.reasons, reason)
				seenReasons[reason] = true
			}
		}
	}
	if score.score == 0 {
		contentLower := strings.ToLower(content)
		for _, keyword := range keywords {
			if strings.Contains(contentLower, keyword) {
				score.score += 5
				reason := fmt.Sprintf("content matched %q (+5)", keyword)
				if !seenReasons[reason] {
					score.reasons = append(score.reasons, reason)
					seenReasons[reason] = true
				}
			}
		}
	}
	if score.score > 0 && strings.Contains(relPath, "/projects/") {
		score.score += 3
		score.reasons = append(score.reasons, "project memory bonus (+3)")
	}
	if len(score.reasons) > 4 {
		score.reasons = score.reasons[:4]
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

func extractMemorySection(content, heading string) string {
	prefix := "### " + heading
	lines := strings.Split(content, "\n")
	var b strings.Builder
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") {
			if inSection {
				break
			}
			if trimmed == prefix {
				inSection = true
			}
			continue
		}
		if inSection {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func stableMemoryEntryID(title, content string) string {
	if id := extractMemoryField(content, "id"); id != "" {
		return id
	}
	title = strings.TrimSpace(title)
	if strings.HasPrefix(title, "mem-") || strings.HasPrefix(title, "sop-") {
		return title
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.Join(strings.Fields(title+"\n"+content), " ")))
	return fmt.Sprintf("mem-%08x", h.Sum32())
}

func relevantMemoryHitLogs(matches []relevantMemoryMatch) []RelevantMemoryHitLog {
	logs := make([]RelevantMemoryHitLog, 0, len(matches))
	for _, match := range matches {
		logs = append(logs, RelevantMemoryHitLog{
			EntryID: match.entryID,
			Source:  match.relPath,
			Title:   match.title,
			Score:   match.score,
			Reasons: append([]string(nil), match.reasons...),
		})
	}
	return logs
}
