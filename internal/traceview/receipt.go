package traceview

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"cohort/internal/observability"
)

const ReceiptLedgerSchemaVersion = 1

const (
	UsageSourceProviderReported = "provider_reported"
	UsageSourceUnavailable      = "unavailable"
	EstimateSourceCharHeuristic = "context_char_heuristic"
)

// ReceiptLedger 把不可变运行事件聚合成逐轮 Provider 回执账本。
// Provider 实际回执与本地估算严格分栏，调用方不能把估算值冒充计费数据。
type ReceiptLedger struct {
	SchemaVersion     int               `json:"schema_version"`
	SessionID         string            `json:"session_id"`
	RunID             string            `json:"run_id"`
	EvidenceSource    string            `json:"evidence_source"`
	UsageSource       string            `json:"usage_source"`
	InputTokens       int64             `json:"input_tokens"`
	OutputTokens      int64             `json:"output_tokens"`
	TotalTokens       int64             `json:"total_tokens"`
	CacheReadTokens   int64             `json:"cache_read_tokens"`
	CacheWriteTokens  int64             `json:"cache_write_tokens"`
	EstimatedCostUSD  *float64          `json:"estimated_cost_usd,omitempty"`
	CostPricingSource string            `json:"cost_pricing_source"`
	ProviderTurns     int               `json:"provider_turns"`
	UnavailableTurns  int               `json:"unavailable_turns"`
	Receipts          []ProviderReceipt `json:"receipts"`
}

type ProviderReceipt struct {
	Turn                 int       `json:"turn"`
	ReceivedAt           time.Time `json:"received_at"`
	Status               string    `json:"status"`
	DurationMS           int64     `json:"duration_ms"`
	UsageSource          string    `json:"usage_source"`
	InputTokens          int64     `json:"input_tokens,omitempty"`
	OutputTokens         int64     `json:"output_tokens,omitempty"`
	TotalTokens          int64     `json:"total_tokens,omitempty"`
	CacheReadTokens      int64     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens     int64     `json:"cache_write_tokens,omitempty"`
	EstimatedInputTokens int64     `json:"estimated_input_tokens,omitempty"`
	EstimateSource       string    `json:"estimate_source,omitempty"`
	EstimatedCostUSD     *float64  `json:"estimated_cost_usd,omitempty"`
	RequestMessages      int64     `json:"request_messages,omitempty"`
	RequestChars         int64     `json:"request_chars,omitempty"`
	ToolSchemaCount      int64     `json:"tool_schema_count,omitempty"`
}

type receiptRequest struct {
	estimatedTokens int64
	messages        int64
	chars           int64
	tools           int64
}

