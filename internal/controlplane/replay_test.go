package controlplane

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/llm"
	"cohort/internal/replay"
	"cohort/internal/session"
)

func TestHandleReplayReturnsManifestAndExactProof(t *testing.T) {
	projectRoot := t.TempDir()
	sessionID := "session-1"
	runID := "run-1"
	sessionDir := filepath.Join(projectRoot, session.DefaultRootDir, sessionID)
	recorder, err := replay.NewRecorder(replay.RecorderConfig{
		SessionDir:       sessionDir,
		SessionID:        sessionID,
		RunID:            runID,
		WorkingDirectory: projectRoot,
		SystemPrompt:     "system",
		Input:            "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.RecordRequest(1, "system", []llm.Message{{Role: llm.RoleUser, Content: "task"}}, nil)
	recorder.RecordResponse(1, &llm.Response{Content: "done"})
	if err := recorder.Complete("done", nil); err != nil {
		t.Fatal(err)
	}

	server := &Server{projectRoot: projectRoot}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/replays/"+sessionID+"/"+runID, nil)
	response := httptest.NewRecorder()
	server.handleReplay(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !containsAll(body, `"manifest"`, `"exact_proof"`, `"verified":true`, `"experiments":[]`) {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestHandleReplayRejectsTraversal(t *testing.T) {
	server := &Server{projectRoot: t.TempDir()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/replays/../run-1", nil)
	response := httptest.NewRecorder()
	server.handleReplay(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
