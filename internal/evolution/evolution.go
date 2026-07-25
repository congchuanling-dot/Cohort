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
	// MemoryDirName 是工作区内长期记忆文件的根目录名。
	MemoryDirName = "memory"
	// MemoryIndexPath 是仅包含记忆入口指针的轻量索引文件。
	MemoryIndexPath = "memory/index.md"
	// GlobalMemoryPath 存放跨项目复用的稳定经验。
	GlobalMemoryPath = "memory/global.md"
	// SOPCandidateMemoryPath 存放等待人工审阅与提升的工作流候选。
	SOPCandidateMemoryPath = "memory/reflection/sop_candidates.md"
	// MemoryAuditPath 以 JSONL 形式追加每次实际写入的审计记录。
	MemoryAuditPath = "memory/audit.jsonl"
	// SOPIndexPath 是经审阅 SOP 的项目内导航索引。
	SOPIndexPath = "sops/index.md"
	// defaultMemoryFilePerm 是普通 Markdown 和记忆审计文件权限。
	defaultMemoryFilePerm = 0644
	// defaultMemoryDirectoryPerm 是记忆目录权限。
	defaultMemoryDirectoryPerm = 0755
)

// Manager 负责受控长期记忆的初始化、候选校验和追加写入。
type Manager struct {
	// Workspace 是所有 memory/ 相对路径的绝对解析根目录。
	Workspace string
	// ProjectID 是从 Git 根目录或工作区推导出的稳定项目标识。
	ProjectID string
}

// Candidate 是一条待写入的长期记忆提案。
// 模型只提出内容；本包验证目标、证据、敏感信息和写入方式后才允许安全追加。
type Candidate struct {
	// Type 是经验分类，例如 project_lesson、user_preference 或 sop_candidate。
	Type string `json:"type"`
	// Target 是受 allowlist 限制的记忆相对路径。
	Target string `json:"target"`
	// Content 是兼容旧格式的额外自由文本，不应取代结构化字段。
	Content string `json:"content"`
	// Scene 描述未来可复用该经验的简短场景。
	Scene string `json:"scene,omitempty"`
	// TriggerKeywords 是用于后续检索该经验的关键词。
	TriggerKeywords []string `json:"trigger_keywords,omitempty"`
	// Lesson 是经过验证、可长期保留的结论。
	Lesson string `json:"lesson,omitempty"`
	// RecommendedSteps 是可复现的操作步骤。
	RecommendedSteps []string `json:"recommended_steps,omitempty"`
	// PromoteToSOP 表示此经验值得生成为待审阅 SOP 候选。
	PromoteToSOP bool `json:"promote_to_sop,omitempty"`
	// SOPTitle 是候选 SOP 的展示标题。
	SOPTitle string `json:"sop_title,omitempty"`
	// SOPPath 是候选 SOP 建议落在 sops/ 下的相对路径。
	SOPPath string `json:"sop_path,omitempty"`
	// EvidenceIDs 必须引用本次任务中已验证的证据账本条目。
	EvidenceIDs []string `json:"evidence_ids"`
	// Risk 是提案风险分级；P0 只允许低风险追加。
	Risk string `json:"risk"`
	// Action 是请求的写入方式；P0 仅允许 append。
	Action string `json:"action"`
	// RequiresUserConfirmation 表示即使内容通过校验也必须先询问用户。
	RequiresUserConfirmation bool `json:"requires_user_confirmation,omitempty"`
}

// Evidence 记录可以支撑长期记忆候选的单个来源。
// Summary 只能包含元数据，刻意不在此保留原始工具输出。
type Evidence struct {
	// ID 是候选提案引用的稳定账本标识。
	ID string `json:"id"`
	// Source 表示证据来自用户输入还是工具执行。
	Source string `json:"source"`
	// ToolName 是工具来源时的工具名。
	ToolName string `json:"tool_name,omitempty"`
	// Turn 是证据产生于第几轮 Agent 循环。
	Turn int `json:"turn,omitempty"`
	// CallID 是模型工具调用 ID，用于审计关联。
	CallID string `json:"call_id,omitempty"`
	// Verified 表示系统已按工具特性验证结果成功。
	Verified bool `json:"verified"`
	// Summary 仅存元数据摘要，绝不存原始工具输出。
	Summary string `json:"summary"`
}

// ValidationResult 描述记忆工具是否可安全应用某个候选。
type ValidationResult struct {
	// Valid 表示候选通过所有当前 P0 安全规则。
	Valid bool `json:"valid"`
	// Reasons 包含拒绝或警告原因，供模型修改候选或选择 skip。
	Reasons []string `json:"reasons,omitempty"`
}

// ProposedCandidate 是 memory_propose_update 返回的候选及其验证结果。
type ProposedCandidate struct {
	// Candidate 是原始结构化提案。
	Candidate Candidate `json:"candidate"`
	// Validation 是不写磁盘的校验结果。
	Validation ValidationResult `json:"validation"`
}

