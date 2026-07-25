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
	// defaultGlobalMemoryPath 是不依赖索引、每轮都可参与相关性匹配的全局经验文件。
	defaultGlobalMemoryPath = "memory/global.md"
	// sopCandidateMemoryPath 允许已验证的候选工作流参与匹配，但不等同于已正式启用的 SOP。
	sopCandidateMemoryPath = "memory/reflection/sop_candidates.md"
)

var (
	// memoryPathPattern 从索引文本中提取可安全读取的 Markdown 记忆引用。
	memoryPathPattern = regexp.MustCompile(`memory/[A-Za-z0-9._/\-]+`)
	// taskKeywordHints 给中英文常见工具场景增加基础检索召回。
	taskKeywordHints = []string{
		"飞书", "lark", "浏览器", "browser", "网页", "页面", "点击", "输入",
		"审批", "表单", "登录", "chrome", "snapshot", "selector", "element",
		"元素", "wait_for_stable", "wait_for_load", "wait_for_text", "cdp",
	}
)

// relevantMemoryMatch 是一条进入本轮候选的记忆条目及其可解释匹配信息。
type relevantMemoryMatch struct {
	// relPath 是相对于 workspace 的记忆来源路径。
	relPath string
	// entryID 是条目稳定标识，方便审计日志关联。
	entryID string
	// title 是 Markdown 条目的标题。
	title string
	// content 是实际要注入的条目正文。
	content string
	// score 是关键词匹配得到的确定性分数。
	score int
	// reasons 是各字段命中带来的分数说明。
	reasons []string
}

// loadRelevantLongTermMemory 从受限记忆文件中找出与最新用户任务最相关的少量条目。
// 它只做确定性关键词打分，不访问网络、不修改记忆文件。
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

// buildRelevantLongTermMemoryMessage 将命中条目编码为请求前缀，并同步记录可审计匹配理由。
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

// recentUserTaskText 从历史末尾逆向寻找最近一条实际用户任务，忽略工具和系统注入消息。
func recentUserTaskText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

// extractTaskKeywords 结合预置场景词和通用分词提取检索词，并保持首次出现顺序。
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

// splitKeywordWords 按非字母数字边界分词，但保留下划线和连字符组成的工具名。
func splitKeywordWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		if r == '_' || r == '-' {
			return false
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// candidateMemoryPaths 汇总固定、索引引用和自动发现的项目记忆路径，并去重。
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

// discoverProjectMemoryPaths 查找 memory/projects/*/project.md，使项目记忆不必全部手写进索引。
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

// normalizeMemoryReference 拒绝索引中指向 audit、原始会话或目录逃逸的路径。
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

// loadMemoryMarkdown 在词法路径检查后读取一份 Markdown 记忆；不存在和空文件都不是错误。
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

// memoryEntry 是从一个 Markdown 文件按二级标题拆出的可独立检索单元。
type memoryEntry struct {
	// title 是条目标题，没有标准标题时为空。
	title string
	// content 是保留标题在内的原始条目文本。
	content string
}

// splitMemoryEntries 识别标准 Memory Entry/SOP Candidate 二级标题，并保留无法识别的完整文件作为兜底条目。
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

// relevantMemoryScore 保存确定性关键词评分及可展示的命中理由。
type relevantMemoryScore struct {
	// score 是字段权重的累计分数。
	score int
	// reasons 是至多四项最早命中的解释。
	reasons []string
}

// scoreRelevantMemory 对结构化字段赋予高于正文的权重，优先召回明确标注场景和关键词的经验。
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

// extractMemoryField 从 Markdown 元数据行中读取一个小写字段值。
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

// extractMemorySection 返回指定三级标题到下一个三级标题之间的正文。
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

// stableMemoryEntryID 优先复用条目显式 ID，否则根据规范化文本生成稳定 FNV 标识。
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

// relevantMemoryHitLogs 将内部匹配结果复制为不包含正文的诊断日志结构。
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
