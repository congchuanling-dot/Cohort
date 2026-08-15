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

func TestHandleReplayTurnDetailExposesVerbatimText(t *testing.T) {
	projectRoot := t.TempDir()
	sessionID := "session-2"
	runID := "run-2"
	sessionDir := filepath.Join(projectRoot, session.DefaultRootDir, sessionID)
	recorder, err := replay.NewRecorder(replay.RecorderConfig{
		SessionDir:       sessionDir,
		SessionID:        sessionID,
		RunID:            runID,
		WorkingDirectory: projectRoot,
		SystemPrompt:     "system",
		Input:            "读取 README",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.RecordRequest(1, "system", []llm.Message{{Role: llm.RoleUser, Content: "读取 README"}}, nil)
	recorder.RecordResponse(1, &llm.Response{
		Content:   "好的，我来读",
		ToolCalls: []llm.ToolCall{{ID: "c1", Function: llm.ToolFunction{Name: "file_read"}}},
		Raw:       "RAW_STREAM_PAYLOAD",
	})
	recorder.RecordTool(1, 0, llm.ToolCall{ID: "c1", Function: llm.ToolFunction{Name: "file_read"}},
		map[string]any{"path": "README.md"}, "文件正文内容", "", false, nil, 0)
	recorder.RecordResponse(2, &llm.Response{Content: "总结完成"})
	if err := recorder.Complete("done", nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{projectRoot: projectRoot}

	// 默认（不带 turn）向后兼容：不返回原文明细。
	base := httptest.NewRequest(http.MethodGet, "/api/v1/replays/"+sessionID+"/"+runID, nil)
	baseResp := httptest.NewRecorder()
	server.handleReplay(baseResp, base)
	if strings.Contains(baseResp.Body.String(), "turn_detail") {
		t.Fatalf("turn_detail must be omitted without ?turn: %s", baseResp.Body.String())
	}

	// ?turn=1 返回该 turn 的请求/响应/工具原文，默认不含 raw。
	detail := httptest.NewRequest(http.MethodGet, "/api/v1/replays/"+sessionID+"/"+runID+"?turn=1", nil)
	detailResp := httptest.NewRecorder()
	server.handleReplay(detailResp, detail)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", detailResp.Code, detailResp.Body.String())
	}
	body := detailResp.Body.String()
	if !containsAll(body, `"turn_detail"`, "读取 README", "好的，我来读", "file_read", "文件正文内容", "README.md") {
		t.Fatalf("turn detail missing verbatim text: %s", body)
	}
	if strings.Contains(body, "RAW_STREAM_PAYLOAD") {
		t.Fatalf("raw stream payload must be stripped without include_raw: %s", body)
	}
	// turn=1 的明细不应包含 turn 2 的响应正文。
	if strings.Contains(body, "总结完成") {
		t.Fatalf("turn detail must be scoped to requested turn: %s", body)
	}

	// include_raw=true 才返回原始流式载荷。
	withRaw := httptest.NewRequest(http.MethodGet, "/api/v1/replays/"+sessionID+"/"+runID+"?turn=1&include_raw=true", nil)
	withRawResp := httptest.NewRecorder()
	server.handleReplay(withRawResp, withRaw)
	if !strings.Contains(withRawResp.Body.String(), "RAW_STREAM_PAYLOAD") {
		t.Fatalf("include_raw=true should expose raw payload: %s", withRawResp.Body.String())
	}
}

func TestHandleReplayRejectsInvalidTurn(t *testing.T) {
	projectRoot := t.TempDir()
	sessionID := "session-3"
	runID := "run-3"
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
	request := httptest.NewRequest(http.MethodGet, "/api/v1/replays/"+sessionID+"/"+runID+"?turn=abc", nil)
	response := httptest.NewRecorder()
	server.handleReplay(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid turn, got status=%d body=%s", response.Code, response.Body.String())
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
