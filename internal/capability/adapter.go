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

	"cohort/internal/plugin"
)

const AdapterDir = "adapters"
const EnabledAdaptersFile = "enabled_adapters.json"

func (s Store) BuildAdapter(proposalID string, adapterType string) (Capability, []string, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return Capability{}, nil, errors.New("proposal id is required")
	}
	adapterType = strings.ToLower(strings.TrimSpace(adapterType))
	if adapterType == "" {
		adapterType = TypeTool
	}
	if adapterType != TypeTool && adapterType != TypeMCP {
		return Capability{}, nil, fmt.Errorf("adapter type must be %s or %s", TypeTool, TypeMCP)
	}
	registry, err := s.Load()
	if err != nil {
		return Capability{}, nil, err
	}
	proposalIndex := indexProposal(registry.Proposals, proposalID)
	if proposalIndex == -1 {
		return Capability{}, nil, fmt.Errorf("proposal %q not found", proposalID)
	}
	proposal := registry.Proposals[proposalIndex]
	capabilityID := capabilityIDFromProposal(registry, proposal)
	if capabilityID == "" {
		return Capability{}, nil, fmt.Errorf("could not infer capability id from proposal %q", proposal.ID)
	}
	now := time.Now().UTC()
	entry := filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "plugin.json"))
	artifacts, err := s.writeAdapterScaffold(proposal, capabilityID, adapterType, now)
	if err != nil {
		return Capability{}, nil, err
	}
	candidate := Capability{
		ID:       capabilityID,
		Status:   StatusCandidate,
		Type:     adapterType,
		Entry:    entry,
		Triggers: proposalTriggers(proposal),
		Requires: proposal.Dependencies,
		Risk:     proposal.Risk,
		Verification: Verification{
			Command:    fmt.Sprintf("test -f %s", filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "README.md"))),
			SampleTask: proposal.Verification.SampleTask,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if index := indexCapability(registry.Capabilities, capabilityID); index == -1 {
		registry.Capabilities = append(registry.Capabilities, candidate)
	} else {
		existing := registry.Capabilities[index]
		candidate.CreatedAt = existing.CreatedAt
		candidate.Verification.LastPassedAt = existing.Verification.LastPassedAt
		registry.Capabilities[index] = candidate
	}
	registry.Proposals[proposalIndex].Status = StatusCandidate
	registry.Proposals[proposalIndex].Artifacts = artifacts
	registry.Proposals[proposalIndex].Verification = candidate.Verification
	registry.Proposals[proposalIndex].UpdatedAt = now
	if gapIndex := indexGap(registry.Gaps, proposal.GapID); gapIndex != -1 {
		registry.Gaps[gapIndex].Status = StatusCandidate
		registry.Gaps[gapIndex].UpdatedAt = now
	}
	return candidate, artifacts, s.Save(registry)
}

func (s Store) writeAdapterScaffold(proposal Proposal, capabilityID string, adapterType string, now time.Time) ([]string, error) {
	root := filepath.Join(s.projectRoot(), ProjectDirName, AdapterDir, capabilityID)
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	pluginManifest := map[string]any{
		"name":        capabilityID,
		"version":     "0.1.0",
		"description": proposal.Summary,
		"permissions": map[string]any{
			"allow_tools": proposal.Dependencies.Tools,
		},
		"dependencies": proposal.Dependencies,
	}
	if adapterType == TypeMCP {
		pluginManifest["mcp"] = map[string]any{
			"config": "mcp.json",
		}
	} else {
		pluginManifest["commands"] = []map[string]any{{
			"name":        capabilityID,
			"command":     []string{"go", "run", "./adapter.go"},
			"description": "Candidate local tool adapter. Review and wire into Tool Registry manually.",
		}}
	}
	manifestData, err := json.MarshalIndent(pluginManifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestData = append(manifestData, '\n')
	artifacts := []string{
		filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "plugin.json")),
		filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "README.md")),
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), manifestData, 0644); err != nil {
		return nil, err
	}
	readme := adapterReadme(proposal, capabilityID, adapterType, now)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0644); err != nil {
		return nil, err
	}
	if adapterType == TypeMCP {
		artifacts = append(artifacts, filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "mcp.json")))
		if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(adapterMCPJSON(capabilityID)), 0644); err != nil {
			return nil, err
		}
	} else {
		artifacts = append(artifacts, filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, capabilityID, "adapter.go")))
		if err := os.WriteFile(filepath.Join(root, "adapter.go"), []byte(adapterGoStub(capabilityID)), 0644); err != nil {
			return nil, err
		}
	}
	return artifacts, nil
}

