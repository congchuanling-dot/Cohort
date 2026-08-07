package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPersistentTypeScriptLanguageServerQueryAndStatus_BitsUT(t *testing.T) {
	if os.Getenv("COHORT_LSP_TEST_HELPER") == "1" {
		runLSPTestHelper()
		os.Exit(0)
	}
	root := t.TempDir()
	source := "export function greet(name: string) { return name }\nconst value = greet('x')\n"
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "typescript-language-server")
	script := fmt.Sprintf("#!/bin/sh\nCOHORT_LSP_TEST_HELPER=1 exec %q -test.run=TestPersistentTypeScriptLanguageServerQueryAndStatus_BitsUT\n", os.Args[0])
	if err := os.WriteFile(command, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	client := Diagnostics{Root: root, TypeScriptServerCommand: command}
	result, err := client.languageServerQuery(context.Background(), LanguageTypeScript, QueryDefinition, QueryOptions{
		Language: LanguageTypeScript, Kind: QueryDefinition, Position: "main.ts:2:16",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Engine != command || !strings.Contains(result.Output, "main.ts") {
		t.Fatalf("result=%#v", result)
	}
	statuses := ServerStatuses(root)
	if len(statuses) != 2 || !statuses[0].Running || statuses[0].PID <= 0 {
		t.Fatalf("statuses=%#v", statuses)
	}
	if err := StopServer(root, LanguageTypeScript); err != nil {
		t.Fatal(err)
	}
}

func runLSPTestHelper() {
	reader := bufio.NewReader(os.Stdin)
	for {
		length := -1
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if _, value, ok := strings.Cut(line, ":"); ok {
				length, _ = strconv.Atoi(strings.TrimSpace(value))
			}
		}
		if length < 0 {
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(payload, &request) != nil || len(request.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch request.Method {
		case "textDocument/definition":
			result = []map[string]any{{
				"uri": "file:///workspace/main.ts",
				"range": map[string]any{
					"start": map[string]int{"line": 0, "character": 16},
					"end":   map[string]int{"line": 0, "character": 21},
				},
			}}
		case "shutdown":
			result = nil
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
	}
}
