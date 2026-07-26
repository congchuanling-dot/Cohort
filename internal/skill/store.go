package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// SkillFileName 是每个 Skill 包的入口文件名。
	SkillFileName = "SKILL.md"
	// ProjectSkillsDir 是项目级 Skill 根目录。
	ProjectSkillsDir = ".cohort/skills"
	// UserSkillsDir 是用户级 Skill 根目录。沿用 Cohert 当前全局目录拼写。
	UserSkillsDir = ".cohert/skills"

	maxSkillDescriptionRunes = 240
	maxSkillReadBytes        = 200_000
)

// Scope 表示 Skill 来源范围。项目级 Skill 优先级高于用户级 Skill。
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

// Skill 是一个可按需读取的工作流包摘要。
// 摘要会进入系统提示词；正文只有模型显式调用 skill_read 后才进入上下文。
type Skill struct {
	ID            string `json:"id"`
	Alias         string `json:"alias"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	UserInvocable bool   `json:"user_invocable"`
	ArgumentHint  string `json:"argument_hint"`
	Scope         Scope  `json:"scope"`
	Path          string `json:"path"`
}

// ReadResult 是 skill_read 返回给模型的结构化内容。
type ReadResult struct {
	Skill     Skill  `json:"skill"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// Store 保存一次发现到的 Skill 索引，并支持运行中 reload。
type Store struct {
	mu        sync.RWMutex
	workspace string
	homeDir   string
	skills    []Skill
	byID      map[string]Skill
	aliases   map[string][]string
}

// NewStore 创建 Skill Store。workspace 为空时使用当前工作目录，homeDir 为空时使用 os.UserHomeDir。
func NewStore(workspace, homeDir string) *Store {
	if strings.TrimSpace(workspace) == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if strings.TrimSpace(homeDir) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			homeDir = home
		}
	}
	homeDir = strings.TrimSpace(homeDir)
	if homeDir != "" {
		homeDir = filepath.Clean(homeDir)
	}
	return &Store{
		workspace: filepath.Clean(workspace),
		homeDir:   homeDir,
		byID:      map[string]Skill{},
		aliases:   map[string][]string{},
	}
}

