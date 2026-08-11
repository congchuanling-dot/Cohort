package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CriterionCoverage struct {
	CriterionID   string         `json:"criterion_id"`
	Statement     string         `json:"statement"`
	Mandatory     bool           `json:"mandatory"`
	Policy        EvidencePolicy `json:"policy"`
	Status        string         `json:"status"`
	EvidenceIDs   []string       `json:"evidence_ids,omitempty"`
	VerifierRoles []AgentRole    `json:"verifier_roles,omitempty"`
}

type ReviewMetrics struct {
	Nodes            int   `json:"nodes"`
	Candidates       int   `json:"candidates"`
	Selected         int   `json:"selected"`
	RevisionRounds   int   `json:"revision_rounds"`
	Gates            int   `json:"gates"`
	VerifierReports  int   `json:"verifier_reports"`
	OpenFindings     int   `json:"open_findings"`
	BlockingFindings int   `json:"blocking_findings"`
	FilesChanged     int   `json:"files_changed"`
	Tokens           int64 `json:"tokens"`
	DurationMS       int64 `json:"duration_ms"`
}

type ReviewReport struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Delivery      Delivery            `json:"delivery"`
	Contract      AcceptanceContract  `json:"contract"`
	Graph         TaskGraph           `json:"graph"`
	Runtime       RuntimeState        `json:"runtime"`
	Integration   IntegrationState    `json:"integration"`
	Verification  VerificationState   `json:"verification"`
	Revisions     RevisionState       `json:"revisions"`
	Approval      *ApprovalRecord     `json:"approval,omitempty"`
	Merge         *MergeState         `json:"merge,omitempty"`
	Coverage      []CriterionCoverage `json:"coverage"`
	ChangedFiles  []string            `json:"changed_files"`
	Metrics       ReviewMetrics       `json:"metrics"`
	ProofStatus   string              `json:"proof_status"`
	ProofError    string              `json:"proof_error,omitempty"`
}

func (s Store) BuildReviewReport(deliveryID string) (ReviewReport, error) {
	item, err := s.Load(deliveryID)
	if err != nil {
		return ReviewReport{}, err
	}
	contract, graph, err := s.LoadPlan(deliveryID)
	if err != nil {
		return ReviewReport{}, err
	}
	runtime, err := s.LoadRuntime(deliveryID)
	if err != nil {
		return ReviewReport{}, err
	}
	integration, err := s.LoadIntegration(deliveryID)
	if err != nil {
		return ReviewReport{}, err
	}
	verification, err := s.LoadVerification(deliveryID)
	if err != nil {
		return ReviewReport{}, err
	}
	revisions, err := s.LoadRevisions(deliveryID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ReviewReport{}, err
	}
	report := ReviewReport{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Delivery:      item,
		Contract:      contract,
		Graph:         graph,
		Runtime:       runtime,
		Integration:   integration,
		Verification:  verification,
		Revisions:     revisions,
		ProofStatus:   "fresh",
	}
	if approval, approvalErr := s.LoadApproval(deliveryID); approvalErr == nil {
		report.Approval = &approval
	}
	if merge, mergeErr := s.LoadMerge(deliveryID); mergeErr == nil {
		report.Merge = &merge
	}
	if err := validateIntegrationEvidence(s, item, contract, integration); err != nil {
		report.ProofStatus = "stale"
		report.ProofError = err.Error()
	}
	if report.Merge != nil && report.Merge.Status == StatusVerified {
		if err := validateReviewGateResults(s, item, contract, report.Merge.TreeHash, item.ProjectRoot, report.Merge.PostMergeGates); err != nil {
			report.ProofStatus = "stale"
			if report.ProofError == "" {
				report.ProofError = err.Error()
			} else {
				report.ProofError = errors.Join(errors.New(report.ProofError), err).Error()
			}
		}
	}
	report.Coverage = buildCriterionCoverage(contract, integration, verification, report.Approval, report.Merge)
	report.ChangedFiles, report.Metrics = buildReviewMetrics(runtime, integration, verification, revisions)
	return report, nil
}

func buildCriterionCoverage(contract AcceptanceContract, integration IntegrationState, verification VerificationState, approval *ApprovalRecord, merge *MergeState) []CriterionCoverage {
	evidenceByCriterion := map[string][]string{}
	gateResults := append([]GateResult(nil), integration.GateResults...)
	if merge != nil {
		gateResults = append(gateResults, merge.PostMergeGates...)
	}
	for _, result := range gateResults {
		if result.Evidence.Status != EvidencePassed {
			continue
		}
		for _, criterionID := range result.Evidence.CriterionIDs {
			evidenceByCriterion[criterionID] = append(evidenceByCriterion[criterionID], result.Evidence.ID)
		}
	}
	roles := make([]AgentRole, 0, len(verification.Reports))
	for _, report := range verification.Reports {
		if report.Verdict == VerdictPass {
			roles = append(roles, report.Role)
		}
	}
	sort.Slice(roles, func(left, right int) bool { return roles[left] < roles[right] })
	coverage := make([]CriterionCoverage, 0, len(contract.Criteria))
	for _, criterion := range contract.Criteria {
		item := CriterionCoverage{
			CriterionID: criterion.ID, Statement: criterion.Statement,
			Mandatory: criterion.Mandatory, Policy: criterion.EvidencePolicy,
			EvidenceIDs: uniqueStrings(evidenceByCriterion[criterion.ID]),
		}
		switch criterion.EvidencePolicy {
		case EvidenceExecution:
			if len(item.EvidenceIDs) > 0 {
				item.Status = "passed"
			} else {
				item.Status = "missing"
			}
		case EvidenceSemantic:
			item.VerifierRoles = append([]AgentRole(nil), roles...)
			if verification.Status == VerificationPassed && len(roles) > 0 {
				item.Status = "passed"
			} else {
				item.Status = "missing"
			}
		case EvidenceHuman:
			if approval != nil {
				item.Status = "approved"
			} else {
				item.Status = "pending_human"
			}
		default:
			item.Status = "missing"
		}
		coverage = append(coverage, item)
	}
	return coverage
}

