package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cohert/internal/contextmgr"
	"cohert/internal/llm"
	"cohert/internal/session"
)

type contextRecordingClient struct {
	// responses 是测试预设的模型响应队列。
	responses []llm.Response
	// requests 记录 Runner 实际发给模型的请求，便于断言上下文内容。
	requests []llm.ChatRequest
}

func (c *contextRecordingClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.requests = append(c.requests, req)
	response := llm.Response{}
	if len(c.responses) > 0 {
		response = c.responses[0]
		c.responses = c.responses[1:]
	}
	out := make(chan llm.Event, 2)
	if response.Content != "" {
		out <- llm.Event{Type: llm.EventText, Text: response.Content}
	}
	out <- llm.Event{Type: llm.EventDone, Response: &response}
	close(out)
	return out, nil
}

type contextFakeTools struct {
	// result 是 fake 工具每次执行时返回给 Runner 的数据。
	result string
}

func (contextFakeTools) Schemas() []llm.ToolSchema {
	return nil
}

func (t contextFakeTools) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	return Outcome{Data: t.result, NextPrompt: "\n"}, nil
}

type evidenceRecordingTools struct {
	calls []ToolCallContext
}

func (t *evidenceRecordingTools) Schemas() []llm.ToolSchema {
	return nil
}

func (t *evidenceRecordingTools) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	t.calls = append(t.calls, call)
	if call.Name == "code_run" {
		return Outcome{Data: map[string]any{
			"status":    ToolStatusSuccess,
			"exit_code": 0,
		}}, nil
	}
	return Outcome{Data: map[string]any{"status": ToolStatusSuccess}}, nil
}

func TestRunnerInjectsWorkingCheckpoint_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "update_working_checkpoint",
					Arguments: `{"key_info":"按 browser SOP 先 wait 再 scan","related_sop":"sops/browser_sop.md"}`,
				},
			}}},
			{Content: "ok"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: `{"status":"success"}`},
		MaxTurns: 2,
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "测试 checkpoint", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1].Messages
	last := second[len(second)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Content, "[WORKING CHECKPOINT]") {
		t.Fatalf("last message = %#v, want working checkpoint", last)
	}
	if !strings.Contains(last.Content, "按 browser SOP 先 wait 再 scan") || !strings.Contains(last.Content, "sops/browser_sop.md") {
		t.Fatalf("checkpoint content = %q", last.Content)
	}
}

func TestRunnerAddsSOPRouteHint_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "ok"}},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
	}

	var out bytes.Buffer
	_, err := runner.Run(context.Background(), "帮我测试浏览器点击功能", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	messages := client.requests[0].Messages
	last := messages[len(messages)-1]
	if !strings.Contains(last.Content, "[SOP HINT]") || !strings.Contains(last.Content, "sops/browser_sop.md") || !strings.Contains(last.Content, "sops/testing_sop.md") {
		t.Fatalf("route hint = %q", last.Content)
	}
}

func TestRunnerRoutesMemorySOP_BitsUT(t *testing.T) {
	matches := routeSOPs("请优化长期记忆和 skill 能力等级，处理 SOP candidate 晋级")
	if len(matches) == 0 || matches[0] != "sops/memory_sop.md" {
		t.Fatalf("matches = %#v, want memory_sop first", matches)
	}
}

func TestRunnerRoutesDesktopSOP_BitsUT(t *testing.T) {
	matches := routeSOPs("请查看 macOS 桌面原生应用窗口，并读取 Accessibility AX 控件树")
	if !slices.Contains(matches, "sops/desktop_sop.md") {
		t.Fatalf("matches = %#v, want desktop_sop", matches)
	}
}

func TestRunnerRemindsCheckpointAfterSOPRead_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "file_read",
					Arguments: `{"path":"sops/browser_sop.md"}`,
				},
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "browser_scan",
					Arguments: `{}`,
				},
			}}},
			{Content: "ok"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: `{"status":"success"}`},
		MaxTurns: 3,
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "浏览器操作", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(client.requests))
	}
	third := client.requests[2].Messages
	last := third[len(third)-1]
	if !strings.Contains(last.Content, "上一轮读取了 SOP") || !strings.Contains(last.Content, "update_working_checkpoint") {
		t.Fatalf("checkpoint reminder = %q", last.Content)
	}
}

func TestRunnerInjectsRelatedSkillCheckpoint_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "update_working_checkpoint",
					Arguments: `{"key_info":"按 Go Test Skill 先跑 focused tests","related_skill":"project/go-test"}`,
				},
			}}},
			{Content: "ok"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: `{"status":"success"}`},
		MaxTurns: 2,
	}

	result, err := runner.Run(context.Background(), "测试 skill checkpoint", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if !strings.Contains(last.Content, "related_skill: project/go-test") ||
		!strings.Contains(last.Content, "skill_read") {
		t.Fatalf("checkpoint content = %q", last.Content)
	}
}

