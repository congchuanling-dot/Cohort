package agent

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"cohort/internal/llm"
)

const (
	defaultAdaptiveToolMaxExternal      = 8
	defaultAdaptiveToolFailureThreshold = 2
	defaultAdaptiveToolMinSchemas       = 20
)

// AdaptiveToolRoutingConfig 控制按任务意图渐进暴露工具 schema。
// 它只优化模型可见面，不替代 ToolPolicyRunner 的执行权限校验。
type AdaptiveToolRoutingConfig struct {
	Enabled          bool
	MaxExternalTools int
	FailureThreshold int
	MinSchemaCount   int
	Language         string
}

func (c AdaptiveToolRoutingConfig) normalize() AdaptiveToolRoutingConfig {
	if c.MaxExternalTools <= 0 {
		c.MaxExternalTools = defaultAdaptiveToolMaxExternal
	}
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = defaultAdaptiveToolFailureThreshold
	}
	if c.MinSchemaCount <= 0 {
		c.MinSchemaCount = defaultAdaptiveToolMinSchemas
	}
	return c
}

// ToolRouteDecision 是每轮工具路由的可观测摘要，不包含用户 prompt。
type ToolRouteDecision struct {
	Mode             string   `json:"mode"`
	Reason           string   `json:"reason"`
	FullSchemaCount  int      `json:"full_schema_count"`
	SelectedCount    int      `json:"selected_count"`
	SelectedGroups   []string `json:"selected_groups,omitempty"`
	SelectedExternal []string `json:"selected_external,omitempty"`
	FullSchemaBytes  int      `json:"full_schema_bytes"`
	SelectedBytes    int      `json:"selected_bytes"`
	SavedSchemaBytes int      `json:"saved_schema_bytes"`
	Escalated        bool     `json:"escalated"`
	FailureCount     int      `json:"failure_count,omitempty"`
}

type adaptiveToolRouter struct {
	config              AdaptiveToolRoutingConfig
	input               string
	escalated           bool
	escalationReason    string
	consecutiveFailures int
}

func newAdaptiveToolRouter(config AdaptiveToolRoutingConfig, input string) *adaptiveToolRouter {
	return &adaptiveToolRouter{
		config: config.normalize(),
		input:  strings.TrimSpace(input),
	}
}

// PlanAdaptiveToolRoute 提供不启动 LLM 的路由预览，供 CLI、测试和调优工具复用。
func PlanAdaptiveToolRoute(
	config AdaptiveToolRoutingConfig,
	input string,
	full []llm.ToolSchema,
) ([]llm.ToolSchema, ToolRouteDecision) {
	return newAdaptiveToolRouter(config, input).Route(full)
}

func (r *adaptiveToolRouter) Route(full []llm.ToolSchema) ([]llm.ToolSchema, ToolRouteDecision) {
	if r == nil {
		return full, newToolRouteDecision(full, full, "full", "router_unavailable", nil, nil, false, 0)
	}
	if !r.config.Enabled {
		return full, newToolRouteDecision(full, full, "full", "disabled", allToolGroups(full), nil, false, r.consecutiveFailures)
	}
	if len(full) <= r.config.MinSchemaCount {
		return full, newToolRouteDecision(full, full, "full", "small_tool_surface", allToolGroups(full), nil, false, r.consecutiveFailures)
	}
	if r.escalated {
		reason := r.escalationReason
		if reason == "" {
			reason = "progressive_escalation"
		}
		return full, newToolRouteDecision(full, full, "full", reason, allToolGroups(full), nil, true, r.consecutiveFailures)
	}

	selectedGroups := map[string]bool{
		"core":   true,
		"lsp":    true,
		"memory": true,
		"skill":  true,
		"ask":    true,
	}
	normalizedInput := strings.ToLower(r.input)
	if containsAny(normalizedInput, browserIntentTerms) {
		selectedGroups["browser"] = true
	}
	if containsAny(normalizedInput, desktopIntentTerms) {
		selectedGroups["desktop"] = true
		selectedGroups["computer"] = true
	}

	external := selectRelevantExternalTools(full, normalizedInput, r.config.MaxExternalTools)
	externalSet := make(map[string]bool, len(external))
	for _, name := range external {
		externalSet[name] = true
	}
	selected := make([]llm.ToolSchema, 0, len(full))
	for _, schema := range full {
		group := toolRouteGroup(schema.Function.Name)
		if selectedGroups[group] || group == "external" && externalSet[schema.Function.Name] {
			selected = append(selected, schema)
		}
	}
	reason := "baseline"
	if len(selectedGroups) > 5 || len(external) > 0 {
		reason = "intent_match"
	}
	groups := sortedEnabledGroups(selectedGroups)
	if len(external) > 0 {
		groups = append(groups, "external")
		sort.Strings(groups)
	}
	return selected, newToolRouteDecision(
		full,
		selected,
		"adaptive",
		reason,
		groups,
		external,
		false,
		r.consecutiveFailures,
	)
}

