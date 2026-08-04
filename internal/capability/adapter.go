package capability

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const AdapterDir = "adapters"

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
