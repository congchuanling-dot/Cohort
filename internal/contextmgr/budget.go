package contextmgr

import (
	"strings"

	"cohert/internal/llm"
)

const (
	defaultContextWindowTokens  = 1000000
	triggerReasonBelowThreshold = "below_compact_trigger_threshold"
	triggerReasonOverThreshold  = "over_compact_trigger_threshold"
	triggerReasonOverBudget     = "over_usable_input_budget"
)

var modelContextWindows = map[string]int{
	"deepseek-v4-pro": 1000000,
	"dsv4pro":         1000000,
	"dsv4-pro":        1000000,
	"dsv4":            1000000,
}

type budget struct {
	UsableInputTokens    int
	CompactTriggerTokens int
}

// ResolveContextWindowTokens 根据模型名返回上下文窗口。
// 如果配置里显式传入 configured，则优先使用配置值；否则使用内置模型映射。
func ResolveContextWindowTokens(model string, configured int) int {
	if configured > 0 {
		return configured
	}
	key := strings.ToLower(strings.TrimSpace(model))
	if value, ok := modelContextWindows[key]; ok {
		return value
	}
	return defaultContextWindowTokens
}

func newBudget(cfg Config) budget {
	usable := cfg.ContextWindowTokens - cfg.MaxOutputTokens - cfg.SafetyTokens
	if usable <= 0 {
		usable = cfg.ContextWindowTokens
	}
	trigger := int(float64(usable) * cfg.CompactTriggerRatio)
	if trigger <= 0 {
		trigger = usable
	}
	return budget{
		UsableInputTokens:    usable,
		CompactTriggerTokens: trigger,
	}
}

func messageChars(message llm.Message) int {
	total := len([]rune(message.Role)) +
		len([]rune(message.Content)) +
		len([]rune(message.ToolCallID)) +
		len([]rune(message.Name))
	for _, call := range message.ToolCalls {
		total += len([]rune(call.ID))
		total += len([]rune(call.Type))
		total += len([]rune(call.Function.Name))
		total += len([]rune(call.Function.Arguments))
	}
	return total
}

func messagesChars(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += messageChars(message)
	}
	return total
}

func estimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	tokens := chars / 2
	if chars%2 != 0 {
		tokens++
	}
	return tokens
}

func estimateTokens(messages []llm.Message) int {
	return estimateTokensFromChars(messagesChars(messages))
}
