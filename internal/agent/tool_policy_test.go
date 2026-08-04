package agent

import (
	"context"
	"testing"

	"cohort/internal/llm"
)

type policyFakeTools struct {
	calls []string
}

func (t *policyFakeTools) Schemas() []llm.ToolSchema {
	return []llm.ToolSchema{
		{Type: "function", Function: llm.FunctionSchema{Name: "file_read"}},
		{Type: "function", Function: llm.FunctionSchema{Name: "code_run"}},
	}
}

func (t *policyFakeTools) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	t.calls = append(t.calls, call.Name)
	return Outcome{Data: map[string]any{"status": ToolStatusSuccess}}, nil
}

func TestToolPolicyRunnerFiltersSchemasAndDeniesRuntimeCall_BitsUT(t *testing.T) {
	base := &policyFakeTools{}
	runner := ToolPolicyRunner{
		Base: base,
		Policy: ToolPolicy{
			Name:       "skill:test",
			AllowTools: []string{"file_read"},
		},
	}
	schemas := runner.Schemas()
	if len(schemas) != 1 || schemas[0].Function.Name != "file_read" {
		t.Fatalf("schemas = %#v, want only file_read", schemas)
	}
	if _, err := runner.Run(context.Background(), ToolCallContext{Name: "file_read"}); err != nil {
		t.Fatal(err)
	}
	outcome, err := runner.Run(context.Background(), ToolCallContext{Name: "code_run"})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := outcome.Data.(ToolErrorData)
	if !ok || data.Code != "tool_denied_by_active_policy" {
		t.Fatalf("outcome = %#v, want policy denial", outcome)
	}
	if len(base.calls) != 1 || base.calls[0] != "file_read" {
		t.Fatalf("base calls = %#v, want only allowed call", base.calls)
	}
}