func validateReviewGateResults(store Store, item Delivery, contract AcceptanceContract, treeHash string, root string, results []GateResult) error {
	environmentHash, err := EnvironmentHash(root)
	if err != nil {
		return err
	}
	gates := make(map[string]GateSpec, len(contract.RequiredGates))
	for _, gate := range contract.RequiredGates {
		gates[gate.ID] = gate
	}
	latest := make(map[string]GateResult, len(results))
	for _, result := range results {
		latest[result.Gate.ID] = result
	}
	evidence := make([]EvidenceEnvelope, 0, len(latest))
	for _, gateSpec := range contract.RequiredGates {
		result, exists := latest[gateSpec.ID]
		if !exists {
			continue
		}
		envelope := result.Evidence
		gate, exists := gates[envelope.GateID]
		if !exists {
			return fmt.Errorf("post-merge evidence references unknown gate %q", envelope.GateID)
		}
		if err := VerifyEvidenceFreshness(envelope, item, treeHash, gate, environmentHash); err != nil {
			return err
		}
		if _, _, err := store.ReadArtifact(item.ID, envelope.ArtifactHash); err != nil {
			return err
		}
		evidence = append(evidence, envelope)
	}
	return ValidateMandatoryEvidence(contract, evidence)
}

func buildReviewMetrics(runtime RuntimeState, integration IntegrationState, verification VerificationState, revisions RevisionState) ([]string, ReviewMetrics) {
	files := map[string]bool{}
	metrics := ReviewMetrics{
		Nodes: len(runtime.Nodes), RevisionRounds: len(revisions.Records),
		Gates: len(integration.GateResults), VerifierReports: len(verification.Reports),
	}
	for _, node := range runtime.Nodes {
		metrics.Candidates += len(node.Candidates)
		if node.SelectedID != "" {
			metrics.Selected++
		}
		for _, candidate := range node.Candidates {
			if candidate.ID != node.SelectedID {
				continue
			}
			metrics.Tokens += candidate.Tokens
			metrics.DurationMS += candidate.DurationMS
			for _, path := range candidate.ActualWrites {
				files[path] = true
			}
		}
	}
	for _, revision := range revisions.Records {
		metrics.Tokens += revision.Candidate.Tokens
		metrics.DurationMS += revision.Candidate.DurationMS
		for _, path := range revision.Candidate.ActualWrites {
			files[path] = true
		}
	}
	for _, finding := range verification.Findings {
		if finding.Status != FindingOpen {
			continue
		}
		metrics.OpenFindings++
		if finding.Severity == SeverityHigh || finding.Severity == SeverityCritical {
			metrics.BlockingFindings++
		}
	}
	changed := make([]string, 0, len(files))
	for path := range files {
		changed = append(changed, path)
	}
	sort.Strings(changed)
	metrics.FilesChanged = len(changed)
	return changed, metrics
}

func WriteReviewHTML(report ReviewReport, outputPath string) error {
	if report.Delivery.ID == "" {
		return errors.New("review report delivery is required")
	}
	outputPath = filepath.Clean(strings.TrimSpace(outputPath))
	if outputPath == "." || outputPath == "" {
		return errors.New("review report output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	view := struct {
		ReviewReport
		Data template.JS
	}{
		ReviewReport: report,
		Data:         template.JS(data),
	}
	temp, err := os.CreateTemp(filepath.Dir(outputPath), ".delivery-review-*.html")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := reviewHTMLTemplate.Execute(temp, view); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, outputPath)
}

