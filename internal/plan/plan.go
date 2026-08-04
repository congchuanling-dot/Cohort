package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	PlanFileName   = "plan.json"
	maxPromptRunes = 6000
)

type Status string

const (
	StatusActive  Status = "active"
	StatusDone    Status = "done"
	StatusBlocked Status = "blocked"
)

type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepInProgress StepStatus = "in_progress"
	StepCompleted  StepStatus = "completed"
)

// State is persisted in .cohort/plan.json so a session can be resumed later.
type State struct {
	SchemaVersion int       `json:"schema_version"`
	Title         string    `json:"title"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Steps         []Step    `json:"steps"`
}

// Step is a single plan item. Completed steps must carry verification evidence.
type Step struct {
	ID         int        `json:"id"`
	Text       string     `json:"text"`
	Status     StepStatus `json:"status"`
	Evidence   string     `json:"evidence,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

type Store struct {
	Root string
}

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

func (s Store) Path() string {
	return filepath.Join(s.Root, ".cohort", PlanFileName)
}

func (s Store) Load() (State, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return State{}, os.ErrNotExist
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse %s: %w", s.Path(), err)
	}
	return normalizeState(state), nil
}

func (s Store) Save(state State) error {
	state = normalizeState(state)
	if err := validateState(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.Path(), append(data, '\n'), 0644)
}

func (s Store) Create(title string, stepTexts []string) (State, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Active Plan"
	}
	cleanSteps := make([]Step, 0, len(stepTexts))
	for _, text := range stepTexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		cleanSteps = append(cleanSteps, Step{
			ID:     len(cleanSteps) + 1,
			Text:   text,
			Status: StepPending,
		})
	}
	if len(cleanSteps) == 0 {
		return State{}, fmt.Errorf("plan requires at least one step")
	}
	now := time.Now().UTC()
	state := State{
		SchemaVersion: 1,
		Title:         title,
		Status:        StatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
		Steps:         cleanSteps,
	}
	return state, s.Save(state)
}

func (s Store) StartStep(id int) (State, error) {
	state, err := s.Load()
	if err != nil {
		return State{}, err
	}
	step, err := findStep(&state, id)
	if err != nil {
		return State{}, err
	}
	if step.Status == StepCompleted {
		return State{}, fmt.Errorf("step %d is already completed and verified", id)
	}
	for i := range state.Steps {
		if state.Steps[i].Status == StepInProgress {
			state.Steps[i].Status = StepPending
		}
	}
	step.Status = StepInProgress
	state.Status = StatusActive
	state.UpdatedAt = time.Now().UTC()
	return state, s.Save(state)
}

func (s Store) VerifyStep(id int, evidence string) (State, error) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return State{}, fmt.Errorf("verification evidence is required; completed steps must be backed by a command, file diff, test, or explicit review note")
	}
	state, err := s.Load()
	if err != nil {
		return State{}, err
	}
	step, err := findStep(&state, id)
	if err != nil {
		return State{}, err
	}
	now := time.Now().UTC()
	step.Status = StepCompleted
	step.Evidence = evidence
	step.VerifiedAt = &now
	state.UpdatedAt = now
	if allStepsCompleted(state.Steps) {
		state.Status = StatusDone
	} else {
		state.Status = StatusActive
	}
	return state, s.Save(state)
}

func (s Store) Block(reason string) (State, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return State{}, fmt.Errorf("blocked reason is required")
	}
	state, err := s.Load()
	if err != nil {
		return State{}, err
	}
	state.Status = StatusBlocked
	state.UpdatedAt = time.Now().UTC()
	state.Steps = append(state.Steps, Step{
		ID:       len(state.Steps) + 1,
		Text:     "BLOCKED: " + reason,
		Status:   StepCompleted,
		Evidence: reason,
	})
	return state, s.Save(state)
}

func (s Store) Prompt() string {
	state, err := s.Load()
	if err != nil || len(state.Steps) == 0 {
		return ""
	}
	text := Format(state)
	return "\n\n[Plan Mode]\nRules: a step may be marked completed only after verification evidence is recorded. Keep exactly one in_progress step when work is active. Resume this state across sessions from .cohort/plan.json.\n" + truncateRunes(text, maxPromptRunes)
}

func Format(state State) string {
	state = normalizeState(state)
	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", state.Title)
	fmt.Fprintf(&b, "status: %s\n", state.Status)
	fmt.Fprintf(&b, "updated_at: %s\n", state.UpdatedAt.Format(time.RFC3339))
	for _, step := range state.Steps {
		fmt.Fprintf(&b, "- [%s] %d. %s", step.Status, step.ID, step.Text)
		if step.Evidence != "" {
			fmt.Fprintf(&b, " (evidence: %s)", step.Evidence)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func ParseStepID(value string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("step id must be a positive integer")
	}
	return id, nil
}

func normalizeState(state State) State {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Status == "" {
		state.Status = StatusActive
	}
	if state.Title == "" {
		state.Title = "Active Plan"
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = state.CreatedAt
	}
	for i := range state.Steps {
		if state.Steps[i].ID == 0 {
			state.Steps[i].ID = i + 1
		}
		if state.Steps[i].Status == "" {
			state.Steps[i].Status = StepPending
		}
	}
	return state
}

func validateState(state State) error {
	inProgress := 0
	for _, step := range state.Steps {
		if strings.TrimSpace(step.Text) == "" {
			return fmt.Errorf("step %d text is empty", step.ID)
		}
		if step.Status == StepInProgress {
			inProgress++
		}
		if step.Status == StepCompleted && strings.TrimSpace(step.Evidence) == "" {
			return fmt.Errorf("step %d cannot be completed without verification evidence", step.ID)
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("only one plan step may be in_progress")
	}
	return nil
}

func findStep(state *State, id int) (*Step, error) {
	for i := range state.Steps {
		if state.Steps[i].ID == id {
			return &state.Steps[i], nil
		}
	}
	return nil, fmt.Errorf("step %d not found", id)
}

func allStepsCompleted(steps []Step) bool {
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if step.Status != StepCompleted {
			return false
		}
	}
	return true
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "\n...[truncated]"
}
