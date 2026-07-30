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
	// EnvRuntimeScriptsDir 显式指定 Cohort 运行时 helper 脚本目录。
	EnvRuntimeScriptsDir = "COHORT_RUNTIME_SCRIPTS_DIR"
	// EnvDesktopDarwinHelperPath 显式指定 macOS 桌面 helper 脚本路径。
	EnvDesktopDarwinHelperPath = "COHORT_DESKTOP_DARWIN_HELPER_PATH"
	// EnvBrowserOCRHelperPath 显式指定 OCR helper 脚本路径。
	EnvBrowserOCRHelperPath = "COHORT_BROWSER_OCR_HELPER_PATH"
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
	if env := scriptPathEnvFor(relativePath); env != "" {
		candidates = append(candidates, os.Getenv(env))
	}
	if runtimeScriptsDir := strings.TrimSpace(os.Getenv(EnvRuntimeScriptsDir)); runtimeScriptsDir != "" {
		candidates = append(candidates, filepath.Join(runtimeScriptsDir, filepath.Base(relativePath)))
	}
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

func scriptPathEnvFor(relativePath string) string {
	switch filepath.Base(relativePath) {
	case "desktop_darwin.py":
		return EnvDesktopDarwinHelperPath
	case "browser_ocr.py":
		return EnvBrowserOCRHelperPath
	default:
		return ""
	}
}
