package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	RegistryVersion     = 1
	ProjectDirName      = ".cohort"
	CapabilityDir       = "capabilities"
	RegistryFile        = "registry.json"
	skillScaffoldMarker = "COHORT_SKILL_SCAFFOLD_INCOMPLETE"

	maxCapabilityIndexItems        = 50
	maxCapabilityIndexFieldRunes   = 180
	maxCapabilityIndexTriggerCount = 3
	defaultSuggestionMinCount      = 2
	maxSuggestionExamples          = 3

	doctorStatusOK    = "ok"
	doctorStatusWarn  = "warn"
	doctorStatusError = "error"
)

type Store struct {
	ProjectRoot string
	RootDir     string
}

func NewStore(projectRoot string) Store {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	projectRoot = filepath.Clean(projectRoot)
	return Store{
		ProjectRoot: projectRoot,
		RootDir:     filepath.Clean(filepath.Join(projectRoot, ProjectDirName, CapabilityDir)),
	}
}

func (s Store) RegistryPath() string {
	return filepath.Join(s.RootDir, RegistryFile)
}

func (s Store) IndexPrompt() string {
	registry, err := s.Load()
	if err != nil {
		return ""
	}
	available := availableCapabilities(registry.Capabilities)
	if len(available) == 0 {
		return ""
	}
	if len(available) > maxCapabilityIndexItems {
		available = available[:maxCapabilityIndexItems]
	}
	var b strings.Builder
	b.WriteString("\n\n[Capability Index]\n")
	b.WriteString("Capability 是已经 verify/promote 的可复用能力索引。只有 status=available 的能力会出现在这里；candidate/failed/disabled 不自动启用。任务命中 skill 类型 capability 时，先调用 skill_read 读取 skill_id，再按 Skill 工作流执行，并调用 update_working_checkpoint 保存 related_skill 和关键约束。\n")
	for _, item := range available {
		fmt.Fprintf(&b, "- id: `%s`; type: %s; skill_id: `%s`; risk: %s; triggers: %s\n",
			item.ID,
			firstNonEmpty(item.Type, TypeSkill),
			skillIDForCapability(item),
			truncateIndexField(item.Risk),
			truncateIndexField(strings.Join(indexTriggers(item.Triggers), " | ")),
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s Store) projectRoot() string {
	if strings.TrimSpace(s.ProjectRoot) != "" {
		return filepath.Clean(s.ProjectRoot)
	}
	return filepath.Clean(filepath.Dir(filepath.Dir(s.RootDir)))
}

func (s Store) Load() (Registry, error) {
	path := s.RegistryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Registry{Version: RegistryVersion}, nil
		}
		return Registry{}, err
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return Registry{}, fmt.Errorf("parse capability registry %s: %w", path, err)
	}
	if registry.Version == 0 {
		registry.Version = RegistryVersion
	}
	return registry, nil
}

func (s Store) Save(registry Registry) error {
	now := time.Now().UTC()
	registry.Version = RegistryVersion
	registry.UpdatedAt = now
	sortRegistry(&registry)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.RegistryPath(), data, 0644)
}

func (s Store) AddGap(gap Gap) (Gap, error) {
	registry, err := s.Load()
	if err != nil {
		return Gap{}, err
	}
	now := time.Now().UTC()
	gap.Task = strings.TrimSpace(gap.Task)
	gap.MissingCapability = normalizeID(gap.MissingCapability)
	if gap.MissingCapability == "" {
		gap.MissingCapability = inferCapabilityID(gap.Task)
	}
	if gap.Source == "" {
		gap.Source = "manual"
	}
	if gap.Status == "" {
		gap.Status = StatusMissing
	}
	if gap.ID == "" {
		gap.ID = "gap_" + gap.MissingCapability
	}
	gap.ID = uniqueGapID(registry.Gaps, gap.ID)
	gap.CreatedAt = now
	gap.UpdatedAt = now
	if len(gap.SuggestedActions) == 0 {
		gap.SuggestedActions = defaultSuggestedActions()
	}
	registry.Gaps = append(registry.Gaps, gap)
	return gap, s.Save(registry)
}

