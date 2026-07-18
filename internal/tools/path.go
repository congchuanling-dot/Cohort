package tools

import (
	"os"
	"path/filepath"
)

type workspaceTool struct {
	workspace string
}

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

func (t workspaceTool) resolve(path string) string {
	if path == "" {
		return t.workspace
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(t.workspace, path))
}

func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
