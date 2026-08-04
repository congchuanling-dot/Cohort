package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"cohort/internal/llm"
)

// ToolPolicy constrains which tools are visible and executable for a focused run.
type ToolPolicy struct {
	Name       string
	AllowTools []string
	DenyTools  []string
}

// Empty returns whether the policy has no effective restrictions.
func (p ToolPolicy) Empty() bool {
	return len(cleanToolList(p.AllowTools)) == 0 && len(cleanToolList(p.DenyTools)) == 0
}

// ToolPolicyRunner wraps another ToolRunner with allow/deny enforcement.
type ToolPolicyRunner struct {
	Base   ToolRunner
	Policy ToolPolicy
}

func (r ToolPolicyRunner) Schemas() []llm.ToolSchema {
	if r.Base == nil {
		return nil
	}
	schemas := r.Base.Schemas()
	if r.Policy.Empty() {
		return schemas
	}
	out := make([]llm.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		if r.allowed(schema.Function.Name) {
			out = append(out, schema)
		}
	}
	return out
}

func (r ToolPolicyRunner) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	if r.Base == nil {
		return Outcome{}, fmt.Errorf("tool policy runner has no base runner")
	}
	if !r.allowed(call.Name) {
		allowed := cleanToolList(r.Policy.AllowTools)
		if len(allowed) == 0 {
			allowed = []string{"all tools except: " + strings.Join(cleanToolList(r.Policy.DenyTools), ", ")}
		}
		return Outcome{
			Data: NewToolError(
				"tool_denied_by_active_policy",
				fmt.Sprintf("tool %s is denied by active policy %s", call.Name, r.Policy.Name),
				"Use only tools allowed by the active Skill policy, or stop and ask the user to change the Skill permissions.",
			),
			NextPrompt: "当前 active policy 禁止调用 " + call.Name + "；允许范围：" + strings.Join(allowed, ", "),
			Audit: map[string]any{
				"policy": r.Policy.Name,
				"tool":   call.Name,
				"status": ToolStatusError,
			},
		}, nil
	}
	return r.Base.Run(ctx, call)
}

func (r ToolPolicyRunner) allowed(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, denied := range cleanToolList(r.Policy.DenyTools) {
		if denied == name {
			return false
		}
	}
	allowed := cleanToolList(r.Policy.AllowTools)
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == name {
			return true
		}
	}
	return false
}

func cleanToolList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
