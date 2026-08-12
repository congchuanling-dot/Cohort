package traceview

import (
	"fmt"
	"strings"
	"time"

	"cohort/internal/observability"
)

const GovernanceSchemaVersion = 1

type GovernanceReport struct {
	SchemaVersion int                      `json:"schema_version"`
	PolicyVersion string                   `json:"policy_version"`
	SessionID     string                   `json:"session_id"`
	RunID         string                   `json:"run_id"`
	State         string                   `json:"state"`
	Policies      []GovernancePolicy       `json:"policies"`
	Interventions []GovernanceIntervention `json:"interventions"`
}

type GovernancePolicy struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Threshold   string `json:"threshold"`
	Action      string `json:"action"`
}

type GovernanceIntervention struct {
	ID          string          `json:"id"`
	PolicyID    string          `json:"policy_id"`
	Turn        int             `json:"turn,omitempty"`
	Time        time.Time       `json:"time,omitempty"`
	Action      string          `json:"action"`
	Enforcement string          `json:"enforcement"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	Evidence    []GraphEvidence `json:"evidence"`
}

func (v RunView) Governance(model string) GovernanceReport {
	report := GovernanceReport{
		SchemaVersion: GovernanceSchemaVersion,
		PolicyVersion: "2026-08-12",
		SessionID:     v.SessionID,
		RunID:         v.RunID,
		State:         "clear",
		Policies: []GovernancePolicy{
			{ID: "context.capacity", Description: "按 Provider 回执或本地估算控制上下文占用", Enabled: true, Threshold: "warn=70%, compact=90%, block=100%", Action: "compact_or_block"},
			{ID: "tool.repeated_identical_failure", Description: "阻止相同工具与参数无证据重复失败", Enabled: true, Threshold: "2 failures", Action: "circuit_break"},
			{ID: "tool.route_escalation", Description: "能力不足或连续失败后升级工具面", Enabled: true, Threshold: "runtime router decision", Action: "expose_full_surface"},
			{ID: "permission.gate", Description: "外部或高风险工具必须通过权限门禁", Enabled: true, Threshold: "risk policy", Action: "allow_ask_or_deny"},
		},
		Interventions: []GovernanceIntervention{},
	}
	events := append([]observability.Event(nil), v.Events...)
	sortEvents(events)
	for _, event := range events {
		switch event.EventType {
		case observability.EventGovernanceIntervention:
			report.Interventions = append(report.Interventions, interventionFromEvent(event))
		case observability.EventContextBuilt:
			if contextChanged(event.Data) {
				report.Interventions = append(report.Interventions, GovernanceIntervention{
					ID:          interventionID(event, "context"),
					PolicyID:    "context.capacity",
					Turn:        event.Turn,
					Time:        event.Time,
					Action:      contextAction(event.Data),
					Enforcement: "enforced",
					Status:      "applied",
					Reason:      firstStringDefault(event.Data, "trigger_reason", "context changed"),
					Evidence:    eventEvidence(event, "context governance"),
				})
			}
		case observability.EventToolRouteSelected:
			if graphBool(event.Data, "escalated") || graphString(event.Data, "mode") == "escalating" {
				report.Interventions = append(report.Interventions, GovernanceIntervention{
					ID:          interventionID(event, "route"),
					PolicyID:    "tool.route_escalation",
					Turn:        event.Turn,
					Time:        event.Time,
					Action:      "expose_full_surface",
					Enforcement: "enforced",
					Status:      "applied",
					Reason:      firstStringDefault(event.Data, "reason", "tool route escalation"),
					Evidence:    eventEvidence(event, "tool route escalation"),
				})
			}
		case observability.EventPermissionDecision:
			decision := graphStringDefault(event.Data, "permission_decision", "unknown")
			if decision != "allow" {
				report.Interventions = append(report.Interventions, GovernanceIntervention{
					ID:          interventionID(event, "permission"),
					PolicyID:    "permission.gate",
					Turn:        event.Turn,
					Time:        event.Time,
					Action:      decision,
					Enforcement: "enforced",
					Status:      "applied",
					Reason:      fmt.Sprintf("risk=%s external=%t", graphString(event.Data, "risk"), graphBool(event.Data, "external")),
					Evidence:    eventEvidence(event, "permission decision"),
				})
			}
		}
	}
	capacity := v.ContextCapacity(model)
	if capacity.State == "critical" || capacity.State == "blocked" {
		lastTurn := 0
		if len(capacity.Turns) > 0 {
			lastTurn = capacity.Turns[len(capacity.Turns)-1].Turn
		}
		action := "full_compact"
		if capacity.State == "blocked" {
			action = "block_next_request"
		}
		report.Interventions = append(report.Interventions, GovernanceIntervention{
			ID:          fmt.Sprintf("capacity-%s-%d", capacity.State, lastTurn),
			PolicyID:    "context.capacity",
			Turn:        lastTurn,
			Action:      action,
			Enforcement: "recommended",
			Status:      "pending",
			Reason:      fmt.Sprintf("maximum usable-input occupancy %.1f%%", capacity.MaxOccupancyRatio*100),
			Evidence: []GraphEvidence{{
				Type: "capacity_report", Ref: v.RunID, Label: "provider-calibrated context capacity",
			}},
		})
	}
	for _, item := range report.Interventions {
		if item.Status == "pending" {
			report.State = "action_required"
			break
		}
		if report.State == "clear" {
			report.State = "intervened"
		}
	}
	return report
}

func interventionFromEvent(event observability.Event) GovernanceIntervention {
	return GovernanceIntervention{
		ID:          interventionID(event, "runtime"),
		PolicyID:    firstStringDefault(event.Data, "policy_id", "runtime.policy"),
		Turn:        event.Turn,
		Time:        event.Time,
		Action:      firstStringDefault(event.Data, "action", "intervene"),
		Enforcement: firstStringDefault(event.Data, "enforcement", "enforced"),
		Status:      "applied",
		Reason:      firstStringDefault(event.Data, "reason", "runtime governance decision"),
		Evidence:    eventEvidence(event, "governance intervention"),
	}
}

func contextAction(data map[string]any) string {
	switch {
	case intValue(data, "trimmed_messages") > 0:
		return "trim_history"
	case intValue(data, "compacted_tool_results") > 0:
		return "micro_compact"
	case boolValue(data, "injected_compact_summary"):
		return "inject_compact_summary"
	case boolValue(data, "injected_session_memory") || boolValue(data, "injected_relevant_memory"):
		return "inject_memory"
	default:
		return "capacity_guard"
	}
}

func interventionID(event observability.Event, suffix string) string {
	id := strings.TrimSpace(event.EventID)
	if id == "" {
		id = fmt.Sprintf("%s-%d-%d", event.EventType, event.Turn, event.Time.UnixNano())
	}
	return id + "-" + suffix
}