func (r *adaptiveToolRouter) ObserveToolResult(succeeded bool) {
	if r == nil || r.escalated {
		return
	}
	if succeeded {
		r.consecutiveFailures = 0
		return
	}
	r.consecutiveFailures++
	if r.consecutiveFailures >= r.config.FailureThreshold {
		r.Escalate("repeated_tool_failures")
	}
}

func (r *adaptiveToolRouter) ShouldEscalateNoTool(content string, selectedCount int, fullCount int) bool {
	if r == nil || !r.config.Enabled || r.escalated || selectedCount >= fullCount {
		return false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	return containsAny(normalized, capabilityLimitationTerms)
}

func (r *adaptiveToolRouter) Escalate(reason string) {
	if r == nil {
		return
	}
	r.escalated = true
	r.escalationReason = strings.TrimSpace(reason)
}

func newToolRouteDecision(
	full []llm.ToolSchema,
	selected []llm.ToolSchema,
	mode string,
	reason string,
	groups []string,
	external []string,
	escalated bool,
	failures int,
) ToolRouteDecision {
	fullBytes := toolSchemasBytes(full)
	selectedBytes := toolSchemasBytes(selected)
	return ToolRouteDecision{
		Mode:             mode,
		Reason:           reason,
		FullSchemaCount:  len(full),
		SelectedCount:    len(selected),
		SelectedGroups:   append([]string(nil), groups...),
		SelectedExternal: append([]string(nil), external...),
		FullSchemaBytes:  fullBytes,
		SelectedBytes:    selectedBytes,
		SavedSchemaBytes: max(0, fullBytes-selectedBytes),
		Escalated:        escalated,
		FailureCount:     failures,
	}
}

func toolRouteSystemPrompt(decision ToolRouteDecision, language string) string {
	english := strings.EqualFold(strings.TrimSpace(language), "en")
	switch {
	case decision.Mode == "adaptive":
		if english {
			return "\n\n[Adaptive Tool Route]\nThis request exposes only task-relevant tool groups: " +
				strings.Join(decision.SelectedGroups, ", ") +
				". Hidden tools are not permanently unavailable. If the task truly requires a capability outside the current schema, explicitly state \"missing tool capability\" so the runtime can retry with the full tool surface. Call only tools present in this request."
		}
		return "\n\n[Adaptive Tool Route]\n本轮只暴露与当前任务相关的工具组：" +
			strings.Join(decision.SelectedGroups, ", ") +
			"。未显示的工具不是永久不可用；如果任务确实需要当前 schema 外的能力，请明确说明“缺少工具能力”，Runtime 会在下一轮升级完整工具面。只能调用本轮 schema 中存在的工具。"
	case decision.Escalated:
		if english {
			return "\n\n[Adaptive Tool Route]\nThe full tool surface is now exposed after a capability limitation or repeated failures. Re-check the current schema and continue without repeating identical failed arguments."
		}
		return "\n\n[Adaptive Tool Route]\n因前序能力不足或连续失败，本轮已升级为完整工具面。请重新检查当前 schema 并继续任务，不要重复使用已经失败的相同参数。"
	default:
		return ""
	}
}

func toolSchemasBytes(schemas []llm.ToolSchema) int {
	data, err := json.Marshal(schemas)
	if err != nil {
		return 0
	}
	return len(data)
}

func toolRouteGroup(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(name, "file_"), name == "code_run":
		return "core"
	case strings.HasPrefix(name, "lsp_"):
		return "lsp"
	case strings.HasPrefix(name, "browser_"):
		return "browser"
	case strings.HasPrefix(name, "desktop_"):
		return "desktop"
	case strings.HasPrefix(name, "computer_"):
		return "computer"
	case name == "skill_read":
		return "skill"
	case name == "ask_user":
		return "ask"
	case name == "update_working_checkpoint",
		name == "start_long_term_update",
		strings.HasPrefix(name, "memory_"):
		return "memory"
	default:
		return "external"
	}
}

