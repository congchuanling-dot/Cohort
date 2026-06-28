package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunCommandTool 执行系统命令的工具。
// 公有工具，默认超时 30 秒，防止命令挂死。
type RunCommandTool struct{}

// NewRunCommandTool 创建一个 RunCommand 工具实例。
func NewRunCommandTool() *RunCommandTool {
	return &RunCommandTool{}
}

func (t *RunCommandTool) Name() string {
	return "RunCommand"
}

func (t *RunCommandTool) Description() string {
	return "执行一个系统命令并返回输出。用于编译代码、运行测试、安装依赖等。超时 30 秒。"
}

func (t *RunCommandTool) Parameters() map[string]string {
	return map[string]string{
		"command": "要执行的命令，如 'go build ./...' 或 'go test ./...'",
		"workdir": "可选，工作目录（不填则使用当前工作区）",
	}
}

func (t *RunCommandTool) Run(ctx context.Context, args map[string]any) (string, error) {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return "", fmt.Errorf("RunCommand: 'command' 参数缺失")
	}

	workDir, _ := args["workdir"].(string)

	// 带超时的 context
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	parts := strings.Fields(cmdStr)
	var cmd *exec.Cmd
	if len(parts) == 1 {
		cmd = exec.CommandContext(ctx, parts[0])
	} else {
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("❌ 命令失败: %s\n退出码: %v\n输出:\n%s", cmdStr, err, string(output)), nil
	}

	result := string(output)
	if result == "" {
		result = "(无输出)"
	}

	// 限制输出长度
	if len(result) > 5000 {
		result = result[:5000] + fmt.Sprintf("\n...（输出共 %d 字符，已截断）", len(result)+5000)
	}

	return fmt.Sprintf("✅ 命令执行成功:\n%s", result), nil
}
