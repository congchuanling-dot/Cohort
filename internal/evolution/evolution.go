package evolution

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	MemoryDirName              = "memory"
	MemoryIndexPath            = "memory/index.md"
	GlobalMemoryPath           = "memory/global.md"
	DefaultProjectMemoryPath   = "memory/projects/default/project.md"
	SOPCandidateMemoryPath     = "memory/reflection/sop_candidates.md"
	MemoryAuditPath            = "memory/audit.jsonl"
	defaultMemoryFilePerm      = 0644
	defaultMemoryDirectoryPerm = 0755
)

// Manager owns controlled long-term memory initialization, validation, and writes.
type Manager struct {
	Workspace string
	ProjectID string
}

// Candidate is a proposed long-term memory update. It is intentionally small:
// the model proposes it, this package validates boundaries and writes only safe appends.
type Candidate struct {
	Type                     string   `json:"type"`
	Target                   string   `json:"target"`
	Content                  string   `json:"content"`
	Scene                    string   `json:"scene,omitempty"`
	TriggerKeywords          []string `json:"trigger_keywords,omitempty"`
	Lesson                   string   `json:"lesson,omitempty"`
	RecommendedSteps         []string `json:"recommended_steps,omitempty"`
	PromoteToSOP             bool     `json:"promote_to_sop,omitempty"`
	SOPTitle                 string   `json:"sop_title,omitempty"`
	SOPPath                  string   `json:"sop_path,omitempty"`
	EvidenceIDs              []string `json:"evidence_ids"`
	Risk                     string   `json:"risk"`
	Action                   string   `json:"action"`
	RequiresUserConfirmation bool     `json:"requires_user_confirmation,omitempty"`
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

// ApplyResult is returned after a candidate is appended and read back from disk.
type ApplyResult struct {
	AuditRecord       AuditRecord `json:"audit_record"`
	MemoryRoot        string      `json:"memory_root"`
	TargetPath        string      `json:"target_path"`
	SOPCandidatePath  string      `json:"sop_candidate_path,omitempty"`
	ReadBackConfirmed bool        `json:"read_back_confirmed"`
	ReadBackBytes     int         `json:"read_back_bytes"`
}

// AuditRecord is appended to memory/audit.jsonl after every applied update.
type AuditRecord struct {
	Time          string   `json:"time"`
	Target        string   `json:"target"`
	Action        string   `json:"action"`
	SourceSession string   `json:"source_session,omitempty"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Summary       string   `json:"summary"`
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
	workspace = filepath.Clean(workspace)
	return Manager{Workspace: workspace, ProjectID: discoverProjectID(workspace)}
}

// MemoryRoot returns the absolute memory directory path.
func (m Manager) MemoryRoot() string {
	return filepath.Join(m.Workspace, MemoryDirName)
}

// ProjectMemoryPath returns the project-specific long-term memory path.
func (m Manager) ProjectMemoryPath() string {
	return projectMemoryPath(m.ProjectID)
}

// EnsureStructure creates the P0 memory files if they do not exist.
func (m Manager) EnsureStructure() ([]string, error) {
	files := map[string]string{
		MemoryIndexPath:        defaultIndexContent(m.ProjectID, m.ProjectMemoryPath()),
		GlobalMemoryPath:       defaultGlobalContent(),
		m.ProjectMemoryPath():  defaultProjectContent(m.ProjectID),
		SOPCandidateMemoryPath: defaultSOPCandidateContent(),
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
	} else if !m.isAppendAllowedTarget(normalized) {
		reasons = append(reasons, "target is outside the P0 append allowlist")
	}
	if action := normalizedAction(candidate.Action); action != "append" {
		reasons = append(reasons, "only append action is allowed")
	}
	if strings.TrimSpace(candidateMemoryText(candidate)) == "" {
		reasons = append(reasons, "lesson or content is required")
	}
	if candidate.PromoteToSOP && strings.TrimSpace(candidateSOPTitle(candidate)) == "" {
		reasons = append(reasons, "sop_title or scene is required when promote_to_sop is true")
	}
	if len(candidate.EvidenceIDs) == 0 {
		reasons = append(reasons, "at least one evidence_id is required")
	} else if unsupported := unsupportedEvidenceIDs(candidate.EvidenceIDs, evidence); len(unsupported) > 0 {
		reasons = append(reasons, "evidence_ids are missing or unverified: "+strings.Join(unsupported, ", "))
	}
	if containsSensitiveMaterial(candidateAllText(candidate)) {
		reasons = append(reasons, "candidate appears to contain sensitive material")
	}
	if duplicate, err := m.hasDuplicateContent(normalized, candidateDuplicateText(candidate)); err != nil {
		reasons = append(reasons, "failed to check duplicate memory content: "+err.Error())
	} else if duplicate {
		reasons = append(reasons, "duplicate memory content already exists")
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
func (m Manager) ApplyCandidate(candidate Candidate, evidence []Evidence, sourceSession string) (ApplyResult, error) {
	candidate.Target = normalizeMemoryPath(candidate.Target)
	candidate.Action = normalizedAction(candidate.Action)
	validation := m.ValidateCandidate(candidate, evidence)
	if !validation.Valid {
		return ApplyResult{}, fmt.Errorf("candidate is not safe to apply: %s", strings.Join(validation.Reasons, "; "))
	}
	if _, err := m.EnsureStructure(); err != nil {
		return ApplyResult{}, err
	}
	validation = m.ValidateCandidate(candidate, evidence)
	if !validation.Valid {
		return ApplyResult{}, fmt.Errorf("candidate is not safe to apply: %s", strings.Join(validation.Reasons, "; "))
	}
	path, err := m.resolveMemoryPath(candidate.Target)
	if err != nil {
		return ApplyResult{}, err
	}
	entry := formatMemoryEntry(candidate, sourceSession)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultMemoryFilePerm)
	if err != nil {
		return ApplyResult{}, err
	}
	_, writeErr := f.WriteString(entry)
	closeErr := f.Close()
	if writeErr != nil {
		return ApplyResult{}, writeErr
	}
	if closeErr != nil {
		return ApplyResult{}, closeErr
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read back memory target: %w", err)
	}
	if !strings.Contains(string(readBack), entry) {
		return ApplyResult{}, fmt.Errorf("read back memory target did not contain applied entry")
	}
	sopCandidatePath := ""
	if candidate.PromoteToSOP && candidate.Target != SOPCandidateMemoryPath {
		path, err := m.appendSOPCandidate(candidate, sourceSession)
		if err != nil {
			return ApplyResult{}, err
		}
		sopCandidatePath = path
	}

	record := AuditRecord{
		Time:          time.Now().Format(time.RFC3339),
		Target:        candidate.Target,
		Action:        candidate.Action,
		SourceSession: sourceSession,
		EvidenceIDs:   append([]string(nil), candidate.EvidenceIDs...),
		Summary:       summarize(candidateMemoryText(candidate)),
	}
	if err := m.appendAudit(record); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		AuditRecord:       record,
		MemoryRoot:        m.MemoryRoot(),
		TargetPath:        path,
		SOPCandidatePath:  sopCandidatePath,
		ReadBackConfirmed: true,
		ReadBackBytes:     len(readBack),
	}, nil
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

func (m Manager) isAppendAllowedTarget(path string) bool {
	switch path {
	case GlobalMemoryPath, m.ProjectMemoryPath(), SOPCandidateMemoryPath:
		return true
	default:
		return false
	}
}

func (m Manager) appendSOPCandidate(candidate Candidate, sourceSession string) (string, error) {
	path, err := m.resolveMemoryPath(SOPCandidateMemoryPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return "", err
	}
	entry := formatSOPCandidateEntry(candidate, sourceSession)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultMemoryFilePerm)
	if err != nil {
		return "", err
	}
	_, writeErr := f.WriteString(entry)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
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

func (m Manager) hasDuplicateContent(normalizedTarget, content string) (bool, error) {
	content = normalizeMemoryContent(content)
	if normalizedTarget == "" || content == "" {
		return false, nil
	}
	path, err := m.resolveMemoryPath(normalizedTarget)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(normalizeMemoryContent(string(data)), content), nil
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

func normalizeMemoryContent(content string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
}

func candidateMemoryText(candidate Candidate) string {
	if text := strings.TrimSpace(candidate.Lesson); text != "" {
		return text
	}
	return strings.TrimSpace(candidate.Content)
}

func candidateDuplicateText(candidate Candidate) string {
	parts := []string{
		candidate.Scene,
		strings.Join(candidate.TriggerKeywords, " "),
		candidateMemoryText(candidate),
		strings.Join(candidate.RecommendedSteps, " "),
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func candidateAllText(candidate Candidate) string {
	parts := []string{
		candidate.Type,
		candidate.Target,
		candidate.Content,
		candidate.Scene,
		strings.Join(candidate.TriggerKeywords, " "),
		candidate.Lesson,
		strings.Join(candidate.RecommendedSteps, " "),
		candidate.SOPTitle,
		candidate.SOPPath,
	}
	return strings.Join(parts, "\n")
}

func candidateSOPTitle(candidate Candidate) string {
	if title := strings.TrimSpace(candidate.SOPTitle); title != "" {
		return title
	}
	return strings.TrimSpace(candidate.Scene)
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
	entryID := memoryEntryID(candidate)
	var b strings.Builder
	b.WriteString("\n\n## Memory Entry: ")
	b.WriteString(entryID)
	b.WriteString("\n\n")
	writeMemoryField(&b, "type", defaultString(candidate.Type, "memory"))
	writeMemoryField(&b, "scene", candidate.Scene)
	writeMemoryField(&b, "trigger_keywords", strings.Join(cleanStringSlice(candidate.TriggerKeywords), ", "))
	writeMemoryField(&b, "risk", candidate.Risk)
	writeMemoryField(&b, "action", normalizedAction(candidate.Action))
	writeMemoryField(&b, "created_at", time.Now().Format("2006-01-02"))
	writeMemoryField(&b, "evidence_ids", strings.Join(candidate.EvidenceIDs, ", "))
	writeMemoryField(&b, "source_session", sourceSession)
	if candidate.PromoteToSOP {
		writeMemoryField(&b, "promote_to_sop", "true")
		writeMemoryField(&b, "sop_title", candidateSOPTitle(candidate))
		writeMemoryField(&b, "sop_path", candidateSOPPath(candidate))
	}
	b.WriteString("\n### Lesson\n\n")
	b.WriteString(strings.TrimSpace(candidateMemoryText(candidate)))
	b.WriteByte('\n')
	if steps := cleanStringSlice(candidate.RecommendedSteps); len(steps) > 0 {
		b.WriteString("\n### Recommended Steps\n\n")
		for _, step := range steps {
			b.WriteString("- ")
			b.WriteString(step)
			b.WriteByte('\n')
		}
	}
	if content := strings.TrimSpace(candidate.Content); content != "" && content != strings.TrimSpace(candidate.Lesson) {
		b.WriteString("\n### Notes\n\n")
		b.WriteString(content)
		b.WriteByte('\n')
	}
	return b.String()
}

func formatSOPCandidateEntry(candidate Candidate, sourceSession string) string {
	var b strings.Builder
	b.WriteString("\n\n## SOP Candidate: ")
	b.WriteString(candidateSOPTitle(candidate))
	b.WriteString("\n\n")
	writeMemoryField(&b, "scene", candidate.Scene)
	writeMemoryField(&b, "trigger_keywords", strings.Join(cleanStringSlice(candidate.TriggerKeywords), ", "))
	writeMemoryField(&b, "proposed_sop_path", candidateSOPPath(candidate))
	writeMemoryField(&b, "source_session", sourceSession)
	writeMemoryField(&b, "evidence_ids", strings.Join(candidate.EvidenceIDs, ", "))
	b.WriteString("\n### Why This Should Become SOP\n\n")
	b.WriteString(strings.TrimSpace(candidateMemoryText(candidate)))
	b.WriteByte('\n')
	if steps := cleanStringSlice(candidate.RecommendedSteps); len(steps) > 0 {
		b.WriteString("\n### Draft Steps\n\n")
		for _, step := range steps {
			b.WriteString("- ")
			b.WriteString(step)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func writeMemoryField(b *strings.Builder, key string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func memoryEntryID(candidate Candidate) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(candidateDuplicateText(candidate)))
	return fmt.Sprintf("mem-%s-%08x", time.Now().Format("20060102"), h.Sum32())
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func cleanStringSlice(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func candidateSOPPath(candidate Candidate) string {
	path := normalizeSOPPath(candidate.SOPPath)
	if path != "" {
		return path
	}
	title := candidateSOPTitle(candidate)
	if title == "" {
		title = candidate.Scene
	}
	return "sops/" + slugify(title) + ".md"
}

func normalizeSOPPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "." {
		return ""
	}
	if strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
		return ""
	}
	if !strings.HasPrefix(path, "sops/") || !strings.HasSuffix(path, ".md") {
		return ""
	}
	return path
}

func slugify(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	lastDash := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "generated-sop"
	}
	return slug
}

func projectMemoryPath(projectID string) string {
	projectID = sanitizeProjectID(projectID)
	if projectID == "" {
		projectID = "default"
	}
	return "memory/projects/" + projectID + "/project.md"
}

func discoverProjectID(workspace string) string {
	if root := findGitRoot(workspace); root != "" {
		return sanitizeProjectID(filepath.Base(root))
	}
	return sanitizeProjectID(filepath.Base(workspace))
}

func findGitRoot(path string) string {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func sanitizeProjectID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func summarize(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) <= 120 {
		return content
	}
	return string(runes[:120])
}

func defaultIndexContent(projectID string, projectMemoryPath string) string {
	return fmt.Sprintf(`# Memory Index

This index is injected as a lightweight pointer. Relevant long-term memory entries may be injected automatically when task keywords match.

- Global memory: memory/global.md
- Project memory (%s): %s
- SOP candidates: memory/reflection/sop_candidates.md
- Memory audit log: memory/audit.jsonl
`, projectID, projectMemoryPath)
}

func defaultGlobalContent() string {
	return `# Global Memory

Long-lived user preferences and environment facts can be appended here only after verification.
`
}

func defaultProjectContent(projectID string) string {
	return fmt.Sprintf(`# Project Memory: %s

Reusable project conventions, validated lessons, and durable constraints can be appended here.

Entries use structured sections:
- scene
- trigger_keywords
- lesson
- recommended_steps
- evidence_ids
`, projectID)
}

func defaultSOPCandidateContent() string {
	return `# SOP Candidates

Reusable flows that may deserve promotion to reviewed SOP files are appended here.
`
}
