package agent

import (
	"cohort/internal/guardian"
	"cohort/internal/observability"
)

func guardianBlocks(decision guardian.Decision) bool {
	return decision.Action == guardian.ActionDeny ||
		decision.Action == guardian.ActionRequireDeclassification
}

func guardianBlockedOutcome(decision guardian.Decision) Outcome {
	code := "guardian_denied"
	hint := "Do not retry the same call. Remove the untrusted dependency or use an explicitly authorized, sanitized derivative."
	if decision.Action == guardian.ActionRequireDeclassification {
		code = "guardian_declassification_required"
		hint = "Secret-tainted data cannot leave the local boundary. Request explicit declassification of a bounded derivative."
	}
	return Outcome{
		Data:       NewToolError(code, decision.Reason, hint),
		NextPrompt: "\n",
		Audit:      mergeGuardianAudit(nil, decision, decision.InputLabel),
	}
}

func mergeGuardianAudit(audit map[string]any, decision guardian.Decision, label guardian.Label) map[string]any {
	result := make(map[string]any, len(audit)+7)
	for name, value := range audit {
		result[name] = value
	}
	result["guardian_action"] = decision.Action
	result["guardian_rule_id"] = decision.RuleID
	result["guardian_policy_hash"] = decision.PolicyHash
	result["guardian_contract_hash"] = decision.ContractHash
	result["guardian_confidentiality"] = label.Confidentiality
	result["guardian_integrity"] = label.Integrity
	result["guardian_sources"] = append([]string(nil), label.Sources...)
	return result
}

func guardianDecisionData(decision guardian.Decision, toolCallID string, index int) map[string]any {
	return map[string]any{
		"tool":          decision.Tool,
		"tool_call_id":  toolCallID,
		"index":         index,
		"action":        decision.Action,
		"rule_id":       decision.RuleID,
		"reason":        decision.Reason,
		"policy_hash":   decision.PolicyHash,
		"contract_hash": decision.ContractHash,
		"input_label":   decision.InputLabel,
		"output_label":  decision.OutputLabel,
	}
}

func guardianSeverity(decision guardian.Decision) observability.Severity {
	switch decision.Action {
	case guardian.ActionDeny, guardian.ActionRequireDeclassification:
		return observability.SeverityError
	case guardian.ActionAsk, guardian.ActionForkIsolated, guardian.ActionSanitize:
		return observability.SeverityWarn
	default:
		return observability.SeverityInfo
	}
}
