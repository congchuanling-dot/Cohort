package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cohort/internal/traceview"
)

func (s *Service) ServeAPI(ctx context.Context, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1:18778"
	}
	if err := validateLoopbackAddress(address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	actualAddress := listener.Addr().String()
	s.mu.Lock()
	status, _ := s.Store.LoadStatus()
	status.APIAddress = actualAddress
	if _, portText, splitErr := net.SplitHostPort(actualAddress); splitErr == nil {
		status.APIPort, _ = strconv.Atoi(portText)
	}
	_ = s.Store.SaveStatus(status)
	s.mu.Unlock()

	server := &http.Server{
		Handler:           s.apiHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Service) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/actions", s.handleActions)
	mux.HandleFunc("/actions/", s.handleAction)
	mux.HandleFunc("/eval/runs", s.handleEvalRuns)
	mux.HandleFunc("/trace/", s.handleTrace)
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJob)
	mux.HandleFunc("/events", s.handleEvents)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := s.Store.LoadStatus()
	writeAPIResult(w, status, err)
}

func (s *Service) handleActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	queue, err := s.Store.LoadQueue()
	if err == nil {
		limit := queryLimit(r, len(queue.Actions), 1000)
		if len(queue.Actions) > limit {
			queue.Actions = queue.Actions[:limit]
		}
	}
	writeAPIResult(w, queue, err)
}

func (s *Service) handleAction(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/actions/")
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		writeAPIError(w, http.StatusBadRequest, "invalid action id")
		return
	}
	if r.Method == http.MethodGet {
		queue, err := s.Store.LoadQueue()
		if err != nil {
			writeAPIResult(w, nil, err)
			return
		}
		for _, action := range queue.Actions {
			if action.ID == id {
				writeAPIResult(w, action, nil)
				return
			}
		}
		writeAPIError(w, http.StatusNotFound, "action not found")
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request struct {
		Status string `json:"status"`
		RunID  string `json:"run_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Status == QueueStatusResolved {
		action, err := VerifyActionWithRun(s.Store, s.EvalStore, id, request.RunID, true)
		writeAPIResult(w, action, err)
		return
	}
	action, err := UpdateActionStatus(s.Store, id, request.Status)
	writeAPIResult(w, action, err)
}

func (s *Service) handleEvalRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	results, err := s.EvalStore.ListResults()
	if err == nil {
		limit := queryLimit(r, 50, 500)
		if len(results) > limit {
			results = results[:limit]
		}
	}
	writeAPIResult(w, results, err)
}

func (s *Service) handleTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/trace/"))
	if sessionID == "" || strings.Contains(sessionID, "/") || strings.Contains(sessionID, "..") {
		writeAPIError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	roots := []string{
		filepath.Join(s.EvalStore.Root, "sessions"),
		filepath.Join(s.ProjectRoot, ".cohort", "sessions"),
	}
	var lastErr error
	for _, root := range roots {
		view, err := traceview.LoadSessionRun(root, sessionID, runID)
		if err == nil {
			summary := view.Summary()
			limit := queryLimit(r, 500, 5000)
			events := view.Events
			truncated := false
			if len(events) > limit {
				events = events[len(events)-limit:]
				truncated = true
			}
			writeAPIResult(w, map[string]any{
				"session_id": view.SessionID,
				"run_id":     view.RunID,
				"path":       view.Path,
				"summary": map[string]any{
					"status":           summary.Status,
					"duration_ms":      summary.DurationMS,
					"event_count":      summary.EventCount,
					"turn_count":       summary.TurnCount,
					"warning_count":    summary.WarningCount,
					"error_count":      summary.ErrorCount,
					"llm_calls":        summary.LLMCalls,
					"llm_duration_ms":  summary.LLMDurationMS,
					"tool_calls":       summary.ToolCalls,
					"tool_failures":    summary.ToolFailures,
					"tool_duration_ms": summary.ToolDurationMS,
					"total_tokens":     summary.TotalTokens,
				},
				"events":    events,
				"truncated": truncated,
			}, nil)
			return
		}
		lastErr = err
	}
	writeAPIError(w, http.StatusNotFound, lastErr.Error())
}

func (s *Service) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobs, err := s.Store.LoadJobs()
	writeAPIResult(w, jobs, err)
}

func (s *Service) handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/jobs/"))
	if id == "" || strings.Contains(id, "/") {
		writeAPIError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := FindJob(s.Store, id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, err.Error())
		return
	}
	writeAPIResult(w, job, nil)
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	events, err := s.Store.LoadEvents(queryLimit(r, 100, 1000))
	writeAPIResult(w, events, err)
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid api listen address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("hermes api may only listen on loopback, got %q", host)
	}
	return nil
}

func queryLimit(r *http.Request, fallback int, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func writeAPIResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message, "status": status})
}
