package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DependencyFile    = "deps.json"
	DependencyVersion = 1

	DependencyStatusPlanned   = "planned"
	DependencyStatusApproved  = "approved"
	DependencyStatusInstalled = "installed"
	DependencyStatusFailed    = "failed"
	DependencyStatusDryRun    = "dry_run"

	defaultDependencyInstallTimeout = 5 * time.Minute
	maxDependencyOutputRunes        = 4000
)

type DependencyInstallOptions struct {
	DryRun  bool
	Timeout time.Duration
}

func (s Store) DependencyPath() string {
	return filepath.Join(s.RootDir, DependencyFile)
}

func (s Store) LoadDependencies() (DependencyState, error) {
	path := s.DependencyPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DependencyState{Version: DependencyVersion}, nil
		}
		return DependencyState{}, err
	}
	var state DependencyState
	if err := json.Unmarshal(data, &state); err != nil {
		return DependencyState{}, fmt.Errorf("parse dependency state %s: %w", path, err)
	}
	if state.Version == 0 {
		state.Version = DependencyVersion
	}
	return state, nil
}

func (s Store) SaveDependencies(state DependencyState) error {
	now := time.Now().UTC()
	state.Version = DependencyVersion
	state.UpdatedAt = now
	sortDependencyState(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.DependencyPath(), data, 0644)
}

func (s Store) PlanDependencies(proposalID string) (DependencyPlan, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return DependencyPlan{}, errors.New("proposal id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return DependencyPlan{}, err
	}
	proposalIndex := indexProposal(registry.Proposals, proposalID)
	if proposalIndex == -1 {
		return DependencyPlan{}, fmt.Errorf("proposal %q not found", proposalID)
	}
	proposal := registry.Proposals[proposalIndex]
	capabilityID := capabilityIDFromProposal(registry, proposal)
	actions := dependencyActionsFromRequirements(proposal.Dependencies)
	if len(actions) == 0 {
		return DependencyPlan{}, fmt.Errorf("proposal %q has no installable python/npm/brew dependencies", proposal.ID)
	}

	state, err := s.LoadDependencies()
	if err != nil {
		return DependencyPlan{}, err
	}
	now := time.Now().UTC()
	plan := DependencyPlan{
		ID:           uniqueDependencyPlanID(state.Plans, "depplan_"+capabilityID),
		ProposalID:   proposal.ID,
		CapabilityID: capabilityID,
		Status:       DependencyStatusPlanned,
		Scope:        firstNonEmpty(proposal.InstallScope, "project"),
		Risk:         dependencyPlanRisk(actions),
		Actions:      actions,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	state.Plans = append(state.Plans, plan)
	return plan, s.SaveDependencies(state)
}

func (s Store) ApproveDependencyPlan(planID string) (DependencyPlan, error) {
	return s.updateDependencyPlanStatus(planID, DependencyStatusApproved)
}

func (s Store) InstallDependencyPlan(planID string, opts DependencyInstallOptions) (DependencyPlan, []DependencyInstall, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return DependencyPlan{}, nil, errors.New("dependency plan id is required")
	}
	state, err := s.LoadDependencies()
	if err != nil {
		return DependencyPlan{}, nil, err
	}
	index := indexDependencyPlan(state.Plans, planID)
	if index == -1 {
		return DependencyPlan{}, nil, fmt.Errorf("dependency plan %q not found", planID)
	}
	plan := state.Plans[index]
	if plan.Status != DependencyStatusApproved {
		return DependencyPlan{}, nil, fmt.Errorf("dependency plan %q is %q; run `cohort capability deps approve %s` first", plan.ID, plan.Status, plan.ID)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultDependencyInstallTimeout
	}

	now := time.Now().UTC()
	records := make([]DependencyInstall, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		record := DependencyInstall{
			ID:          uniqueDependencyInstallID(append(state.Installs, records...), "depinstall_"+plan.ID+"_"+action.ID),
			PlanID:      plan.ID,
			ActionID:    action.ID,
			Manager:     action.Manager,
			Name:        action.Name,
			Scope:       action.Scope,
			Command:     append([]string(nil), action.Command...),
			Status:      DependencyStatusDryRun,
			InstalledAt: now,
		}
		if opts.DryRun {
			records = append(records, record)
			continue
		}
		exitCode, output, runErr := runDependencyAction(s.projectRoot(), action, opts.Timeout)
		record.ExitCode = exitCode
		record.Output = truncateRunes(output, maxDependencyOutputRunes)
		if runErr != nil {
			record.Status = DependencyStatusFailed
			records = append(records, record)
			state.Installs = append(state.Installs, records...)
			plan.Status = DependencyStatusFailed
			plan.UpdatedAt = now
			state.Plans[index] = plan
			_ = s.SaveDependencies(state)
			return plan, records, fmt.Errorf("dependency action %s failed: %w", action.ID, runErr)
		}
		record.Status = DependencyStatusInstalled
		records = append(records, record)
	}
	if opts.DryRun {
		return plan, records, nil
	}
	plan.Status = DependencyStatusInstalled
	plan.InstalledAt = now
	plan.UpdatedAt = now
	state.Plans[index] = plan
	state.Installs = append(state.Installs, records...)
	return plan, records, s.SaveDependencies(state)
}

func (s Store) updateDependencyPlanStatus(planID string, status string) (DependencyPlan, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return DependencyPlan{}, errors.New("dependency plan id is required")
	}
	state, err := s.LoadDependencies()
	if err != nil {
		return DependencyPlan{}, err
	}
	index := indexDependencyPlan(state.Plans, planID)
	if index == -1 {
		return DependencyPlan{}, fmt.Errorf("dependency plan %q not found", planID)
	}
	plan := state.Plans[index]
	if plan.Status == DependencyStatusInstalled {
		return DependencyPlan{}, fmt.Errorf("dependency plan %q is already installed", plan.ID)
	}
	now := time.Now().UTC()
	plan.Status = status
	plan.UpdatedAt = now
	if status == DependencyStatusApproved {
		plan.ApprovedAt = now
	}
	state.Plans[index] = plan
	return plan, s.SaveDependencies(state)
}

