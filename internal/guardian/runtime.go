package guardian

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	LineageDirName  = "guardian"
	LineageFileName = "lineage.jsonl"
)

type Runtime struct {
	Policy     Policy
	PolicyHash string
}

type Trajectory struct {
	mu        sync.Mutex
	runtime   *Runtime
	runID     string
	sessionID string
	path      string
	label     Label
	lastEvent string
	events    []ProvenanceEvent
}

type Summary struct {
	RunID       string `json:"run_id"`
	SessionID   string `json:"session_id"`
	PolicyHash  string `json:"policy_hash"`
	EventCount  int    `json:"event_count"`
	DeniedCount int    `json:"denied_count"`
	FinalLabel  Label  `json:"final_label"`
	LineagePath string `json:"lineage_path,omitempty"`
}

func NewRuntime(projectRoot string) (*Runtime, error) {
	policy, hash, err := LoadPolicy(projectRoot)
	if err != nil {
		return nil, err
	}
	return &Runtime{Policy: policy, PolicyHash: hash}, nil
}

func (r *Runtime) Begin(runID, sessionID, sessionDir string) (*Trajectory, error) {
	if r == nil {
		return nil, errors.New("guardian runtime is nil")
	}
	runID = strings.TrimSpace(runID)
	sessionID = strings.TrimSpace(sessionID)
	if runID == "" {
		return nil, errors.New("guardian run id is required")
	}
	path := ""
	if strings.TrimSpace(sessionDir) != "" {
		dir := filepath.Join(filepath.Clean(sessionDir), LineageDirName, runID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		path = filepath.Join(dir, LineageFileName)
	}
	return &Trajectory{
		runtime: r, runID: runID, sessionID: sessionID, path: path,
		label: InitialLabel(),
	}, nil
}

func (t *Trajectory) Before(turn, index int, toolCallID, tool string, args map[string]any) (Decision, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	contract := resolveContract(t.runtime.Policy, tool, args)
	policy := t.runtime.Policy
	policy.Contracts = cloneContracts(policy.Contracts)
	policy.Contracts[tool] = contract
	decision := Decide(policy, t.label, tool)
	// The proof binds to the project policy and the runtime-refined contract
	// independently. Refined path sensitivity must not rewrite PolicyHash.
	decision.PolicyHash = t.runtime.PolicyHash
	decision.ContractHash = ContractHash(contract)
	decision.OutputLabel = Fold(t.label, OutputLabel(contract))
	if err := t.appendLocked(ProvenanceEvent{
		SchemaVersion: SchemaVersion,
		ID:            nextEventID(),
		Time:          time.Now().UTC(),
		RunID:         t.runID,
		SessionID:     t.sessionID,
		Turn:          turn,
		Index:         index,
		Tool:          tool,
		ToolCallID:    toolCallID,
		Phase:         "pre_dispatch",
		Decision:      decision,
		ArgsHash:      HashValue(args),
		ParentIDs:     parentIDs(t.lastEvent),
	}); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (t *Trajectory) After(turn, index int, toolCallID, tool string, args map[string]any, decision Decision, result any, executed bool) (Label, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if executed {
		contract := resolveContract(t.runtime.Policy, tool, args)
		t.label = Fold(t.label, OutputLabel(contract))
	}
	phase := "post_dispatch"
	if !executed {
		phase = "blocked"
	}
	decision.OutputLabel = t.label
	if err := t.appendLocked(ProvenanceEvent{
		SchemaVersion: SchemaVersion,
		ID:            nextEventID(),
		Time:          time.Now().UTC(),
		RunID:         t.runID,
		SessionID:     t.sessionID,
		Turn:          turn,
		Index:         index,
		Tool:          tool,
		ToolCallID:    toolCallID,
		Phase:         phase,
		Decision:      decision,
		ArgsHash:      HashValue(args),
		ResultHash:    HashValue(result),
		ParentIDs:     parentIDs(t.lastEvent),
	}); err != nil {
		return Label{}, err
	}
	return t.label, nil
}

func (t *Trajectory) Label() Label {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.label
}

func (t *Trajectory) Events() []ProvenanceEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]ProvenanceEvent(nil), t.events...)
}

func (t *Trajectory) Summary() Summary {
	t.mu.Lock()
	defer t.mu.Unlock()
	summary := Summary{
		RunID: t.runID, SessionID: t.sessionID, PolicyHash: t.runtime.PolicyHash,
		EventCount: len(t.events), FinalLabel: t.label, LineagePath: t.path,
	}
	for _, event := range t.events {
		if event.Phase == "blocked" {
			summary.DeniedCount++
		}
	}
	return summary
}

func (t *Trajectory) appendLocked(event ProvenanceEvent) error {
	if t.path == "" {
		t.events = append(t.events, event)
		t.lastEvent = event.ID
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(t.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	t.events = append(t.events, event)
	t.lastEvent = event.ID
	return nil
}

func LoadLineage(sessionRoot, sessionID, runID string) ([]ProvenanceEvent, error) {
	if !safeID(sessionID) || !safeID(runID) {
		return nil, errors.New("invalid guardian lineage id")
	}
	path := filepath.Join(sessionRoot, sessionID, LineageDirName, runID, LineageFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []ProvenanceEvent
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event ProvenanceEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode guardian lineage line %d: %w", lineNumber+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func resolveContract(policy Policy, tool string, args map[string]any) ToolContract {
	contract := Contract(policy, tool)
	if tool == "file_read" && sensitivePath(fmt.Sprint(args["path"])) {
		contract.OutputConfidentiality = ConfidentialitySecret
		contract.Readers = []string{"local"}
	}
	return contract
}

func sensitivePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	for _, marker := range []string{
		".env", ".pem", ".key", "id_rsa", "id_ed25519", "credentials",
		"secret", "token", ".aws/", ".ssh/", ".kube/config",
	} {
		if strings.Contains(path, marker) {
			return true
		}
	}
	return false
}

func cloneContracts(input map[string]ToolContract) map[string]ToolContract {
	result := make(map[string]ToolContract, len(input)+1)
	for name, contract := range input {
		result[name] = contract
	}
	return result
}

func parentIDs(last string) []string {
	if last == "" {
		return nil
	}
	return []string{last}
}

func safeID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\`) && !strings.Contains(value, "..")
}

var eventSequence atomic.Uint64

func nextEventID() string {
	return fmt.Sprintf("guard_%d_%d", time.Now().UTC().UnixNano(), eventSequence.Add(1))
}
