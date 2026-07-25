package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/mcp"
)

type recordingMCPPrompter struct {
	decision mcpPermissionDecision
	calls    int
}

func (p *recordingMCPPrompter) Prompt(_ context.Context, _, _, _ string) (mcpPermissionDecision, error) {
	p.calls++
	return p.decision, nil
}

func TestMCPToolAsksOnceThenCachesSessionPermission(t *testing.T) {
	var calls int
	server := newToolMCPServer(t, "send_message", func(_ map[string]any) map[string]any {
		calls++
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "sent"}},
		}
	})
	defer server.Close()

	manager := mcp.NewManager()
	manager.Load(context.Background(), []mcp.ServerConfig{{
		Name: "lark", Type: mcp.TransportHTTP, URL: server.URL,
	}})
	defer manager.Close()
	registered := manager.Tools()
	if len(registered) != 1 {
		t.Fatalf("registered = %#v", registered)
	}
	prompter := &recordingMCPPrompter{decision: "allow_session"}
	tool := NewMCPTool(registered[0], manager, NewMCPPermissionStore(), prompter)
	for i := 0; i < 2; i++ {
		outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
			Args: map[string]any{"text": "hello"},
		})
		if err != nil {
			t.Fatal(err)
		}
		data := outcome.Data.(map[string]any)
		if data["status"] != agent.ToolStatusSuccess || data["content"] != "sent" {
			t.Fatalf("outcome = %#v", outcome)
		}
		if data["untrusted_external_content"] != true {
			t.Fatalf("MCP outcome must mark result untrusted: %#v", data)
		}
	}
	if prompter.calls != 1 {
		t.Fatalf("permission prompts = %d, want 1", prompter.calls)
	}
	if calls != 2 {
		t.Fatalf("MCP calls = %d, want 2", calls)
	}
}

func TestMCPToolRefusesHighRiskToolWithoutCallingServer(t *testing.T) {
	var calls int
	server := newToolMCPServer(t, "delete_file", func(_ map[string]any) map[string]any {
		calls++
		return map[string]any{}
	})
	defer server.Close()

	manager := mcp.NewManager()
	manager.Load(context.Background(), []mcp.ServerConfig{{
		Name: "lark", Type: mcp.TransportHTTP, URL: server.URL,
	}})
	defer manager.Close()
	tool := NewMCPTool(manager.Tools()[0], manager, NewMCPPermissionStore(), &recordingMCPPrompter{
		decision: mcpPermissionAllow,
	})
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := outcome.Data.(agent.ToolErrorData)
	if !ok || data.Code != "mcp_tool_permission_required" {
		t.Fatalf("outcome = %#v", outcome)
	}
	if calls != 0 {
		t.Fatalf("high-risk MCP tool calls = %d, want 0", calls)
	}
}

func TestMCPToolProjectPermissionMatchesExactArguments(t *testing.T) {
	var calls int
	server := newToolMCPServer(t, "send_message", func(_ map[string]any) map[string]any {
		calls++
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "sent"}},
		}
	})
	defer server.Close()

	manager := mcp.NewManager()
	manager.Load(context.Background(), []mcp.ServerConfig{{
		Name: "custom", Type: mcp.TransportHTTP, URL: server.URL,
	}})
	defer manager.Close()
	registered := manager.Tools()[0]

	store := mcp.NewStore(t.TempDir())
	permissions, storeErr := NewProjectMCPPermissionStore(store)
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	firstPrompter := &recordingMCPPrompter{decision: mcpPermissionAllowProject}
	firstTool := NewMCPTool(registered, manager, permissions, firstPrompter)
	args := map[string]any{"recipient": "self", "text": "hello"}
	if _, runErr := firstTool.Run(context.Background(), agent.ToolCallContext{Args: args}); runErr != nil {
		t.Fatal(runErr)
	}
	if firstPrompter.calls != 1 || calls != 1 {
		t.Fatalf("initial prompt/calls = %d/%d, want 1/1", firstPrompter.calls, calls)
	}

	// 新建授权缓存模拟下次启动，确认授权确实来自项目文件而非内存 session。
	reloadedPermissions, reloadErr := NewProjectMCPPermissionStore(store)
	if reloadErr != nil {
		t.Fatal(reloadErr)
	}
	secondPrompter := &recordingMCPPrompter{decision: mcpPermissionDeny}
	secondTool := NewMCPTool(registered, manager, reloadedPermissions, secondPrompter)
	if _, runErr := secondTool.Run(context.Background(), agent.ToolCallContext{Args: args}); runErr != nil {
		t.Fatal(runErr)
	}
	if secondPrompter.calls != 0 || calls != 2 {
		t.Fatalf("matching grant prompt/calls = %d/%d, want 0/2", secondPrompter.calls, calls)
	}

	differentArgs := map[string]any{"recipient": "self", "text": "different"}
	outcome, runErr := secondTool.Run(context.Background(), agent.ToolCallContext{Args: differentArgs})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if _, denied := outcome.Data.(agent.ToolErrorData); !denied {
		t.Fatalf("different arguments should require a new decision: %#v", outcome)
	}
	if secondPrompter.calls != 1 || calls != 2 {
		t.Fatalf("different args prompt/calls = %d/%d, want 1/2", secondPrompter.calls, calls)
	}
}

func TestTruncateMCPResult(t *testing.T) {
	content, truncated := truncateMCPResult(string(make([]byte, maxMCPToolResultChars+100)))
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(content) <= maxMCPToolResultChars {
		t.Fatalf("content length = %d, want marker-preserved expanded result", len(content))
	}
}

func newToolMCPServer(
	t *testing.T,
	toolName string,
	call func(map[string]any) map[string]any,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeToolMCPResponse(t, w, request.ID, map[string]any{})
		case "tools/list":
			writeToolMCPResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        toolName,
					"description": toolName,
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
		case "tools/call":
			var params struct {
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			writeToolMCPResponse(t, w, request.ID, call(params.Arguments))
		default:
			t.Fatalf("unexpected MCP method %q", request.Method)
		}
	}))
}

func writeToolMCPResponse(t *testing.T, writer http.ResponseWriter, id int, result any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatal(err)
	}
}
