package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"cohort/internal/worktree"
)

const maxGateOutputBytes = 8 << 20

type GateRunner struct {
	Store Store
}

func (r GateRunner) Run(ctx context.Context, item Delivery, contract AcceptanceContract, gate GateSpec, manager worktree.Manager, spec worktree.Spec, commit string, treeHash string) (GateResult, error) {
	if err := ValidateGateCommand(gate); err != nil {
		return GateResult{}, err
	}
	before, err := manager.Status(ctx, spec)
	if err != nil {
		return GateResult{}, err
	}
	if before != "" {
		return GateResult{}, fmt.Errorf("integration worktree is dirty before gate %q: %s", gate.ID, before)
	}
	timeout := time.Duration(gate.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, gate.Command[0], gate.Command[1:]...)
	command.Dir = spec.Path
	var output cappedBuffer
	output.limit = maxGateOutputBytes
	command.Stdout = &output
	command.Stderr = &output
	startedAt := time.Now().UTC()
	runErr := command.Run()
	finishedAt := time.Now().UTC()
	exitCode := 0
	if runErr != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	if runCtx.Err() != nil {
		runErr = runCtx.Err()
	}
	after, statusErr := manager.Status(ctx, spec)
	if statusErr != nil {
		return GateResult{}, statusErr
	}
	if after != "" {
		if runErr == nil {
			runErr = fmt.Errorf("gate mutated tracked or untracked repository state: %s", after)
		}
	}
	payload := output.Bytes()
	if output.truncated {
		payload = append(payload, []byte("\n...[gate output truncated]...\n")...)
	}
	artifact, err := r.Store.PublishArtifact(item.ID, ArtifactMeta{
		Kind:       "gate_output",
		Producer:   "gate:" + gate.ID,
		BaseCommit: item.BaseCommit,
		TreeHash:   treeHash,
		MediaType:  "text/plain",
	}, payload)
	if err != nil {
		return GateResult{}, err
	}
	environmentHash, err := EnvironmentHash(spec.Path)
	if err != nil {
		return GateResult{}, err
	}
	commandHash, err := GateCommandHash(gate)
	if err != nil {
		return GateResult{}, err
	}
	status := EvidencePassed
	errorText := ""
	if runErr != nil {
		status = EvidenceFailed
		errorText = runErr.Error()
	}
	evidence := EvidenceEnvelope{
		SchemaVersion:   SchemaVersion,
		ID:              newEvidenceID(gate.ID, treeHash),
		DeliveryID:      item.ID,
		CriterionIDs:    CriteriaForGate(contract, gate.ID),
		GateID:          gate.ID,
		Producer:        "deterministic_gate",
		ContractHash:    item.ContractHash,
		BaseCommit:      item.BaseCommit,
		CandidateCommit: commit,
		TreeHash:        treeHash,
		CommandHash:     commandHash,
		EnvironmentHash: environmentHash,
		ExitCode:        exitCode,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		ArtifactHash:    artifact.ID,
		Status:          status,
		Error:           errorText,
	}
	if err := r.Store.SaveEvidence(item.ID, evidence); err != nil {
		return GateResult{}, err
	}
	result := GateResult{
		Gate:       gate,
		Evidence:   evidence,
		Output:     previewGateOutput(string(payload), 4000),
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
	}
	if runErr != nil {
		return result, fmt.Errorf("gate %s failed: %w", gate.ID, runErr)
	}
	return result, nil
}

func ValidateGateCommand(gate GateSpec) error {
	if gate.Kind != "command" {
		return fmt.Errorf("unsupported deterministic gate kind %q", gate.Kind)
	}
	if len(gate.Command) == 0 || strings.TrimSpace(gate.Command[0]) == "" {
		return errors.New("gate command argv is required")
	}
	executable := strings.TrimSpace(gate.Command[0])
	allowed := map[string]bool{
		"go": true, "npm": true, "pnpm": true, "yarn": true,
		"cargo": true, "rustc": true,
		"python": true, "python3": true, "pytest": true,
		"java": true, "mvn": true, "./mvnw": true,
		"gradle": true, "./gradlew": true,
		"swift": true, "xcodebuild": true,
		"cmake": true, "ctest": true, "make": true,
	}
	if !allowed[executable] {
		return fmt.Errorf("gate executable %q is not allowlisted", executable)
	}
	for _, arg := range gate.Command[1:] {
		if strings.ContainsRune(arg, '\x00') || strings.ContainsAny(arg, "\r\n") {
			return errors.New("gate arguments may not contain NUL or newlines")
		}
	}
	return nil
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(data) > remaining {
		_, _ = b.buffer.Write(data[:remaining])
		b.truncated = true
		return original, nil
	}
	_, _ = b.buffer.Write(data)
	return original, nil
}

func (b *cappedBuffer) Bytes() []byte {
	return append([]byte(nil), b.buffer.Bytes()...)
}

func previewGateOutput(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxChars <= 0 || len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars]) + "...[truncated]"
}
