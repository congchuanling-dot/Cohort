package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task represents a single to-do item.
type Task struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	Completed   bool       `json:"completed"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// NewTask creates a new Task with the given id and title.
// It validates the title, trims whitespace, sets CreatedAt to now,
// and initializes Completed to false.
func NewTask(id int, title string) (Task, error) {
	if err := ValidateTitle(title); err != nil {
		return Task{}, err
	}
	return Task{
		ID:        id,
		Title:     strings.TrimSpace(title),
		Completed: false,
		CreatedAt: time.Now(),
	}, nil
}

// MarkDone marks the task as completed and records the completion time.
// It returns an error if the task is already completed.
func (t *Task) MarkDone() error {
	if t.Completed {
		return errors.New("task already completed")
	}
	t.Completed = true
	now := time.Now()
	t.CompletedAt = &now
	return nil
}

// ValidateTitle checks that the title is not empty and does not exceed 500 characters.
func ValidateTitle(title string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return errors.New("title cannot be empty")
	}
	if len(trimmed) > 500 {
		return fmt.Errorf("title too long: %d characters, max 500", len(trimmed))
	}
	return nil
}
