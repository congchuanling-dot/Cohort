package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"todo/model"
)

// Store defines the interface for persistent task storage.
type Store interface {
	Load() ([]model.Task, error)
	Save(tasks []model.Task) error
}

// JSONStore implements Store using a JSON file on disk.
type JSONStore struct {
	filePath string
}

// NewJSONStore creates a new JSONStore that reads and writes to the given file path.
func NewJSONStore(filePath string) *JSONStore {
	return &JSONStore{filePath: filePath}
}

// Load reads all tasks from the JSON file. If the file does not exist,
// it creates an empty JSON file and returns an empty slice.
func (s *JSONStore) Load() ([]model.Task, error) {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty := []model.Task{}
			if saveErr := s.Save(empty); saveErr != nil {
				return nil, saveErr
			}
			return empty, nil
		}
		return nil, fmt.Errorf("failed to read file %s: %w", s.filePath, err)
	}

	if len(data) == 0 {
		return nil, nil
	}

	var tasks []model.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
	}
	return tasks, nil
}

// Save writes all tasks to the JSON file atomically (write to temp file, then rename).
func (s *JSONStore) Save(tasks []model.Task) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".tmp-todo-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // clean up in case rename fails
	defer tmpFile.Close()

	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(tasks); err != nil {
		return fmt.Errorf("failed to encode tasks: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", s.filePath, err)
	}
	return nil
}
