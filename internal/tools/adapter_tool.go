package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/llm"
	"cohort/internal/plugin"
)

type CommandAdapterTool struct {
	name        string
	description string
	command     []string
	root        string
}

func NewCommandAdapterTool(manifest plugin.Plugin, command plugin.Command) *CommandAdapterTool {
	return &CommandAdapterTool{
		name:        strings.TrimSpace(command.Name),
		description: strings.TrimSpace(command.Description),
		command:     append([]string(nil), command.Command...),
		root:        manifest.Root,
	}
}

func (t *CommandAdapterTool) Name() string { return t.name }

func (t *CommandAdapterTool) Schema() llm.ToolSchema {
	description := t.description
	if description == "" {
		description = "Run an explicitly enabled local command adapter. Arguments are passed as JSON on stdin."
	}
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: description,
		Parameters: objectSchema(map[string]any{
			"args": objectProp("Adapter-specific JSON arguments. They are passed to the adapter process on stdin."),
		}),
	}}
}

func (t *CommandAdapterTool) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if len(t.command) == 0 || strings.TrimSpace(t.command[0]) == "" {
		return agent.Outcome{Data: agent.NewToolError("adapter_command_missing", "adapter command is empty", "Disable or fix this adapter before using it."), NextPrompt: "\n"}, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, t.command[0], t.command[1:]...)
	if t.root != "" {
		cmd.Dir = filepath.Clean(t.root)
	}
	payload, _ := json.Marshal(call.Args["args"])
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		payload = []byte("{}")
	}
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	data := map[string]any{
		"status":    agent.ToolStatusSuccess,
		"command":   t.command,
		"output":    strings.TrimSpace(string(output)),
		"exit_code": exitCode(err),
	}
	if runCtx.Err() != nil {
		data["status"] = agent.ToolStatusError
		data["error"] = runCtx.Err().Error()
	} else if err != nil {
		data["status"] = agent.ToolStatusError
		data["error"] = err.Error()
	}
	return agent.Outcome{
		Data:       data,
		NextPrompt: "\n",
		Audit: map[string]any{
			"external":            true,
			"risk":                "adapter",
			"permission_decision": "enabled_adapter",
		},
	}, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
