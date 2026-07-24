package evolution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

)

const (
	MemoryDirName              = "memory"
	MemoryIndexPath            = "memory/index.md"
	GlobalMemoryPath           = "memory/global.md"
	DefaultProjectMemoryPath   = "memory/projects/default/project.md"
	MemoryAuditPath            = "memory/audit.jsonl"
	defaultMemoryFilePerm      = 0644
	defaultMemoryDirectoryPerm = 0755
)

var appendAllowedTargets = map[string]bool{
	GlobalMemoryPath:         true,
	DefaultProjectMemoryPath: true,
}

// Manager owns controlled long-term memory initialization, validation, and writes.
type Manager struct {
	Workspace string
}

// Candidate is a proposed long-term memory update. It is intentionally small:
// the model proposes it, this package validates boundaries and writes only safe appends.
type Candidate struct {
	Type                     string `json:"type"`
	Target                   string `json:"target"`
	Content                  string `json:"content"`
	EvidenceIDs              []string `json:"evidence_ids"`
	Risk                     string `json:"risk"`
	Action                   string `json:"action"`
	RequiresUserConfirmation bool   `json:"requires_user_confirmation,omitempty"`
}

// Evidence records a single source that may support a memory candidate.
// Summary must be metadata only; raw tool output is deliberately not retained here.
type Evidence struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	ToolName string `json:"tool_name,omitempty"`
	Turn     int    `json:"turn,omitempty"`
	CallID   string `json:"call_id,omitempty"`
	Verified bool   `json:"verified"`
	Summary  string `json:"summary"`
}

// ValidationResult describes whether a candidate can be applied by the memory tool.
type ValidationResult struct {
	Valid   bool     `json:"valid"`
	Reasons []string `json:"reasons,omitempty"`
}

// ProposedCandidate is returned by memory_propose_update.
type ProposedCandidate struct {
	Candidate  Candidate        `json:"candidate"`
	Validation ValidationResult `json:"validation"`
}

