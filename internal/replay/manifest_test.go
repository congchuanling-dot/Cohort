package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"cohort/internal/llm"
)

func TestRecorderPersistsCompleteBundle(t *testing.T) {
	sessionDir := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{
		SessionDir:       sessionDir,
		SessionID:        "session-1",
		RunID:            "run-1",
		Provider:         "openai",
		Model:            "model-1",
		WorkingDirectory: t.TempDir(),
		SystemPrompt:     "system",
		Tools: []llm.ToolSchema{{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:       "read",
				Parameters: map[string]any{"type": "object"},
			},
		}},
		Input: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := llm.ChatRequest{
		System:   "system",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
		Tools:    recorderTools(),
	}
	recorder.RecordRequest(1, request.System, request.Messages, request.Tools)
	recorder.RecordResponse(1, &llm.Response{Content: "done"})
	if err := recorder.Complete("done", nil); err != nil {
		t.Fatal(err)
	}

	manifest, frames, err := LoadBundle(filepath.Dir(sessionDir), filepath.Base(sessionDir), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "complete" || manifest.FrameCount != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if len(frames) != 2 || frames[0].Kind != FrameRequest || frames[1].Kind != FrameResponse {
		t.Fatalf("unexpected frames: %+v", frames)
	}
	if info, err := os.Stat(recorder.Dir()); err != nil || !info.IsDir() {
		t.Fatalf("bundle directory missing: %v", err)
	}
	result, err := ExactReplay(filepath.Dir(sessionDir), filepath.Base(sessionDir), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.LLMCalls != 1 || result.FinalResponse != "done" {
		t.Fatalf("unexpected exact replay result: %+v", result)
	}
}

func TestExactReplayDetectsTamperedFrame(t *testing.T) {
	sessionDir := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{
		SessionDir:       sessionDir,
		SessionID:        "session-1",
		RunID:            "run-1",
		WorkingDirectory: t.TempDir(),
		SystemPrompt:     "system",
		Input:            "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.RecordRequest(1, "system", []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, nil)
	recorder.RecordResponse(1, &llm.Response{Content: "original"})
	if err := recorder.Complete("done", nil); err != nil {
		t.Fatal(err)
	}
	framesPath := filepath.Join(recorder.Dir(), FramesFileName)
	data, err := os.ReadFile(framesPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(string(data[:len(data)-1]) + " \n")
	if err := os.WriteFile(framesPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	// 修改 JSON 空白不会改变语义或内容哈希，因此仍应通过。
	result, err := ExactReplay(filepath.Dir(sessionDir), filepath.Base(sessionDir), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("semantic-equivalent frame should verify: %+v", result)
	}

	data, err = os.ReadFile(framesPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(string(data))
	old := []byte(`"content":"original"`)
	newValue := []byte(`"content":"tampered"`)
	data = bytes.Replace(data, old, newValue, 1)
	if err := os.WriteFile(framesPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = ExactReplay(filepath.Dir(sessionDir), filepath.Base(sessionDir), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verified || result.FirstDivergence == nil {
		t.Fatalf("tampered frame was not detected: %+v", result)
	}
}

func recorderTools() []llm.ToolSchema {
	return []llm.ToolSchema{{
		Type: "function",
		Function: llm.FunctionSchema{
			Name:       "read",
			Parameters: map[string]any{"type": "object"},
		},
	}}
}
