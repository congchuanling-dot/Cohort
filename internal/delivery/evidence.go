package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type EvidenceStatus string

const (
	EvidencePassed EvidenceStatus = "passed"
	EvidenceFailed EvidenceStatus = "failed"
	EvidenceStale  EvidenceStatus = "stale"
)

type EvidenceEnvelope struct {
	SchemaVersion   int            `json:"schema_version"`
	ID              string         `json:"id"`
	DeliveryID      string         `json:"delivery_id"`
	CriterionIDs    []string       `json:"criterion_ids"`
	GateID          string         `json:"gate_id"`
	Producer        string         `json:"producer"`
	ContractHash    string         `json:"contract_hash"`
	BaseCommit      string         `json:"base_commit"`
	CandidateCommit string         `json:"candidate_commit"`
	TreeHash        string         `json:"tree_hash"`
	CommandHash     string         `json:"command_hash"`
	EnvironmentHash string         `json:"environment_hash"`
	ExitCode        int            `json:"exit_code"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	ArtifactHash    string         `json:"artifact_hash"`
	Status          EvidenceStatus `json:"status"`
	Error           string         `json:"error,omitempty"`
}

type GateResult struct {
	Gate       GateSpec         `json:"gate"`
	Evidence   EvidenceEnvelope `json:"evidence"`
	Output     string           `json:"output,omitempty"`
	DurationMS int64            `json:"duration_ms"`
}

func (s Store) SaveEvidence(deliveryID string, evidence EvidenceEnvelope) error {
	if err := validateStableID(evidence.ID); err != nil {
		return err
	}
	if evidence.DeliveryID != deliveryID {
		return errors.New("evidence delivery identity mismatch")
	}
	path := filepath.Join(s.deliveryDir(deliveryID), "evidence", evidence.ID+".json")
	return s.writeJSON(path, evidence)
}

func (s Store) LoadEvidence(deliveryID string, evidenceID string) (EvidenceEnvelope, error) {
	if err := validateStableID(evidenceID); err != nil {
		return EvidenceEnvelope{}, err
	}
	var evidence EvidenceEnvelope
	path := filepath.Join(s.deliveryDir(deliveryID), "evidence", evidenceID+".json")
	if err := readJSON(path, &evidence); err != nil {
		return EvidenceEnvelope{}, err
	}
	return evidence, nil
}

func VerifyEvidenceFreshness(evidence EvidenceEnvelope, item Delivery, treeHash string, gate GateSpec, environmentHash string) error {
	if evidence.DeliveryID != item.ID {
		return errors.New("evidence belongs to another delivery")
	}
	if evidence.ContractHash != item.ContractHash {
		return errors.New("evidence contract hash is stale")
	}
	if evidence.BaseCommit != item.BaseCommit {
		return errors.New("evidence base commit is stale")
	}
	if evidence.TreeHash != treeHash {
		return errors.New("evidence tree hash is stale")
	}
	commandHash, err := GateCommandHash(gate)
	if err != nil {
		return err
	}
	if evidence.CommandHash != commandHash {
		return errors.New("evidence command hash is stale")
	}
	if evidence.EnvironmentHash != environmentHash {
		return errors.New("evidence environment hash is stale")
	}
	if evidence.ArtifactHash == "" {
		return errors.New("evidence output artifact is missing")
	}
	if evidence.Status != EvidencePassed {
		return fmt.Errorf("evidence status is %s", evidence.Status)
	}
	return nil
}

func GateCommandHash(gate GateSpec) (string, error) {
	payload := struct {
		Kind           string   `json:"kind"`
		Command        []string `json:"command"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}{
		Kind:           gate.Kind,
		Command:        gate.Command,
		TimeoutSeconds: gate.TimeoutSeconds,
	}
	return ContentHash(payload)
}

func EnvironmentHash(projectRoot string) (string, error) {
	type environment struct {
		OS        string            `json:"os"`
		Arch      string            `json:"arch"`
		GoVersion string            `json:"go_version"`
		Files     map[string]string `json:"files"`
	}
	state := environment{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Files:     map[string]string{},
	}
	for _, name := range []string{
		"go.mod", "go.sum",
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"Cargo.toml", "Cargo.lock",
		"pyproject.toml", "poetry.lock", "requirements.txt",
		"pom.xml", "build.gradle", "build.gradle.kts",
	} {
		data, err := os.ReadFile(filepath.Join(projectRoot, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		state.Files[name] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return ContentHash(state)
}

func CriteriaForGate(contract AcceptanceContract, gateID string) []string {
	var ids []string
	for _, criterion := range contract.Criteria {
		for _, candidate := range criterion.GateIDs {
			if candidate == gateID {
				ids = append(ids, criterion.ID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func ValidateMandatoryEvidence(contract AcceptanceContract, evidence []EvidenceEnvelope) error {
	passed := map[string]bool{}
	for _, item := range evidence {
		if item.Status != EvidencePassed {
			continue
		}
		for _, criterionID := range item.CriterionIDs {
			passed[criterionID] = true
		}
	}
	var missing []string
	for _, criterion := range contract.Criteria {
		if !criterion.Mandatory || criterion.EvidencePolicy != EvidenceExecution {
			continue
		}
		if !passed[criterion.ID] {
			missing = append(missing, criterion.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("mandatory criteria lack passing evidence: %s", strings.Join(missing, ", "))
	}
	return nil
}

func newEvidenceID(gateID string, treeHash string) string {
	payload, _ := json.Marshal([]string{gateID, treeHash, time.Now().UTC().Format(time.RFC3339Nano)})
	sum := sha256.Sum256(payload)
	return "evidence_" + slug(gateID) + "_" + hex.EncodeToString(sum[:6])
}
