package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/llm"
	"cohert/internal/session"
)

type fakeClient struct {
	calls int
}

func (c *fakeClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.calls++
	out := make(chan llm.Event, 2)
	out <- llm.Event{Type: llm.EventText, Text: "ok"}
	out <- llm.Event{Type: llm.EventDone, Response: &llm.Response{Content: "ok"}}
	close(out)
	return out, nil
}

type fakeTools struct{}

func (fakeTools) Schemas() []llm.ToolSchema {
	return []llm.ToolSchema{
		{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "file_read",
				Description: "read file",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}
}

func (fakeTools) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	return agent.Outcome{}, nil
}

func TestStartHandlesSlashCommandsLocally(t *testing.T) {
	// slash 命令必须由 REPL 本地处理，不能发给模型。
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	err := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/model\n/tools\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"Cohert", "model:", "tools:", "file_read", "bye"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStartPrintsCommandPaletteForSlashInNonTerminal(t *testing.T) {
	// 非真实终端里无法打开上下键选择菜单，因此 "/" 会退化为文本命令面板。
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	err := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if !strings.Contains(output, "Slash commands") {
		t.Fatalf("output does not contain command palette:\n%s", output)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
}

func TestStartResumesSessionWithSlashCommand(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("old task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleUser, Content: "旧问题"}); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{
		Client: &fakeClient{},
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: store,
		In:           strings.NewReader("/resume " + sess.ID + "\n/session\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if runner.SessionID() != sess.ID {
		t.Fatalf("session id = %q, want %q", runner.SessionID(), sess.ID)
	}
	if runner.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", runner.HistoryLen())
	}
	output := out.String()
	if !strings.Contains(output, "resumed session "+sess.ID) {
		t.Fatalf("output does not contain resume message:\n%s", output)
	}
	if !strings.Contains(output, "messages: 1") {
		t.Fatalf("output does not contain session message count:\n%s", output)
	}
}

func testConfig() app.Config {
	return app.Config{
		Language:  "zh",
		Workspace: "./workspace",
		LogDir:    "./temp/model_responses",
		MaxTurns:  40,
		LLM: app.LLMConfig{
			Provider: "openai",
			Name:     "deepseek",
			APIBase:  "https://api.deepseek.com",
			Model:    "deepseek-v4-pro",
			Stream:   true,
		},
	}
}
