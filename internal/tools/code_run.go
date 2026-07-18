// ignore_security_alert_file RCE
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"cohert/internal/agent"
	"cohert/internal/llm"
)

// CodeRun 在 workspace 中执行 shell 命令，用于构建、测试和本地检查。
type CodeRun struct {
	workspaceTool
}

func NewCodeRun(workspace string) *CodeRun {
	return &CodeRun{workspaceTool: newWorkspaceTool(workspace)}
}

func (t *CodeRun) Name() string { return ToolNameCodeRun }

// Schema 告诉模型需要提供 script，可选 timeout 和 cwd。
func (t *CodeRun) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Run a shell command in the workspace. Use for build, test, and inspection commands.",
		Parameters: objectSchema(map[string]any{
			"script":  stringProp("Shell command to run"),
			"timeout": intProp("Timeout in seconds", 60),
			"cwd":     stringProp("Working directory relative to workspace"),
		}, "script"),
	}}
}

// Run 执行命令并返回 stdout、exit_code、timeout 等结构化结果。
func (t *CodeRun) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	script := asString(call.Args["script"])
	if script == "" {
		return agent.Outcome{}, fmt.Errorf("script is empty")
	}
	timeout := asInt(call.Args["timeout"], 60)
	if timeout <= 0 {
		timeout = 60
	}
	cwd := t.resolve(asString(call.Args["cwd"]))
	if asString(call.Args["cwd"]) == "" {
		cwd = t.workspace
	}

	// 每次命令都带独立超时，避免 Agent 被长时间阻塞。
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	// Windows 和类 Unix 使用不同 shell。
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	} else {
		cmd = exec.CommandContext(runCtx, "bash", "-lc", script)
	}
	cmd.Dir = cwd

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	stdout := out.String()
	if len(stdout) > 12000 {
		// 输出过长会影响模型上下文，保留首尾即可定位问题。
		stdout = stdout[:6000] + "\n...[omitted long output]...\n" + stdout[len(stdout)-4000:]
	}
	status := agent.ToolStatusSuccess
	if err != nil {
		status = agent.ToolStatusError
	}
	timeoutHit := runCtx.Err() == context.DeadlineExceeded
	return agent.Outcome{
		Data: map[string]any{
			"status":    status,
			"stdout":    stdout,
			"exit_code": exitCode,
			"timeout":   timeoutHit,
		},
		NextPrompt: "\n",
	}, nil
}
