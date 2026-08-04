package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ProjectDirName = ".cohort"
	PluginDirName  = "plugins"
	ManifestJSON   = "plugin.json"
	ManifestYAML   = "plugin.yaml"
	ManifestYML    = "plugin.yml"
)

type Manifest struct {
	Name         string       `json:"name"`
	Version      string       `json:"version,omitempty"`
	Description  string       `json:"description,omitempty"`
	Skills       []string     `json:"skills,omitempty"`
	Commands     []Command    `json:"commands,omitempty"`
	MCP          MCPSection   `json:"mcp,omitempty"`
	Permissions  Permissions  `json:"permissions,omitempty"`
	Dependencies Dependencies `json:"dependencies,omitempty"`
}

type Command struct {
	Name        string   `json:"name"`
	Command     []string `json:"command"`
	Description string   `json:"description,omitempty"`
}

type MCPSection struct {
	Config  string      `json:"config,omitempty"`
	Servers []MCPServer `json:"servers,omitempty"`
}

type MCPServer struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type Permissions struct {
	AllowTools []string `json:"allow_tools,omitempty"`
	DenyTools  []string `json:"deny_tools,omitempty"`
	AllowMCP   []string `json:"allow_mcp,omitempty"`
	DenyMCP    []string `json:"deny_mcp,omitempty"`
}

type Dependencies struct {
	Commands []string `json:"commands,omitempty"`
	Python   []string `json:"python,omitempty"`
	NPM      []string `json:"npm,omitempty"`
	Brew     []string `json:"brew,omitempty"`
	Env      []string `json:"env,omitempty"`
}

type Plugin struct {
	Manifest Manifest `json:"manifest"`
	Root     string   `json:"root"`
	Path     string   `json:"path"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DoctorResult struct {
	Plugin Plugin  `json:"plugin"`
	Checks []Check `json:"checks"`
}

func Discover(projectRoot string) ([]Plugin, error) {
	root := filepath.Join(firstNonEmpty(projectRoot, "."), ProjectDirName, PluginDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var plugins []Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginRoot := filepath.Join(root, entry.Name())
		manifestPath := firstExistingManifest(pluginRoot)
		if manifestPath == "" {
			continue
		}
		plugin, err := Load(manifestPath)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Manifest.Name < plugins[j].Manifest.Name
	})
	return plugins, nil
}

func Load(path string) (Plugin, error) {
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return Plugin{}, err
	}
	var manifest Manifest
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &manifest); err != nil {
			return Plugin{}, fmt.Errorf("parse plugin manifest %s: %w", path, err)
		}
	case ".yaml", ".yml":
		parsed, err := parseSimpleYAMLManifest(string(data))
		if err != nil {
			return Plugin{}, fmt.Errorf("parse plugin manifest %s: %w", path, err)
		}
		manifest = parsed
	default:
		return Plugin{}, fmt.Errorf("unsupported plugin manifest extension %q", filepath.Ext(path))
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" {
		return Plugin{}, errors.New("plugin manifest requires name")
	}
	return Plugin{Manifest: manifest, Root: filepath.Dir(path), Path: path}, nil
}

func Doctor(plugin Plugin) DoctorResult {
	result := DoctorResult{Plugin: plugin}
	add := func(name, status, message string) {
		result.Checks = append(result.Checks, Check{Name: name, Status: status, Message: message})
	}
	add("manifest", "ok", filepath.Clean(plugin.Path))
	for _, skillPath := range plugin.Manifest.Skills {
		path := filepath.Join(plugin.Root, filepath.FromSlash(skillPath))
		if fileExists(path) {
			add("skill:"+skillPath, "ok", "exists")
		} else {
			add("skill:"+skillPath, "error", "missing")
		}
	}
	for _, command := range plugin.Manifest.Commands {
		if strings.TrimSpace(command.Name) == "" {
			add("command", "error", "command name is required")
			continue
		}
		if len(command.Command) == 0 || strings.TrimSpace(command.Command[0]) == "" {
			add("command:"+command.Name, "error", "command argv is required")
			continue
		}
		add("command:"+command.Name, "ok", strings.Join(command.Command, " "))
	}
	if plugin.Manifest.MCP.Config != "" {
		path := filepath.Join(plugin.Root, filepath.FromSlash(plugin.Manifest.MCP.Config))
		if fileExists(path) {
			add("mcp.config", "ok", "exists")
		} else {
			add("mcp.config", "error", "missing")
		}
	}
	for _, server := range plugin.Manifest.MCP.Servers {
		if strings.TrimSpace(server.Name) == "" {
			add("mcp.server", "error", "server name is required")
			continue
		}
		add("mcp.server:"+server.Name, "ok", firstNonEmpty(server.Type, "stdio/http inferred"))
	}
	for _, command := range plugin.Manifest.Dependencies.Commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if _, err := exec.LookPath(command); err != nil {
			add("dependency.command:"+command, "error", "not found in PATH")
		} else {
			add("dependency.command:"+command, "ok", "available")
		}
	}
	for _, env := range plugin.Manifest.Dependencies.Env {
		env = strings.TrimSpace(env)
		if env == "" {
			continue
		}
		if _, ok := os.LookupEnv(env); ok {
			add("dependency.env:"+env, "ok", "set")
		} else {
			add("dependency.env:"+env, "error", "not set")
		}
	}
	return result
}

func firstExistingManifest(root string) string {
	for _, name := range []string{ManifestJSON, ManifestYAML, ManifestYML} {
		path := filepath.Join(root, name)
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// parseSimpleYAMLManifest 支持常见一层字段和字符串数组。完整复杂配置请使用 plugin.json。
func parseSimpleYAMLManifest(content string) (Manifest, error) {
	var manifest Manifest
	currentList := ""
	currentSection := ""
	for _, line := range strings.Split(content, "\n") {
		raw := strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "- "); ok {
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			switch currentList {
			case "skills":
				manifest.Skills = append(manifest.Skills, value)
			case "dependencies.commands":
				manifest.Dependencies.Commands = append(manifest.Dependencies.Commands, value)
			case "dependencies.env":
				manifest.Dependencies.Env = append(manifest.Dependencies.Env, value)
			case "permissions.allow_tools":
				manifest.Permissions.AllowTools = append(manifest.Permissions.AllowTools, value)
			case "permissions.deny_tools":
				manifest.Permissions.DenyTools = append(manifest.Permissions.DenyTools, value)
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return Manifest{}, fmt.Errorf("unsupported yaml line %q", raw)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" {
			if leadingSpaces(raw) == 0 {
				currentSection = key
				currentList = key
			} else if currentSection != "" {
				currentList = currentSection + "." + key
			} else {
				currentList = key
			}
			continue
		}
		currentList = ""
		fullKey := key
		if leadingSpaces(raw) > 0 && currentSection != "" {
			fullKey = currentSection + "." + key
		}
		switch fullKey {
		case "name":
			manifest.Name = value
		case "version":
			manifest.Version = value
		case "description":
			manifest.Description = value
		case "mcp_config":
			manifest.MCP.Config = value
		case "mcp.config":
			manifest.MCP.Config = value
		}
	}
	return manifest, nil
}

func leadingSpaces(value string) int {
	count := 0
	for _, r := range value {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}