func (s Store) AddProposal(input Proposal) (Proposal, error) {
	registry, err := s.Load()
	if err != nil {
		return Proposal{}, err
	}
	now := time.Now().UTC()
	input.Summary = strings.TrimSpace(input.Summary)
	if input.Summary == "" {
		return Proposal{}, errors.New("proposal summary is required")
	}
	if input.InstallScope == "" {
		input.InstallScope = "project"
	}
	if input.Risk == "" {
		input.Risk = "R2: changes local project or user environment; requires approval before install"
	}
	if input.Status == "" {
		input.Status = StatusProposed
	}
	if input.ID == "" {
		input.ID = "proposal_" + normalizeID(input.Summary)
	}
	input.ID = uniqueProposalID(registry.Proposals, input.ID)
	input.CreatedAt = now
	input.UpdatedAt = now
	registry.Proposals = append(registry.Proposals, input)
	return input, s.Save(registry)
}

func (s Store) Suggestions() ([]Suggestion, error) {
	registry, err := s.Load()
	if err != nil {
		return nil, err
	}
	blocked := blockedSuggestionCapabilities(registry)
	groups := map[string]*suggestionAccumulator{}
	for _, gap := range registry.Gaps {
		if !gapCountsForSuggestion(gap) {
			continue
		}
		capabilityID := normalizeID(gap.MissingCapability)
		if capabilityID == "" || blocked[capabilityID] {
			continue
		}
		group := groups[capabilityID]
		if group == nil {
			group = &suggestionAccumulator{missingCapability: capabilityID}
			groups[capabilityID] = group
		}
		group.add(gap)
	}
	suggestions := make([]Suggestion, 0, len(groups))
	for _, group := range groups {
		if group.count < defaultSuggestionMinCount {
			continue
		}
		suggestions = append(suggestions, group.suggestion())
	}
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].Count != suggestions[j].Count {
			return suggestions[i].Count > suggestions[j].Count
		}
		if !suggestions[i].LastSeenAt.Equal(suggestions[j].LastSeenAt) {
			return suggestions[i].LastSeenAt.After(suggestions[j].LastSeenAt)
		}
		return suggestions[i].MissingCapability < suggestions[j].MissingCapability
	})
	return suggestions, nil
}

