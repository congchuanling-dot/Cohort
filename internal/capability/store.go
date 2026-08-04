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
	RegistryVersion = 1
	ProjectDirName  = ".cohort"
	CapabilityDir   = "capabilities"
	RegistryFile    = "registry.json"
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
		candidate.Verification.LastPassedAt = existing.Verification.LastPassedAt
		if existing.Status == StatusAvailable {
			candidate.Status = StatusAvailable
		}
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
		return Capability{}, "", fmt.Errorf("capability %q has type %q; only skill capabilities can be verified", capabilityID, item.Type)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := filepath.Join(s.projectRoot(), ProjectDirName, "skills", capabilityID, "tests", "smoke.sh")
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
`, capabilityID, description, escapeFrontMatterValue(argumentHint), capabilityID, proposal.Summary, capabilityID)
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