func allToolGroups(schemas []llm.ToolSchema) []string {
	groups := map[string]bool{}
	for _, schema := range schemas {
		groups[toolRouteGroup(schema.Function.Name)] = true
	}
	return sortedEnabledGroups(groups)
}

func sortedEnabledGroups(groups map[string]bool) []string {
	result := make([]string, 0, len(groups))
	for group, enabled := range groups {
		if enabled {
			result = append(result, group)
		}
	}
	sort.Strings(result)
	return result
}

type externalToolScore struct {
	name  string
	score int
}

func selectRelevantExternalTools(schemas []llm.ToolSchema, input string, limit int) []string {
	if limit <= 0 || strings.TrimSpace(input) == "" {
		return nil
	}
	inputTokens := semanticTokens(input)
	var scores []externalToolScore
	for _, schema := range schemas {
		if toolRouteGroup(schema.Function.Name) != "external" {
			continue
		}
		name := strings.ToLower(schema.Function.Name)
		description := strings.ToLower(schema.Function.Description)
		score := 0
		if strings.Contains(input, name) {
			score += 100
		}
		nameTokens := semanticTokens(strings.ReplaceAll(name, "_", " "))
		descriptionTokens := semanticTokens(description)
		for token := range inputTokens {
			if nameTokens[token] {
				score += 4
			} else if descriptionTokens[token] {
				score++
			}
		}
		for _, aliases := range externalDomainAliases {
			if containsAny(input, aliases) && containsAny(name+" "+description, aliases) {
				score += 8
			}
		}
		if score >= 2 {
			scores = append(scores, externalToolScore{name: schema.Function.Name, score: score})
		}
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].name < scores[j].name
		}
		return scores[i].score > scores[j].score
	})
	if len(scores) > limit {
		scores = scores[:limit]
	}
	result := make([]string, 0, len(scores))
	for _, item := range scores {
		result = append(result, item.name)
	}
	sort.Strings(result)
	return result
}

func semanticTokens(value string) map[string]bool {
	tokens := map[string]bool{}
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		token := strings.ToLower(string(current))
		current = current[:0]
		if isHanToken(token) {
			runes := []rune(token)
			if len(runes) >= 2 && len(runes) <= 12 {
				tokens[token] = true
			}
			for i := 0; i+2 <= len(runes); i++ {
				tokens[string(runes[i:i+2])] = true
			}
			return
		}
		if len(token) >= 3 && !toolRouteStopWords[token] {
			tokens[token] = true
		}
	}
	for _, char := range []rune(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			current = append(current, char)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func isHanToken(value string) bool {
	for _, char := range value {
		if unicode.Is(unicode.Han, char) {
			return true
		}
	}
	return false
}

func containsAny(value string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

var browserIntentTerms = []string{
	"http://", "https://", " url", "url ", "website", "web page", "webpage", "browser",
	"frontend ui", "playwright", "网页", "网站", "浏览器", "页面", "链接", "抓取网页", "前端页面",
}

var desktopIntentTerms = []string{
	"desktop", "computer use", "macos app", "native app", "application window", "screen automation",
	"gui automation", "桌面", "电脑操作", "原生应用", "应用窗口", "屏幕", "系统设置", "键盘操作",
}

var capabilityLimitationTerms = []string{
	"没有可用工具", "缺少工具", "工具不可用", "无法访问", "无法操作", "不能访问", "不能操作",
	"no available tool", "missing tool", "tool is unavailable", "tools are unavailable",
	"cannot access", "can't access", "cannot interact", "can't interact",
}

var externalDomainAliases = [][]string{
	{"feishu", "lark", "飞书"},
	{"slack"},
	{"github", "gitlab"},
	{"database", "mysql", "postgres", "redis", "数据库"},
	{"calendar", "日历", "日程"},
	{"email", "mail", "邮件"},
	{"document", "docs", "文档"},
	{"spreadsheet", "sheet", "表格"},
}

var toolRouteStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true,
	"that": true, "tool": true, "call": true, "use": true, "using": true, "local": true,
	"current": true, "result": true, "input": true, "output": true, "data": true,
}
