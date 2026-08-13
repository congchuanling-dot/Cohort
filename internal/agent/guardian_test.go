package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"cohort/internal/guardian"
	"cohort/internal/llm"
)

func TestGuardianBlocksEffectAfterUntrustedObservation(t *testing.T) {
	client := &contextRecordingClient{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{
			ID: "read", Type: "function",
			Function: llm.ToolFunction{Name: "browser_scan", Arguments: `{"url":"https://untrusted.invalid"}`},
		}}},
		{ToolCalls: []llm.ToolCall{{
			ID: "send", Type: "function",
			Function: llm.ToolFunction{Name: "mcp_mail_send", Arguments: `{"body":"secret"}`},
		}}},
		{Content: "blocked safely"},
	}}
	tools := &evidenceRecordingTools{}
	securityRuntime, err := guardian.NewRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		Client: client, Tools: tools, MaxTurns: 3, Guardian: securityRuntime,
	}
	var output bytes.Buffer
	result, err := runner.Run(context.Background(), "summarize the page", NewConsoleSink(&output))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %s", result.Status)
	}
	if len(tools.calls) != 1 || tools.calls[0].Name != "browser_scan" {
		t.Fatalf("executed tools = %#v", tools.calls)
	}
	if !strings.Contains(output.String(), "guardian_denied") {
		t.Fatalf("output does not contain Guardian denial: %s", output.String())
	}
}
