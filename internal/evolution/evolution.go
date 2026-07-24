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
	SOPCandidateMemoryPath     = "memory/reflection/sop_candidates.md"
	MemoryAuditPath            = "memory/audit.jsonl"
	SOPIndexPath               = "sops/index.md"
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

// SOPCandidate is a reviewed workflow candidate stored under memory/reflection.
type SOPCandidate struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Scene           string   `json:"scene,omitempty"`
	TriggerKeywords []string `json:"trigger_keywords,omitempty"`
	ProposedSOPPath string   `json:"proposed_sop_path,omitempty"`
	SourceSession   string   `json:"source_session,omitempty"`
	EvidenceIDs     []string `json:"evidence_ids,omitempty"`
	Why             string   `json:"why,omitempty"`
	DraftSteps      []string `json:"draft_steps,omitempty"`
}

// SOPPromotionResult describes the controlled SOP promotion side effects.
type SOPPromotionResult struct {
	Candidate                 SOPCandidate `json:"candidate"`
	SOPRoot                   string       `json:"sop_root"`
	SOPPath                   string       `json:"sop_path"`
	SOPAbsolutePath           string       `json:"sop_absolute_path"`
	SOPCreated                bool         `json:"sop_created"`
	IndexPath                 string       `json:"index_path,omitempty"`
	IndexUpdated              bool         `json:"index_updated"`
	RequiresIndexConfirmation bool         `json:"requires_index_confirmation"`
	Confirmation              string       `json:"confirmation,omitempty"`
}

