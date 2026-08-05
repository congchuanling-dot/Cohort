package contextmgr

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const ContextStateFileName = "context_state.json"

type ContextState struct {
	Version                     int       `json:"version"`
	UpdatedAt                   time.Time `json:"updated_at"`
	AutoCompactAttempts         int       `json:"auto_compact_attempts"`
	AutoCompactSuccesses        int       `json:"auto_compact_successes"`
	AutoCompactConsecutiveFails int       `json:"auto_compact_consecutive_failures"`
	AutoCompactDisabled         bool      `json:"auto_compact_disabled"`
	LastAutoCompactAttemptAt    time.Time `json:"last_auto_compact_attempt_at,omitempty"`
	LastAutoCompactSuccessAt    time.Time `json:"last_auto_compact_success_at,omitempty"`
	LastAutoCompactError        string    `json:"last_auto_compact_error,omitempty"`
	LastCompactPath             string    `json:"last_compact_path,omitempty"`
}

func LoadContextState(sessionDir string) (ContextState, error) {
	path := contextStatePath(sessionDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ContextState{Version: 1}, nil
		}
		return ContextState{}, err
	}
	var state ContextState
	if err := json.Unmarshal(data, &state); err != nil {
		return ContextState{}, err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return state, nil
}

func SaveContextState(sessionDir string, state ContextState) error {
	path := contextStatePath(sessionDir)
	state.Version = 1
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func contextStatePath(sessionDir string) string {
	return filepath.Join(filepath.Clean(sessionDir), ContextStateFileName)
}