func TestRunnerRemindsCheckpointAfterSkillRead_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "skill_read",
					Arguments: `{"skill_id":"project/go-test"}`,
				},
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "code_run",
					Arguments: `{}`,
				},
			}}},
			{Content: "ok"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: `{"status":"success"}`},
		MaxTurns: 3,
	}

	result, err := runner.Run(context.Background(), "按 skill 执行测试", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	last := client.requests[2].Messages[len(client.requests[2].Messages)-1]
	if !strings.Contains(last.Content, "上一轮读取了 Skill") ||
		!strings.Contains(last.Content, "related_skill") {
		t.Fatalf("skill reminder = %q", last.Content)
	}
}

func TestRunnerPromptsLongTermMemoryAfterSuccessfulCodeRun_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "code_run",
					Arguments: `{"script":"go test ./..."}`,
				},
			}}},
			{Content: "tests passed"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: `{"status":"success","exit_code":0}`},
		MaxTurns: 2,
	}

	result, err := runner.Run(context.Background(), "运行测试", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if !strings.Contains(last.Content, "[LONG-TERM MEMORY HINT]") || !strings.Contains(last.Content, "成功的命令/测试验证") {
		t.Fatalf("long-term memory hint = %q", last.Content)
	}
}

func TestRunnerPromptsLongTermMemoryWhenUserRequestsIt_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "收到"}},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
	}

	result, err := runner.Run(context.Background(), "请记住我偏好简洁的中文回复", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	last := client.requests[0].Messages[len(client.requests[0].Messages)-1]
	if !strings.Contains(last.Content, "[LONG-TERM MEMORY HINT]") || !strings.Contains(last.Content, "用户明确要求保留经验") {
		t.Fatalf("long-term memory hint = %q", last.Content)
	}
}

func TestRunnerWritesRedactedRunLog_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-log",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "mcp_custom_send",
					Arguments: `{"token":"super-secret","text":"hello"}`,
				},
			}}},
			{Content: "done"},
		},
	}
	root := t.TempDir()
	store := session.NewStore(filepath.Join(root, "sessions"))
	runner := &Runner{
		Client:       client,
		Tools:        contextFakeTools{result: `{"status":"success"}`},
		MaxTurns:     2,
		LogDir:       filepath.Join(root, "logs"),
		SessionStore: &store,
	}

	if _, err := runner.Run(context.Background(), "record audit", NewConsoleSink(&bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(store.SessionDir(runner.SessionID()), runLogFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "super-secret") {
		t.Fatalf("run.log leaked secret: %s", content)
	}
	var entry runLogEntry
	if err := json.Unmarshal(bytes.TrimSpace(content), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Tool != "mcp_custom_send" || entry.Status != ToolStatusSuccess || entry.ArgsHash == "" {
		t.Fatalf("run log entry = %#v", entry)
	}
	if !strings.Contains(entry.ArgsSummary, "[redacted]") {
		t.Fatalf("args summary must redact token: %q", entry.ArgsSummary)
	}
}

func TestRunnerPassesVerifiedEvidenceLedgerToMemoryTools_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-run",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "code_run",
					Arguments: `{"script":"go test ./..."}`,
				},
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-memory",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "start_long_term_update",
					Arguments: `{"reason":"tests passed"}`,
				},
			}}},
			{Content: "done"},
		},
	}
	tools := &evidenceRecordingTools{}
	runner := &Runner{Client: client, Tools: tools, MaxTurns: 3}

	result, err := runner.Run(context.Background(), "运行测试", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(tools.calls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(tools.calls))
	}
	evidence := tools.calls[1].Evidence
	if len(evidence) != 2 {
		t.Fatalf("evidence entries = %d, want user + code_run: %#v", len(evidence), evidence)
	}
	codeRunEvidence := evidence[1]
	if codeRunEvidence.ID != "tool:1:0" || codeRunEvidence.ToolName != "code_run" || !codeRunEvidence.Verified {
		t.Fatalf("code_run evidence = %#v", codeRunEvidence)
	}
	if codeRunEvidence.Summary != "code_run completed with exit_code=0" {
		t.Fatalf("code_run evidence summary = %q", codeRunEvidence.Summary)
	}
}

func TestRunnerForcesLongTermMemoryReviewBeforeFinal_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-scan",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "browser_scan",
					Arguments: `{"tab_id":"1"}`,
				},
			}}},
			{Content: "任务已完成"},
			{ToolCalls: []llm.ToolCall{{
				ID:   "call-memory",
				Type: "function",
				Function: llm.ToolFunction{
					Name:      "start_long_term_update",
					Arguments: `{"reason":"browser workflow verified"}`,
				},
			}}},
			{Content: "done"},
		},
	}
	tools := &evidenceRecordingTools{}
	runner := &Runner{Client: client, Tools: tools, MaxTurns: 4}

	result, err := runner.Run(context.Background(), "在浏览器里操作飞书", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want done", result.Status)
	}
	if len(client.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(client.requests))
	}
	reviewPrompt := client.requests[2].Messages[len(client.requests[2].Messages)-1]
	if !strings.Contains(reviewPrompt.Content, "[LONG-TERM MEMORY FINAL REVIEW]") || !strings.Contains(reviewPrompt.Content, "start_long_term_update") {
		t.Fatalf("final review prompt = %q", reviewPrompt.Content)
	}
	if len(tools.calls) != 2 || tools.calls[1].Name != "start_long_term_update" {
		t.Fatalf("tool calls = %#v, want browser_scan then start_long_term_update", tools.calls)
	}
}

