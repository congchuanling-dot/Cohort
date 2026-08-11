package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var deliveryTransitions = map[DeliveryStatus]map[DeliveryStatus]bool{
	StatusDraft: {
		StatusPlanned:            true,
		StatusNeedsHumanDecision: true,
		StatusFailed:             true,
		StatusCancelled:          true,
	},
	StatusPlanned: {
		StatusRunning:            true,
		StatusNeedsHumanDecision: true,
		StatusCancelled:          true,
		StatusFailed:             true,
	},
	StatusRunning: {
		StatusIntegrating:        true,
		StatusNeedsRevision:      true,
		StatusNeedsHumanDecision: true,
		StatusBudgetExhausted:    true,
		StatusFailed:             true,
		StatusCancelled:          true,
	},
	StatusNeedsRevision: {
		StatusRunning:            true,
		StatusNeedsHumanDecision: true,
		StatusBudgetExhausted:    true,
		StatusFailed:             true,
		StatusCancelled:          true,
	},
	StatusIntegrating: {
		StatusVerifying:          true,
		StatusNeedsRevision:      true,
		StatusNeedsHumanDecision: true,
		StatusFailed:             true,
		StatusCancelled:          true,
	},
	StatusVerifying: {
		StatusReadyForReview:     true,
		StatusNeedsRevision:      true,
		StatusNeedsHumanDecision: true,
		StatusBudgetExhausted:    true,
		StatusFailed:             true,
		StatusCancelled:          true,
	},
	StatusNeedsHumanDecision: {
		StatusPlanned:        true,
		StatusRunning:        true,
		StatusReadyForReview: true,
		StatusCancelled:      true,
		StatusFailed:         true,
	},
	StatusReadyForReview: {
		StatusApproved:      true,
		StatusNeedsRevision: true,
		StatusCancelled:     true,
		StatusFailed:        true,
	},
	StatusApproved: {
		StatusMerging:       true,
		StatusNeedsRevision: true,
		StatusCancelled:     true,
		StatusFailed:        true,
	},
	StatusMerging: {
		StatusApproved:         true,
		StatusMergedUnverified: true,
		StatusFailed:           true,
	},
	StatusMergedUnverified: {
		StatusVerified: true,
		StatusFailed:   true,
	},
	StatusBudgetExhausted: {
		StatusRunning:   true,
		StatusCancelled: true,
		StatusFailed:    true,
	},
	StatusFailed: {
		StatusRunning:   true,
		StatusCancelled: true,
	},
}

func ValidateTransition(from DeliveryStatus, to DeliveryStatus) error {
	if from == to {
		return nil
	}
	if !deliveryTransitions[from][to] {
		return fmt.Errorf("delivery cannot transition from %s to %s", from, to)
	}
	return nil
}

func ValidateContract(contract AcceptanceContract) error {
	if contract.SchemaVersion != 0 && contract.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported contract schema version %d", contract.SchemaVersion)
	}
	if strings.TrimSpace(contract.RequirementHash) == "" {
		return errors.New("contract requirement_hash is required")
	}
	if strings.TrimSpace(contract.BaseCommit) == "" {
		return errors.New("contract base_commit is required")
	}
	if strings.TrimSpace(contract.Summary) == "" {
		return errors.New("contract summary is required")
	}
	if len(contract.Criteria) == 0 {
		return errors.New("contract requires at least one criterion")
	}
	if len(contract.AllowedScope) == 0 {
		return errors.New("contract allowed_scope is required")
	}
	if !validRisk(contract.RiskProfile.Level) {
		return fmt.Errorf("invalid contract risk level %q", contract.RiskProfile.Level)
	}
	for _, path := range append(append([]string(nil), contract.AllowedScope...), contract.ForbiddenScope...) {
		if err := validateRepoPattern(path); err != nil {
			return fmt.Errorf("contract scope %q: %w", path, err)
		}
	}

	gates := make(map[string]GateSpec, len(contract.RequiredGates))
	for _, gate := range contract.RequiredGates {
		gate.ID = strings.TrimSpace(gate.ID)
		if gate.ID == "" {
			return errors.New("gate id is required")
		}
		if err := validateStableID(gate.ID); err != nil {
			return fmt.Errorf("gate id %q: %w", gate.ID, err)
		}
		if _, exists := gates[gate.ID]; exists {
			return fmt.Errorf("duplicate gate id %q", gate.ID)
		}
		if strings.TrimSpace(gate.Name) == "" || strings.TrimSpace(gate.Kind) == "" {
			return fmt.Errorf("gate %q requires name and kind", gate.ID)
		}
		if gate.Kind == "command" && len(gate.Command) == 0 {
			return fmt.Errorf("command gate %q requires argv", gate.ID)
		}
		for _, path := range gate.Paths {
			if err := validateRepoPattern(path); err != nil {
				return fmt.Errorf("gate %q path %q: %w", gate.ID, path, err)
			}
		}
		gates[gate.ID] = gate
	}

	criteria := map[string]bool{}
	mandatory := 0
	for _, criterion := range contract.Criteria {
		id := strings.TrimSpace(criterion.ID)
		if id == "" {
			return errors.New("criterion id is required")
		}
		if err := validateStableID(id); err != nil {
			return fmt.Errorf("criterion id %q: %w", id, err)
		}
		if criteria[id] {
			return fmt.Errorf("duplicate criterion id %q", id)
		}
		criteria[id] = true
		if strings.TrimSpace(criterion.Statement) == "" {
			return fmt.Errorf("criterion %q statement is required", id)
		}
		if !validVerificationKind(criterion.Verification) {
			return fmt.Errorf("criterion %q has invalid verification %q", id, criterion.Verification)
		}
		if !validEvidencePolicy(criterion.EvidencePolicy) {
			return fmt.Errorf("criterion %q has invalid evidence policy %q", id, criterion.EvidencePolicy)
		}
		if criterion.Mandatory {
			mandatory++
			if criterion.Verification != VerifyHuman && len(criterion.GateIDs) == 0 && criterion.EvidencePolicy == EvidenceExecution {
				return fmt.Errorf("mandatory execution criterion %q requires at least one gate", id)
			}
		}
		for _, gateID := range criterion.GateIDs {
			if _, exists := gates[gateID]; !exists {
				return fmt.Errorf("criterion %q references unknown gate %q", id, gateID)
			}
		}
		for _, path := range criterion.TargetPaths {
			if err := validateRepoPattern(path); err != nil {
				return fmt.Errorf("criterion %q path %q: %w", id, path, err)
			}
		}
	}
	if mandatory == 0 {
		return errors.New("contract requires at least one mandatory criterion")
	}
	return nil
}

