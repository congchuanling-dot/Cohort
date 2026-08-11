package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FindingSeverity string

const (
	SeverityLow      FindingSeverity = "low"
	SeverityMedium   FindingSeverity = "medium"
	SeverityHigh     FindingSeverity = "high"
	SeverityCritical FindingSeverity = "critical"
)

type FindingStatus string

const (
	FindingOpen      FindingStatus = "open"
	FindingResolved  FindingStatus = "resolved"
	FindingDismissed FindingStatus = "dismissed"
)

type VerifierVerdict string

const (
	VerdictPass       VerifierVerdict = "pass"
	VerdictFail       VerifierVerdict = "fail"
	VerdictNeedsHuman VerifierVerdict = "needs_human"
)

type Finding struct {
	ID          string          `json:"id"`
	Fingerprint string          `json:"fingerprint"`
	Verifier    AgentRole       `json:"verifier"`
	CriterionID string          `json:"criterion_id,omitempty"`
	Severity    FindingSeverity `json:"severity"`
	Confidence  float64         `json:"confidence"`
	File        string          `json:"file,omitempty"`
	Line        int             `json:"line,omitempty"`
	Claim       string          `json:"claim"`
	Evidence    []string        `json:"evidence"`
	FixHint     string          `json:"fix_hint,omitempty"`
	Status      FindingStatus   `json:"status"`
}