func dependencyActionsFromRequirements(req Requirements) []DependencyAction {
	var actions []DependencyAction
	for _, pkg := range req.Python {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		actions = append(actions, DependencyAction{
			ID:      "pip_" + normalizeID(pkg),
			Manager: "pip",
			Name:    pkg,
			Scope:   "user",
			Command: []string{"python3", "-m", "pip", "install", "--user", pkg},
			Risk:    "R2: installs a Python package into the user site-packages directory",
		})
	}
	for _, pkg := range req.NPM {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		actions = append(actions, DependencyAction{
			ID:      "npm_" + normalizeID(pkg),
			Manager: "npm",
			Name:    pkg,
			Scope:   "project",
			Command: []string{"npm", "install", pkg},
			Risk:    "R2: modifies project npm dependencies and node_modules",
		})
	}
	for _, pkg := range req.Brew {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		actions = append(actions, DependencyAction{
			ID:      "brew_" + normalizeID(pkg),
			Manager: "brew",
			Name:    pkg,
			Scope:   "machine",
			Command: []string{"brew", "install", pkg},
			Risk:    "R2: installs a Homebrew package on this machine",
		})
	}
	return actions
}

func (r *DoctorResult) checkDependencyRecords(store Store, item Capability) {
	expected := dependencyActionsFromRequirements(item.Requires)
	if len(expected) == 0 {
		return
	}
	state, err := store.LoadDependencies()
	if err != nil {
		r.addCheck("dependencies", doctorStatusWarn, "could not read dependency state: "+err.Error())
		return
	}
	installed := installedDependencySet(state.Installs)
	missing := make([]string, 0, len(expected))
	for _, action := range expected {
		key := dependencyInstallKey(action.Manager, action.Name)
		if installed[key] {
			r.addCheck("dependency:"+action.Manager+":"+action.Name, doctorStatusOK, "installed by recorded dependency plan")
			continue
		}
		missing = append(missing, action.Manager+":"+action.Name)
	}
	if len(missing) > 0 {
		r.addCheck("dependencies", doctorStatusWarn, "missing install records for "+strings.Join(missing, ",")+"; run `cohort capability deps plan <proposal_id>`")
	}
}

func installedDependencySet(installs []DependencyInstall) map[string]bool {
	out := map[string]bool{}
	for _, install := range installs {
		if install.Status != DependencyStatusInstalled {
			continue
		}
		out[dependencyInstallKey(install.Manager, install.Name)] = true
	}
	return out
}

func dependencyInstallKey(manager string, name string) string {
	return strings.TrimSpace(manager) + ":" + strings.TrimSpace(name)
}

func runDependencyAction(projectRoot string, action DependencyAction, timeout time.Duration) (int, string, error) {
	if err := validateDependencyAction(action); err != nil {
		return -1, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, action.Command[0], action.Command[1:]...)
	cmd.Dir = projectRoot
	outputBytes, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(outputBytes))
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return exitCode, output, fmt.Errorf("dependency install timed out")
		}
		return exitCode, output, err
	}
	return exitCode, output, nil
}

func validateDependencyAction(action DependencyAction) error {
	if len(action.Command) == 0 {
		return fmt.Errorf("dependency action %q has empty command", action.ID)
	}
	switch action.Manager {
	case "pip":
		if len(action.Command) < 6 || action.Command[0] != "python3" || action.Command[1] != "-m" || action.Command[2] != "pip" || action.Command[3] != "install" || action.Command[4] != "--user" {
			return fmt.Errorf("invalid pip install command for action %q", action.ID)
		}
	case "npm":
		if len(action.Command) < 3 || action.Command[0] != "npm" || action.Command[1] != "install" {
			return fmt.Errorf("invalid npm install command for action %q", action.ID)
		}
	case "brew":
		if len(action.Command) < 3 || action.Command[0] != "brew" || action.Command[1] != "install" {
			return fmt.Errorf("invalid brew install command for action %q", action.ID)
		}
	default:
		return fmt.Errorf("unsupported dependency manager %q", action.Manager)
	}
	return nil
}

func dependencyPlanRisk(actions []DependencyAction) string {
	managers := make([]string, 0, len(actions))
	for _, action := range actions {
		managers = appendUnique(managers, action.Manager)
	}
	sort.Strings(managers)
	return "R2: explicit approval required before installing dependencies via " + strings.Join(managers, ",")
}

func sortDependencyState(state *DependencyState) {
	sort.Slice(state.Plans, func(i, j int) bool {
		return state.Plans[i].CreatedAt.After(state.Plans[j].CreatedAt)
	})
	sort.Slice(state.Installs, func(i, j int) bool {
		return state.Installs[i].InstalledAt.After(state.Installs[j].InstalledAt)
	})
}

func indexDependencyPlan(plans []DependencyPlan, id string) int {
	for index, plan := range plans {
		if plan.ID == id {
			return index
		}
	}
	return -1
}

func uniqueDependencyPlanID(existing []DependencyPlan, base string) string {
	seen := map[string]bool{}
	for _, item := range existing {
		seen[item.ID] = true
	}
	return uniqueID(seen, base)
}

func uniqueDependencyInstallID(existing []DependencyInstall, base string) string {
	seen := map[string]bool{}
	for _, item := range existing {
		seen[item.ID] = true
	}
	return uniqueID(seen, base)
}