var reviewHTMLTemplate = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Delivery Review · {{.Delivery.ID}}</title>
<style>
:root{color-scheme:dark;--bg:#0b1020;--panel:#131a2e;--line:#2a3555;--text:#e8ecf7;--muted:#98a4bf;--green:#3ddc97;--amber:#f5b942;--red:#ff6b78;--blue:#71a7ff}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}.wrap{max-width:1280px;margin:auto;padding:28px}
h1{font:700 28px/1.2 system-ui;margin:0 0 8px}.muted{color:var(--muted)}.grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:22px 0}.card,.section{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px}.num{font-size:24px;font-weight:700}
.ok{color:var(--green)}.warn{color:var(--amber)}.bad{color:var(--red)}.section{margin:14px 0}.section h2{font:650 17px system-ui;margin:0 0 12px}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:9px;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted)}code{color:var(--blue);word-break:break-all}
.pill{display:inline-block;padding:2px 8px;border:1px solid var(--line);border-radius:999px}.nodes{display:flex;gap:10px;overflow:auto}.node{min-width:210px;border-left:3px solid var(--blue)}
button{background:#1c2948;color:var(--text);border:1px solid var(--line);padding:7px 11px;border-radius:8px;cursor:pointer}.hidden{display:none}
@media(max-width:800px){.grid{grid-template-columns:repeat(2,1fr)}.wrap{padding:14px}}
</style></head><body><main class="wrap">
<h1>Evidence-Driven Delivery</h1><div class="muted">{{.Delivery.ID}} · generated {{.GeneratedAt}}</div>
<div class="grid">
<div class="card"><div class="muted">State</div><div class="num">{{.Delivery.Status}}</div></div>
<div class="card"><div class="muted">Proof</div><div class="num {{if eq .ProofStatus "fresh"}}ok{{else}}bad{{end}}">{{.ProofStatus}}</div></div>
<div class="card"><div class="muted">Criteria</div><div class="num">{{len .Coverage}}</div></div>
<div class="card"><div class="muted">Blocking</div><div class="num {{if eq .Metrics.BlockingFindings 0}}ok{{else}}bad{{end}}">{{.Metrics.BlockingFindings}}</div></div>
</div>
{{if .ProofError}}<div class="section bad">{{.ProofError}}</div>{{end}}
<section class="section"><h2>Acceptance Coverage</h2><table><thead><tr><th>ID</th><th>Status</th><th>Policy</th><th>Statement</th><th>Proof</th></tr></thead><tbody>
{{range .Coverage}}<tr><td><code>{{.CriterionID}}</code></td><td><span class="pill">{{.Status}}</span></td><td>{{.Policy}}</td><td>{{.Statement}}</td><td>{{range .EvidenceIDs}}<code>{{.}}</code><br>{{end}}{{range .VerifierRoles}}{{.}}<br>{{end}}</td></tr>{{end}}
</tbody></table></section>
<section class="section"><h2>Task DAG</h2><div class="nodes">{{range .Graph.Nodes}}<div class="card node"><b>{{.ID}}</b><div>{{.Title}}</div><div class="muted">depends: {{range .Dependencies}}{{.}} {{else}}root{{end}}</div><div>risk: {{.Risk}} · K={{.CandidateCount}}</div></div>{{end}}</div></section>
<section class="section"><h2>Delivery Metrics</h2><table>
<tr><th>Nodes / Candidates / Selected</th><td>{{.Metrics.Nodes}} / {{.Metrics.Candidates}} / {{.Metrics.Selected}}</td></tr>
<tr><th>Revision rounds</th><td>{{.Metrics.RevisionRounds}}</td></tr><tr><th>Gates / Verifiers</th><td>{{.Metrics.Gates}} / {{.Metrics.VerifierReports}}</td></tr>
<tr><th>Tokens / Agent duration</th><td>{{.Metrics.Tokens}} / {{.Metrics.DurationMS}} ms</td></tr><tr><th>Integration tree</th><td><code>{{.Integration.TreeHash}}</code></td></tr>
</table></section>
<section class="section"><h2>Changed Files ({{len .ChangedFiles}})</h2>{{range .ChangedFiles}}<div><code>{{.}}</code></div>{{else}}<span class="muted">No selected write metadata.</span>{{end}}</section>
<section class="section"><h2>Verifier Findings</h2><button onclick="toggleResolved()">Toggle resolved</button><table id="findings"><thead><tr><th>Severity</th><th>Criterion</th><th>Location</th><th>Claim / Evidence</th><th>Status</th></tr></thead><tbody>
{{range .Verification.Findings}}<tr data-status="{{.Status}}"><td>{{.Severity}}</td><td>{{.CriterionID}}</td><td><code>{{.File}}:{{.Line}}</code></td><td>{{.Claim}}<br><span class="muted">{{range .Evidence}}{{.}}; {{end}}</span></td><td>{{.Status}}</td></tr>{{else}}<tr><td colspan="5" class="ok">No findings.</td></tr>{{end}}
</tbody></table></section>
<section class="section"><h2>Human Gate</h2>{{if .Approval}}Approved by <b>{{.Approval.ApprovedBy}}</b> at {{.Approval.ApprovedAt}}{{else}}<span class="warn">Pending explicit cohort deliver accept.</span>{{end}}</section>
</main><script>const report={{.Data}};function toggleResolved(){document.querySelectorAll('#findings tr[data-status]').forEach(x=>{if(x.dataset.status!=='open')x.classList.toggle('hidden')})}</script></body></html>`))

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func DefaultReviewPath(store Store, deliveryID string) string {
	return filepath.Join(store.RootDir, deliveryID, "reports", fmt.Sprintf("review-%s.html", slug(deliveryID)))
}
