package builtin

import (
	"context"
	"fmt"
	"os"
)

// ReadFileTool 读取文件内容的工具。
// 所有 Role 都可以使用（公有工具）。
type ReadFileTool struct{}

// NewReadFileTool 创建一个 ReadFile 工具实例。
func NewReadFileTool() *ReadFileTool {
	return &ReadFileTool{}
}

func (t *ReadFileTool) Name() string {
	return "ReadFile"
}

func (t *ReadFileTool) Description() string {
	return "读取指定路径的文件内容。当需要查看已有代码、文档或配置时使用。"
}

func (t *ReadFileTool) Parameters() map[string]string {
	return map[string]string{
		"path": "文件路径（相对于工作区的绝对或相对路径）",
	}
}

func (t *ReadFileTool) Run(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("ReadFile: 'path' 参数缺失或不是字符串")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ReadFile: 读取 %s 失败: %w", path, err)
	}

	// 限制输出长度，防止撑爆上下文
	if len(data) > 10000 {
		data = data[:10000]
		return fmt.Sprintf("文件内容（截断前 10000 字符）:\n\n%s\n\n...（文件共 %d 字节）", string(data), len(data)+10000), nil
	}

	return fmt.Sprintf("文件内容:\n\n%s", string(data)), nil
}
