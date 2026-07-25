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

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
	ScopeLocal   Scope = "local"
)

// Store owns Cohert's MCP configuration files. Project scope deliberately
// uses Claude Code's .mcp.json format for direct compatibility.
type Store struct {
	ProjectRoot string
	HomeDir     func() (string, error)
}

// ScopedServer records the effective definition and the scope that won the
// precedence merge. It is used for CLI diagnostics only.
type ScopedServer struct {
	Server ServerConfig
	Scope  Scope
}

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

func ParseScope(value string) (Scope, error) {
	scope := Scope(strings.ToLower(strings.TrimSpace(value)))
	switch scope {
	case ScopeProject, ScopeUser, ScopeLocal:
		return scope, nil
	default:
		return "", fmt.Errorf("unknown MCP scope %q; use project, user, or local", value)
	}
}

func withoutName(server ServerConfig) ServerConfig {
	server.Name = ""
	return server
}
