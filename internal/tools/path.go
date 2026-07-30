package tools

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	envRuntimeScriptsDir      = "COHORT_RUNTIME_SCRIPTS_DIR"
	envBrowserOCRHelperPath   = "COHORT_BROWSER_OCR_HELPER_PATH"
	browserOCRHelperFileName  = "browser_ocr.py"
	userRuntimeScriptsDirName = ".cohort/scripts"
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

// resolveSOPReadFallback 为只读 SOP 提供仓库级路径回退。
//
// 普通相对路径仍严格相对 workspace；只有 sops/ 前缀的读取请求才会
// 向 workspace、Git 根目录和其父目录查找，以免工作区配置影响规则文件可见性。
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

// candidateSOPRoots 按优先级生成可能包含 sops/ 的目录，并去重保证查找稳定。
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

// findGitRoot 从给定路径向上查找 .git，用于定位项目级 SOP。
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

func resolveRuntimeScriptPath(workspace string, scriptName string) string {
	scriptName = filepath.Base(strings.TrimSpace(scriptName))
	if scriptName == "" || scriptName == "." {
		return ""
	}
	candidates := make([]string, 0, 7)
	if scriptName == browserOCRHelperFileName {
		candidates = append(candidates, os.Getenv(envBrowserOCRHelperPath))
	}
	if runtimeScriptsDir := strings.TrimSpace(os.Getenv(envRuntimeScriptsDir)); runtimeScriptsDir != "" {
		candidates = append(candidates, filepath.Join(runtimeScriptsDir, scriptName))
	}
	workspaceRoot := newWorkspaceTool(workspace).workspace
	if root := findGitRoot(workspaceRoot); root != "" {
		candidates = append(candidates, filepath.Join(root, "scripts", scriptName))
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := findGitRoot(cwd); root != "" {
			candidates = append(candidates, filepath.Join(root, "scripts", scriptName))
		}
		candidates = append(candidates, filepath.Join(cwd, "scripts", scriptName))
	}
	if exe, err := os.Executable(); err == nil {
		installRoot := filepath.Dir(filepath.Dir(exe))
		candidates = append(candidates, filepath.Join(installRoot, "runtime-scripts", scriptName))
		candidates = append(candidates, filepath.Join(installRoot, "scripts", scriptName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, userRuntimeScriptsDirName, scriptName))
	}
	return firstExistingPath(candidates, filepath.Join("scripts", scriptName))
}

func firstExistingPath(candidates []string, fallback string) string {
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "" || candidate == "." || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate != "" && candidate != "." {
			return candidate
		}
	}
	return fallback
}

// ensureParent 确保目标文件的父目录存在，写文件前使用。
func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
