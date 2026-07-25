package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOpenHTTPClientListsAndCallsTools(t *testing.T) {
	var methods []string
	var sessionHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("MCP-Protocol-Version"); got != protocolVersion {
			t.Errorf("protocol version = %q", got)
		}
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
		if request.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "session-1")
		} else {
			sessionHeader = r.Header.Get("Mcp-Session-Id")
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeTestResponse(t, w, request.ID, map[string]any{"protocolVersion": protocolVersion})
		case "tools/list":
			writeTestResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "read_note",
					"description": "Read a note",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"id": map[string]any{"type": "string"}},
					},
				}},
			})
		case "tools/call":
			writeTestResponse(t, w, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "hello from MCP"}},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client, err := Open(context.Background(), ServerConfig{
		Name: "docs",
		Type: TransportHTTP,
		URL:  server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "read_note" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.CallTool(context.Background(), "read_note", map[string]any{"id": "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello from MCP" || result.IsError {
		t.Fatalf("result = %#v", result)
	}
	if sessionHeader != "session-1" {
		t.Fatalf("session header = %q, want session-1", sessionHeader)
	}
	if got := strings.Join(methods, ","); got != "initialize,notifications/initialized,tools/list,tools/call" {
		t.Fatalf("methods = %s", got)
	}
}

func TestOpenStdioClientListsAndCallsTools(t *testing.T) {
	if os.Getenv("COHERT_MCP_HELPER") == "1" {
		runMCPStdioHelper(t)
		return
	}
	client, err := Open(context.Background(), ServerConfig{
		Name:    "test",
		Type:    TransportStdio,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestOpenStdioClientListsAndCallsTools"},
		Env:     map[string]string{"COHERT_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.CallTool(context.Background(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "echo: hello" {
		t.Fatalf("result = %#v", result)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHTTPClientReadsStreamableHTTPEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeTestResponse(t, w, request.ID, map[string]any{})
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			response, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"tools": []map[string]any{{"name": "streamed"}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", response)
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client, err := Open(context.Background(), ServerConfig{Name: "sse", Type: TransportHTTP, URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "streamed" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestOpenHTTPClientListsAllToolPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeTestResponse(t, w, request.ID, map[string]any{})
		case "tools/list":
			var params struct {
				Cursor string `json:"cursor"`
			}
			paramsJSON, err := json.Marshal(request.Params)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(paramsJSON, &params); err != nil {
				t.Fatal(err)
			}
			if params.Cursor == "" {
				writeTestResponse(t, w, request.ID, map[string]any{
					"tools":      []map[string]any{{"name": "first"}},
					"nextCursor": "page-2",
				})
				return
			}
			if params.Cursor == "page-2" {
				writeTestResponse(t, w, request.ID, map[string]any{
					"tools": []map[string]any{{"name": "second"}},
				})
				return
			}
			t.Fatalf("unexpected tools/list cursor %q", params.Cursor)
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client, err := Open(context.Background(), ServerConfig{Name: "paged", Type: TransportHTTP, URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestManagerReloadReplacesDiscoveredToolSnapshot(t *testing.T) {
	toolName := "first_tool"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeTestResponse(t, w, request.ID, map[string]any{})
		case "tools/list":
			writeTestResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{"name": toolName}},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	configs := []ServerConfig{{Name: "custom", Type: TransportHTTP, URL: server.URL}}
	manager := NewManager()
	manager.Load(context.Background(), configs)
	if tools := manager.Tools(); len(tools) != 1 || tools[0].CohertID != "mcp_custom_first_tool" {
		t.Fatalf("initial tools = %#v", tools)
	}

	toolName = "second_tool"
	manager.Reload(context.Background(), configs)
	defer manager.Close()
	if tools := manager.Tools(); len(tools) != 1 || tools[0].CohertID != "mcp_custom_second_tool" {
		t.Fatalf("reloaded tools = %#v", tools)
	}
}

func TestStoreMergesScopesWithLocalPrecedence(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	store := NewStore(root)
	store.HomeDir = func() (string, error) { return home, nil }
	for scope, command := range map[Scope]string{
		ScopeUser:    "user-server",
		ScopeProject: "project-server",
		ScopeLocal:   "local-server",
	} {
		if err := store.Add(scope, ServerConfig{
			Name:    "shared",
			Type:    TransportStdio,
			Command: command,
		}); err != nil {
			t.Fatal(err)
		}
	}
	servers, err := store.LoadEffective()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Command != "local-server" {
		t.Fatalf("servers = %#v", servers)
	}
	projectPath, err := store.Path(ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"mcpServers"`) {
		t.Fatalf("project config does not use .mcp.json format: %s", content)
	}
}

func TestToolNameNormalizesExternalNames(t *testing.T) {
	if got := ToolName("Lark-Docs", "read.note"); got != "mcp_lark_docs_read_note" {
		t.Fatalf("tool name = %q", got)
	}
}

func writeTestResponse(t *testing.T, writer http.ResponseWriter, id int, result any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatal(err)
	}
}

func runMCPStdioHelper(t *testing.T) {
	t.Helper()
	reader := json.NewDecoder(bufio.NewReader(os.Stdin))
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for {
		var request rpcRequest
		if err := reader.Decode(&request); err != nil {
			return
		}
		switch request.Method {
		case "notifications/initialized":
			continue
		case "initialize":
			writeStdioTestResponse(t, writer, request.ID, map[string]any{"protocolVersion": protocolVersion})
		case "tools/list":
			writeStdioTestResponse(t, writer, request.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo input",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
		case "tools/call":
			var params struct {
				Arguments map[string]any `json:"arguments"`
			}
			content, _ := json.Marshal(request.Params)
			_ = json.Unmarshal(content, &params)
			writeStdioTestResponse(t, writer, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + params.Arguments["text"].(string)}},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}
}

func writeStdioTestResponse(t *testing.T, writer *bufio.Writer, id int, result any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
}
