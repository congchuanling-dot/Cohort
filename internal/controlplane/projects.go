package controlplane

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProjectRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Root         string    `json:"root"`
	LastOpenedAt time.Time `json:"last_opened_at"`
}

type ProjectRegistry struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func NewProjectRegistry(path string) (*ProjectRegistry, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return nil, errors.New("project registry path must be absolute")
	}
	registry := &ProjectRegistry{path: path, now: func() time.Time { return time.Now().UTC() }}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return registry, nil
}

func NewDefaultProjectRegistry() (*ProjectRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewProjectRegistry(filepath.Join(home, ".cohort", "control-center", "projects.json"))
}

func (r *ProjectRegistry) Register(root string) (ProjectRecord, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return ProjectRecord{}, err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil {
		return ProjectRecord{}, err
	}
	if !info.IsDir() {
		return ProjectRecord{}, errors.New("project root must be a directory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadUnlocked()
	if err != nil {
		return ProjectRecord{}, err
	}
	record := ProjectRecord{
		ID: projectID(absolute), Name: filepath.Base(absolute),
		Root: absolute, LastOpenedAt: r.now(),
	}
	replaced := false
	for index := range records {
		if records[index].Root == absolute {
			records[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}
	if err := r.saveUnlocked(records); err != nil {
		return ProjectRecord{}, err
	}
	return record, nil
}

func projectID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return "project_" + fmt.Sprintf("%x", sum[:8])
}

func (r *ProjectRegistry) List() ([]ProjectRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadUnlocked()
	if err != nil {
		return nil, err
	}
	result := records[:0]
	for _, record := range records {
		if info, statErr := os.Stat(record.Root); statErr == nil && info.IsDir() {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].LastOpenedAt.After(result[right].LastOpenedAt)
	})
	if len(result) != len(records) {
		_ = r.saveUnlocked(result)
	}
	return append([]ProjectRecord(nil), result...), nil
}

func (r *ProjectRegistry) loadUnlocked() ([]ProjectRecord, error) {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []ProjectRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *ProjectRegistry) saveUnlocked(records []ProjectRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(r.path), ".projects-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, r.path)
}