func (s Store) Doctor(capabilityID string) (DoctorResult, error) {
	capabilityID = normalizeID(capabilityID)
	if capabilityID == "" {
		return DoctorResult{}, errors.New("capability id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return DoctorResult{}, err
	}
	capabilityIndex := indexCapability(registry.Capabilities, capabilityID)
	if capabilityIndex == -1 {
		return DoctorResult{}, fmt.Errorf("capability %q not found", capabilityID)
	}
	item := registry.Capabilities[capabilityIndex]
	result := DoctorResult{Capability: item}
	result.addCheck("registry", doctorStatusOK, "capability is registered")
	result.addCheck("status", doctorStatusForCapability(item), doctorStatusMessage(item))
	result.checkSkillArtifacts(s.projectRoot(), item)
	result.checkRequirements(item.Requires)
	result.checkDependencyRecords(s, item)
	if item.Verification.LastPassedAt.IsZero() {
		result.addCheck("verification", doctorStatusWarn, "no successful verification recorded")
	} else {
		result.addCheck("verification", doctorStatusOK, "last successful verification at "+item.Verification.LastPassedAt.Format(time.RFC3339))
	}
	result.ReadyToVerify = !result.hasErrors() && item.Status != StatusDisabled
	result.ReadyToPromote = result.ReadyToVerify && item.Status != StatusFailed && !item.Verification.LastPassedAt.IsZero()
	result.NextActions = doctorNextActions(item, result)
	return result, nil
}

func (s Store) Build(proposalID string) (Capability, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return Capability{}, errors.New("proposal id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return Capability{}, err
	}
	proposalIndex := indexProposal(registry.Proposals, proposalID)
	if proposalIndex == -1 {
		return Capability{}, fmt.Errorf("proposal %q not found", proposalID)
	}
	proposal := registry.Proposals[proposalIndex]
	if proposal.Status != "" && proposal.Status != StatusProposed && proposal.Status != StatusCandidate {
		return Capability{}, fmt.Errorf("proposal %q has status %q; only proposed or candidate proposals can be built", proposal.ID, proposal.Status)
	}
	capabilityID := capabilityIDFromProposal(registry, proposal)
	if capabilityID == "" {
		return Capability{}, fmt.Errorf("could not infer capability id from proposal %q", proposal.ID)
	}

	now := time.Now().UTC()
	entry := skillEntry(capabilityID)
	verificationCommand := skillSmokeCommand(capabilityID)
	if err := s.writeSkillScaffold(proposal, capabilityID, entry, verificationCommand, now); err != nil {
		return Capability{}, err
	}

	candidate := Capability{
		ID:       capabilityID,
		Status:   StatusCandidate,
		Type:     TypeSkill,
		Entry:    entry,
		Triggers: proposalTriggers(proposal),
		Requires: mergeRequirements(proposal.Dependencies, Requirements{
			Commands: []string{"bash"},
		}),
		Risk: proposal.Risk,
		Verification: Verification{
			Command:    verificationCommand,
			SampleTask: proposal.Verification.SampleTask,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	capabilityIndex := indexCapability(registry.Capabilities, capabilityID)
	if capabilityIndex == -1 {
		registry.Capabilities = append(registry.Capabilities, candidate)
	} else {
		existing := registry.Capabilities[capabilityIndex]
		candidate.CreatedAt = existing.CreatedAt
		registry.Capabilities[capabilityIndex] = candidate
	}
	registry.Proposals[proposalIndex].Status = StatusCandidate
	registry.Proposals[proposalIndex].Artifacts = scaffoldArtifacts(capabilityID)
	registry.Proposals[proposalIndex].Verification.Command = verificationCommand
	registry.Proposals[proposalIndex].UpdatedAt = now
	if gapIndex := indexGap(registry.Gaps, proposal.GapID); gapIndex != -1 {
		registry.Gaps[gapIndex].Status = StatusCandidate
		registry.Gaps[gapIndex].UpdatedAt = now
	}
	return candidate, s.Save(registry)
}

func (s Store) Verify(capabilityID string) (Capability, string, error) {
	capabilityID = normalizeID(capabilityID)
	if capabilityID == "" {
		return Capability{}, "", errors.New("capability id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return Capability{}, "", err
	}
	capabilityIndex := indexCapability(registry.Capabilities, capabilityID)
	if capabilityIndex == -1 {
		return Capability{}, "", fmt.Errorf("capability %q not found", capabilityID)
	}
	item := registry.Capabilities[capabilityIndex]
	if item.Status == StatusDisabled {
		return Capability{}, "", fmt.Errorf("capability %q is disabled", capabilityID)
	}
	if item.Type != TypeSkill {
		if item.Type == TypeTool || item.Type == TypeMCP {
			return s.verifyAdapter(registry, capabilityIndex)
		}
		return Capability{}, "", fmt.Errorf("capability %q has unsupported type %q", capabilityID, item.Type)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	skillPath := filepath.Join(s.projectRoot(), filepath.FromSlash(item.Entry))
	skillData, err := os.ReadFile(skillPath)
	if err != nil {
		return item, "", fmt.Errorf("read capability skill %q: %w", capabilityID, err)
	}
	if strings.Contains(string(skillData), skillScaffoldMarker) {
		return item, "", fmt.Errorf("capability %q is still the generated Skill scaffold; implement the workflow and remove %s", capabilityID, skillScaffoldMarker)
	}
	script := filepath.Join(s.projectRoot(), ProjectDirName, "skills", capabilityID, "tests", "smoke.sh")
	scriptData, err := os.ReadFile(script)
	if err != nil {
		return item, "", fmt.Errorf("read capability smoke test %q: %w", capabilityID, err)
	}
	if string(scriptData) == smokeScriptContent(capabilityID) {
		return item, "", fmt.Errorf("capability %q still uses the generated structural smoke test; replace it with a behavior-level test", capabilityID)
	}
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = s.projectRoot()
	outputBytes, runErr := cmd.CombinedOutput()
	output := strings.TrimSpace(string(outputBytes))
	now := time.Now().UTC()
	if runErr != nil {
		item.Status = StatusFailed
		item.UpdatedAt = now
		registry.Capabilities[capabilityIndex] = item
		_ = s.Save(registry)
		if ctx.Err() == context.DeadlineExceeded {
			return item, output, fmt.Errorf("capability %q verification timed out", capabilityID)
		}
		return item, output, fmt.Errorf("capability %q verification failed: %w", capabilityID, runErr)
	}
	if item.Status == StatusFailed || item.Status == "" {
		item.Status = StatusCandidate
	}
	item.Verification.Command = skillSmokeCommand(capabilityID)
	item.Verification.LastPassedAt = now
	item.UpdatedAt = now
	registry.Capabilities[capabilityIndex] = item
	return item, output, s.Save(registry)
}

func (s Store) Promote(capabilityID string) (Capability, error) {
	if _, _, err := s.Verify(capabilityID); err != nil {
		return Capability{}, fmt.Errorf("capability %q cannot be promoted because verification is not currently passing: %w", capabilityID, err)
	}
	return s.updateCapabilityStatus(capabilityID, StatusAvailable, true)
}

func (s Store) Disable(capabilityID string) (Capability, error) {
	return s.updateCapabilityStatus(capabilityID, StatusDisabled, false)
}

func (s Store) Find(id string) (string, any, error) {
	registry, err := s.Load()
	if err != nil {
		return "", nil, err
	}
	for _, item := range registry.Capabilities {
		if item.ID == id {
			return "capability", item, nil
		}
	}
	for _, item := range registry.Gaps {
		if item.ID == id {
			return "gap", item, nil
		}
	}
	for _, item := range registry.Proposals {
		if item.ID == id {
			return "proposal", item, nil
		}
	}
	return "", nil, fmt.Errorf("capability item %q not found", id)
}

func (s Store) updateCapabilityStatus(capabilityID string, status string, requireVerification bool) (Capability, error) {
	capabilityID = normalizeID(capabilityID)
	if capabilityID == "" {
		return Capability{}, errors.New("capability id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return Capability{}, err
	}
	capabilityIndex := indexCapability(registry.Capabilities, capabilityID)
	if capabilityIndex == -1 {
		return Capability{}, fmt.Errorf("capability %q not found", capabilityID)
	}
	item := registry.Capabilities[capabilityIndex]
	if requireVerification && item.Verification.LastPassedAt.IsZero() {
		return Capability{}, fmt.Errorf("capability %q has no successful verification; run `cohort capability verify %s` first", capabilityID, capabilityID)
	}
	if requireVerification && item.Status == StatusFailed {
		return Capability{}, fmt.Errorf("capability %q is failed; run `cohort capability verify %s` after fixing it", capabilityID, capabilityID)
	}
	now := time.Now().UTC()
	item.Status = status
	item.UpdatedAt = now
	registry.Capabilities[capabilityIndex] = item
	for index := range registry.Proposals {
		if proposalHasCapabilityID(registry.Proposals[index], capabilityID) {
			registry.Proposals[index].Status = status
			registry.Proposals[index].UpdatedAt = now
			if gapIndex := indexGap(registry.Gaps, registry.Proposals[index].GapID); gapIndex != -1 {
				registry.Gaps[gapIndex].Status = status
				registry.Gaps[gapIndex].UpdatedAt = now
			}
		}
	}
	return item, s.Save(registry)
}

func (s Store) writeSkillScaffold(proposal Proposal, capabilityID string, entry string, verificationCommand string, generatedAt time.Time) error {
	root := filepath.Join(s.projectRoot(), ProjectDirName, "skills", capabilityID)
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, "SKILL.md"), []byte(skillScaffoldContent(proposal, capabilityID))); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, "cohort-capability.json"), []byte(capabilityManifestContent(proposal, capabilityID, entry, verificationCommand, generatedAt))); err != nil {
		return err
	}
	smokePath := filepath.Join(root, "tests", "smoke.sh")
	if err := writeFileIfMissing(smokePath, []byte(smokeScriptContent(capabilityID))); err != nil {
		return err
	}
	return os.Chmod(smokePath, 0755)
}

func writeFileIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func NewGapFromTask(task string) Gap {
	capabilityID := inferCapabilityID(task)
	return Gap{
		Task:              strings.TrimSpace(task),
		MissingCapability: capabilityID,
		Source:            "manual",
		Status:            StatusMissing,
		Evidence:          []string{"manual proposal request"},
		SuggestedActions:  defaultSuggestedActions(),
	}
}

func NewProposalFromGap(gap Gap) Proposal {
	capabilityID := gap.MissingCapability
	if capabilityID == "" {
		capabilityID = inferCapabilityID(gap.Task)
	}
	artifacts := scaffoldArtifacts(capabilityID)
	return Proposal{
		GapID:        gap.ID,
		Summary:      fmt.Sprintf("Add capability %s for task: %s", capabilityID, gap.Task),
		InstallScope: "project",
		Dependencies: Requirements{
			Tools: []string{"code_run", "file_read", "file_write", "file_patch"},
		},
		Artifacts: artifacts,
		Risk:      "R2: may create project files and may require dependency installation after user approval",
		Verification: Verification{
			Command:    "cohort capability verify " + capabilityID,
			SampleTask: gap.Task,
		},
		Status: StatusProposed,
	}
}

func sortRegistry(registry *Registry) {
	sort.Slice(registry.Capabilities, func(i, j int) bool { return registry.Capabilities[i].ID < registry.Capabilities[j].ID })
	sort.Slice(registry.Gaps, func(i, j int) bool { return registry.Gaps[i].CreatedAt.After(registry.Gaps[j].CreatedAt) })
	sort.Slice(registry.Proposals, func(i, j int) bool { return registry.Proposals[i].CreatedAt.After(registry.Proposals[j].CreatedAt) })
}

func uniqueGapID(existing []Gap, base string) string {
	seen := map[string]bool{}
	for _, item := range existing {
		seen[item.ID] = true
	}
	return uniqueID(seen, base)
}

func uniqueProposalID(existing []Proposal, base string) string {
	seen := map[string]bool{}
	for _, item := range existing {
		seen[item.ID] = true
	}
	return uniqueID(seen, base)
}

func uniqueID(seen map[string]bool, base string) string {
	base = normalizeID(base)
	if base == "" {
		base = "item"
	}
	if !seen[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !seen[candidate] {
			return candidate
		}
	}
}

var nonIDChars = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonIDChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if len(value) > 64 {
		value = strings.Trim(value[:64], "_")
	}
	return value
}

func inferCapabilityID(task string) string {
	id := normalizeID(task)
	if id == "" {
		return "unknown_capability"
	}
	return id
}

func defaultSuggestedActions() []string {
	return []string{
		"probe current environment and available tools",
		"identify missing dependencies or adapters",
		"generate a project-level Skill or Tool scaffold",
		"run a smoke test before promotion",
	}
}

func (r *DoctorResult) addCheck(name string, status string, message string) {
	r.Checks = append(r.Checks, DoctorCheck{
		Name:    name,
		Status:  status,
		Message: message,
	})
}

func (r DoctorResult) hasErrors() bool {
	for _, check := range r.Checks {
		if check.Status == doctorStatusError {
			return true
		}
	}
	return false
}

func (r *DoctorResult) checkSkillArtifacts(projectRoot string, item Capability) {
	if item.Type != "" && item.Type != TypeSkill {
		r.addCheck("type", doctorStatusWarn, "doctor only performs artifact checks for skill capabilities")
		return
	}
	entry := strings.TrimSpace(item.Entry)
	if entry == "" {
		r.addCheck("skill_entry", doctorStatusError, "skill capability has no entry path")
		return
	}
	entryPath := filepath.Join(projectRoot, filepath.FromSlash(entry))
	if fileExists(entryPath) {
		r.addCheck("skill_entry", doctorStatusOK, entry)
		if data, err := os.ReadFile(entryPath); err != nil {
			r.addCheck("skill_implementation", doctorStatusError, err.Error())
		} else if strings.Contains(string(data), skillScaffoldMarker) {
			r.addCheck("skill_implementation", doctorStatusError, "generated scaffold marker is still present")
		} else {
			r.addCheck("skill_implementation", doctorStatusOK, "scaffold marker removed")
		}
	} else {
		r.addCheck("skill_entry", doctorStatusError, "missing "+entry)
	}
	root := filepath.Dir(entryPath)
	manifest := filepath.Join(root, "cohort-capability.json")
	if fileExists(manifest) {
		r.addCheck("manifest", doctorStatusOK, relPath(projectRoot, manifest))
	} else {
		r.addCheck("manifest", doctorStatusWarn, "missing "+relPath(projectRoot, manifest))
	}
	smoke := filepath.Join(root, "tests", "smoke.sh")
	if fileExists(smoke) {
		if data, err := os.ReadFile(smoke); err != nil {
			r.addCheck("smoke_test", doctorStatusError, err.Error())
		} else if string(data) == smokeScriptContent(item.ID) {
			r.addCheck("smoke_test", doctorStatusError, "replace generated structural smoke test with behavior-level verification")
		} else {
			r.addCheck("smoke_test", doctorStatusOK, relPath(projectRoot, smoke))
		}
	} else {
		r.addCheck("smoke_test", doctorStatusError, "missing "+relPath(projectRoot, smoke))
	}
}

func (r *DoctorResult) checkRequirements(requires Requirements) {
	for _, command := range requires.Commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if _, err := exec.LookPath(command); err != nil {
			r.addCheck("command:"+command, doctorStatusError, "command not found in PATH")
		} else {
			r.addCheck("command:"+command, doctorStatusOK, "command is available")
		}
	}
	for _, env := range requires.Env {
		env = strings.TrimSpace(env)
		if env == "" {
			continue
		}
		if _, ok := os.LookupEnv(env); ok {
			r.addCheck("env:"+env, doctorStatusOK, "environment variable is set")
		} else {
			r.addCheck("env:"+env, doctorStatusError, "environment variable is not set")
		}
	}
	if len(requires.Tools) > 0 {
		r.addCheck("tools", doctorStatusOK, "declared Cohort tools: "+strings.Join(requires.Tools, ","))
	}
	if len(requires.Python) > 0 {
		r.addCheck("python", doctorStatusWarn, "declared Python packages are not installed automatically: "+strings.Join(requires.Python, ","))
	}
}

