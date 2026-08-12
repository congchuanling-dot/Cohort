package agent

import "strings"

const defaultToolFailureCircuitThreshold = 2

type toolFailureCircuit struct {
	threshold int
	failures  map[string]int
}

type toolCircuitDecision struct {
	PolicyID      string
	Action        string
	Reason        string
	Tool          string
	ArgumentsHash string
	FailureCount  int
	Threshold     int
}

func newToolFailureCircuit(threshold int) *toolFailureCircuit {
	if threshold <= 0 {
		threshold = defaultToolFailureCircuitThreshold
	}
	return &toolFailureCircuit{threshold: threshold, failures: map[string]int{}}
}

func (c *toolFailureCircuit) Before(tool, argumentsHash string) (toolCircuitDecision, bool) {
	if c == nil {
		return toolCircuitDecision{}, false
	}
	key := toolCircuitKey(tool, argumentsHash)
	count := c.failures[key]
	if count < c.threshold {
		return toolCircuitDecision{}, false
	}
	return toolCircuitDecision{
		PolicyID:      "tool.repeated_identical_failure",
		Action:        "circuit_break",
		Reason:        "相同工具和参数已连续失败，阻止无证据的重复执行",
		Tool:          strings.TrimSpace(tool),
		ArgumentsHash: strings.TrimSpace(argumentsHash),
		FailureCount:  count,
		Threshold:     c.threshold,
	}, true
}

func (c *toolFailureCircuit) Observe(tool, argumentsHash string, succeeded bool) {
	if c == nil {
		return
	}
	key := toolCircuitKey(tool, argumentsHash)
	if succeeded {
		delete(c.failures, key)
		return
	}
	c.failures[key]++
}

func toolCircuitKey(tool, argumentsHash string) string {
	return strings.TrimSpace(tool) + "\x00" + strings.TrimSpace(argumentsHash)
}

func governanceInterventionData(decision toolCircuitDecision) map[string]any {
	return map[string]any{
		"policy_id":     decision.PolicyID,
		"action":        decision.Action,
		"reason":        decision.Reason,
		"tool":          decision.Tool,
		"args_hash":     decision.ArgumentsHash,
		"failure_count": decision.FailureCount,
		"threshold":     decision.Threshold,
		"enforcement":   "enforced",
	}
}