// Reload 重新扫描项目级和用户级 Skill 目录。
func (s *Store) Reload() error {
	skills, err := s.scan()
	if err != nil {
		return err
	}
	byID := map[string]Skill{}
	aliases := map[string][]string{}
	for _, item := range skills {
		byID[item.ID] = item
		aliases[item.Alias] = append(aliases[item.Alias], item.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills = skills
	s.byID = byID
	s.aliases = aliases
	return nil
}

// Skills 返回当前 Skill 摘要副本。
func (s *Store) Skills() []Skill {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Skill, len(s.skills))
	copy(out, s.skills)
	return out
}

// Find 通过完整 ID 或唯一 alias 查找 Skill。
func (s *Store) Find(id string) (Skill, error) {
	if s == nil {
		return Skill{}, fmt.Errorf("skill store is not configured")
	}
	id = normalizeID(id)
	if id == "" {
		return Skill{}, fmt.Errorf("skill_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.byID[id]; ok {
		return item, nil
	}
	matches := s.aliases[id]
	if len(matches) == 1 {
		return s.byID[matches[0]], nil
	}
	if len(matches) > 1 {
		return Skill{}, fmt.Errorf("skill_id %q is ambiguous; use one of: %s", id, strings.Join(matches, ", "))
	}
	return Skill{}, fmt.Errorf("skill %q not found", id)
}

// Read 读取 Skill 的 SKILL.md 正文。
func (s *Store) Read(id string) (ReadResult, error) {
	item, err := s.Find(id)
	if err != nil {
		return ReadResult{}, err
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return ReadResult{}, err
	}
	truncated := false
	if len(data) > maxSkillReadBytes {
		data = data[:maxSkillReadBytes]
		truncated = true
	}
	return ReadResult{
		Skill:     item,
		Content:   string(data),
		Truncated: truncated,
	}, nil
}

// IndexPrompt 生成系统提示词里的 Skill 摘要索引。
func (s *Store) IndexPrompt() string {
	skills := s.Skills()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Skill Index]\n")
	b.WriteString("Skill 是可按需读取的任务工作流包，不是 MCP 工具。只有摘要在这里；命中任务场景时先调用 skill_read(skill_id) 读取完整 SKILL.md，再按其中规则执行，并调用 update_working_checkpoint 保存 related_skill 和关键约束。\n")
	for _, item := range skills {
		fmt.Fprintf(&b, "- id: `%s`; name: %s; scope: %s; description: %s\n", item.ID, item.Name, item.Scope, item.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Store) scan() ([]Skill, error) {
	roots := []struct {
		scope Scope
		path  string
	}{
		{scope: ScopeProject, path: filepath.Join(s.workspace, filepath.FromSlash(ProjectSkillsDir))},
	}
	if s.homeDir != "" {
		roots = append(roots, struct {
			scope Scope
			path  string
		}{scope: ScopeUser, path: filepath.Join(s.homeDir, filepath.FromSlash(UserSkillsDir))})
	}
	var skills []Skill
	for _, root := range roots {
		found, err := scanRoot(root.scope, root.path)
		if err != nil {
			return nil, err
		}
		skills = append(skills, found...)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Scope != skills[j].Scope {
			return skills[i].Scope < skills[j].Scope
		}
		return skills[i].ID < skills[j].ID
	})
	return skills, nil
}

func scanRoot(scope Scope, root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		alias := normalizeID(entry.Name())
		if alias == "" {
			continue
		}
		path := filepath.Join(root, entry.Name(), SkillFileName)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		metadata := parseMetadata(data, entry.Name())
		skills = append(skills, Skill{
			ID:            string(scope) + "/" + alias,
			Alias:         alias,
			Name:          metadata.Name,
			Description:   metadata.Description,
			UserInvocable: metadata.UserInvocable,
			ArgumentHint:  metadata.ArgumentHint,
			Scope:         scope,
			Path:          filepath.Clean(path),
		})
	}
	return skills, nil
}

type Metadata struct {
	Name          string
	Description   string
	UserInvocable bool
	ArgumentHint  string
}

func parseMetadata(data []byte, fallbackName string) Metadata {
	text := string(data)
	frontMatter := parseFrontMatter(text)
	name := strings.TrimSpace(frontMatter["name"])
	description := strings.TrimSpace(frontMatter["description"])
	argumentHint := strings.TrimSpace(frontMatter["argument-hint"])
	userInvocable := parseBool(frontMatter["user-invocable"])
	if name == "" {
		name = firstMarkdownHeading(text)
	}
	if name == "" {
		name = fallbackName
	}
	if description == "" {
		description = firstBodyParagraph(text)
	}
	if description == "" {
		description = "No description provided."
	}
	return Metadata{
		Name:          name,
		Description:   truncateRunes(strings.Join(strings.Fields(description), " "), maxSkillDescriptionRunes),
		UserInvocable: userInvocable,
		ArgumentHint:  argumentHint,
	}
}

func parseSummary(data []byte, fallbackName string) (string, string) {
	metadata := parseMetadata(data, fallbackName)
	return metadata.Name, metadata.Description
}

func parseFrontMatter(text string) map[string]string {
	result := map[string]string{}
	if !strings.HasPrefix(text, "---") {
		return result
	}
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return result
	}
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) == "---" {
			return result
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if !isMetadataKey(key) {
			continue
		}
		if value == ">" || value == "|" {
			var block []string
			for index+1 < len(lines) {
				next := lines[index+1]
				if strings.TrimSpace(next) == "---" {
					break
				}
				if strings.TrimSpace(next) != "" && !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
					break
				}
				block = append(block, strings.TrimSpace(next))
				index++
			}
			if value == ">" {
				value = strings.Join(nonEmptyLines(block), " ")
			} else {
				value = strings.Join(block, "\n")
			}
		}
		result[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return map[string]string{}
}

func isMetadataKey(key string) bool {
	switch key {
	case "name", "description", "user-invocable", "argument-hint":
		return true
	default:
		return false
	}
}

func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "y", "1", "on":
		return true
	default:
		return false
	}
}

func firstMarkdownHeading(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func firstBodyParagraph(text string) string {
	if strings.HasPrefix(text, "---") {
		lines := strings.Split(text, "\n")
		for index, line := range lines[1:] {
			if strings.TrimSpace(line) == "---" {
				text = strings.Join(lines[index+2:], "\n")
				break
			}
		}
	}
	scanner := bytes.NewBufferString(text)
	for {
		line, err := scanner.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "#") {
			return line
		}
		if err != nil {
			return ""
		}
	}
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "/")
	value = strings.ReplaceAll(value, "\\", "/")
	return value
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
