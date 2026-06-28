package action

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ActionNode 从 LLM 的非结构化输出中提取结构化数据。
// 这是 MetaGPT ActionNode 的 Go 版本。
//
// 核心思想：LLM 的输出是自然语言，可能嵌有代码块或 JSON。
// ActionNode 用两层策略提取：
//  1. 尝试直接解析为 JSON（如 ```json ... ``` 代码块）
//  2. 按 Schema 模板逐个字段用正则提取
//
// 使用示例：
//
//	node := NewActionNode(map[string]string{
//	    "title":   "PRD 标题",
//	    "features": "核心功能列表",
//	})
//	var prd struct {
//	    Title    string `json:"title"`
//	    Features string `json:"features"`
//	}
//	node.Fill(llmOutput, &prd)
type ActionNode struct {
	Schema map[string]string // 字段名 → 提取提示
}

// NewActionNode 创建一个结构化输出解析器。
// schema 的 key 是目标字段名，value 是该字段的描述（用于给 LLM 的提示）。
func NewActionNode(schema map[string]string) *ActionNode {
	return &ActionNode{Schema: schema}
}

// Fill 从 LLM 输出中提取结构化数据，填充到 target。
//
// 两条策略（按优先级）：
//  1. 尝试从输出中提取 JSON 块，直接解析到 target
//  2. 按 Schema 逐字段正则提取，再 JSON 映射到 target
//
// target 必须是一个指针，类型可以是 struct 或 map。
func (n *ActionNode) Fill(llmOutput string, target any) error {
	// 策略 1：尝试直接解析为 JSON
	if jsonStr := extractJSON(llmOutput); jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
			return nil
		}
		// JSON 解析失败 → 继续尝试策略 2
	}

	// 策略 2：按模板字段逐个提取
	if n.Schema == nil || len(n.Schema) == 0 {
		return fmt.Errorf("action node: no schema defined and no JSON found in output")
	}
	result := make(map[string]string, len(n.Schema))
	for field, hint := range n.Schema {
		extracted := extractField(llmOutput, field, hint)
		if extracted != "" {
			result[field] = extracted
		}
	}

	// map → JSON → target（利用 json.Unmarshal 做字段映射）
	data, _ := json.Marshal(result)
	return json.Unmarshal(data, target)
}

// extractJSON 从文本中提取 JSON 块。
// 查找顺序：```json ... ``` → ``` ... ``` 中含 JSON → 裸 {...}
func extractJSON(text string) string {
	// 策略 1：匹配 ```json ... ``` 代码块
	reJSONBlock := regexp.MustCompile("(?s)" + "```json" + "\\s*\\n(.*?)\\n\\s*" + "```")
	if matches := reJSONBlock.FindStringSubmatch(text); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// 策略 2：匹配 ``` ... ``` 中含有 { 的代码块
	reCodeBlock := regexp.MustCompile("(?s)" + "```" + "\\s*\\n(\\{.*?\\})\\s*\\n\\s*" + "```")
	if matches := reCodeBlock.FindStringSubmatch(text); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// 策略 3：裸 JSON 对象（简单嵌套）
	reBareJSON := regexp.MustCompile(`(?s)\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}`)
	if match := reBareJSON.FindString(text); match != "" {
		return strings.TrimSpace(match)
	}

	return ""
}

// extractField 根据字段名从文本中提取值。
// 匹配模式：
//   - "field: value" 或 "field：value"（注意：中文冒号也支持）
//   - "**field**: value"（Markdown 粗体）
func extractField(text, field, hint string) string {
	patterns := []string{
		fmt.Sprintf(`(?i)%s\s*[:：]\s*(.+?)(?:\n|$)`, regexp.QuoteMeta(field)),
		fmt.Sprintf(`(?i)\*\*%s\*\*\s*[:：]\s*(.+?)(?:\n|$)`, regexp.QuoteMeta(field)),
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if matches := re.FindStringSubmatch(text); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}
	return ""
}
