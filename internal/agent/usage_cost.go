package agent

import (
	"os"
	"strconv"

	"cohort/internal/llm"
)

type costEstimate struct {
	USD    float64
	Source string
	OK     bool
}

func mergeUsageTotals(total *llm.Usage, next llm.Usage) {
	if total == nil {
		return
	}
	total.InputTokens += next.InputTokens
	total.OutputTokens += next.OutputTokens
	if next.TotalTokens > 0 {
		total.TotalTokens += next.TotalTokens
	}
	total.CacheCreationInputTokens += next.CacheCreationInputTokens
	total.CacheReadInputTokens += next.CacheReadInputTokens
}

func usageSummary(usage llm.Usage) map[string]any {
	return map[string]any{
		"input_tokens":                usage.InputTokens,
		"output_tokens":               usage.OutputTokens,
		"total_tokens":                usage.NormalizedTotal(),
		"cache_creation_input_tokens": usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     usage.CacheReadInputTokens,
	}
}

func estimateUsageCost(usage llm.Usage) costEstimate {
	inputRate, inputOK := envFloat("COHORT_COST_INPUT_USD_PER_1M")
	outputRate, outputOK := envFloat("COHORT_COST_OUTPUT_USD_PER_1M")
	cacheWriteRate, cacheWriteOK := envFloat("COHORT_COST_CACHE_WRITE_USD_PER_1M")
	cacheReadRate, cacheReadOK := envFloat("COHORT_COST_CACHE_READ_USD_PER_1M")
	if !inputOK && !outputOK && !cacheWriteOK && !cacheReadOK {
		return costEstimate{Source: "not_configured"}
	}
	total := float64(usage.InputTokens)*inputRate/1_000_000 +
		float64(usage.OutputTokens)*outputRate/1_000_000 +
		float64(usage.CacheCreationInputTokens)*cacheWriteRate/1_000_000 +
		float64(usage.CacheReadInputTokens)*cacheReadRate/1_000_000
	return costEstimate{USD: total, Source: "COHORT_COST_*_USD_PER_1M", OK: true}
}

func envFloat(name string) (float64, bool) {
	value := os.Getenv(name)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}