// AuditRecord is appended to memory/audit.jsonl after every applied update.
type AuditRecord struct {
	Time          string   `json:"time"`
	Target        string   `json:"target"`
	Action        string   `json:"action"`
	SourceSession string   `json:"source_session,omitempty"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Summary       string   `json:"summary"`
	Confirmation  string   `json:"confirmation,omitempty"`
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

// SOPRoot returns the project root used for reviewed SOP files.
func (m Manager) SOPRoot() string {
	if root := findGitRoot(m.Workspace); root != "" {
		return root
	}
	return m.Workspace
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

// ListSOPCandidates returns structured SOP promotion candidates.
func (m Manager) ListSOPCandidates() ([]SOPCandidate, error) {
	if _, err := m.EnsureStructure(); err != nil {
		return nil, err
	}
	path, err := m.resolveMemoryPath(SOPCandidateMemoryPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blocks := splitHeadingBlocks(string(data), "## SOP Candidate:")
	candidates := make([]SOPCandidate, 0, len(blocks))
	for _, block := range blocks {
		candidate := parseSOPCandidateBlock(block)
		if candidate.ID == "" || candidate.Title == "" {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// PromoteSOPCandidate writes a reviewed SOP file and, only with explicit
// confirmation, updates sops/index.md so future tasks can route to it.
func (m Manager) PromoteSOPCandidate(id string, confirmIndex bool, confirmation string) (SOPPromotionResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SOPPromotionResult{}, fmt.Errorf("sop candidate id is required")
	}
	candidates, err := m.ListSOPCandidates()
	if err != nil {
		return SOPPromotionResult{}, err
	}
	candidate, err := findSOPCandidate(candidates, id)
	if err != nil {
		return SOPPromotionResult{}, err
	}
	sopPath := normalizeSOPPath(candidate.ProposedSOPPath)
	if sopPath == "" {
		sopPath = "sops/" + slugify(candidate.Title) + ".md"
	}
	sopAbs, err := m.resolveSOPPath(sopPath)
	if err != nil {
		return SOPPromotionResult{}, err
	}
	created, err := writePromotedSOPFile(sopAbs, sopPath, candidate)
	if err != nil {
		return SOPPromotionResult{}, err
	}
	result := SOPPromotionResult{
		Candidate:                 candidate,
		SOPRoot:                   m.SOPRoot(),
		SOPPath:                   sopPath,
		SOPAbsolutePath:           sopAbs,
		SOPCreated:                created,
		RequiresIndexConfirmation: !confirmIndex,
		Confirmation:              strings.TrimSpace(confirmation),
	}
	if !confirmIndex {
		return result, nil
	}
	if result.Confirmation == "" {
		return SOPPromotionResult{}, fmt.Errorf("human confirmation is required before updating %s", SOPIndexPath)
	}
	indexPath, updated, err := m.updateSOPIndex(candidate, sopPath, result.Confirmation)
	if err != nil {
		return SOPPromotionResult{}, err
	}
	result.IndexPath = indexPath
	result.IndexUpdated = updated
	result.RequiresIndexConfirmation = false
	if err := m.appendAudit(AuditRecord{
		Time:          time.Now().Format(time.RFC3339),
		Target:        SOPIndexPath,
		Action:        "sop_promote",
		SourceSession: candidate.SourceSession,
		EvidenceIDs:   append([]string(nil), candidate.EvidenceIDs...),
		Summary:       fmt.Sprintf("promoted SOP candidate %s to %s; index_updated=%t", candidate.ID, sopPath, updated),
		Confirmation:  result.Confirmation,
	}); err != nil {
		return SOPPromotionResult{}, err
	}
	return result, nil
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

func (m Manager) resolveSOPPath(rel string) (string, error) {
	rel = normalizeSOPPath(rel)
	if rel == "" {
		return "", fmt.Errorf("invalid SOP path")
	}
	root := filepath.Clean(m.SOPRoot())
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe SOP path %q", rel)
	}
	return path, nil
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
	writeMemoryField(&b, "id", entryID)
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
	candidateID := sopCandidateID(SOPCandidate{
		Title:           candidateSOPTitle(candidate),
		Scene:           candidate.Scene,
		TriggerKeywords: cleanStringSlice(candidate.TriggerKeywords),
		ProposedSOPPath: candidateSOPPath(candidate),
		SourceSession:   sourceSession,
		EvidenceIDs:     cleanStringSlice(candidate.EvidenceIDs),
		Why:             candidateMemoryText(candidate),
		DraftSteps:      cleanStringSlice(candidate.RecommendedSteps),
	})
	var b strings.Builder
	b.WriteString("\n\n## SOP Candidate: ")
	b.WriteString(candidateSOPTitle(candidate))
	b.WriteString("\n\n")
	writeMemoryField(&b, "id", candidateID)
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
	return stableHashID("mem", candidateDuplicateText(candidate))
}

func sopCandidateID(candidate SOPCandidate) string {
	parts := []string{
		candidate.Title,
		candidate.Scene,
		strings.Join(candidate.TriggerKeywords, " "),
		candidate.ProposedSOPPath,
		candidate.Why,
		strings.Join(candidate.DraftSteps, " "),
	}
	return stableHashID("sop", strings.Join(strings.Fields(strings.Join(parts, " ")), " "))
}

func stableHashID(prefix, value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.Join(strings.Fields(value), " ")))
	return fmt.Sprintf("%s-%08x", prefix, h.Sum32())
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

func splitHeadingBlocks(content string, headingPrefix string) []string {
	lines := strings.Split(content, "\n")
	var blocks []string
	var current strings.Builder
	inBlock := false
	flush := func() {
		if !inBlock {
			return
		}
		block := strings.TrimSpace(current.String())
		if block != "" {
			blocks = append(blocks, block)
		}
		current.Reset()
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), headingPrefix) {
			flush()
			inBlock = true
		}
		if inBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	flush()
	return blocks
}

func parseSOPCandidateBlock(block string) SOPCandidate {
	title := ""
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## SOP Candidate:") {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "## SOP Candidate:"))
			break
		}
	}
	candidate := SOPCandidate{
		ID:              extractListField(block, "id"),
		Title:           title,
		Scene:           extractListField(block, "scene"),
		TriggerKeywords: splitCommaList(extractListField(block, "trigger_keywords")),
		ProposedSOPPath: normalizeSOPPath(extractListField(block, "proposed_sop_path")),
		SourceSession:   extractListField(block, "source_session"),
		EvidenceIDs:     splitCommaList(extractListField(block, "evidence_ids")),
		Why:             extractMarkdownSection(block, "Why This Should Become SOP"),
		DraftSteps:      extractBulletSection(block, "Draft Steps"),
	}
	if candidate.ID == "" {
		candidate.ID = sopCandidateID(candidate)
	}
	return candidate
}

func extractListField(content, field string) string {
	prefix := "- " + field + ":"
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}

func splitCommaList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，'
	})
	return cleanStringSlice(fields)
}

func extractMarkdownSection(content, heading string) string {
	prefix := "### " + heading
	lines := strings.Split(content, "\n")
	var b strings.Builder
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") {
			if inSection {
				break
			}
			if trimmed == prefix {
				inSection = true
			}
			continue
		}
		if inSection {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func extractBulletSection(content, heading string) []string {
	text := extractMarkdownSection(content, heading)
	var steps []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line != "" {
			steps = append(steps, line)
		}
	}
	return cleanStringSlice(steps)
}

func findSOPCandidate(candidates []SOPCandidate, id string) (SOPCandidate, error) {
	var matches []SOPCandidate
	for _, candidate := range candidates {
		if candidate.ID == id || strings.HasPrefix(candidate.ID, id) || strings.EqualFold(candidate.Title, id) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return SOPCandidate{}, fmt.Errorf("SOP candidate %q not found", id)
	}
	if len(matches) > 1 {
		return SOPCandidate{}, fmt.Errorf("SOP candidate %q is ambiguous", id)
	}
	return matches[0], nil
}

func writePromotedSOPFile(path, relPath string, candidate SOPCandidate) (bool, error) {
	if data, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(data), "promoted_from: "+candidate.ID) {
			return false, nil
		}
		return false, fmt.Errorf("SOP file already exists and was not generated from candidate %s: %s", candidate.ID, relPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(formatPromotedSOP(candidate, relPath)), defaultMemoryFilePerm)
}

func formatPromotedSOP(candidate SOPCandidate, relPath string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(candidate.Title)
	b.WriteString("\n\n")
	b.WriteString("<!-- promoted_from: ")
	b.WriteString(candidate.ID)
	b.WriteString(" -->\n\n")
	writeMemoryField(&b, "sop_path", relPath)
	writeMemoryField(&b, "source_candidate", candidate.ID)
	writeMemoryField(&b, "source_session", candidate.SourceSession)
	writeMemoryField(&b, "created_at", time.Now().Format("2006-01-02"))
	b.WriteString("\n## Scene\n\n")
	if strings.TrimSpace(candidate.Scene) != "" {
		b.WriteString(candidate.Scene)
	} else {
		b.WriteString(candidate.Title)
	}
	b.WriteString("\n\n## Trigger Keywords\n\n")
	for _, keyword := range candidate.TriggerKeywords {
		b.WriteString("- ")
		b.WriteString(keyword)
		b.WriteByte('\n')
	}
	if len(candidate.TriggerKeywords) == 0 {
		b.WriteString("- ")
		b.WriteString(candidate.Title)
		b.WriteByte('\n')
	}
	b.WriteString("\n## Operating Rule\n\n")
	if strings.TrimSpace(candidate.Why) != "" {
		b.WriteString(strings.TrimSpace(candidate.Why))
	} else {
		b.WriteString("Use this SOP only when the task matches the scene above.")
	}
	b.WriteString("\n\n## Steps\n\n")
	if len(candidate.DraftSteps) == 0 {
		b.WriteString("1. Confirm the task matches this SOP scene.\n")
		b.WriteString("2. Execute the workflow with tool-verified checkpoints.\n")
		b.WriteString("3. Verify the final state before reporting completion.\n")
	} else {
		for i, step := range candidate.DraftSteps {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
	}
	b.WriteString("\n## Checkpoint\n\n")
	b.WriteString("After reading this SOP, call `update_working_checkpoint` with the key constraints and `related_sop: ")
	b.WriteString(relPath)
	b.WriteString("` if it applies.\n")
	return b.String()
}

