package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileTool 将内容写入文件的工具。
// 公有工具，所有 Role 都可以使用。
type WriteFileTool struct{}

// NewWriteFileTool 创建一个 WriteFile 工具实例。
func NewWriteFileTool() *WriteFileTool {
	return &WriteFileTool{}
}

func (t *WriteFileTool) Name() string {
	return "WriteFile"
}

func (t *WriteFileTool) Description() string {
	return "将内容写入指定路径的文件。如果父目录不存在则自动创建。用于保存生成的代码、文档等产出物。"
}

func (t *WriteFileTool) Parameters() map[string]string {
	return map[string]string{
		"path":    "文件路径（相对于工作区的绝对或相对路径）",
		"content": "要写入的文件内容",
	}
}

func (t *WriteFileTool) Run(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("WriteFile: 'path' 参数缺失或不是字符串")
	}

	content, _ := args["content"].(string)

	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("WriteFile: 创建目录 %s 失败: %w", dir, err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("WriteFile: 写入 %s 失败: %w", path, err)
	}

	return fmt.Sprintf("✅ 已写入 %s（%d 字节）", path, len(content)), nil
}
