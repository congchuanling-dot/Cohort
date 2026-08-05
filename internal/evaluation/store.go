package evaluation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	Root string
}

func NewStore(projectRoot string) Store {
	return Store{Root: filepath.Join(projectRoot, ".cohort", "evals")}
}

func (s Store) SuitesDir() string { return filepath.Join(s.Root, "suites") }
func (s Store) RunsDir() string   { return filepath.Join(s.Root, "runs") }
func (s Store) StabilityDir() string {
	return filepath.Join(s.Root, "stability")
}
func (s Store) SuitePath(id string) string {
	if strings.HasSuffix(id, ".json") || strings.ContainsRune(id, filepath.Separator) {
		return id
	}
	return filepath.Join(s.SuitesDir(), id+".json")
}
func (s Store) RunDir(id string) string { return filepath.Join(s.RunsDir(), id) }

func (s Store) SaveResult(result RunResult) (string, error) {
	dir := s.RunDir(result.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, os.WriteFile(filepath.Join(s.Root, "latest"), []byte(result.RunID+"\n"), 0644)
}

func (s Store) LoadResult(id string) (RunResult, error) {
	if id == "" || id == "latest" {
		data, err := os.ReadFile(filepath.Join(s.Root, "latest"))
		if err != nil {
			return RunResult{}, err
		}
		id = strings.TrimSpace(string(data))
	}
	data, err := os.ReadFile(filepath.Join(s.RunDir(id), "result.json"))
	if err != nil {
		return RunResult{}, err
	}
	var result RunResult
	if err := json.Unmarshal(data, &result); err != nil {
		return RunResult{}, err
	}
	return result, nil
}

func (s Store) PreviousResult(suiteID string, before time.Time) (RunResult, error) {
	results, err := s.ListResults()
	if err != nil {
		return RunResult{}, err
	}
	for _, result := range results {
		if result.SuiteID == suiteID && result.StartedAt.Before(before) {
			return result, nil
		}
	}
	return RunResult{}, errors.New("no previous eval result")
}

func (s Store) ListResults() ([]RunResult, error) {
	entries, err := os.ReadDir(s.RunsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var results []RunResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result, err := s.LoadResult(entry.Name())
		if err == nil {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].StartedAt.After(results[j].StartedAt)
	})
	return results, nil
}