func adapterReadme(proposal Proposal, capabilityID string, adapterType string, now time.Time) string {
	return fmt.Sprintf(`# Capability Adapter Candidate

id: %s
type: %s
created_at: %s

## Proposal

%s

## Review Required

- This scaffold is not automatically registered.
- Review permissions, dependencies, and runtime behavior before wiring it into Cohort.
- Keep external effects behind explicit permission policy.
`, capabilityID, adapterType, now.Format(time.RFC3339), proposal.Summary)
}

func adapterGoStub(capabilityID string) string {
	return fmt.Sprintf(`package main

import "fmt"

func main() {
	fmt.Println("candidate tool adapter %s: implement and register manually")
}
`, capabilityID)
}

func adapterMCPJSON(capabilityID string) string {
	return fmt.Sprintf(`{
  "mcpServers": {
    "%s": {
      "type": "stdio",
      "command": "./server"
    }
  }
}
`, capabilityID)
}

type adapterVerifyCheck struct {
	Name    string
	OK      bool
	Message string
}

func (s Store) verifyAdapter(registry Registry, capabilityIndex int) (Capability, string, error) {
	item := registry.Capabilities[capabilityIndex]
	checks := s.adapterVerificationChecks(item)
	failed := 0
	for _, check := range checks {
		if !check.OK {
			failed++
		}
	}
	output := adapterVerificationOutput(checks)
	now := time.Now().UTC()
	if failed > 0 {
		item.Status = StatusFailed
		item.UpdatedAt = now
		registry.Capabilities[capabilityIndex] = item
		_ = s.Save(registry)
		return item, output, fmt.Errorf("capability %q adapter verification failed: %d check(s) failed", item.ID, failed)
	}
	if item.Status == StatusFailed || item.Status == "" {
		item.Status = StatusCandidate
	}
	item.Verification.Command = "cohort capability verify " + item.ID
	item.Verification.LastPassedAt = now
	item.UpdatedAt = now
	registry.Capabilities[capabilityIndex] = item
	return item, output, s.Save(registry)
}

func (s Store) adapterVerificationChecks(item Capability) []adapterVerifyCheck {
	root := filepath.Join(s.projectRoot(), ProjectDirName, AdapterDir, item.ID)
	entry := filepath.Join(s.projectRoot(), filepath.FromSlash(item.Entry))
	checks := []adapterVerifyCheck{}
	add := func(name string, ok bool, message string) {
		checks = append(checks, adapterVerifyCheck{Name: name, OK: ok, Message: message})
	}
	if strings.TrimSpace(item.Entry) == "" {
		add("entry", false, "missing capability entry")
		return checks
	}
	manifest, err := plugin.Load(entry)
	if err != nil {
		add("plugin_manifest", false, err.Error())
	} else {
		add("plugin_manifest", true, filepath.Clean(entry))
		doctor := plugin.Doctor(manifest)
		for _, check := range doctor.Checks {
			add("plugin."+check.Name, check.Status != "error", check.Message)
		}
	}
	readme := filepath.Join(root, "README.md")
	if fileExistsLocal(readme) {
		add("readme", true, filepath.Clean(readme))
	} else {
		add("readme", false, "missing README.md")
	}
	switch item.Type {
	case TypeTool:
		s.verifyToolAdapter(root, add)
	case TypeMCP:
		s.verifyMCPAdapter(root, add)
	default:
		add("adapter_type", false, "unsupported adapter type "+item.Type)
	}
	return checks
}

func (s Store) verifyToolAdapter(root string, add func(name string, ok bool, message string)) {
	adapterPath := filepath.Join(root, "adapter.go")
	if !fileExistsLocal(adapterPath) {
		add("tool.adapter_go", false, "missing adapter.go")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./adapter.go")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	message := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		add("tool.go_run", false, ctx.Err().Error())
		return
	}
	if err != nil {
		if message != "" {
			message = err.Error() + ": " + message
		} else {
			message = err.Error()
		}
		add("tool.go_run", false, message)
		return
	}
	if message == "" {
		message = "go run ./adapter.go passed"
	}
	add("tool.go_run", true, message)
}

