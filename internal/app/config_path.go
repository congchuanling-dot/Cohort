package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvConfigPath 是显式指定 Cohort 配置文件的环境变量名。
	EnvConfigPath = "COHORT_CONFIG"
	// ProjectConfigPath 是项目级默认配置路径，兼容开发期在仓库根目录运行。
	ProjectConfigPath = "configs/config.yaml"
	// UserConfigRelativePath 是用户级默认配置路径。
	UserConfigRelativePath = ".cohort/config.yaml"
)

// ResolveConfigPath 按优先级解析配置文件路径。
//
// 优先级：显式 --config/-c > COHORT_CONFIG > 当前目录 configs/config.yaml >
// ~/.cohort/config.yaml。显式指定的路径必须存在；自动搜索不到配置文件时返回
// 项目默认路径，让 LoadConfig 继续使用内置默认值。
func ResolveConfigPath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return requireConfigPath(explicit, "explicit config path")
	}
	if value := strings.TrimSpace(os.Getenv(EnvConfigPath)); value != "" {
		return requireConfigPath(value, EnvConfigPath)
	}
	if fileExists(ProjectConfigPath) {
		return filepath.Clean(ProjectConfigPath), nil
	}
	userPath, err := UserConfigPath()
	if err == nil && fileExists(userPath) {
		return userPath, nil
	}
	return filepath.Clean(ProjectConfigPath), nil
}

// UserConfigPath 返回当前用户的默认 Cohort 配置文件路径。
func UserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, UserConfigRelativePath), nil
}

func requireConfigPath(path string, source string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s %q is not readable: %w", source, path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s %q is a directory, want config file", source, path)
	}
	return path, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
