package replay

import (
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