func (s Store) verifyMCPAdapter(root string, add func(name string, ok bool, message string)) {
	mcpPath := filepath.Join(root, "mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		add("mcp.config", false, err.Error())
		return
	}
	var parsed struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		add("mcp.config", false, err.Error())
		return
	}
	if len(parsed.MCPServers) == 0 {
		add("mcp.config", false, "mcpServers is empty")
		return
	}
	add("mcp.config", true, fmt.Sprintf("%d server(s)", len(parsed.MCPServers)))
}

func adapterVerificationOutput(checks []adapterVerifyCheck) string {
	var b strings.Builder
	for _, check := range checks {
		status := "ok"
		if !check.OK {
			status = "fail"
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", status, check.Name, check.Message)
	}
	return strings.TrimSpace(b.String())
}

func (s Store) EnableAdapter(capabilityID string) (EnableAdapterResult, error) {
	capabilityID = normalizeID(capabilityID)
	if capabilityID == "" {
		return EnableAdapterResult{}, errors.New("capability id is required")
	}
	registry, err := s.Load()
	if err != nil {
		return EnableAdapterResult{}, err
	}
	index := indexCapability(registry.Capabilities, capabilityID)
	if index == -1 {
		return EnableAdapterResult{}, fmt.Errorf("capability %q not found", capabilityID)
	}
	item := registry.Capabilities[index]
	if item.Status != StatusAvailable {
		return EnableAdapterResult{}, fmt.Errorf("capability %q is %q; run verify/promote before enable", capabilityID, item.Status)
	}
	if item.Type != TypeTool && item.Type != TypeMCP {
		return EnableAdapterResult{}, fmt.Errorf("capability %q has type %q; enable only supports tool or mcp adapters", capabilityID, item.Type)
	}
	if item.Verification.LastPassedAt.IsZero() {
		return EnableAdapterResult{}, fmt.Errorf("capability %q has no successful verification", capabilityID)
	}
	checks := s.adapterVerificationChecks(item)
	for _, check := range checks {
		if !check.OK {
			return EnableAdapterResult{}, fmt.Errorf("capability %q adapter is not clean: %s: %s", capabilityID, check.Name, check.Message)
		}
	}
	state, err := s.loadEnabledAdapters()
	if err != nil {
		return EnableAdapterResult{}, err
	}
	now := time.Now().UTC()
	enabled := EnabledAdapter{
		CapabilityID: item.ID,
		Type:         item.Type,
		Entry:        item.Entry,
		EnabledAt:    now,
	}
	replaced := false
	for index := range state.Adapters {
		if state.Adapters[index].CapabilityID == item.ID {
			state.Adapters[index] = enabled
			replaced = true
			break
		}
	}
	if !replaced {
		state.Adapters = append(state.Adapters, enabled)
	}
	if err := s.saveEnabledAdapters(state); err != nil {
		return EnableAdapterResult{}, err
	}
	result := EnableAdapterResult{
		Capability: item,
		StatePath:  s.EnabledAdaptersPath(),
		Enabled:    true,
	}
	if item.Type == TypeMCP {
		result.MCPImport = filepath.ToSlash(filepath.Join(ProjectDirName, AdapterDir, item.ID, "mcp.json"))
	}
	return result, nil
}

func (s Store) EnabledAdaptersPath() string {
	return filepath.Join(s.RootDir, EnabledAdaptersFile)
}

func (s Store) ListEnabledAdapters() ([]EnabledAdapter, error) {
	state, err := s.loadEnabledAdapters()
	if err != nil {
		return nil, err
	}
	return append([]EnabledAdapter(nil), state.Adapters...), nil
}

func (s Store) loadEnabledAdapters() (EnabledAdapterState, error) {
	path := s.EnabledAdaptersPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EnabledAdapterState{Version: 1}, nil
		}
		return EnabledAdapterState{}, err
	}
	var state EnabledAdapterState
	if err := json.Unmarshal(data, &state); err != nil {
		return EnabledAdapterState{}, err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return state, nil
}

func (s Store) saveEnabledAdapters(state EnabledAdapterState) error {
	state.Version = 1
	state.UpdatedAt = time.Now().UTC()
	sort.Slice(state.Adapters, func(i, j int) bool {
		return state.Adapters[i].CapabilityID < state.Adapters[j].CapabilityID
	})
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.EnabledAdaptersPath(), data, 0644)
}

func fileExistsLocal(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
