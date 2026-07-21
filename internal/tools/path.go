package tools

import (
	"os"
	"path/filepath"
)

// workspaceTool 保存工具工作区，并提供路径解析能力。
// 相对路径会落在 workspace 下，绝对路径会直接清理后使用。
type workspaceTool struct {
	// workspace 是工具解析相对路径时使用的工作区根目录。
	workspace string
}

// newWorkspaceTool 把配置里的 workspace 转成稳定的绝对路径。
func newWorkspaceTool(workspace string) workspaceTool {
	if workspace == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	return workspaceTool{workspace: filepath.Clean(workspace)}
}

// resolve 把模型传入的路径转换成本地真实路径。
func (t workspaceTool) resolve(path string) string {
	if path == "" {
		return t.workspace
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(t.workspace, path))
}

// ensureParent 确保目标文件的父目录存在，写文件前使用。
func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