func ValidateGraph(graph TaskGraph, contract AcceptanceContract) error {
	if graph.SchemaVersion != 0 && graph.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported graph schema version %d", graph.SchemaVersion)
	}
	if strings.TrimSpace(graph.DeliveryID) == "" {
		return errors.New("graph delivery_id is required")
	}
	if graph.BaseCommit != contract.BaseCommit {
		return fmt.Errorf("graph base_commit %q does not match contract %q", graph.BaseCommit, contract.BaseCommit)
	}
	if len(graph.Nodes) == 0 {
		return errors.New("graph requires at least one node")
	}
	criterionIDs := map[string]bool{}
	for _, criterion := range contract.Criteria {
		criterionIDs[criterion.ID] = true
	}
	nodes := make(map[string]TaskNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			return errors.New("graph node id is required")
		}
		if err := validateStableID(id); err != nil {
			return fmt.Errorf("graph node id %q: %w", id, err)
		}
		if _, exists := nodes[id]; exists {
			return fmt.Errorf("duplicate graph node id %q", id)
		}
		if strings.TrimSpace(node.Title) == "" || strings.TrimSpace(node.Objective) == "" {
			return fmt.Errorf("graph node %q requires title and objective", id)
		}
		if !validRole(node.Role) {
			return fmt.Errorf("graph node %q has invalid role %q", id, node.Role)
		}
		if node.Role != RoleScout && node.Role != RolePlanner && len(node.Criteria) == 0 {
			return fmt.Errorf("graph node %q requires at least one criterion", id)
		}
		for _, criterionID := range node.Criteria {
			if !criterionIDs[criterionID] {
				return fmt.Errorf("graph node %q references unknown criterion %q", id, criterionID)
			}
		}
		if !validRisk(node.Risk) {
			return fmt.Errorf("graph node %q has invalid risk %q", id, node.Risk)
		}
		if node.CandidateCount < 1 || node.CandidateCount > 2 {
			return fmt.Errorf("graph node %q candidate_count must be 1 or 2", id)
		}
		if node.CandidateCount > 1 && node.Risk != RiskHigh && node.Risk != RiskCritical {
			return fmt.Errorf("graph node %q may use multiple candidates only for high risk work", id)
		}
		if node.Budget.MaxTurns < 1 || node.Budget.MaxTokens < 1 || node.Budget.MaxDurationSecond < 1 {
			return fmt.Errorf("graph node %q requires positive turn, token, and duration budgets", id)
		}
		for _, pattern := range append(append([]string(nil), node.ReadSet...), node.DeclaredWrites...) {
			if err := validateRepoPattern(pattern); err != nil {
				return fmt.Errorf("graph node %q path %q: %w", id, pattern, err)
			}
		}
		if (node.Role == RoleBuilder || node.Role == RoleTestBuilder || node.Role == RoleRevisionBuilder) && len(node.DeclaredWrites) == 0 {
			return fmt.Errorf("builder node %q requires declared_writes", id)
		}
		nodes[id] = node
	}
	for _, node := range graph.Nodes {
		for _, dependency := range node.Dependencies {
			if dependency == node.ID {
				return fmt.Errorf("graph node %q cannot depend on itself", node.ID)
			}
			if _, exists := nodes[dependency]; !exists {
				return fmt.Errorf("graph node %q depends on unknown node %q", node.ID, dependency)
			}
		}
	}
	if _, err := TopologicalOrder(graph); err != nil {
		return err
	}
	if err := validateWriteSetOrdering(graph); err != nil {
		return err
	}
	return nil
}

