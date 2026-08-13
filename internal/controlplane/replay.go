package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cohort/internal/replay"
	"cohort/internal/session"
)

type replayExperimentSummary struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	ForkTurn    int     `json:"fork_turn"`
	Trials      int     `json:"trials"`
	SuccessRate float64 `json:"success_rate"`
	ProofHash   string  `json:"proof_hash"`
	ReportPath  string  `json:"report_path"`
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/replays/"), "/"), "/")
	if len(parts) != 2 || !safeReplayID(parts[0]) || !safeReplayID(parts[1]) {
		writeControlError(w, http.StatusBadRequest, "expected /api/v1/replays/{session_id}/{run_id}")
		return
	}
	sessionRoot := filepath.Join(s.projectRoot, session.DefaultRootDir)
	manifest, _, err := replay.LoadBundle(sessionRoot, parts[0], parts[1])
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeControlError(w, http.StatusNotFound, "replay bundle not found")
			return
		}
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exact, err := replay.ExactReplay(sessionRoot, parts[0], parts[1])
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	experiments, err := loadReplayExperimentSummaries(sessionRoot, parts[0], parts[1])
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{
		"manifest":    manifest,
		"exact_proof": exact,
		"experiments": experiments,
	})
}

func loadReplayExperimentSummaries(sessionRoot, sessionID, runID string) ([]replayExperimentSummary, error) {
	root := filepath.Join(sessionRoot, sessionID, replay.ReplayDirName, runID, "experiments")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []replayExperimentSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	summaries := make([]replayExperimentSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !safeReplayID(entry.Name()) {
			continue
		}
		reportPath := filepath.Join(root, entry.Name(), "report.json")
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		var report replay.ExperimentReport
		if err := json.Unmarshal(data, &report); err != nil {
			continue
		}
		summaries = append(summaries, replayExperimentSummary{
			ID:          report.ID,
			CreatedAt:   report.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			ForkTurn:    report.ForkTurn,
			Trials:      len(report.Trials),
			SuccessRate: report.SuccessRate,
			ProofHash:   report.ProofHash,
			ReportPath:  filepath.ToSlash(filepath.Join("experiments", entry.Name(), "report.json")),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt > summaries[j].CreatedAt
	})
	return summaries, nil
}

func safeReplayID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
