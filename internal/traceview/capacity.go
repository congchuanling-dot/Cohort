package traceview

import (
	"math"

	"cohort/internal/contextmgr"
	"cohort/internal/observability"
)

const ContextCapacitySchemaVersion = 1

type ContextCapacityReport struct {
	SchemaVersion      int                        `json:"schema_version"`
	SessionID          string                     `json:"session_id"`
	RunID              string                     `json:"run_id"`
	Model              string                     `json:"model"`
	Capability         contextmgr.ModelCapability `json:"capability"`
	State              string                     `json:"state"`
	MaxOccupancyRatio  float64                    `json:"max_occupancy_ratio"`
	Calibration        ContextCalibration         `json:"calibration"`
	Turns              []ContextTurn              `json:"turns"`
	RecommendedActions []string                   `json:"recommended_actions"`
}

type ContextCalibration struct {
	Samples            int     `json:"samples"`
	AverageActualRatio float64 `json:"average_actual_ratio,omitempty"`
	LastActualRatio    float64 `json:"last_actual_ratio,omitempty"`
}

type ContextTurn struct {
	Turn                   int                `json:"turn"`
	Build                  int                `json:"build"`
	EstimatedInputTokens   int64              `json:"estimated_input_tokens"`
	ProviderInputTokens    int64              `json:"provider_input_tokens,omitempty"`
	EffectiveInputTokens   int64              `json:"effective_input_tokens"`
	MeasurementSource      string             `json:"measurement_source"`
	ContextWindowTokens    int64              `json:"context_window_tokens"`
	UsableInputTokens      int64              `json:"usable_input_tokens"`
	CompactTriggerTokens   int64              `json:"compact_trigger_tokens"`
	OccupancyRatio         float64            `json:"occupancy_ratio"`
	State                  string             `json:"state"`
	TriggerReason          string             `json:"trigger_reason,omitempty"`
	OriginalMessages       int64              `json:"original_messages"`
	FinalMessages          int64              `json:"final_messages"`
	TrimmedMessages        int64              `json:"trimmed_messages"`
	CompactedToolResults   int64              `json:"compacted_tool_results"`
	InjectedMemory         bool               `json:"injected_memory"`
	InjectedCompactSummary bool               `json:"injected_compact_summary"`
	Waterfall              []ContextWaterfall `json:"waterfall"`
}

type ContextWaterfall struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Tokens int64  `json:"tokens"`
}

// ContextCapacity 重建每次请求的容量决策，并用 Provider input_tokens 反校准字符估算。
func (v RunView) ContextCapacity(model string) ContextCapacityReport {
	report := ContextCapacityReport{
		SchemaVersion: ContextCapacitySchemaVersion,
		SessionID:     v.SessionID,
		RunID:         v.RunID,
		Model:         model,
		Capability:    contextmgr.ResolveModelCapability(model),
		State:         "healthy",
		Turns:         []ContextTurn{},
	}
	receipts := v.ReceiptLedger()
	providerByTurn := map[int]int64{}
	for _, receipt := range receipts.Receipts {
		if receipt.UsageSource == UsageSourceProviderReported && receipt.InputTokens > 0 {
			providerByTurn[receipt.Turn] = receipt.InputTokens
		}
	}
	events := append([]observability.Event(nil), v.Events...)
	sortEvents(events)
	builds := map[int]int{}
	var ratios []float64
	for _, event := range events {
		if event.EventType != observability.EventContextBuilt {
			continue
		}
		builds[event.Turn]++
		capability := capacityCapability(event, report.Capability, model)
		report.Capability = capability
		turn := contextTurnFromEvent(event, builds[event.Turn], capability, providerByTurn[event.Turn])
		report.Turns = append(report.Turns, turn)
		report.MaxOccupancyRatio = math.Max(report.MaxOccupancyRatio, turn.OccupancyRatio)
		report.State = worseCapacityState(report.State, turn.State)
		if turn.ProviderInputTokens > 0 && turn.EstimatedInputTokens > 0 {
			ratio := float64(turn.ProviderInputTokens) / float64(turn.EstimatedInputTokens)
			ratios = append(ratios, ratio)
			report.Calibration.LastActualRatio = ratio
		}
	}
	report.Calibration.Samples = len(ratios)
	for _, ratio := range ratios {
		report.Calibration.AverageActualRatio += ratio
	}
	if len(ratios) > 0 {
		report.Calibration.AverageActualRatio /= float64(len(ratios))
	}
	report.RecommendedActions = capacityRecommendations(report)
	return report
}

func capacityCapability(event observability.Event, fallback contextmgr.ModelCapability, model string) contextmgr.ModelCapability {
	window := intValue(event.Data, "context_window_tokens")
	if window <= 0 {
		return fallback
	}
	return contextmgr.ModelCapability{
		Model:               model,
		ContextWindowTokens: int(window),
		Source:              firstStringDefault(event.Data, "context_window_source", fallback.Source),
		Version:             firstStringDefault(event.Data, "capability_version", fallback.Version),
		Confidence:          firstStringDefault(event.Data, "capability_confidence", fallback.Confidence),
	}
}

