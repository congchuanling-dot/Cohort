package capability

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	RootDir string
}

func NewStore(projectRoot string) Store {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	return Store{RootDir: filepath.Clean(filepath.Join(projectRoot, ProjectDirName, CapabilityDir))}
}

func (s Store) RegistryPath() string {
	return filepath.Join(s.RootDir, RegistryFile)
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
	return Proposal{
		GapID:        gap.ID,
		Summary:      fmt.Sprintf("Add capability %s for task: %s", capabilityID, gap.Task),
		InstallScope: "project",
		Dependencies: Requirements{
			Tools: []string{"code_run", "file_read", "file_write", "file_patch"},
		},
		Artifacts: []string{
			filepath.Join(".cohort", "skills", capabilityID, "SKILL.md"),
			filepath.Join(".cohort", "skills", capabilityID, "cohort-capability.json"),
			filepath.Join(".cohort", "skills", capabilityID, "tests", "smoke.sh"),
		},
		Risk: "R2: may create project files and may require dependency installation after user approval",
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