// ReceiptLedger 返回一次 Run 的逐轮回执。它只使用已脱敏事件中的数值摘要。
func (v RunView) ReceiptLedger() ReceiptLedger {
	ledger := ReceiptLedger{
		SchemaVersion:     ReceiptLedgerSchemaVersion,
		SessionID:         v.SessionID,
		RunID:             v.RunID,
		EvidenceSource:    ObservationLogFileName,
		UsageSource:       UsageSourceUnavailable,
		CostPricingSource: "not_configured",
		Receipts:          []ProviderReceipt{},
	}
	events := append([]observability.Event(nil), v.Events...)
	sortEvents(events)
	requests := map[int]receiptRequest{}
	contextTokens := map[int]int64{}
	for _, event := range events {
		switch event.EventType {
		case observability.EventContextBuilt:
			contextTokens[event.Turn] = intValue(event.Data, "final_tokens")
		case observability.EventLLMRequestStarted:
			requests[event.Turn] = receiptRequest{
				estimatedTokens: contextTokens[event.Turn],
				messages:        intValue(event.Data, "message_count"),
				chars:           intValue(event.Data, "request_chars"),
				tools:           intValue(event.Data, "tool_schema_count"),
			}
		case observability.EventLLMResponseFinished:
			request := requests[event.Turn]
			receipt := ProviderReceipt{
				Turn:                 event.Turn,
				ReceivedAt:           event.Time,
				Status:               firstStringDefault(event.Data, "status", "unknown"),
				DurationMS:           intValue(event.Data, "duration_ms"),
				UsageSource:          UsageSourceUnavailable,
				EstimatedInputTokens: request.estimatedTokens,
				RequestMessages:      request.messages,
				RequestChars:         request.chars,
				ToolSchemaCount:      request.tools,
			}
			if receipt.EstimatedInputTokens > 0 {
				receipt.EstimateSource = EstimateSourceCharHeuristic
			}
			usage := valueMap(event.Data["usage"])
			if numericUsageAvailable(usage) {
				receipt.UsageSource = UsageSourceProviderReported
				receipt.InputTokens = intValue(usage, "input_tokens")
				receipt.OutputTokens = intValue(usage, "output_tokens")
				receipt.TotalTokens = intValue(usage, "total_tokens")
				receipt.CacheReadTokens = intValue(usage, "cache_read_input_tokens")
				receipt.CacheWriteTokens = intValue(usage, "cache_creation_input_tokens")
				if receipt.TotalTokens == 0 {
					receipt.TotalTokens = receipt.InputTokens + receipt.OutputTokens
				}
				ledger.ProviderTurns++
				ledger.InputTokens += receipt.InputTokens
				ledger.OutputTokens += receipt.OutputTokens
				ledger.TotalTokens += receipt.TotalTokens
				ledger.CacheReadTokens += receipt.CacheReadTokens
				ledger.CacheWriteTokens += receipt.CacheWriteTokens
			} else {
				ledger.UnavailableTurns++
			}
			ledger.Receipts = append(ledger.Receipts, receipt)
		}
	}
	if ledger.ProviderTurns > 0 {
		ledger.UsageSource = UsageSourceProviderReported
	}
	rates, configured := loadReceiptCostRates()
	if configured {
		ledger.CostPricingSource = "COHORT_COST_*_USD_PER_1M"
		total := receiptCost(ledger.InputTokens, ledger.OutputTokens, ledger.CacheWriteTokens, ledger.CacheReadTokens, rates)
		ledger.EstimatedCostUSD = &total
		for index := range ledger.Receipts {
			if ledger.Receipts[index].UsageSource != UsageSourceProviderReported {
				continue
			}
			cost := receiptCost(
				ledger.Receipts[index].InputTokens,
				ledger.Receipts[index].OutputTokens,
				ledger.Receipts[index].CacheWriteTokens,
				ledger.Receipts[index].CacheReadTokens,
				rates,
			)
			ledger.Receipts[index].EstimatedCostUSD = &cost
		}
	}
	return ledger
}

func sortEvents(events []observability.Event) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
}

func numericUsageAvailable(usage map[string]any) bool {
	if len(usage) == 0 {
		return false
	}
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		switch usage[key].(type) {
		case int, int64, float64, json.Number:
			return true
		}
	}
	return false
}

type receiptCostRates struct {
	input, output, cacheWrite, cacheRead float64
}

func loadReceiptCostRates() (receiptCostRates, bool) {
	input, inputOK := receiptEnvFloat("COHORT_COST_INPUT_USD_PER_1M")
	output, outputOK := receiptEnvFloat("COHORT_COST_OUTPUT_USD_PER_1M")
	cacheWrite, cacheWriteOK := receiptEnvFloat("COHORT_COST_CACHE_WRITE_USD_PER_1M")
	cacheRead, cacheReadOK := receiptEnvFloat("COHORT_COST_CACHE_READ_USD_PER_1M")
	return receiptCostRates{input: input, output: output, cacheWrite: cacheWrite, cacheRead: cacheRead},
		inputOK || outputOK || cacheWriteOK || cacheReadOK
}

func receiptEnvFloat(name string) (float64, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func receiptCost(input, output, cacheWrite, cacheRead int64, rates receiptCostRates) float64 {
	return (float64(input)*rates.input +
		float64(output)*rates.output +
		float64(cacheWrite)*rates.cacheWrite +
		float64(cacheRead)*rates.cacheRead) / 1_000_000
}
