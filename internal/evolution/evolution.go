package evolution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohert/internal/llm"
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
	Evidence                 string `json:"evidence"`
	Risk                     string `json:"risk"`
	Action                   string `json:"action"`
	RequiresUserConfirmation bool   `json:"requires_user_confirmation,omitempty"`
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
	Evidence      string `json:"evidence"`
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
func (m Manager) ValidateCandidate(candidate Candidate, history []llm.Message) ValidationResult {
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
	if strings.TrimSpace(candidate.Evidence) == "" {
		reasons = append(reasons, "evidence is required")
	} else if !evidenceIsSupported(candidate.Evidence, history) {
		reasons = append(reasons, "evidence is not supported by successful tools, existing memory, or explicit user preference")
	}
	if containsSensitiveMaterial(candidate.Content) || containsSensitiveMaterial(candidate.Evidence) {
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
func (m Manager) ApplyCandidate(candidate Candidate, history []llm.Message, sourceSession string) (AuditRecord, error) {
	candidate.Target = normalizeMemoryPath(candidate.Target)
	candidate.Action = normalizedAction(candidate.Action)
	validation := m.ValidateCandidate(candidate, history)
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
		Evidence:      candidate.Evidence,
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

func evidenceIsSupported(evidence string, history []llm.Message) bool {
	lower := strings.ToLower(evidence)
	if strings.Contains(lower, "existing memory") || strings.Contains(lower, "已存在记忆") {
		return true
	}
	if strings.Contains(lower, "user preference") || strings.Contains(lower, "用户偏好") || strings.Contains(lower, "用户明确") {
		return hasUserMessage(history)
	}
	for _, message := range history {
		if message.Role != llm.RoleTool {
			continue
		}
		name := strings.ToLower(message.Name)
		callID := strings.ToLower(message.ToolCallID)
		if name == "" && callID == "" {
			continue
		}
		if (name != "" && strings.Contains(lower, name)) || (callID != "" && strings.Contains(lower, callID)) {
			if toolResultIsVerified(message) {
				return true
			}
		}
	}
	return false
}

func hasUserMessage(history []llm.Message) bool {
	for _, message := range history {
		if message.Role == llm.RoleUser && strings.TrimSpace(message.Content) != "" {
			return true
		}
	}
	return false
}

func toolResultIsVerified(message llm.Message) bool {
	content := strings.ToLower(message.Content)
	if strings.HasPrefix(strings.TrimSpace(content), "error:") || strings.Contains(content, `"status":"error"`) {
		return false
	}
	switch message.Name {
	case "code_run":
		return strings.Contains(content, `"status":"success"`) && strings.Contains(content, `"exit_code":0`)
	case "file_read":
		return strings.TrimSpace(message.Content) != ""
	default:
		return strings.Contains(content, `"status":"success"`) || strings.TrimSpace(message.Content) != ""
	}
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
	b.WriteString("- evidence: ")
	b.WriteString(strings.TrimSpace(candidate.Evidence))
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