func TopologicalOrder(graph TaskGraph) ([]string, error) {
	inDegree := map[string]int{}
	next := map[string][]string{}
	for _, node := range graph.Nodes {
		inDegree[node.ID] = len(node.Dependencies)
		for _, dependency := range node.Dependencies {
			next[dependency] = append(next[dependency], node.ID)
		}
	}
	var ready []string
	for id, degree := range inDegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, candidate := range next[id] {
			inDegree[candidate]--
			if inDegree[candidate] == 0 {
				ready = append(ready, candidate)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(graph.Nodes) {
		return nil, errors.New("delivery graph contains a dependency cycle")
	}
	return order, nil
}

func ContentHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func HashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateWriteSetOrdering(graph TaskGraph) error {
	reachable := dependencyClosure(graph)
	for leftIndex := range graph.Nodes {
		left := graph.Nodes[leftIndex]
		if len(left.DeclaredWrites) == 0 {
			continue
		}
		for rightIndex := leftIndex + 1; rightIndex < len(graph.Nodes); rightIndex++ {
			right := graph.Nodes[rightIndex]
			if len(right.DeclaredWrites) == 0 || !writeSetsOverlap(left.DeclaredWrites, right.DeclaredWrites) {
				continue
			}
			if !reachable[left.ID][right.ID] && !reachable[right.ID][left.ID] {
				return fmt.Errorf("graph nodes %q and %q have overlapping declared_writes but no dependency ordering", left.ID, right.ID)
			}
		}
	}
	return nil
}

func dependencyClosure(graph TaskGraph) map[string]map[string]bool {
	closure := map[string]map[string]bool{}
	for _, node := range graph.Nodes {
		closure[node.ID] = map[string]bool{}
	}
	var visit func(string, string)
	visit = func(origin string, current string) {
		if closure[origin][current] {
			return
		}
		closure[origin][current] = true
		for _, node := range graph.Nodes {
			for _, dependency := range node.Dependencies {
				if dependency == current {
					visit(origin, node.ID)
				}
			}
		}
	}
	for _, node := range graph.Nodes {
		visit(node.ID, node.ID)
	}
	return closure
}

func writeSetsOverlap(left []string, right []string) bool {
	for _, leftPattern := range left {
		for _, rightPattern := range right {
			if patternsMayOverlap(leftPattern, rightPattern) {
				return true
			}
		}
	}
	return false
}

func patternsMayOverlap(left string, right string) bool {
	leftPrefix := fixedPatternPrefix(left)
	rightPrefix := fixedPatternPrefix(right)
	if leftPrefix == "." || rightPrefix == "." {
		return true
	}
	return leftPrefix == rightPrefix ||
		strings.HasPrefix(leftPrefix, rightPrefix+"/") ||
		strings.HasPrefix(rightPrefix, leftPrefix+"/")
}

func fixedPatternPrefix(pattern string) string {
	pattern = filepath.ToSlash(filepath.Clean(strings.TrimSpace(pattern)))
	if index := strings.IndexAny(pattern, "*?["); index >= 0 {
		pattern = strings.TrimSuffix(pattern[:index], "/")
	}
	if pattern == "" {
		return "."
	}
	return pattern
}

func validateRepoPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("path pattern is empty")
	}
	if filepath.IsAbs(pattern) {
		return errors.New("absolute paths are forbidden")
	}
	clean := filepath.ToSlash(filepath.Clean(pattern))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("path escapes project root")
	}
	return nil
}

func validateStableID(id string) error {
	if len(id) > 96 {
		return errors.New("id exceeds 96 characters")
	}
	for _, char := range id {
		if char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.' {
			continue
		}
		return errors.New("id may contain only letters, digits, dot, dash, and underscore")
	}
	return nil
}

func validRole(role AgentRole) bool {
	switch role {
	case RoleScout, RolePlanner, RoleBuilder, RoleTestBuilder, RoleIntegrator,
		RoleSpecVerifier, RoleCorrectnessVerifier, RoleSecurityVerifier,
		RolePerformanceVerifier, RoleCompatibilityVerifier, RoleRevisionBuilder:
		return true
	default:
		return false
	}
}

func validRisk(risk RiskLevel) bool {
	switch risk {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func validVerificationKind(kind VerificationKind) bool {
	switch kind {
	case VerifyCommand, VerifyFileAssertion, VerifyAPIContract, VerifyBehavioralEval, VerifyRubric, VerifyHuman:
		return true
	default:
		return false
	}
}

func validEvidencePolicy(policy EvidencePolicy) bool {
	switch policy {
	case EvidenceExecution, EvidenceStatic, EvidenceSemantic, EvidenceHuman:
		return true
	default:
		return false
	}
}
