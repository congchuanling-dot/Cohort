package contextmgr

import (
	"strings"

	"cohort/internal/llm"
)

const (
	// defaultContextWindowTokens 是未知模型的兜底窗口。
	// 未知模型不能继承某个内部模型的 1M 能力，否则会在没有证据时放大超窗风险。
	defaultContextWindowTokens  = 128000
	triggerReasonBelowThreshold = "below_compact_trigger_threshold"
	triggerReasonOverThreshold  = "over_compact_trigger_threshold"
	triggerReasonOverBudget     = "over_usable_input_budget"
)

const modelCapabilityRegistryVersion = "2026-08-12"

type ModelCapability struct {
	Model               string `json:"model"`
	ContextWindowTokens int    `json:"context_window_tokens"`
	Source              string `json:"source"`
	Version             string `json:"version"`
	Confidence          string `json:"confidence"`
}

var modelCapabilities = map[string]ModelCapability{
	"deepseek-v4-pro":   {ContextWindowTokens: 1000000, Source: "cohort_builtin_override", Confidence: "configured"},
	"dsv4pro":           {ContextWindowTokens: 1000000, Source: "cohort_builtin_override", Confidence: "configured"},
	"dsv4-pro":          {ContextWindowTokens: 1000000, Source: "cohort_builtin_override", Confidence: "configured"},
	"dsv4":              {ContextWindowTokens: 1000000, Source: "cohort_builtin_override", Confidence: "configured"},
	"deepseek-chat":     {ContextWindowTokens: 128000, Source: "cohort_model_registry", Confidence: "documented"},
	"deepseek-reasoner": {ContextWindowTokens: 128000, Source: "cohort_model_registry", Confidence: "documented"},
}

type budget struct {
	// UsableInputTokens 是本轮输入消息可使用的最大 token 数。
	// 它等于模型窗口扣掉输出预留和安全余量。
	UsableInputTokens int

	// CompactTriggerTokens 是触发压缩的阈值。
	// 默认是 UsableInputTokens * 0.70。
	CompactTriggerTokens int
}

// ResolveContextWindowTokens 根据模型名返回上下文窗口。
// 模型 API 通常不提供稳定的上下文窗口接口，因此这里使用内置模型表。
func ResolveContextWindowTokens(model string) int {
	return ResolveModelCapability(model).ContextWindowTokens
}

// ResolveModelCapability 返回带来源和版本的模型能力，供运行时和控制面共同解释容量依据。
func ResolveModelCapability(model string) ModelCapability {
	key := strings.ToLower(strings.TrimSpace(model))
	if capability, ok := modelCapabilities[key]; ok {
		capability.Model = strings.TrimSpace(model)
		capability.Version = modelCapabilityRegistryVersion
		return capability
	}
	return ModelCapability{
		Model:               strings.TrimSpace(model),
		ContextWindowTokens: defaultContextWindowTokens,
		Source:              "conservative_fallback",
		Version:             modelCapabilityRegistryVersion,
		Confidence:          "unknown",
	}
}

// newBudget 计算本轮请求的输入预算和压缩触发线。
//
// 例子：
//
//	ContextWindowTokens = 1000000
//	MaxOutputTokens     = 4096
//	SafetyTokens        = 4000
//
// 则：
//
//	UsableInputTokens = 1000000 - 4096 - 4000 = 991904
//	CompactTriggerTokens = 991904 * 0.70
//
// Build 会在 estimated_input_tokens >= CompactTriggerTokens 时才开始压缩。
func newBudget(cfg Config) budget {
	usable := cfg.ContextWindowTokens - cfg.MaxOutputTokens - cfg.SafetyTokens
	if usable <= 0 {
		// 配置异常时不要算出负预算，否则会导致每轮都进入裁剪。
		// 这里退回到模型窗口本身，至少保证行为可用。
		usable = cfg.ContextWindowTokens
	}
	trigger := int(float64(usable) * cfg.CompactTriggerRatio)
	if trigger <= 0 {
		// CompactTriggerRatio 异常时，让触发线等于可用预算。
		// Normalize 正常会兜底，这里是最后一道防御。
		trigger = usable
	}
	return budget{
		UsableInputTokens:    usable,
		CompactTriggerTokens: trigger,
	}
}

// messageChars 估算单条消息占用的字符数。
//
// 这里不只看 Content，还要把 role、tool_call_id、tool name、tool call arguments 等协议字段算进去。
// 虽然这不是精确 tokenizer，但比只算 Content 更接近实际请求大小。
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

// messagesChars 汇总一组 messages 的估算字符数。
func messagesChars(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += messageChars(message)
	}
	return total
}

// estimateTokensFromChars 用字符数粗略估算 token 数。
//
// 当前第一版没有引入模型 tokenizer，先用 chars / 2 做稳定的近似。
// 这个估算偏粗，所以预算里还保留了 SafetyTokens。
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

// estimateTokens 估算一组 messages 的 token 数。
func estimateTokens(messages []llm.Message) int {
	return estimateTokensFromChars(messagesChars(messages))
}
