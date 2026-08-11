package agent

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"cohort/internal/llm"
)

func TestAdaptiveToolRouterSelectsIntentGroupsAndSavesSchemaBytes_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	router := newAdaptiveToolRouter(AdaptiveToolRoutingConfig{
		Enabled:          true,
		MaxExternalTools: 4,
		MinSchemaCount:   1,
	}, "打开 https://example.com，检查网页上的登录按钮")

	selected, decision := router.Route(full)
	names := schemaNames(selected)
	for _, required := range []string{"file_read", "code_run", "browser_open", "browser_snapshot", "ask_user"} {
		if !slices.Contains(names, required) {
			t.Fatalf("selected=%#v, missing %s", names, required)
		}
	}
	for _, hidden := range []string{"desktop_windows", "computer_see", "lark_create_document"} {
		if slices.Contains(names, hidden) {
			t.Fatalf("selected=%#v, unrelated tool %s leaked", names, hidden)
		}
	}
	if decision.Mode != "adaptive" || decision.SelectedCount >= decision.FullSchemaCount || decision.SavedSchemaBytes <= 0 {
		t.Fatalf("decision=%#v, want reduced adaptive route", decision)
	}
}

func TestAdaptiveToolRouterSelectsRelevantExternalTools_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	router := newAdaptiveToolRouter(AdaptiveToolRoutingConfig{
		Enabled:          true,
		MaxExternalTools: 2,
		MinSchemaCount:   1,
	}, "帮我创建一个飞书文档")

	selected, decision := router.Route(full)
	names := schemaNames(selected)
	if !slices.Contains(names, "lark_create_document") {
		t.Fatalf("selected=%#v, want relevant Lark tool", names)
	}
	if slices.Contains(names, "mysql_query_database") {
		t.Fatalf("selected=%#v, unrelated database tool leaked", names)
	}
	if !slices.Contains(decision.SelectedExternal, "lark_create_document") {
		t.Fatalf("decision external=%#v", decision.SelectedExternal)
	}
}

func TestAdaptiveToolRouterEscalatesAfterCapabilityStopOrFailures_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	router := newAdaptiveToolRouter(AdaptiveToolRoutingConfig{
		Enabled:          true,
		FailureThreshold: 2,
		MinSchemaCount:   1,
	}, "分析当前项目")
	selected, _ := router.Route(full)
	if !router.ShouldEscalateNoTool("当前缺少工具，无法操作这个外部系统", len(selected), len(full)) {
		t.Fatal("capability limitation did not request escalation")
	}
	router.Escalate("capability_limitation")
	escalated, decision := router.Route(full)
	if len(escalated) != len(full) || !decision.Escalated || decision.Reason != "capability_limitation" {
		t.Fatalf("decision=%#v selected=%d/%d", decision, len(escalated), len(full))
	}

	failureRouter := newAdaptiveToolRouter(AdaptiveToolRoutingConfig{
		Enabled:          true,
		FailureThreshold: 2,
		MinSchemaCount:   1,
	}, "分析当前项目")
	failureRouter.ObserveToolResult(false)
	if _, decision := failureRouter.Route(full); decision.Escalated {
		t.Fatal("router escalated before failure threshold")
	}
	failureRouter.ObserveToolResult(false)
	if _, decision := failureRouter.Route(full); !decision.Escalated || decision.Reason != "repeated_tool_failures" {
		t.Fatalf("decision=%#v, want failure escalation", decision)
	}
}

func TestRunnerAdaptiveToolRoutingEscalatesCapabilityStop_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	client := &contextRecordingClient{responses: []llm.Response{
		{Content: "当前没有可用工具，无法操作这个外部系统。"},
		{Content: "已使用完整工具面重新检查。"},
	}}
	runner := &Runner{
		Client:   client,
		Tools:    adaptiveRoutingFakeTools{schemas: full},
		MaxTurns: 2,
		AdaptiveToolRouting: AdaptiveToolRoutingConfig{
			Enabled:        true,
			MinSchemaCount: 1,
		},
	}
	result, err := runner.Run(context.Background(), "分析这个需求", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone || len(client.requests) != 2 {
		t.Fatalf("result=%#v requests=%d", result, len(client.requests))
	}
	if len(client.requests[0].Tools) >= len(full) {
		t.Fatalf("first request tools=%d, want reduced from %d", len(client.requests[0].Tools), len(full))
	}
	if !strings.Contains(client.requests[0].System, "[Adaptive Tool Route]") ||
		!strings.Contains(client.requests[0].System, "缺少工具能力") {
		t.Fatalf("first request system prompt missing route contract: %q", client.requests[0].System)
	}
	if len(client.requests[1].Tools) != len(full) {
		t.Fatalf("second request tools=%d, want full %d after escalation", len(client.requests[1].Tools), len(full))
	}
	if !strings.Contains(client.requests[1].System, "完整工具面") {
		t.Fatalf("second request system prompt missing escalation state: %q", client.requests[1].System)
	}
}