func (m Manager) updateSOPIndex(candidate SOPCandidate, sopPath string, confirmation string) (string, bool, error) {
	indexPath, err := m.resolveSOPPath(SOPIndexPath)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), defaultMemoryDirectoryPerm); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		data = []byte("# SOP Index\n\n## Rules\n\n- SOP 全文按需读取，不要凭印象执行。\n")
	}
	text := string(data)
	if strings.Contains(text, "`"+sopPath+"`") || strings.Contains(text, sopPath) {
		return indexPath, false, nil
	}
	entry := formatSOPIndexEntry(candidate, sopPath, confirmation)
	insertAt := strings.Index(text, "\n## Rules")
	if insertAt >= 0 {
		text = strings.TrimRight(text[:insertAt], "\n") + "\n\n" + entry + "\n" + text[insertAt:]
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n" + entry + "\n"
	}
	return indexPath, true, os.WriteFile(indexPath, []byte(text), defaultMemoryFilePerm)
}

func formatSOPIndexEntry(candidate SOPCandidate, sopPath string, confirmation string) string {
	var b strings.Builder
	b.WriteString("## ")
	b.WriteString(candidate.Title)
	b.WriteString("\n\n")
	b.WriteString("- SOP: `")
	b.WriteString(sopPath)
	b.WriteString("`\n")
	writeMemoryField(&b, "场景", candidate.Scene)
	if len(candidate.TriggerKeywords) > 0 {
		writeMemoryField(&b, "关键词", strings.Join(candidate.TriggerKeywords, ", "))
	}
	writeMemoryField(&b, "来源", "SOP candidate `"+candidate.ID+"`")
	writeMemoryField(&b, "确认", confirmation+"; "+time.Now().Format(time.RFC3339))
	return strings.TrimSpace(b.String())
}
