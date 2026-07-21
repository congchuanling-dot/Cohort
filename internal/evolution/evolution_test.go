package evolution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/llm"
)

func TestManagerEnsureStructureCreatesP0Files_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)

	created, err := manager.EnsureStructure()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 4 {
		t.Fatalf("created files = %d, want 4: %#v", len(created), created)
	}
	for _, rel := range []string{MemoryIndexPath, GlobalMemoryPath, DefaultProjectMemoryPath, MemoryAuditPath} {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestManagerApplyCandidateAppendsAndAudits_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "实现并测试长期记忆"},
		{Role: llm.RoleTool, Name: "code_run", ToolCallID: "call-test", Content: `{"status":"success","stdout":"ok","exit_code":0,"timeout":false}`},
	}
	candidate := Candidate{
		Type:     "project_lesson",
		Target:   DefaultProjectMemoryPath,
		Content:  "When adding controlled memory updates, keep writes append-only and record memory/audit.jsonl.",
		Evidence: "code_run call-test exit_code=0",
		Risk:     "low",
		Action:   "append",
	}

	record, err := manager.ApplyCandidate(candidate, history, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Target != DefaultProjectMemoryPath || record.SourceSession != "session-1" {
		t.Fatalf("audit record = %#v", record)
	}

	projectData, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(DefaultProjectMemoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectData), candidate.Content) || !strings.Contains(string(projectData), candidate.Evidence) {
		t.Fatalf("project memory missing applied entry:\n%s", projectData)
	}

	auditData, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(MemoryAuditPath)))
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuditRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(auditData))), &decoded); err != nil {
		t.Fatalf("audit json is invalid: %v\n%s", err, auditData)
	}
	if decoded.Target != DefaultProjectMemoryPath || decoded.Action != "append" {
		t.Fatalf("decoded audit = %#v", decoded)
	}
}

func TestManagerRejectsUnsafeMemoryCandidate_BitsUT(t *testing.T) {
	manager := NewManager(t.TempDir())
	validation := manager.ValidateCandidate(Candidate{
		Type:     "project_lesson",
		Target:   "internal/agent/runner.go",
		Content:  "token=abc123",
		Evidence: "model guessed this",
		Action:   "overwrite",
	}, nil)

	if validation.Valid {
		t.Fatal("expected unsafe candidate to be rejected")
	}
	got := strings.Join(validation.Reasons, "\n")
	for _, want := range []string{"allowlist", "append", "evidence", "sensitive"} {
		if !strings.Contains(got, want) {
			t.Fatalf("validation reasons missing %q: %#v", want, validation.Reasons)
		}
	}
}
