package app

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// DesktopDarwinHelperPath 是 macOS 桌面 helper 在源码仓库中的相对路径。
	DesktopDarwinHelperPath = "scripts/desktop_darwin.py"
	// BrowserOCRHelperPath 是 OCR helper 在源码仓库中的相对路径。
	BrowserOCRHelperPath = "scripts/browser_ocr.py"
)

// ResolveRuntimeScriptPath 按源码仓库、全局安装目录、当前目录的顺序解析运行时脚本。
//
// release 安装场景中二进制通常位于 ~/.cohort/bin/cohort，对应脚本应放在
// ~/.cohort/scripts/。开发场景仍优先使用仓库内 scripts/。
func ResolveRuntimeScriptPath(workspace string, relativePath string) string {
	relativePath = filepath.Clean(strings.TrimSpace(relativePath))
	if relativePath == "." || relativePath == "" {
		return ""
	}
	candidates := make([]string, 0, 5)
	if root := findProjectRoot(workspace); root != "" {
		candidates = append(candidates, filepath.Join(root, relativePath))
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := findProjectRoot(cwd); root != "" {
			candidates = append(candidates, filepath.Join(root, relativePath))
		}
		candidates = append(candidates, filepath.Join(cwd, relativePath))
	}
	if exe, err := os.Executable(); err == nil {
		installRoot := filepath.Dir(filepath.Dir(exe))
		candidates = append(candidates, filepath.Join(installRoot, "scripts", filepath.Base(relativePath)))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".cohort", "scripts", filepath.Base(relativePath)))
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if fileExists(candidate) {
			return candidate
		}
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return relativePath
}