// AuditRecord is appended to memory/audit.jsonl after every applied update.
type AuditRecord struct {
	Time          string `json:"time"`
	Target        string `json:"target"`
	Action        string `json:"action"`
	SourceSession string `json:"source_session,omitempty"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Summary       string `json:"summary"`
}

// NewManager creates a Manager rooted at workspace. Relative memory paths are resolved there.
func NewManager(workspace string) Manager {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	return Manager{Workspace: filepath.Clean(workspace)}
}

// MemoryRoot returns the absolute memory directory path.
func (m Manager) MemoryRoot() string {
	return filepath.Join(m.Workspace, MemoryDirName)
}

// EnsureStructure creates the P0 memory files if they do not exist.
func (m Manager) EnsureStructure() ([]string, error) {
	files := map[string]string{
		MemoryIndexPath:          defaultIndexContent(),
		GlobalMemoryPath:         defaultGlobalContent(),
		DefaultProjectMemoryPath: defaultProjectContent(),
	}
	var created []string
	for rel, content := range files {
		path, err := m.resolveMemoryPath(rel)
		if err != nil {
			return created, err
		}
		if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
			return created, err
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return created, err
		}
		if err := os.WriteFile(path, []byte(content), defaultMemoryFilePerm); err != nil {
			return created, err
		}
		created = append(created, rel)
	}
	auditPath, err := m.resolveMemoryPath(MemoryAuditPath)
	if err != nil {
		return created, err
	}
	if err := os.MkdirAll(filepath.Dir(auditPath), defaultMemoryDirectoryPerm); err != nil {
		return created, err
	}
	if _, err := os.Stat(auditPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(auditPath, nil, defaultMemoryFilePerm); err != nil {
			return created, err
		}
		created = append(created, MemoryAuditPath)
	} else if err != nil {
		return created, err
	}
	return created, nil
}

// ValidateCandidate enforces the P0 safety policy before a candidate can be written.
func (m Manager) ValidateCandidate(candidate Candidate, evidence []Evidence) ValidationResult {
	var reasons []string
	normalized := normalizeMemoryPath(candidate.Target)
	if normalized == "" {
		reasons = append(reasons, "target is required")
	} else if !appendAllowedTargets[normalized] {
		reasons = append(reasons, "target is outside the P0 append allowlist")
	}
	if action := normalizedAction(candidate.Action); action != "append" {
		reasons = append(reasons, "only append action is allowed")
	}
	if strings.TrimSpace(candidate.Content) == "" {
		reasons = append(reasons, "content is required")
	}
	if len(candidate.EvidenceIDs) == 0 {
		reasons = append(reasons, "at least one evidence_id is required")
	} else if unsupported := unsupportedEvidenceIDs(candidate.EvidenceIDs, evidence); len(unsupported) > 0 {
		reasons = append(reasons, "evidence_ids are missing or unverified: "+strings.Join(unsupported, ", "))
	}
	if containsSensitiveMaterial(candidate.Content) {
		reasons = append(reasons, "candidate appears to contain sensitive material")
	}
	if candidate.RequiresUserConfirmation {
		reasons = append(reasons, "candidate requires user confirmation")
	}
	if len(reasons) > 0 {
		return ValidationResult{Valid: false, Reasons: reasons}
	}
	return ValidationResult{Valid: true}
}

// ApplyCandidate appends a validated candidate and records an audit entry.
func (m Manager) ApplyCandidate(candidate Candidate, evidence []Evidence, sourceSession string) (AuditRecord, error) {
	candidate.Target = normalizeMemoryPath(candidate.Target)
	candidate.Action = normalizedAction(candidate.Action)
	validation := m.ValidateCandidate(candidate, evidence)
	if !validation.Valid {
		return AuditRecord{}, fmt.Errorf("candidate is not safe to apply: %s", strings.Join(validation.Reasons, "; "))
	}
	if _, err := m.EnsureStructure(); err != nil {
		return AuditRecord{}, err
	}
	path, err := m.resolveMemoryPath(candidate.Target)
	if err != nil {
		return AuditRecord{}, err
	}
	entry := formatMemoryEntry(candidate, sourceSession)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultMemoryFilePerm)
	if err != nil {
		return AuditRecord{}, err
	}
	_, writeErr := f.WriteString(entry)
	closeErr := f.Close()
	if writeErr != nil {
		return AuditRecord{}, writeErr
	}
	if closeErr != nil {
		return AuditRecord{}, closeErr
	}

	record := AuditRecord{
		Time:          time.Now().Format(time.RFC3339),
		Target:        candidate.Target,
		Action:        candidate.Action,
		SourceSession: sourceSession,
		EvidenceIDs:   append([]string(nil), candidate.EvidenceIDs...),
		Summary:       summarize(candidate.Content),
	}
	if err := m.appendAudit(record); err != nil {
		return AuditRecord{}, err
	}
	return record, nil
}

func (m Manager) appendAudit(record AuditRecord) error {
	auditPath, err := m.resolveMemoryPath(MemoryAuditPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(auditPath), defaultMemoryDirectoryPerm); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultMemoryFilePerm)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (m Manager) resolveMemoryPath(rel string) (string, error) {
	rel = normalizeMemoryPath(rel)
	if rel == "" {
		return "", fmt.Errorf("memory path is empty")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", fmt.Errorf("unsafe memory path %q", rel)
	}
	if !strings.HasPrefix(rel, MemoryDirName+"/") {
		return "", fmt.Errorf("memory path must be under memory/: %q", rel)
	}
	return filepath.Join(m.Workspace, filepath.FromSlash(rel)), nil
}

func normalizeMemoryPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return path
}

func normalizedAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "append"
	}
	return action
}

func unsupportedEvidenceIDs(ids []string, evidence []Evidence) []string {
	verified := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		if item.ID != "" && item.Verified {
			verified[item.ID] = true
		}
	}
	var unsupported []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || !verified[id] {
			unsupported = append(unsupported, id)
		}
	}
	return unsupported
}

func containsSensitiveMaterial(text string) bool {
	lower := strings.ToLower(text)
	sensitiveMarkers := []string{
		"api_key", "apikey", "access_token", "refresh_token", "authorization:",
		"cookie:", "set-cookie:", "secret=", "password=", "passwd=", "token=",
	}
	for _, marker := range sensitiveMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func formatMemoryEntry(candidate Candidate, sourceSession string) string {
	var b strings.Builder
	b.WriteString("\n\n## ")
	if candidate.Type != "" {
		b.WriteString(strings.TrimSpace(candidate.Type))
	} else {
		b.WriteString("memory")
	}
	b.WriteString(" - ")
	b.WriteString(time.Now().Format("2006-01-02"))
	b.WriteString("\n\n")
	b.WriteString("- evidence_ids: ")
	b.WriteString(strings.Join(candidate.EvidenceIDs, ", "))
	b.WriteByte('\n')
	if sourceSession != "" {
		b.WriteString("- source_session: ")
		b.WriteString(sourceSession)
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(candidate.Content))
	b.WriteByte('\n')
	return b.String()
}

func summarize(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) <= 120 {
		return content
	}
	return string(runes[:120])
}

func defaultIndexContent() string {
	return `# Memory Index

This index is injected as a lightweight pointer. Read the referenced files only when relevant.

- Global memory: memory/global.md
- Default project memory: memory/projects/default/project.md
- Memory audit log: memory/audit.jsonl
`
}

func defaultGlobalContent() string {
	return `# Global Memory

Long-lived user preferences and environment facts can be appended here only after verification.
`
}

func defaultProjectContent() string {
	return `# Default Project Memory

Reusable project conventions, validated lessons, and durable constraints can be appended here.
`
}
