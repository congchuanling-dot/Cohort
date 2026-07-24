package tools

import (
	"os"
	"path/filepath"
	"strings"
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

// resolveSOPReadFallback resolves repository SOP files for read-only access.
// SOPs live beside the configured workspace in this project, while file tools
// intentionally resolve ordinary relative paths inside workspace.
func (t workspaceTool) resolveSOPReadFallback(path string) (string, bool) {
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(clean, "sops/") || strings.Contains(clean, "/../") {
		return "", false
	}
	for _, root := range candidateSOPRoots(t.workspace) {
		candidate := filepath.Join(root, filepath.FromSlash(clean))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func candidateSOPRoots(workspace string) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	add(workspace)
	if root := findGitRoot(workspace); root != "" {
		add(root)
	}
	add(filepath.Dir(workspace))
	return roots
}

func findGitRoot(path string) string {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

// ensureParent 确保目标文件的父目录存在，写文件前使用。
func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
