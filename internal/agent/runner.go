package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohert/internal/llm"
)

type ToolRunner interface {
	Schemas() []llm.ToolSchema
	Run(ctx context.Context, call ToolCallContext) (Outcome, error)
}

type ToolCallContext struct {
	Name      string
	Args      map[string]any
	Response  llm.Response
	Turn      int
	Index     int
	ToolCount int
}

type Runner struct {
	Client       llm.Client
	Tools        ToolRunner
	SystemPrompt string
	MaxTurns     int
	LogDir       string

	history []llm.Message
}

func (r *Runner) Run(ctx context.Context, input string, sink OutputSink) (RunResult, error) {
	if r.MaxTurns <= 0 {
		r.MaxTurns = 40
	}
	if err := r.ensureLogDir(); err != nil {
		return RunResult{}, err
	}

	r.history = append(r.history, llm.Message{Role: llm.RoleUser, Content: input})
	messages := append([]llm.Message(nil), r.history...)

	for turn := 1; turn <= r.MaxTurns; turn++ {
		sink.WriteText(fmt.Sprintf("\nLLM Running (Turn %d) ...\n\n", turn))
		stream, err := r.Client.Chat(ctx, llm.ChatRequest{
			System:   r.SystemPrompt,
			Messages: messages,
			Tools:    r.Tools.Schemas(),
		})
		if err != nil {
			return RunResult{}, err
		}

		resp, err := consume(stream, sink)
		if err != nil {
			return RunResult{}, err
		}
		r.logResponse(turn, resp)

		if len(resp.ToolCalls) == 0 {
			r.history = append(r.history, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})
			return RunResult{Status: "done", Response: resp}, nil
		}

		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls}
		r.history = append(r.history, assistantMsg)

		var toolMessages []llm.Message
		for i, call := range resp.ToolCalls {
			args, err := parseToolArgs(call.Function.Arguments)
			if err != nil {
				args = map[string]any{}
			}
			sink.WriteToolCall(call)
			outcome, runErr := r.Tools.Run(ctx, ToolCallContext{
				Name:      call.Function.Name,
				Args:      args,
				Response:  *resp,
				Turn:      turn,
				Index:     i,
				ToolCount: len(resp.ToolCalls),
			})
			if runErr != nil {
				outcome = Outcome{
					Data:       map[string]any{"status": "error", "msg": runErr.Error()},
					NextPrompt: "\n",
				}
			}
			if outcome.ShouldExit {
				return RunResult{Status: "exited", Response: resp}, nil
			}
			resultText := stringify(outcome.Data)
			sink.WriteToolResult(call.Function.Name, resultText)
			toolMessages = append(toolMessages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    resultText,
			})
		}
		r.history = append(r.history, toolMessages...)
		messages = append([]llm.Message(nil), r.history...)
	}
	return RunResult{Status: "max_turns_exceeded"}, nil
}

func (r *Runner) ToolSchemas() []llm.ToolSchema {
	return r.Tools.Schemas()
}

func (r *Runner) Reset() {
	r.history = nil
}

func consume(stream <-chan llm.Event, sink OutputSink) (*llm.Response, error) {
	for event := range stream {
		switch event.Type {
		case llm.EventText:
			sink.WriteText(event.Text)
		case llm.EventDone:
			if event.Response == nil {
				return &llm.Response{}, nil
			}
			return event.Response, nil
		case llm.EventError:
			if event.Err != nil {
				sink.WriteError(event.Err)
				return nil, event.Err
			}
		}
	}
	return nil, fmt.Errorf("llm stream closed without done event")
}

func parseToolArgs(raw string) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		data, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(data)
	}
}

func (r *Runner) ensureLogDir() error {
	if r.LogDir == "" {
		return nil
	}
	return os.MkdirAll(r.LogDir, 0755)
}

func (r *Runner) logResponse(turn int, resp *llm.Response) {
	if r.LogDir == "" || resp == nil {
		return
	}
	path := filepath.Join(r.LogDir, time.Now().Format("20060102")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "=== turn %d %s ===\n%s\n\n", turn, time.Now().Format(time.RFC3339), resp.Raw)
}