func contextTurnFromEvent(event observability.Event, build int, capability contextmgr.ModelCapability, providerInput int64) ContextTurn {
	estimated := intValue(event.Data, "final_tokens")
	usable := intValue(event.Data, "usable_input_tokens")
	window := intValue(event.Data, "context_window_tokens")
	if window <= 0 {
		window = int64(capability.ContextWindowTokens)
	}
	if usable <= 0 {
		usable = window
	}
	effective := estimated
	source := "estimated"
	if providerInput > 0 {
		effective = providerInput
		source = UsageSourceProviderReported
	}
	ratio := float64(0)
	if usable > 0 {
		ratio = float64(effective) / float64(usable)
	}
	turn := ContextTurn{
		Turn:                 event.Turn,
		Build:                build,
		EstimatedInputTokens: estimated,
		ProviderInputTokens:  providerInput,
		EffectiveInputTokens: effective,
		MeasurementSource:    source,
		ContextWindowTokens:  window,
		UsableInputTokens:    usable,
		CompactTriggerTokens: intValue(event.Data, "compact_trigger_tokens"),
		OccupancyRatio:       ratio,
		State:                capacityState(ratio),
		TriggerReason:        firstString(event.Data, "trigger_reason"),
		OriginalMessages:     intValue(event.Data, "original_messages"),
		FinalMessages:        intValue(event.Data, "final_messages"),
		TrimmedMessages:      intValue(event.Data, "trimmed_messages"),
		CompactedToolResults: intValue(event.Data, "compacted_tool_results"),
		InjectedMemory: boolValue(event.Data, "injected_session_memory") ||
			boolValue(event.Data, "injected_relevant_memory"),
		InjectedCompactSummary: boolValue(event.Data, "injected_compact_summary"),
	}
	turn.Waterfall = buildContextWaterfall(event.Data, estimated, providerInput)
	return turn
}

func buildContextWaterfall(data map[string]any, finalEstimate, providerInput int64) []ContextWaterfall {
	items := []ContextWaterfall{
		{Kind: "base", Label: "Original history", Tokens: intValue(data, "original_tokens")},
	}
	appendChars := func(kind, label, key string) {
		if tokens := estimateTokensForChars(intValue(data, key)); tokens > 0 {
			items = append(items, ContextWaterfall{Kind: kind, Label: label, Tokens: tokens})
		}
	}
	appendChars("injected", "Session memory", "session_memory_chars")
	appendChars("injected", "Relevant memory", "relevant_memory_chars")
	appendChars("injected", "Compact summary", "compact_summary_chars")
	if tokens := estimateTokensForChars(intValue(data, "omitted_tool_result_chars")); tokens > 0 {
		items = append(items, ContextWaterfall{Kind: "removed", Label: "Micro compact removed", Tokens: -tokens})
	}
	items = append(items, ContextWaterfall{Kind: "estimate", Label: "Final local estimate", Tokens: finalEstimate})
	if providerInput > 0 {
		items = append(items, ContextWaterfall{Kind: "provider", Label: "Provider reported input", Tokens: providerInput})
	}
	return items
}

func estimateTokensForChars(chars int64) int64 {
	if chars <= 0 {
		return 0
	}
	return (chars + 1) / 2
}

func capacityState(ratio float64) string {
	switch {
	case ratio >= 1:
		return "blocked"
	case ratio >= 0.9:
		return "critical"
	case ratio >= 0.7:
		return "warning"
	default:
		return "healthy"
	}
}

func worseCapacityState(current, next string) string {
	rank := map[string]int{"healthy": 0, "warning": 1, "critical": 2, "blocked": 3}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func capacityRecommendations(report ContextCapacityReport) []string {
	var actions []string
	switch report.State {
	case "blocked":
		actions = append(actions, "阻断下一次 LLM 请求，先执行 Full Compact 或降低工具 Schema。")
	case "critical":
		actions = append(actions, "在下一轮前执行 Full Compact，并保留最近工具结果。")
	case "warning":
		actions = append(actions, "启用 Micro Compact，优先压缩旧工具结果。")
	}
	if report.Capability.Confidence == "unknown" {
		actions = append(actions, "为当前模型登记经过验证的 Context Window，当前使用 128K 保守兜底。")
	}
	if report.Calibration.Samples > 0 &&
		(report.Calibration.AverageActualRatio > 1.25 || report.Calibration.AverageActualRatio < 0.75) {
		actions = append(actions, "Provider 回执与本地估算偏差超过 25%，应按回执校准容量预警。")
	}
	return actions
}