func TestRunnerAdaptiveToolRoutingEscalatesRepeatedFailures_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	client := &contextRecordingClient{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{
			{ID: "call-1", Type: "function", Function: llm.ToolFunction{Name: "file_read", Arguments: `{"path":"missing-a"}`}},
			{ID: "call-2", Type: "function", Function: llm.ToolFunction{Name: "file_read", Arguments: `{"path":"missing-b"}`}},
		}},
		{Content: "已切换完整工具面排查。"},
	}}
	runner := &Runner{
		Client:   client,
		Tools:    adaptiveRoutingFakeTools{schemas: full, fail: true},
		MaxTurns: 2,
		AdaptiveToolRouting: AdaptiveToolRoutingConfig{
			Enabled:          true,
			MinSchemaCount:   1,
			FailureThreshold: 2,
		},
	}
	result, err := runner.Run(context.Background(), "检查缺失文件", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone || len(client.requests) != 2 {
		t.Fatalf("result=%#v requests=%d", result, len(client.requests))
	}
	if len(client.requests[1].Tools) != len(full) {
		t.Fatalf("second request tools=%d, want full %d after failures", len(client.requests[1].Tools), len(full))
	}
}

func TestRunnerAdaptiveToolRoutingIsDisabledForIsolatedModes_BitsUT(t *testing.T) {
	full := adaptiveRouterFixtureSchemas()
	for _, mode := range []RunMode{RunModeEval, RunModeRepair, RunModeExplorer} {
		t.Run(string(mode), func(t *testing.T) {
			client := &contextRecordingClient{responses: []llm.Response{{Content: "done"}}}
			runner := &Runner{
				Client:   client,
				Tools:    adaptiveRoutingFakeTools{schemas: full},
				MaxTurns: 1,
				RunMode:  mode,
				AdaptiveToolRouting: AdaptiveToolRoutingConfig{
					Enabled:        true,
					MinSchemaCount: 1,
				},
			}
			if _, err := runner.Run(context.Background(), "isolated run", NewConsoleSink(&bytes.Buffer{})); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 1 || len(client.requests[0].Tools) != len(full) {
				t.Fatalf("mode=%s tools=%d, want full %d", mode, len(client.requests[0].Tools), len(full))
			}
		})
	}
}

type adaptiveRoutingFakeTools struct {
	schemas []llm.ToolSchema
	fail    bool
}

func (t adaptiveRoutingFakeTools) Schemas() []llm.ToolSchema {
	return append([]llm.ToolSchema(nil), t.schemas...)
}

func (t adaptiveRoutingFakeTools) Run(context.Context, ToolCallContext) (Outcome, error) {
	if t.fail {
		return Outcome{Data: NewToolError("fixture_failure", "fixture failed", "retry")}, nil
	}
	return Outcome{Data: map[string]any{"status": ToolStatusSuccess}}, nil
}

func adaptiveRouterFixtureSchemas() []llm.ToolSchema {
	names := []string{
		"file_read", "file_write", "file_patch", "code_run",
		"lsp_diagnostics", "lsp_symbols",
		"update_working_checkpoint", "start_long_term_update", "memory_propose_update", "memory_apply_update",
		"skill_read", "ask_user",
		"browser_open", "browser_snapshot", "browser_click_element", "browser_type_element",
		"desktop_windows", "desktop_ax_snapshot", "desktop_click",
		"computer_see", "computer_find", "computer_execute_step",
		"lark_create_document", "lark_search_document", "mysql_query_database",
	}
	var result []llm.ToolSchema
	for _, name := range names {
		description := "Local Cohort tool for " + name
		switch name {
		case "lark_create_document":
			description = "Create a Feishu Lark document."
		case "lark_search_document":
			description = "Search Feishu Lark documents."
		case "mysql_query_database":
			description = "Query a MySQL database."
		}
		result = append(result, llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
			Name:        name,
			Description: description + fmt.Sprintf(" fixture-%d", len(result)),
			Parameters:  map[string]any{"type": "object"},
		}})
	}
	return result
}

func schemaNames(schemas []llm.ToolSchema) []string {
	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Function.Name)
	}
	return names
}
