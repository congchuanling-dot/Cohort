package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope 表示 MCP 配置的保存位置和合并优先级。
type Scope string

const (
	// ScopeProject 是可提交到项目仓库、供团队共享的 .mcp.json。
	ScopeProject Scope = "project"
	// ScopeUser 是当前用户跨项目复用的 ~/.cohert/mcp.json。
	ScopeUser Scope = "user"
	// ScopeLocal 是当前机器私有且应被忽略的 .cohort/local.mcp.json。
	ScopeLocal Scope = "local"
)

// Store owns Cohert's MCP configuration files. Project scope deliberately
// uses Claude Code's .mcp.json format for direct compatibility.
type Store struct {
	// ProjectRoot 是项目级和本地级配置的根目录。
	ProjectRoot string
	// HomeDir 注入用户目录查询，方便测试隔离真实 HOME。
	HomeDir func() (string, error)
}

// ScopedServer 记录合并后生效的服务器定义及胜出的 scope，只用于 CLI 诊断。
type ScopedServer struct {
	Server ServerConfig
	Scope  Scope
}

// NewStore 基于项目根目录构造配置存储。
func NewStore(projectRoot string) Store {
	return Store{
		ProjectRoot: filepath.Clean(projectRoot),
		HomeDir:     os.UserHomeDir,
	}
}

// LoadEffective merges user, project, and local definitions in ascending
// precedence. Later scopes override earlier entries with the same name.
func (s Store) LoadEffective() ([]ServerConfig, error) {
	effective, err := s.LoadEffectiveWithScopes()
	if err != nil {
		return nil, err
	}
	servers := make([]ServerConfig, 0, len(effective))
	for _, entry := range effective {
		servers = append(servers, entry.Server)
	}
	return servers, nil
}

// LoadEffectiveWithScopes merges server definitions and preserves the scope
// that contributed each final value.
func (s Store) LoadEffectiveWithScopes() ([]ScopedServer, error) {
	merged := map[string]ScopedServer{}
	for _, scope := range []Scope{ScopeUser, ScopeProject, ScopeLocal} {
		config, err := s.Load(scope)
		if err != nil {
			return nil, err
		}
		for name, server := range config.Servers {
			server.Name = name
			validated, err := server.Validate()
			if err != nil {
				return nil, fmt.Errorf("%s MCP config: %w", scope, err)
			}
			merged[name] = ScopedServer{Server: validated, Scope: scope}
		}
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]ScopedServer, 0, len(names))
	for _, name := range names {
		servers = append(servers, merged[name])
	}
	return servers, nil
}

// Load 读取单一 scope；配置文件不存在被视为尚未配置，而不是错误。
func (s Store) Load(scope Scope) (Config, error) {
	path, err := s.Path(scope)
	if err != nil {
		return Config{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Servers: map[string]ServerConfig{}}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if config.Servers == nil {
		config.Servers = map[string]ServerConfig{}
	}
	return config, nil
}

// Add 校验并写入一个服务器定义；同名定义会覆盖当前 scope 的旧值。
func (s Store) Add(scope Scope, server ServerConfig) error {
	server.Name = strings.TrimSpace(server.Name)
	validated, err := server.Validate()
	if err != nil {
		return err
	}
	config, err := s.Load(scope)
	if err != nil {
		return err
	}
	config.Servers[validated.Name] = withoutName(validated)
	return s.Save(scope, config)
}

// Remove 删除当前 scope 中同名服务器，返回值说明该配置是否原本存在。
func (s Store) Remove(scope Scope, name string) (bool, error) {
	config, err := s.Load(scope)
	if err != nil {
		return false, err
	}
	if _, ok := config.Servers[name]; !ok {
		return false, nil
	}
	delete(config.Servers, name)
	return true, s.Save(scope, config)
}

// Save 以缩进 JSON 写入 scope 配置，并使用 0600 避免 HTTP 头等本地凭据被其他用户读取。
func (s Store) Save(scope Scope, config Config) error {
	path, err := s.Path(scope)
	if err != nil {
		return err
	}
	if config.Servers == nil {
		config.Servers = map[string]ServerConfig{}
	}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0600)
}

// Path 返回指定 scope 的规范配置路径。
// 本地 scope 不与项目配置混写，便于将机器密钥排除在版本控制之外。
func (s Store) Path(scope Scope) (string, error) {
	switch scope {
	case ScopeProject:
		return filepath.Join(s.ProjectRoot, ".mcp.json"), nil
	case ScopeLocal:
		return filepath.Join(s.ProjectRoot, ".cohort", "local.mcp.json"), nil
	case ScopeUser:
		homeDir, err := s.HomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(homeDir, ".cohert", "mcp.json"), nil
	default:
		return "", fmt.Errorf("unknown MCP scope %q", scope)
	}
}

// ParseScope 将命令行输入解析为已支持的 scope 枚举。
func ParseScope(value string) (Scope, error) {
	scope := Scope(strings.ToLower(strings.TrimSpace(value)))
	switch scope {
	case ScopeProject, ScopeUser, ScopeLocal:
		return scope, nil
	default:
		return "", fmt.Errorf("unknown MCP scope %q; use project, user, or local", value)
	}
}

// withoutName 清除 map 键已承载的 Name，避免 JSON 文件重复存同一信息。
func withoutName(server ServerConfig) ServerConfig {
	server.Name = ""
	return server
}
