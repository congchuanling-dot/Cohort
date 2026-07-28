// ignore_security_alert_file RCE
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

const (
	// defaultCodeRunTimeoutSeconds 是 code_run 的默认超时时间。
	defaultCodeRunTimeoutSeconds = 60

	// maxCodeRunTimeoutSeconds 是模型可请求的最大超时时间。
	// 模型传入更大的值时会被截断，避免一次工具调用长时间占住 CLI。
	maxCodeRunTimeoutSeconds = 120

	// maxCodeRunOutputChars 限制返回给模型的最大输出长度。
	// 工具输出会进入下一轮上下文，过长会拖慢模型甚至撑爆上下文。
	maxCodeRunOutputChars  = 12000
	codeRunOutputHeadChars = 6000
	codeRunOutputTailChars = 4000
)

// CodeRun 在 workspace 中执行 shell 命令，用于构建、测试和本地检查。
type CodeRun struct {
	// workspaceTool 提供 code_run 的默认工作目录和 cwd 解析能力。
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
			"timeout": intProp("Timeout in seconds. Default 60, max 120.", defaultCodeRunTimeoutSeconds),
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
	timeout := normalizeCodeRunTimeout(asInt(call.Args["timeout"], defaultCodeRunTimeoutSeconds))
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
		// -l 会加载用户 shell 配置，容易把 .bashrc/.bash_profile 里的噪音带进工具结果。
		cmd = exec.CommandContext(runCtx, "bash", "-c", script)
	}
	cmd.Dir = cwd
	prepareCodeRunCommand(cmd)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	timeoutHit := runCtx.Err() == context.DeadlineExceeded
	if timeoutHit {
		killCodeRunProcessGroup(cmd)
	}
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if timeoutHit {
		exitCode = -1
	}
	stdout := out.String()
	if timeoutHit {
		stdout += "\n[Timeout Error] 超时强制终止"
	}
	stdout = truncateCodeRunOutput(stdout)
	status := agent.ToolStatusSuccess
	if err != nil {
		status = agent.ToolStatusError
	}
	data := map[string]any{
		"status":          status,
		"stdout":          stdout,
		"exit_code":       exitCode,
		"timeout":         timeoutHit,
		"timeout_seconds": timeout,
	}
	if timeoutHit {
		data["hint"] = "命令执行超时。请缩小搜索范围，优先使用 rg，并避免递归扫描 HOME、根目录或过大的目录。"
	}
	return agent.Outcome{
		Data:       data,
		NextPrompt: "\n",
	}, nil
}

// normalizeCodeRunTimeout 规范化模型传入的超时时间。
// 过小值使用默认值，过大值截断到最大值，避免模型让一次工具调用长时间占住进程。
func normalizeCodeRunTimeout(timeout int) int {
	if timeout <= 0 {
		return defaultCodeRunTimeoutSeconds
	}
	if timeout > maxCodeRunTimeoutSeconds {
		return maxCodeRunTimeoutSeconds
	}
	return timeout
}

// truncateCodeRunOutput 按首尾保留策略裁剪命令输出。
func truncateCodeRunOutput(stdout string) string {
	if len(stdout) <= maxCodeRunOutputChars {
		return stdout
	}
	return stdout[:codeRunOutputHeadChars] +
		"\n...[omitted long output]...\n" +
		stdout[len(stdout)-codeRunOutputTailChars:]
}