type VerifierReport struct {
	SchemaVersion int             `json:"schema_version"`
	DeliveryID    string          `json:"delivery_id"`
	Role          AgentRole       `json:"role"`
	Verdict       VerifierVerdict `json:"verdict"`
	Score         float64         `json:"score"`
	Summary       string          `json:"summary"`
	ContractHash  string          `json:"contract_hash"`
	TreeHash      string          `json:"tree_hash"`
	Findings      []Finding       `json:"findings"`
	ArtifactID    string          `json:"artifact_id,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
}

type VerificationStatus string

const (
	VerificationRunning    VerificationStatus = "running"
	VerificationPassed     VerificationStatus = "passed"
	VerificationFailed     VerificationStatus = "failed"
	VerificationNeedsHuman VerificationStatus = "needs_human"
)

type VerificationState struct {
	SchemaVersion int                `json:"schema_version"`
	DeliveryID    string             `json:"delivery_id"`
	Round         int                `json:"round"`
	Status        VerificationStatus `json:"status"`
	TreeHash      string             `json:"tree_hash"`
	Reports       []VerifierReport   `json:"reports"`
	Findings      []Finding          `json:"findings"`
	StartedAt     time.Time          `json:"started_at"`
	FinishedAt    time.Time          `json:"finished_at,omitempty"`
	Error         string             `json:"error,omitempty"`
}

type SemanticVerifier func(context.Context, Delivery, AcceptanceContract, IntegrationState, AgentRole) (VerifierReport, error)

type VerifierCouncil struct {
	Store       Store
	MaxParallel int
}

func (c VerifierCouncil) Run(ctx context.Context, deliveryID string, verify SemanticVerifier) (VerificationState, error) {
	if verify == nil {
		return VerificationState{}, errors.New("semantic verifier is required")
	}
	item, err := c.Store.Load(deliveryID)
	if err != nil {
		return VerificationState{}, err
	}
	if item.Status != StatusVerifying {
		return VerificationState{}, fmt.Errorf("delivery %q cannot verify from status %s", deliveryID, item.Status)
	}
	contract, _, err := c.Store.LoadPlan(deliveryID)
	if err != nil {
		return VerificationState{}, err
	}
	integration, err := c.Store.LoadIntegration(deliveryID)
	if err != nil {
		return VerificationState{}, err
	}
	if integration.Status != IntegrationPassed {
		return VerificationState{}, errors.New("integration has not passed deterministic gates")
	}
	previous, _ := c.Store.LoadVerification(deliveryID)
	round := previous.Round + 1
	if round < 1 {
		round = 1
	}
	state := VerificationState{
		SchemaVersion: SchemaVersion,
		DeliveryID:    deliveryID,
		Round:         round,
		Status:        VerificationRunning,
		TreeHash:      integration.TreeHash,
		StartedAt:     time.Now().UTC(),
	}
	if err := c.Store.SaveVerification(deliveryID, state); err != nil {
		return state, err
	}
	roles := verifierRoles(contract.RiskProfile)
	parallel := c.MaxParallel
	if parallel <= 0 || parallel > len(roles) {
		parallel = len(roles)
	}
	semaphore := make(chan struct{}, parallel)
	type reportResult struct {
		role   AgentRole
		report VerifierReport
		err    error
	}
	results := make(chan reportResult, len(roles))
	var workers sync.WaitGroup
	for _, role := range roles {
		role := role
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- reportResult{role: role, err: ctx.Err()}
				return
			}
			report, err := verify(ctx, item, contract, integration, role)
			results <- reportResult{role: role, report: report, err: err}
		}()
	}
	workers.Wait()
	close(results)
	var runErrors []string
	for result := range results {
		if result.err != nil {
			runErrors = append(runErrors, fmt.Sprintf("%s: %v", result.role, result.err))
			continue
		}
		result.report.Role = result.role
		report := normalizeVerifierReport(result.report, item, integration)
		if err := ValidateVerifierReport(report, contract); err != nil {
			runErrors = append(runErrors, fmt.Sprintf("%s: %v", report.Role, err))
			continue
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			runErrors = append(runErrors, err.Error())
			continue
		}
		artifact, err := c.Store.PublishArtifact(deliveryID, ArtifactMeta{
			Kind:       "verifier_report",
			Producer:   string(report.Role),
			BaseCommit: item.BaseCommit,
			TreeHash:   integration.TreeHash,
			MediaType:  "application/json",
		}, data)
		if err != nil {
			runErrors = append(runErrors, err.Error())
			continue
		}
		report.ArtifactID = artifact.ID
		state.Reports = append(state.Reports, report)
	}
	if len(runErrors) > 0 {
		state.Status = VerificationFailed
		state.Error = strings.Join(runErrors, "; ")
		state.FinishedAt = time.Now().UTC()
		_ = c.Store.SaveVerification(deliveryID, state)
		_, _ = c.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryVerificationFailed", map[string]any{"error": state.Error})
		return state, errors.New(state.Error)
	}
	sort.Slice(state.Reports, func(left, right int) bool {
		return state.Reports[left].Role < state.Reports[right].Role
	})
	state.Findings = deduplicateFindings(state.Reports)
	state.FinishedAt = time.Now().UTC()
	switch {
	case reportsNeedHuman(state.Reports):
		state.Status = VerificationNeedsHuman
		if err := c.Store.SaveVerification(deliveryID, state); err != nil {
			return state, err
		}
		_, err = c.Store.Transition(deliveryID, StatusNeedsHumanDecision, "DeliveryVerificationNeedsHuman", map[string]any{"findings": len(state.Findings)})
		return state, err
	case blockingFindings(state.Findings):
		state.Status = VerificationFailed
		if err := c.Store.SaveVerification(deliveryID, state); err != nil {
			return state, err
		}
		_, err = c.Store.Transition(deliveryID, StatusNeedsRevision, "DeliveryRevisionRequested", map[string]any{"findings": len(state.Findings), "round": round})
		return state, err
	default:
		state.Status = VerificationPassed
		if err := c.Store.SaveVerification(deliveryID, state); err != nil {
			return state, err
		}
		_, err = c.Store.Transition(deliveryID, StatusReadyForReview, "DeliveryVerificationFinished", map[string]any{"reports": len(state.Reports), "findings": len(state.Findings)})
		return state, err
	}
}

func (s Store) SaveVerification(deliveryID string, state VerificationState) error {
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return errors.New("verification state identity or schema mismatch")
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return err
	}
	defer release()
	return s.writeJSON(filepath.Join(s.deliveryDir(deliveryID), "verification.json"), state)
}

func (s Store) LoadVerification(deliveryID string) (VerificationState, error) {
	var state VerificationState
	if err := readJSON(filepath.Join(s.deliveryDir(deliveryID), "verification.json"), &state); err != nil {
		return VerificationState{}, err
	}
	if state.DeliveryID != deliveryID || state.SchemaVersion != SchemaVersion {
		return VerificationState{}, errors.New("verification state identity or schema mismatch")
	}
	return state, nil
}

func (s Store) SaveVerifierWorkerReport(deliveryID string, treeHash string, role AgentRole, report VerifierReport) error {
	if err := validateStableID(treeHash); err != nil {
		return err
	}
	if !validVerifierRole(role) {
		return fmt.Errorf("invalid verifier role %q", role)
	}
	path := filepath.Join(s.deliveryDir(deliveryID), "verifier-workers", treeHash, string(role)+".json")
	return s.writeJSON(path, report)
}

func (s Store) LoadVerifierWorkerReport(deliveryID string, treeHash string, role AgentRole) (VerifierReport, error) {
	if err := validateStableID(treeHash); err != nil {
		return VerifierReport{}, err
	}
	if !validVerifierRole(role) {
		return VerifierReport{}, fmt.Errorf("invalid verifier role %q", role)
	}
	var report VerifierReport
	path := filepath.Join(s.deliveryDir(deliveryID), "verifier-workers", treeHash, string(role)+".json")
	if err := readJSON(path, &report); err != nil {
		return VerifierReport{}, err
	}
	return report, nil
}

func ParseVerifierReport(response string) (VerifierReport, error) {
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start < 0 || end < start {
		return VerifierReport{}, errors.New("verifier response does not contain a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(response[start : end+1]))
	decoder.DisallowUnknownFields()
	var report VerifierReport
	if err := decoder.Decode(&report); err != nil {
		return VerifierReport{}, fmt.Errorf("parse verifier JSON: %w", err)
	}
	return report, nil
}

func ValidateVerifierReport(report VerifierReport, contract AcceptanceContract) error {
	if !validVerifierRole(report.Role) {
		return fmt.Errorf("invalid verifier role %q", report.Role)
	}
	if report.Verdict != VerdictPass && report.Verdict != VerdictFail && report.Verdict != VerdictNeedsHuman {
		return fmt.Errorf("invalid verifier verdict %q", report.Verdict)
	}
	if report.Score < 0 || report.Score > 100 {
		return errors.New("verifier score must be between 0 and 100")
	}
	if report.Verdict == VerdictPass && report.Score < 70 {
		return errors.New("pass verdict requires score >= 70")
	}
	if report.Verdict == VerdictFail && report.Score >= 70 {
		return errors.New("fail verdict requires score < 70")
	}
	if strings.TrimSpace(report.Summary) == "" {
		return errors.New("verifier summary is required")
	}
	criterionIDs := map[string]bool{}
	for _, criterion := range contract.Criteria {
		criterionIDs[criterion.ID] = true
	}
	for index := range report.Findings {
		finding := report.Findings[index]
		if finding.CriterionID != "" && !criterionIDs[finding.CriterionID] {
			return fmt.Errorf("finding references unknown criterion %q", finding.CriterionID)
		}
		if !validFindingSeverity(finding.Severity) {
			return fmt.Errorf("finding has invalid severity %q", finding.Severity)
		}
		if finding.Confidence < 0 || finding.Confidence > 1 {
			return errors.New("finding confidence must be between 0 and 1")
		}
		if strings.TrimSpace(finding.Claim) == "" || len(finding.Evidence) == 0 {
			return errors.New("finding requires claim and evidence")
		}
		if finding.File != "" {
			if err := validateRepoPattern(finding.File); err != nil {
				return fmt.Errorf("finding file %q: %w", finding.File, err)
			}
		}
	}
	if report.Verdict == VerdictFail && len(report.Findings) == 0 {
		return errors.New("failed verifier report requires findings")
	}
	if report.Verdict == VerdictFail && !blockingFindings(report.Findings) {
		return errors.New("fail verdict requires at least one open high or critical finding")
	}
	if report.Verdict == VerdictPass && blockingFindings(report.Findings) {
		return errors.New("pass verdict cannot include open high or critical findings")
	}
	return nil
}

func VerifierSystemPrompt(role AgentRole, language string) string {
	if strings.EqualFold(strings.TrimSpace(language), "en") {
		return verifierSystemPromptEN(role)
	}
	return verifierSystemPromptZH(role)
}

func VerifierTaskPrompt(item Delivery, contract AcceptanceContract, integration IntegrationState, diff string, role AgentRole) (string, error) {
	payload := struct {
		Requirement string             `json:"requirement"`
		Contract    AcceptanceContract `json:"contract"`
		TreeHash    string             `json:"tree_hash"`
		Role        AgentRole          `json:"role"`
		Diff        string             `json:"diff"`
		GateResults []GateResult       `json:"gate_results"`
	}{
		Requirement: item.Requirement,
		Contract:    contract,
		TreeHash:    integration.TreeHash,
		Role:        role,
		Diff:        diff,
		GateResults: integration.GateResults,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return "Independently verify this integrated candidate. Inspect repository files before deciding.\n\n" + string(data), nil
}

func verifierRoles(risk RiskProfile) []AgentRole {
	roles := []AgentRole{RoleSpecVerifier, RoleCorrectnessVerifier}
	if risk.SecuritySensitive || risk.Level == RiskCritical {
		roles = append(roles, RoleSecurityVerifier)
	}
	if risk.CompatibilitySensitive {
		roles = append(roles, RoleCompatibilityVerifier)
	}
	if risk.PerformanceSensitive {
		roles = append(roles, RolePerformanceVerifier)
	}
	return roles
}

func normalizeVerifierReport(report VerifierReport, item Delivery, integration IntegrationState) VerifierReport {
	now := time.Now().UTC()
	report.SchemaVersion = SchemaVersion
	report.DeliveryID = item.ID
	report.ContractHash = item.ContractHash
	report.TreeHash = integration.TreeHash
	if report.StartedAt.IsZero() {
		report.StartedAt = now
	}
	if report.FinishedAt.IsZero() {
		report.FinishedAt = now
	}
	for index := range report.Findings {
		finding := &report.Findings[index]
		finding.Verifier = report.Role
		finding.Status = FindingOpen
		if finding.Fingerprint == "" {
			finding.Fingerprint = findingFingerprint(*finding)
		}
		if finding.ID == "" {
			finding.ID = "finding_" + strings.TrimPrefix(finding.Fingerprint, "sha256:")[:12]
		}
	}
	return report
}

func deduplicateFindings(reports []VerifierReport) []Finding {
	byFingerprint := map[string]Finding{}
	for _, report := range reports {
		for _, finding := range report.Findings {
			existing, exists := byFingerprint[finding.Fingerprint]
			if !exists || severityRank(finding.Severity) > severityRank(existing.Severity) || finding.Confidence > existing.Confidence {
				byFingerprint[finding.Fingerprint] = finding
			}
		}
	}
	findings := make([]Finding, 0, len(byFingerprint))
	for _, finding := range byFingerprint {
		findings = append(findings, finding)
	}
	sort.Slice(findings, func(left, right int) bool {
		if severityRank(findings[left].Severity) == severityRank(findings[right].Severity) {
			return findings[left].ID < findings[right].ID
		}
		return severityRank(findings[left].Severity) > severityRank(findings[right].Severity)
	})
	return findings
}

func findingFingerprint(finding Finding) string {
	value := strings.ToLower(strings.Join([]string{
		finding.CriterionID,
		filepath.ToSlash(finding.File),
		fmt.Sprint(finding.Line),
		strings.Join(strings.Fields(finding.Claim), " "),
	}, "\x00"))
	return HashString(value)
}

func blockingFindings(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Status == FindingOpen && (finding.Severity == SeverityHigh || finding.Severity == SeverityCritical) {
			return true
		}
	}
	return false
}

func reportsNeedHuman(reports []VerifierReport) bool {
	for _, report := range reports {
		if report.Verdict == VerdictNeedsHuman {
			return true
		}
	}
	return false
}

func validVerifierRole(role AgentRole) bool {
	switch role {
	case RoleSpecVerifier, RoleCorrectnessVerifier, RoleSecurityVerifier, RolePerformanceVerifier, RoleCompatibilityVerifier:
		return true
	default:
		return false
	}
}

func validFindingSeverity(severity FindingSeverity) bool {
	switch severity {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

func severityRank(severity FindingSeverity) int {
	switch severity {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

func verifierSystemPromptZH(role AgentRole) string {
	return fmt.Sprintf(`[DELIVERY VERIFIER: %s]
你是独立只读 Verifier，不继承 Builder 的推理过程，也不为实现辩护。

规则：
1. 必须主动读取集成 worktree 中的相关代码，不能只复述输入 diff。
2. 只报告会影响验收标准、正确性、安全、兼容性或性能的具体缺陷；纯风格偏好不能阻塞。
3. Finding 必须包含 criterion_id（跨领域风险可为空）、severity、0-1 confidence、文件/行号、claim、证据和可执行 fix_hint。
4. 没有证据不得报告 Finding。不得假设测试通过就等于规格满足。
5. verdict=fail 时必须至少有一个 Finding；存在需要产品决策且无法从仓库推断的问题时使用 needs_human。
6. 最终只能输出一个 JSON 对象，不得输出 Markdown。

JSON：
{"schema_version":1,"delivery_id":"","role":"%s","verdict":"pass|fail|needs_human","score":95,"summary":"结论","contract_hash":"","tree_hash":"","findings":[{"id":"","fingerprint":"","verifier":"%s","criterion_id":"AC-1","severity":"low|medium|high|critical","confidence":0.9,"file":"relative/path.go","line":1,"claim":"缺陷","evidence":["具体代码或行为证据"],"fix_hint":"最小修复","status":"open"}],"started_at":"0001-01-01T00:00:00Z","finished_at":"0001-01-01T00:00:00Z"}`,
		role, role, role)
}

func verifierSystemPromptEN(role AgentRole) string {
	return fmt.Sprintf(`[DELIVERY VERIFIER: %s]
Act as an independent read-only verifier. Inspect the integrated worktree and report only concrete defects affecting acceptance criteria, correctness, security, compatibility, or performance. Every finding requires evidence, severity, confidence, repository-relative location, and an actionable fix. Style preferences do not block delivery. A fail verdict requires findings. Return exactly one JSON object matching the requested VerifierReport fields.`, role)
}
