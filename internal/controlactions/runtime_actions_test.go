package controlactions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cohort/internal/capability"
	"cohort/internal/controlplane"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

func TestRuntimeOptimizationActionPersistsCapabilityProposal_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(filepath.Join(root, session.DefaultRootDir))
	sess, err := store.Create("runtime compare", root, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	log := `{"schema_version":1,"event_id":"good-start","event_type":"RunStarted","time":"2026-08-12T00:00:00Z","run_id":"run_good","session_id":"` + sess.ID + `","severity":"info","redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"good-llm","event_type":"LLMResponseFinished","time":"2026-08-12T00:00:00.100Z","run_id":"run_good","session_id":"` + sess.ID + `","turn":1,"severity":"info","data":{"status":"success","duration_ms":100,"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}},"redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"good-finish","event_type":"RunFinished","time":"2026-08-12T00:00:00.200Z","run_id":"run_good","session_id":"` + sess.ID + `","turn":1,"severity":"info","data":{"status":"completed","duration_ms":200},"redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"bad-start","event_type":"RunStarted","time":"2026-08-12T00:01:00Z","run_id":"run_bad","session_id":"` + sess.ID + `","severity":"info","redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"bad-tool","event_type":"ToolFinished","time":"2026-08-12T00:01:00.200Z","run_id":"run_bad","session_id":"` + sess.ID + `","turn":1,"severity":"warn","data":{"tool":"code_run","status":"error","error_code":"exit_1","duration_ms":100},"redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"bad-finish","event_type":"RunFinished","time":"2026-08-12T00:01:00.500Z","run_id":"run_bad","session_id":"` + sess.ID + `","turn":1,"severity":"error","data":{"status":"failed","duration_ms":500},"redaction":{}}` + "\n"
	if writeErr := os.WriteFile(filepath.Join(store.SessionDir(sess.ID), traceview.ObservationLogFileName), []byte(log), 0644); writeErr != nil {
		t.Fatal(writeErr)
	}
	action := runtimeOptimizationActions()[0]
	result, err := action.Handler(context.Background(), controlplane.ActionRequest{
		ProjectRoot: root,
		Actor:       "test",
		Input:       map[string]any{"session_id": sess.ID, "run_id": "run_bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "runtime optimization proposal created" {
		t.Fatalf("result = %#v", result)
	}
	registry, err := capability.NewStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Gaps) != 1 || len(registry.Proposals) != 1 ||
		registry.Gaps[0].Source != "runtime_compare" ||
		registry.Proposals[0].Status != capability.StatusProposed {
		t.Fatalf("registry = %#v", registry)
	}
}
