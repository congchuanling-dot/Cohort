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
	if len(created) != 5 {
		t.Fatalf("created files = %d, want 5: %#v", len(created), created)
	}
	for _, rel := range []string{MemoryIndexPath, GlobalMemoryPath, manager.ProjectMemoryPath(), SOPCandidateMemoryPath, MemoryAuditPath} {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestManagerUsesGitRootNameForProjectMemoryPath_BitsUT(t *testing.T) {
	root := filepath.Join(t.TempDir(), "My Repo")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(workspace)

	if manager.ProjectID != "my-repo" {
		t.Fatalf("project id = %q, want my-repo", manager.ProjectID)
	}
	if manager.ProjectMemoryPath() != "memory/projects/my-repo/project.md" {
		t.Fatalf("project memory path = %q", manager.ProjectMemoryPath())
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
		Type:             "project_lesson",
		Target:           manager.ProjectMemoryPath(),
		Scene:            "controlled memory updates",
		TriggerKeywords:  []string{"memory", "audit", "append"},
		Lesson:           "When adding controlled memory updates, keep writes append-only and record memory/audit.jsonl.",
		RecommendedSteps: []string{"validate evidence", "append entry", "read back target"},
		EvidenceIDs:      []string{"tool:1:0"},
		Risk:             "low",
		Action:           "append",
	}

	result, err := manager.ApplyCandidate(candidate, evidence, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	record := result.AuditRecord
	if record.Target != manager.ProjectMemoryPath() || record.SourceSession != "session-1" {
		t.Fatalf("audit record = %#v", record)
	}
	if !result.ReadBackConfirmed {
		t.Fatal("expected read-back confirmation")
	}
	if result.MemoryRoot != filepath.Join(workspace, MemoryDirName) {
		t.Fatalf("memory root = %q, want %q", result.MemoryRoot, filepath.Join(workspace, MemoryDirName))
	}
	if result.TargetPath != filepath.Join(workspace, filepath.FromSlash(manager.ProjectMemoryPath())) {
		t.Fatalf("target path = %q", result.TargetPath)
	}

	projectData, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(manager.ProjectMemoryPath())))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectData), "## Memory Entry:") ||
		!strings.Contains(string(projectData), "trigger_keywords: memory, audit, append") ||
		!strings.Contains(string(projectData), candidate.Lesson) ||
		!strings.Contains(string(projectData), "tool:1:0") {
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
	if decoded.Target != manager.ProjectMemoryPath() || decoded.Action != "append" {
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
	path := filepath.Join(workspace, filepath.FromSlash(manager.ProjectMemoryPath()))
	if err := os.WriteFile(path, []byte("# Default Project Memory\n\n"+existing+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	validation := manager.ValidateCandidate(Candidate{
		Type:        "project_lesson",
		Target:      manager.ProjectMemoryPath(),
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

func TestManagerApplyCandidateWritesSOPCandidate_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)
	candidate := Candidate{
		Type:             "project_lesson",
		Target:           manager.ProjectMemoryPath(),
		Scene:            "Lark browser automation",
		TriggerKeywords:  []string{"飞书", "浏览器", "审批"},
		Lesson:           "Repeated Lark browser workflows should become a reviewed SOP candidate when the successful steps stabilize.",
		RecommendedSteps: []string{"wait for stable page", "snapshot elements", "verify after click"},
		PromoteToSOP:     true,
		SOPTitle:         "Lark browser automation",
		SOPPath:          "sops/lark_browser_automation.md",
		EvidenceIDs:      []string{"tool:1:0"},
		Action:           "append",
	}

	result, err := manager.ApplyCandidate(candidate, []Evidence{{ID: "tool:1:0", Verified: true}}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.SOPCandidatePath == "" {
		t.Fatal("expected SOP candidate path")
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(SOPCandidateMemoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## SOP Candidate: Lark browser automation", "sops/lark_browser_automation.md", "wait for stable page"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("SOP candidate missing %q:\n%s", want, data)
		}
	}
}

func TestManagerPromoteSOPCandidateRequiresIndexConfirmation_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, "sops"), 0755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(workspace, SOPIndexPath)
	if err := os.WriteFile(indexPath, []byte("# SOP Index\n\n## Rules\n\n- Read SOPs on demand.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		Type:             "project_lesson",
		Target:           manager.ProjectMemoryPath(),
		Scene:            "Lark approval browser flow",
		TriggerKeywords:  []string{"lark", "browser", "approval"},
		Lesson:           "Use the stable browser flow only after the approval page is loaded.",
		RecommendedSteps: []string{"wait for stable page", "snapshot elements", "verify submission"},
		PromoteToSOP:     true,
		SOPTitle:         "Lark Approval Browser Flow",
		SOPPath:          "sops/lark_approval_browser_flow.md",
		EvidenceIDs:      []string{"tool:1:0"},
		Action:           "append",
	}
	if _, err := manager.ApplyCandidate(candidate, []Evidence{{ID: "tool:1:0", Verified: true}}, "session-1"); err != nil {
		t.Fatal(err)
	}
	candidates, err := manager.ListSOPCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: %#v", len(candidates), candidates)
	}

	result, err := manager.PromoteSOPCandidate(candidates[0].ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.SOPCreated || !result.RequiresIndexConfirmation || result.IndexUpdated {
		t.Fatalf("promotion result = %#v", result)
	}
	sopData, err := os.ReadFile(filepath.Join(workspace, "sops", "lark_approval_browser_flow.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sopData), "promoted_from: "+candidates[0].ID) ||
		!strings.Contains(string(sopData), "wait for stable page") {
		t.Fatalf("promoted SOP content is incomplete:\n%s", sopData)
	}
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(indexData), "lark_approval_browser_flow.md") {
		t.Fatalf("index was updated without confirmation:\n%s", indexData)
	}
}

func TestManagerPromoteSOPCandidateUpdatesIndexWithConfirmation_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, "sops"), 0755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(workspace, SOPIndexPath)
	if err := os.WriteFile(indexPath, []byte("# SOP Index\n\n## Rules\n\n- Read SOPs on demand.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		Type:             "project_lesson",
		Target:           manager.ProjectMemoryPath(),
		Scene:            "Repeated release checks",
		TriggerKeywords:  []string{"release", "test", "verify"},
		Lesson:           "Release checks should run tests before reporting completion.",
		RecommendedSteps: []string{"run focused tests", "run full tests", "summarize residual risk"},
		PromoteToSOP:     true,
		SOPTitle:         "Release Checks",
		SOPPath:          "sops/release_checks.md",
		EvidenceIDs:      []string{"tool:1:0"},
		Action:           "append",
	}
	if _, err := manager.ApplyCandidate(candidate, []Evidence{{ID: "tool:1:0", Verified: true}}, "session-2"); err != nil {
		t.Fatal(err)
	}
	candidates, err := manager.ListSOPCandidates()
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.PromoteSOPCandidate(candidates[0].ID, true, "test human confirmed index update")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IndexUpdated || result.RequiresIndexConfirmation {
		t.Fatalf("promotion result = %#v", result)
	}
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Release Checks", "`sops/release_checks.md`", "test human confirmed index update", candidates[0].ID} {
		if !strings.Contains(string(indexData), want) {
			t.Fatalf("index missing %q:\n%s", want, indexData)
		}
	}
	auditData, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(MemoryAuditPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditData), "sop_promote") ||
		!strings.Contains(string(auditData), "test human confirmed index update") {
		t.Fatalf("audit missing SOP promotion confirmation:\n%s", auditData)
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

func TestManagerApplyRejectsUnsafeCandidateWithoutCreatingMemory_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := NewManager(workspace)

	_, err := manager.ApplyCandidate(Candidate{
		Type:        "project_lesson",
		Target:      "../outside.md",
		Content:     "Unsafe candidates must not initialize memory files.",
		EvidenceIDs: []string{"tool:1:0"},
		Action:      "append",
	}, []Evidence{{ID: "tool:1:0", Verified: true}}, "session-1")

	if err == nil {
		t.Fatal("expected unsafe candidate to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(workspace, MemoryDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("memory dir stat err = %v, want not exist", statErr)
	}
}

func TestManagerRejectsUnverifiedEvidenceID_BitsUT(t *testing.T) {
	manager := NewManager(t.TempDir())
	validation := manager.ValidateCandidate(Candidate{
		Type:        "project_lesson",
		Target:      manager.ProjectMemoryPath(),
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
