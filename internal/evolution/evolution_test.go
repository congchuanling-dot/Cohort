package evolution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	evidence := []Evidence{
		{
			ID:       "tool:1:0",
			Source:   "tool",
			ToolName: "code_run",
			Turn:     1,
			CallID:   "call-test",
			Verified: true,
			Summary:  "code_run completed with exit_code=0",
		},
	}
	candidate := Candidate{
		Type:        "project_lesson",
		Target:      DefaultProjectMemoryPath,
		Content:     "When adding controlled memory updates, keep writes append-only and record memory/audit.jsonl.",
		EvidenceIDs: []string{"tool:1:0"},
		Risk:        "low",
		Action:      "append",
	}

	result, err := manager.ApplyCandidate(candidate, evidence, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	record := result.AuditRecord
	if record.Target != DefaultProjectMemoryPath || record.SourceSession != "session-1" {
		t.Fatalf("audit record = %#v", record)
	}
	if !result.ReadBackConfirmed {
		t.Fatal("expected read-back confirmation")
	}
	if result.MemoryRoot != filepath.Join(workspace, MemoryDirName) {
		t.Fatalf("memory root = %q, want %q", result.MemoryRoot, filepath.Join(workspace, MemoryDirName))
	}
	if result.TargetPath != filepath.Join(workspace, filepath.FromSlash(DefaultProjectMemoryPath)) {
		t.Fatalf("target path = %q", result.TargetPath)
	}

	projectData, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(DefaultProjectMemoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectData), candidate.Content) || !strings.Contains(string(projectData), "tool:1:0") {
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

func TestManagerRejectsDuplicateMemoryCandidate_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)
	if _, err := manager.EnsureStructure(); err != nil {
		t.Fatal(err)
	}
	existing := "When memory content already exists, reject duplicate long-term memory updates."
	path := filepath.Join(workspace, filepath.FromSlash(DefaultProjectMemoryPath))
	if err := os.WriteFile(path, []byte("# Default Project Memory\n\n"+existing+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	validation := manager.ValidateCandidate(Candidate{
		Type:        "project_lesson",
		Target:      DefaultProjectMemoryPath,
		Content:     "When memory content already exists,\nreject duplicate long-term memory updates.",
		EvidenceIDs: []string{"tool:1:0"},
		Action:      "append",
	}, []Evidence{{ID: "tool:1:0", Verified: true}})

	if validation.Valid {
		t.Fatal("expected duplicate candidate to be rejected")
	}
	if got := strings.Join(validation.Reasons, "\n"); !strings.Contains(got, "duplicate") {
		t.Fatalf("validation reasons = %#v", validation.Reasons)
	}
}

func TestManagerRejectsUnsafeMemoryCandidate_BitsUT(t *testing.T) {
	manager := NewManager(t.TempDir())
	validation := manager.ValidateCandidate(Candidate{
		Type:        "project_lesson",
		Target:      "internal/agent/runner.go",
		Content:     "token=abc123",
		EvidenceIDs: []string{"tool:99:99"},
		Action:      "overwrite",
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

func TestManagerRejectsUnverifiedEvidenceID_BitsUT(t *testing.T) {
	manager := NewManager(t.TempDir())
	validation := manager.ValidateCandidate(Candidate{
		Type:        "project_lesson",
		Target:      DefaultProjectMemoryPath,
		Content:     "A failed command must not be retained as a verified project lesson.",
		EvidenceIDs: []string{"tool:1:0"},
		Action:      "append",
	}, []Evidence{{
		ID:       "tool:1:0",
		Source:   "tool",
		ToolName: "code_run",
		Verified: false,
		Summary:  "code_run did not produce verified evidence",
	}})

	if validation.Valid {
		t.Fatal("expected unverified evidence to be rejected")
	}
	if got := strings.Join(validation.Reasons, "\n"); !strings.Contains(got, "missing or unverified") {
		t.Fatalf("validation reasons = %#v", validation.Reasons)
	}
}
