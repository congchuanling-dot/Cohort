package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProjectDirName = ".cohort"
	ExplorerDir    = "explorers"
	StateFile      = "tasks.json"
)

var stateFileMu sync.Mutex

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
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

type RunOptions struct {
	WithTests bool
	Search    string
}

type RunResult struct {
	Task   Task          `json:"task"`
	Checks []CheckResult `json:"checks"`
}

type BatchRunResult struct {
	Results    []RunResult `json:"results"`
	ReportPath string      `json:"report_path"`
	Failed     int         `json:"failed"`
}

type CheckResult struct {
	Name     string   `json:"name"`
	Command  []string `json:"command"`
	Output   string   `json:"output,omitempty"`
	OK       bool     `json:"ok"`
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
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

func (s Store) Run(ctx context.Context, id string, opts RunOptions) (RunResult, error) {
	task, err := s.updateTask(id, func(task *Task, now time.Time) {
		task.Status = "running"
		task.LastError = ""
		task.UpdatedAt = now
	})
	if err != nil {
		return RunResult{}, err
	}
	checks := s.runChecks(ctx, opts)
	now := time.Now().UTC()
	failed := failedChecks(checks)
	status := "completed"
	lastError := ""
	if failed > 0 {
		status = "failed"
		lastError = fmt.Sprintf("%d check(s) failed", failed)
	}
	task.Status = status
	task.CompletedAt = now
	task.UpdatedAt = now
	task.LastError = lastError
	result := RunResult{Task: task, Checks: checks}
	if err := os.WriteFile(task.ResultPath, []byte(resultMarkdown(result)), 0644); err != nil {
		return result, err
	}
	if _, err := s.updateTask(id, func(stored *Task, _ time.Time) {
		stored.Status = task.Status
		stored.CompletedAt = task.CompletedAt
		stored.UpdatedAt = task.UpdatedAt
		stored.LastError = task.LastError
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (s Store) RunBatch(ctx context.Context, ids []string, opts RunOptions) (BatchRunResult, error) {
	cleanIDs := cleanIDs(ids)
	if len(cleanIDs) == 0 {
		return BatchRunResult{}, errors.New("at least one explorer task id is required")
	}
	results := make([]RunResult, len(cleanIDs))
	errs := make([]error, len(cleanIDs))
	var wg sync.WaitGroup
	for index, id := range cleanIDs {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			results[index], errs[index] = s.Run(ctx, id, opts)
		}(index, id)
	}
	wg.Wait()
	failed := 0
	for index, err := range errs {
		if err != nil {
			failed++
			if results[index].Task.ID == "" {
				results[index].Task = Task{ID: cleanIDs[index], Status: "failed", LastError: err.Error()}
			}
		} else if results[index].Task.Status == "failed" {
			failed++
		}
	}
	reportPath := filepath.Join(s.RootDir, "aggregate_result.md")
	batch := BatchRunResult{Results: results, ReportPath: reportPath, Failed: failed}
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return batch, err
	}
	if err := os.WriteFile(reportPath, []byte(batchResultMarkdown(batch)), 0644); err != nil {
		return batch, err
	}
	if failed > 0 {
		return batch, fmt.Errorf("explorer batch failed: %d lane(s) failed", failed)
	}
	return batch, nil
}

func (s Store) runChecks(ctx context.Context, opts RunOptions) []CheckResult {
	commands := []struct {
		name string
		argv []string
	}{
		{name: "git_status", argv: []string{"git", "status", "--short"}},
		{name: "git_diff_stat", argv: []string{"git", "diff", "--stat"}},
		{name: "git_diff_name_only", argv: []string{"git", "diff", "--name-only"}},
	}
	if strings.TrimSpace(opts.Search) != "" {
		commands = append(commands, struct {
			name string
			argv []string
		}{name: "search", argv: []string{"rg", "-n", "--", opts.Search}})
	}
	if opts.WithTests {
		commands = append(commands, struct {
			name string
			argv []string
		}{name: "go_test", argv: []string{"go", "test", "./..."}})
	}
	checks := make([]CheckResult, len(commands))
	var wg sync.WaitGroup
	for index, command := range commands {
		wg.Add(1)
		go func(index int, name string, argv []string) {
			defer wg.Done()
			checks[index] = s.runCommand(ctx, name, argv)
		}(index, command.name, command.argv)
	}
	wg.Wait()
	return checks
}

func (s Store) runCommand(ctx context.Context, name string, argv []string) CheckResult {
	result := CheckResult{Name: name, Command: append([]string(nil), argv...), ExitCode: -1}
	if len(argv) == 0 {
		result.Error = "empty command"
		return result
	}
	if !isAllowedExplorerCommand(argv) {
		result.Error = "command is not in explorer read-only allowlist"
		return result
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		result.Error = err.Error()
		return result
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.ProjectRoot
	output, err := cmd.CombinedOutput()
	result.Output = strings.TrimSpace(string(output))
	result.ExitCode = exitCode(err)
	result.OK = err == nil
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		return result
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func (s Store) updateTask(id string, update func(task *Task, now time.Time)) (Task, error) {
	stateFileMu.Lock()
	defer stateFileMu.Unlock()
	id = strings.TrimSpace(id)
	state, err := s.Load()
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	for index := range state.Tasks {
		if state.Tasks[index].ID != id {
			continue
		}
		update(&state.Tasks[index], now)
		if err := s.save(state); err != nil {
			return Task{}, err
		}
		return state.Tasks[index], nil
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

func resultMarkdown(result RunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Explorer Result\n\n")
	fmt.Fprintf(&b, "ID: %s\n", result.Task.ID)
	fmt.Fprintf(&b, "Status: %s\n", result.Task.Status)
	fmt.Fprintf(&b, "Read-only: %t\n", result.Task.ReadOnly)
	if !result.Task.CompletedAt.IsZero() {
		fmt.Fprintf(&b, "Completed: %s\n", result.Task.CompletedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\n## Question\n\n%s\n\n", result.Task.Question)
	fmt.Fprintf(&b, "## Checks\n\n")
	for _, check := range result.Checks {
		status := "ok"
		if !check.OK {
			status = "fail"
		}
		fmt.Fprintf(&b, "### %s [%s]\n\n", check.Name, status)
		fmt.Fprintf(&b, "Command: `%s`\n\n", strings.Join(check.Command, " "))
		if check.Error != "" {
			fmt.Fprintf(&b, "Error: %s\n\n", check.Error)
		}
		if check.Output != "" {
			fmt.Fprintf(&b, "```text\n%s\n```\n\n", check.Output)
		}
	}
	return b.String()
}

func batchResultMarkdown(batch BatchRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Explorer Aggregate Result\n\n")
	fmt.Fprintf(&b, "Lanes: %d\n", len(batch.Results))
	fmt.Fprintf(&b, "Failed: %d\n\n", batch.Failed)
	for _, result := range batch.Results {
		status := result.Task.Status
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(&b, "## %s [%s]\n\n", result.Task.ID, status)
		if result.Task.Question != "" {
			fmt.Fprintf(&b, "%s\n\n", result.Task.Question)
		}
		if result.Task.ResultPath != "" {
			fmt.Fprintf(&b, "Result: `%s`\n\n", result.Task.ResultPath)
		}
		if result.Task.LastError != "" {
			fmt.Fprintf(&b, "Error: %s\n\n", result.Task.LastError)
		}
		for _, check := range result.Checks {
			checkStatus := "ok"
			if !check.OK {
				checkStatus = "fail"
			}
			fmt.Fprintf(&b, "- [%s] %s: `%s`\n", checkStatus, check.Name, strings.Join(check.Command, " "))
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

func cleanIDs(ids []string) []string {
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	return clean
}

func failedChecks(checks []CheckResult) int {
	failed := 0
	for _, check := range checks {
		if !check.OK {
			failed++
		}
	}
	return failed
}

func isAllowedExplorerCommand(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch argv[0] {
	case "git":
		if len(argv) < 2 {
			return false
		}
		switch argv[1] {
		case "status", "diff":
			return true
		default:
			return false
		}
	case "rg":
		return true
	case "go":
		return len(argv) >= 2 && argv[1] == "test"
	default:
		return false
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
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