func doctorStatusForCapability(item Capability) string {
	switch item.Status {
	case StatusCandidate, StatusAvailable:
		return doctorStatusOK
	case StatusFailed, StatusDisabled:
		return doctorStatusWarn
	default:
		return doctorStatusWarn
	}
}

func doctorStatusMessage(item Capability) string {
	switch item.Status {
	case StatusCandidate:
		return "candidate is ready for verification"
	case StatusAvailable:
		return "capability is available"
	case StatusFailed:
		return "last verification failed; rerun verify after fixing artifacts"
	case StatusDisabled:
		return "capability is disabled"
	default:
		return "capability status is " + firstNonEmpty(item.Status, "unknown")
	}
}

func doctorNextActions(item Capability, result DoctorResult) []string {
	if result.hasErrors() {
		return []string{"fix error checks before running verify"}
	}
	if item.Status == StatusDisabled {
		return []string{"capability is disabled; rebuild or re-enable it before use"}
	}
	if item.Status == StatusFailed {
		return []string{"fix the failed smoke test and run: cohort capability verify " + item.ID}
	}
	if item.Verification.LastPassedAt.IsZero() {
		return []string{"cohort capability verify " + item.ID}
	}
	if item.Status != StatusAvailable {
		return []string{"cohort capability promote " + item.ID}
	}
	return []string{"capability is available"}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func relPath(root string, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

type suggestionAccumulator struct {
	missingCapability string
	count             int
	sources           []string
	exampleTasks      []string
	firstSeenAt       time.Time
	lastSeenAt        time.Time
}

func (a *suggestionAccumulator) add(gap Gap) {
	a.count++
	a.sources = appendUnique(a.sources, gap.Source)
	if len(a.exampleTasks) < maxSuggestionExamples {
		a.exampleTasks = appendUnique(a.exampleTasks, gap.Task)
	}
	if a.firstSeenAt.IsZero() || gap.CreatedAt.Before(a.firstSeenAt) {
		a.firstSeenAt = gap.CreatedAt
	}
	if a.lastSeenAt.IsZero() || gap.CreatedAt.After(a.lastSeenAt) {
		a.lastSeenAt = gap.CreatedAt
	}
}

func (a suggestionAccumulator) suggestion() Suggestion {
	return Suggestion{
		MissingCapability: a.missingCapability,
		Count:             a.count,
		Sources:           a.sources,
		ExampleTasks:      a.exampleTasks,
		FirstSeenAt:       a.firstSeenAt,
		LastSeenAt:        a.lastSeenAt,
		NextCommand:       fmt.Sprintf("cohort capability propose %q", firstNonEmpty(firstSliceValue(a.exampleTasks), a.missingCapability)),
		Reason:            fmt.Sprintf("recorded %d repeated missing-capability gaps without an active proposal or registered capability", a.count),
	}
}

func blockedSuggestionCapabilities(registry Registry) map[string]bool {
	blocked := map[string]bool{}
	for _, item := range registry.Capabilities {
		id := normalizeID(item.ID)
		if id != "" && item.Status != StatusDisabled && item.Status != StatusFailed {
			blocked[id] = true
		}
	}
	for _, item := range registry.Proposals {
		id := capabilityIDFromProposal(registry, item)
		if id != "" && item.Status != StatusDisabled && item.Status != StatusFailed {
			blocked[id] = true
		}
	}
	return blocked
}

func gapCountsForSuggestion(gap Gap) bool {
	switch gap.Status {
	case "", StatusMissing, StatusFailed:
		return true
	default:
		return false
	}
}

func firstSliceValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func indexCapability(items []Capability, id string) int {
	for index, item := range items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func indexGap(items []Gap, id string) int {
	for index, item := range items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func indexProposal(items []Proposal, id string) int {
	for index, item := range items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func capabilityIDFromProposal(registry Registry, proposal Proposal) string {
	for _, artifact := range proposal.Artifacts {
		artifact = filepath.ToSlash(filepath.Clean(artifact))
		prefix := ProjectDirName + "/skills/"
		if !strings.HasPrefix(artifact, prefix) || !strings.HasSuffix(artifact, "/SKILL.md") {
			continue
		}
		rest := strings.TrimPrefix(artifact, prefix)
		alias, _, ok := strings.Cut(rest, "/")
		if ok {
			if id := normalizeID(alias); id != "" {
				return id
			}
		}
	}
	if proposal.GapID != "" {
		for _, gap := range registry.Gaps {
			if gap.ID == proposal.GapID && gap.MissingCapability != "" {
				return normalizeID(gap.MissingCapability)
			}
		}
	}
	return normalizeID(proposal.Summary)
}

func proposalHasCapabilityID(proposal Proposal, capabilityID string) bool {
	capabilityID = normalizeID(capabilityID)
	for _, artifact := range proposal.Artifacts {
		if strings.Contains(filepath.ToSlash(artifact), "/skills/"+capabilityID+"/") {
			return true
		}
	}
	return false
}

func proposalTriggers(proposal Proposal) []string {
	triggers := append([]string{}, strings.TrimSpace(proposal.Verification.SampleTask))
	triggers = append(triggers, strings.TrimSpace(proposal.Summary))
	out := make([]string, 0, len(triggers))
	for _, item := range triggers {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func mergeRequirements(left, right Requirements) Requirements {
	return Requirements{
		Tools:    appendUnique(left.Tools, right.Tools...),
		Commands: appendUnique(left.Commands, right.Commands...),
		Python:   appendUnique(left.Python, right.Python...),
		NPM:      appendUnique(left.NPM, right.NPM...),
		Brew:     appendUnique(left.Brew, right.Brew...),
		Env:      appendUnique(left.Env, right.Env...),
	}
}

func appendUnique(values []string, more ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values)+len(more))
	for _, item := range append(values, more...) {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func scaffoldArtifacts(capabilityID string) []string {
	return []string{
		skillEntry(capabilityID),
		filepath.Join(ProjectDirName, "skills", capabilityID, "cohort-capability.json"),
		filepath.Join(ProjectDirName, "skills", capabilityID, "tests", "smoke.sh"),
	}
}

func skillEntry(capabilityID string) string {
	return filepath.Join(ProjectDirName, "skills", capabilityID, "SKILL.md")
}

func skillSmokeCommand(capabilityID string) string {
	return "bash " + filepath.ToSlash(filepath.Join(ProjectDirName, "skills", capabilityID, "tests", "smoke.sh"))
}

func skillScaffoldContent(proposal Proposal, capabilityID string) string {
	description := fmt.Sprintf("Candidate project Skill for capability %s generated from %s.", capabilityID, proposal.ID)
	argumentHint := proposal.Verification.SampleTask
	if argumentHint == "" {
		argumentHint = "task that requires this capability"
	}
	return fmt.Sprintf(`---
name: %s
description: %s
user-invocable: true
argument-hint: "%s"
requires:
  commands:
    - bash
---

# %s

This is a candidate project Skill generated by `+"`cohort capability build`"+`.
Marker: %s

## When To Use

Use this Skill when the task matches this proposal:

%s

## Workflow

1. Restate the capability gap and the concrete task.
2. Probe the local environment with existing Cohort tools before claiming the task is unsupported.
3. Prefer project-local scripts, documented commands, and structured parsers over ad hoc manual steps.
4. Do not install Python, npm, brew, or system dependencies without explicit user approval.
5. After a successful real task, update this Skill with the durable workflow and expand `+"`tests/smoke.sh`"+` with a meaningful smoke test.

## Verification

Run:

`+"```bash"+`
cohort capability verify %s
`+"```"+`
`, capabilityID, description, escapeFrontMatterValue(argumentHint), capabilityID, skillScaffoldMarker, proposal.Summary, capabilityID)
}

func capabilityManifestContent(proposal Proposal, capabilityID string, entry string, verificationCommand string, generatedAt time.Time) string {
	manifest := struct {
		CapabilityID        string `json:"capability_id"`
		ProposalID          string `json:"proposal_id"`
		GapID               string `json:"gap_id,omitempty"`
		Status              string `json:"status"`
		Type                string `json:"type"`
		Entry               string `json:"entry"`
		VerificationCommand string `json:"verification_command"`
		GeneratedAt         string `json:"generated_at"`
	}{
		CapabilityID:        capabilityID,
		ProposalID:          proposal.ID,
		GapID:               proposal.GapID,
		Status:              StatusCandidate,
		Type:                TypeSkill,
		Entry:               filepath.ToSlash(entry),
		VerificationCommand: verificationCommand,
		GeneratedAt:         generatedAt.Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(append(data, '\n'))
}

func smokeScriptContent(capabilityID string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test -f "$skill_dir/SKILL.md"
test -f "$skill_dir/cohort-capability.json"
grep -q "name: %s" "$skill_dir/SKILL.md"

echo "capability smoke passed: %s"
`, capabilityID, capabilityID)
}

func escapeFrontMatterValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func availableCapabilities(items []Capability) []Capability {
	out := make([]Capability, 0, len(items))
	for _, item := range items {
		if item.Status == StatusAvailable {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func skillIDForCapability(item Capability) string {
	if item.Type != "" && item.Type != TypeSkill {
		return "-"
	}
	alias := item.ID
	entry := filepath.ToSlash(filepath.Clean(item.Entry))
	prefix := ProjectDirName + "/skills/"
	if rest, ok := strings.CutPrefix(entry, prefix); ok {
		if candidate, _, ok := strings.Cut(rest, "/"); ok && candidate != "" {
			alias = candidate
		}
	}
	return "project/" + normalizeID(alias)
}

func indexTriggers(triggers []string) []string {
	out := make([]string, 0, maxCapabilityIndexTriggerCount)
	for _, item := range triggers {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= maxCapabilityIndexTriggerCount {
			break
		}
	}
	if len(out) == 0 {
		return []string{"-"}
	}
	return out
}

func truncateIndexField(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "-"
	}
	return truncateRunes(value, maxCapabilityIndexFieldRunes)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