func TestRunnerUsesContextManagerForModelRequest_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "ok"}},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     3,
			KeepRecentToolResults:  1,
			MaxToolResultChars:     1000,
			CompactedToolHeadChars: 100,
			CompactedToolTailChars: 100,
			MaxRequestChars:        10000,
			ContextWindowTokens:    25,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "old user"},
			{Role: llm.RoleAssistant, Content: "old answer"},
			{Role: llm.RoleUser, Content: "middle user"},
		},
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "latest user", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	requestMessages := client.requests[0].Messages
	if len(requestMessages) != 3 {
		t.Fatalf("request messages = %d, want 3: %#v", len(requestMessages), requestMessages)
	}
	if requestMessages[0].Content != "[Cohert context notice] Earlier conversation messages were omitted from this request. Full history is preserved in history.jsonl." {
		t.Fatalf("first request message = %#v, want context notice", requestMessages[0])
	}
	if requestMessages[2].Content != "latest user" {
		t.Fatalf("latest user input was not preserved: %#v", requestMessages)
	}
	if len(runner.history) != 5 {
		t.Fatalf("runner history = %d, want full 5 messages", len(runner.history))
	}
	if runner.history[0].Content != "old user" {
		t.Fatalf("full history was modified: %#v", runner.history)
	}
}

func TestRunnerLogsContextStats_BitsUT(t *testing.T) {
	client := &contextRecordingClient{
		responses: []llm.Response{{Content: "ok"}},
	}
	logDir := t.TempDir()
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{},
		MaxTurns: 1,
		LogDir:   logDir,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     10,
			KeepRecentToolResults:  1,
			MaxToolResultChars:     1000,
			CompactedToolHeadChars: 100,
			CompactedToolTailChars: 100,
			MaxRequestChars:        10000,
			ContextWindowTokens:    1000000,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
	}

	_, err := runner.Run(context.Background(), "hello", NewConsoleSink(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(logDir, contextStatsLogFileName))
	if err != nil {
		t.Fatal(err)
	}
	logLine := string(data)
	for _, want := range []string{`"original_messages":1`, `"final_messages":1`, `"trigger_reason":"below_compact_trigger_threshold"`} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("context stats log does not contain %q:\n%s", want, logLine)
		}
	}
	if strings.Contains(logLine, "hello") {
		t.Fatalf("context stats log leaked message content:\n%s", logLine)
	}
}

func TestRunnerRebuildsContextAfterToolResult_BitsUT(t *testing.T) {
	longResult := strings.Repeat("A", 40) + strings.Repeat("Z", 40)
	client := &contextRecordingClient{
		responses: []llm.Response{
			{
				ToolCalls: []llm.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: llm.ToolFunction{
						Name:      "code_run",
						Arguments: `{}`,
					},
				}},
			},
			{Content: "done"},
		},
	}
	runner := &Runner{
		Client:   client,
		Tools:    contextFakeTools{result: longResult},
		MaxTurns: 2,
		ContextManager: &contextmgr.Manager{Config: contextmgr.Config{
			MaxHistoryMessages:     20,
			KeepRecentToolResults:  0,
			MaxToolResultChars:     30,
			CompactedToolHeadChars: 5,
			CompactedToolTailChars: 5,
			MaxRequestChars:        10000,
			ContextWindowTokens:    80,
			MaxOutputTokens:        0,
			SafetyTokens:           0,
			CompactTriggerRatio:    0.70,
			EnableMicroCompact:     true,
		}},
	}

	var out bytes.Buffer
	result, err := runner.Run(context.Background(), "run command", NewConsoleSink(&out))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunStatusDone {
		t.Fatalf("status = %q, want %q", result.Status, RunStatusDone)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	secondRequest := client.requests[1].Messages
	var compactedTool string
	for _, message := range secondRequest {
		if message.Role == llm.RoleTool {
			compactedTool = message.Content
		}
	}
	if !strings.Contains(compactedTool, "[tool result compacted]") {
		t.Fatalf("second request did not receive compacted tool result:\n%#v", secondRequest)
	}
	for _, message := range runner.history {
		if message.Role == llm.RoleTool && message.Content != longResult {
			t.Fatalf("full history tool result was modified: %#v", message)
		}
	}
}
