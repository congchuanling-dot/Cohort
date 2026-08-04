package explorer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ProjectDirName = ".cohort"
	ExplorerDir    = "explorers"
	StateFile      = "tasks.json"
)

type Store struct {
	ProjectRoot string
	RootDir     string
}

type State struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Tasks     []Task    `json:"tasks"`
}

type Task struct {
	ID              string    `json:"id"`
	Question        string    `json:"question"`
	Status          string    `json:"status"`
	ReadOnly        bool      `json:"read_only"`
	AllowedCommands []string  `json:"allowed_commands"`
	TaskPath        string    `json:"task_path"`
	ResultPath      string    `json:"result_path"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func NewStore(projectRoot string) Store {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	projectRoot = filepath.Clean(projectRoot)
	return Store{
		ProjectRoot: projectRoot,
		RootDir:     filepath.Join(projectRoot, ProjectDirName, ExplorerDir),
	}
}

func (s Store) Path() string {
	return filepath.Join(s.RootDir, StateFile)
}

func (s Store) Load() (State, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Version: 1}, nil
		}
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return state, nil
}

func (s Store) Create(question string) (Task, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return Task{}, errors.New("explorer question is required")
	}
	state, err := s.Load()
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	id := uniqueTaskID(state.Tasks, "explorer_"+slug(question))
	taskDir := filepath.Join(s.RootDir, id)
	task := Task{
		ID:              id,
		Question:        question,
		Status:          "open",
		ReadOnly:        true,
		AllowedCommands: []string{"rg", "sed", "ls", "go test", "git diff", "git status"},
		TaskPath:        filepath.Join(taskDir, "task.md"),
		ResultPath:      filepath.Join(taskDir, "result.md"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return Task{}, err
	}
	if err := os.WriteFile(task.TaskPath, []byte(taskMarkdown(task)), 0644); err != nil {
		return Task{}, err
	}
	state.Tasks = append(state.Tasks, task)
	if err := s.save(state); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s Store) List() ([]Task, error) {
	state, err := s.Load()
	if err != nil {
		return nil, err
	}
	tasks := append([]Task(nil), state.Tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	return tasks, nil
}

func (s Store) Find(id string) (Task, error) {
	id = strings.TrimSpace(id)
	state, err := s.Load()
	if err != nil {
		return Task{}, err
	}
	for _, task := range state.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return Task{}, fmt.Errorf("explorer task %q not found", id)
}

func (s Store) save(state State) error {
	state.Version = 1
	state.UpdatedAt = time.Now().UTC()
	sort.Slice(state.Tasks, func(i, j int) bool {
		return state.Tasks[i].ID < state.Tasks[j].ID
	})
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.Path(), data, 0644)
}

func taskMarkdown(task Task) string {
	return fmt.Sprintf(`# Explorer Task

ID: %s
Status: %s
Read-only: %t

## Question

%s

## Constraints

- Read files and run read-only diagnostics only.
- Do not edit files.
- Do not install dependencies.
- Do not start long-running services.
- Prefer rg/sed/ls/go test/git diff/git status.

## Result

Write findings to:

%s
`, task.ID, task.Status, task.ReadOnly, task.Question, task.ResultPath)
}

func uniqueTaskID(tasks []Task, base string) string {
	used := map[string]bool{}
	for _, task := range tasks {
		used[task.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s_%d", base, i)
		if !used[id] {
			return id
		}
	}
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	underscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		return "task"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "_")
	}
	return slug
}
