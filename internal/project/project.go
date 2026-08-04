package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DirName             = ".cohort"
	ProjectFileName     = "project.md"
	ConfigFileName      = "config.json"
	DefaultProjectTitle = "Cohort Project"
	maxPromptRunes      = 6000
)

// Store owns the explicit project-mode files under .cohort/.
type Store struct {
	Root string
}

// Status is a read-only snapshot for CLI/REPL display.
type Status struct {
	Root        string
	Dir         string
	ProjectPath string
	ConfigPath  string
	Exists      bool
	Content     string
}

// NewStore creates a project store rooted at the repository/workspace root.
func NewStore(root string) Store {
	if strings.TrimSpace(root) == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return Store{Root: filepath.Clean(root)}
}

// Dir returns the .cohort directory path.
func (s Store) Dir() string {
	return filepath.Join(s.Root, DirName)
}

// ProjectPath returns the project.md path.
func (s Store) ProjectPath() string {
	return filepath.Join(s.Dir(), ProjectFileName)
}

// ConfigPath returns the project-level config entry path.
func (s Store) ConfigPath() string {
	return filepath.Join(s.Dir(), ConfigFileName)
}

// Init bootstraps project-mode files. Existing project.md is preserved unless force is true.
func (s Store) Init(title string, force bool) (Status, error) {
	if strings.TrimSpace(title) == "" {
		title = DefaultProjectTitle
	}
	if err := os.MkdirAll(s.Dir(), 0755); err != nil {
		return Status{}, err
	}
	projectPath := s.ProjectPath()
	if !force {
		if _, err := os.Stat(projectPath); err == nil {
			return s.Status()
		} else if !os.IsNotExist(err) {
			return Status{}, err
		}
	}
	content := defaultProjectMarkdown(title)
	if err := os.WriteFile(projectPath, []byte(content), 0644); err != nil {
		return Status{}, err
	}
	configPath := s.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config := "{\n  \"schema_version\": 1,\n  \"project_md\": \".cohort/project.md\",\n  \"plan_state\": \".cohort/plan.json\",\n  \"memory_root\": \"memory\"\n}\n"
		if writeErr := os.WriteFile(configPath, []byte(config), 0644); writeErr != nil {
			return Status{}, writeErr
		}
	} else if err != nil {
		return Status{}, err
	}
	return s.Status()
}

// Status reads project-mode state without mutating it.
func (s Store) Status() (Status, error) {
	status := Status{
		Root:        s.Root,
		Dir:         s.Dir(),
		ProjectPath: s.ProjectPath(),
		ConfigPath:  s.ConfigPath(),
	}
	data, err := os.ReadFile(status.ProjectPath)
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return Status{}, err
	}
	status.Exists = true
	status.Content = string(data)
	return status, nil
}

// Prompt returns the bounded Project Mode block injected into the system prompt.
func (s Store) Prompt() string {
	status, err := s.Status()
	if err != nil || !status.Exists || strings.TrimSpace(status.Content) == "" {
		return ""
	}
	return "\n\n[Project Mode]\n" + truncateRunes(strings.TrimSpace(status.Content), maxPromptRunes)
}

func defaultProjectMarkdown(title string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(`# %s

- schema_version: 1
- created_at: %s
- project_memory_pointer: memory/
- session_memory_pointer: temp/sessions/
- plan_state_pointer: .cohort/plan.json
- project_config_pointer: .cohort/config.json

## Project Intent

TODO: describe the project goal, hard constraints, and preferred engineering style.

## Working Rules

- Keep durable project rules here or in files referenced from this document.
- Keep transient task progress in Plan Mode, not in project memory.
- Prefer explicit configuration over hidden defaults.
`, strings.TrimSpace(title), now)
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 20 {
		return string(runes[:max])
	}
	return string(runes[:max-20]) + "\n...[truncated]"
}