// ApplyResult 是候选追加并从磁盘回读确认后的结果。
type ApplyResult struct {
	// AuditRecord 是已写入 audit.jsonl 的审计条目。
	AuditRecord AuditRecord `json:"audit_record"`
	// MemoryRoot 是本次操作的记忆根目录。
	MemoryRoot string `json:"memory_root"`
	// TargetPath 是实际追加的绝对目标路径。
	TargetPath string `json:"target_path"`
	// SOPCandidatePath 是同步生成候选时的反思文件路径。
	SOPCandidatePath string `json:"sop_candidate_path,omitempty"`
	// ReadBackConfirmed 表示写入后已重新读取文件验证内容。
	ReadBackConfirmed bool `json:"read_back_confirmed"`
	// ReadBackBytes 是回读确认的字节数。
	ReadBackBytes int `json:"read_back_bytes"`
}

// SOPCandidate 是存放在 memory/reflection 下、等待人工审阅的工作流候选。
type SOPCandidate struct {
	// ID 是候选内容派生出的稳定标识。
	ID string `json:"id"`
	// Title 是工作流的简短人类可读名称。
	Title string `json:"title"`
	// Scene 描述该 SOP 适用的场景。
	Scene string `json:"scene,omitempty"`
	// TriggerKeywords 帮助后续任务路由到该候选。
	TriggerKeywords []string `json:"trigger_keywords,omitempty"`
	// ProposedSOPPath 是建议的 sops/ 目标相对路径。
	ProposedSOPPath string `json:"proposed_sop_path,omitempty"`
	// SourceSession 是产生候选的会话标识。
	SourceSession string `json:"source_session,omitempty"`
	// EvidenceIDs 是支撑候选的已验证证据。
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Why 说明该经验为何应提升为可复用规程。
	Why string `json:"why,omitempty"`
	// DraftSteps 是待审阅的操作步骤草案。
	DraftSteps []string `json:"draft_steps,omitempty"`
}

// SOPPromotionResult 描述受控 SOP 提升操作产生的文件与索引副作用。
type SOPPromotionResult struct {
	// Candidate 是被提升的已审阅候选。
	Candidate SOPCandidate `json:"candidate"`
	// SOPRoot 是经解析的项目 SOP 根目录。
	SOPRoot string `json:"sop_root"`
	// SOPPath 是相对于项目根目录的 SOP 路径。
	SOPPath string `json:"sop_path"`
	// SOPAbsolutePath 是实际创建或检查的绝对文件路径。
	SOPAbsolutePath string `json:"sop_absolute_path"`
	// SOPCreated 表示提升过程是否新建了 SOP 文件。
	SOPCreated bool `json:"sop_created"`
	// IndexPath 是更新索引时对应的绝对路径。
	IndexPath string `json:"index_path,omitempty"`
	// IndexUpdated 表示 sops/index.md 是否已写入导航项。
	IndexUpdated bool `json:"index_updated"`
	// RequiresIndexConfirmation 表示索引更新还需要用户明确确认。
	RequiresIndexConfirmation bool `json:"requires_index_confirmation"`
	// Confirmation 是记录在索引审计文本中的用户确认说明。
	Confirmation string `json:"confirmation,omitempty"`
}

// AuditRecord 在每次实际应用更新后追加到 memory/audit.jsonl。
type AuditRecord struct {
	// Time 是本地写入发生的 RFC3339 时间。
	Time string `json:"time"`
	// Target 是被追加的记忆目标相对路径。
	Target string `json:"target"`
	// Action 是实际执行的写入方式。
	Action string `json:"action"`
	// SourceSession 是产生该候选的会话标识。
	SourceSession string `json:"source_session,omitempty"`
	// EvidenceIDs 记录支撑写入的验证证据。
	EvidenceIDs []string `json:"evidence_ids"`
	// Summary 是不含原始数据的审计摘要。
	Summary string `json:"summary"`
	// Confirmation 记录需要用户确认时的确认文本。
	Confirmation string `json:"confirmation,omitempty"`
}

// NewManager 以 workspace 为根创建管理器；所有记忆相对路径都在此解析。
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

// MemoryRoot 返回绝对记忆目录路径。
func (m Manager) MemoryRoot() string {
	return filepath.Join(m.Workspace, MemoryDirName)
}

// SOPRoot 返回经审阅 SOP 文件所在项目根目录，优先使用最近 Git 根。
func (m Manager) SOPRoot() string {
	if root := findGitRoot(m.Workspace); root != "" {
		return root
	}
	return m.Workspace
}

// ProjectMemoryPath 返回相对 memory/ 目录的项目专属长期记忆路径。
func (m Manager) ProjectMemoryPath() string {
	return projectMemoryPath(m.ProjectID)
}

// EnsureStructure 仅在缺失时创建 P0 所需目录和空白模板，不覆盖已有记忆。
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

// ValidateCandidate 在候选实际写入前执行 P0 安全策略校验。
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

// ApplyCandidate 追加一条通过校验的候选，并写入对应审计记录。
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

// ListSOPCandidates 读取可提升为正式 SOP 的结构化候选。
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

// PromoteSOPCandidate 写入经过审阅的 SOP 文件；只有显式确认后才更新 sops/index.md，
// 这样未来任务才会被路由到新 SOP。
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
