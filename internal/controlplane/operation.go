package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
	OperationCancelled OperationStatus = "cancelled"
)

type Operation struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	ActionID      string          `json:"action_id"`
	ProjectRoot   string          `json:"project_root"`
	Actor         string          `json:"actor"`
	Status        OperationStatus `json:"status"`
	Input         map[string]any  `json:"input,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	Result        any             `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	FinishedAt    time.Time       `json:"finished_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type OperationEvent struct {
	Type      string    `json:"type"`
	Operation Operation `json:"operation"`
	Time      time.Time `json:"time"`
}

type OperationManager struct {
	root        string
	catalog     *Catalog
	mu          sync.Mutex
	running     map[string]context.CancelFunc
	subscribers map[int]chan OperationEvent
	nextSubID   int
	now         func() time.Time
}

func NewOperationManager(projectRoot string, catalog *Catalog) (*OperationManager, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return nil, errors.New("operation project root is required")
	}
	absolute, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	manager := &OperationManager{
		root:        filepath.Join(filepath.Clean(absolute), ".cohort", "control", "operations"),
		catalog:     catalog,
		running:     map[string]context.CancelFunc{},
		subscribers: map[int]chan OperationEvent{},
		now:         func() time.Time { return time.Now().UTC() },
	}
	if err := os.MkdirAll(manager.root, 0755); err != nil {
		return nil, err
	}
	if err := manager.recoverInterrupted(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *OperationManager) Start(ctx context.Context, actionID string, request ActionRequest) (Operation, error) {
	if m == nil || m.catalog == nil {
		return Operation{}, errors.New("operation manager has no action catalog")
	}
	spec, request, err := m.catalog.ValidateRequest(actionID, request)
	if err != nil {
		return Operation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	now := m.now()
	operation := Operation{
		SchemaVersion: 1,
		ID:            newOperationID(now),
		ActionID:      spec.ID,
		ProjectRoot:   request.ProjectRoot,
		Actor:         firstNonEmpty(request.Actor, "local-user"),
		Status:        OperationPending,
		Input:         redactOperationInput(spec, request.Input),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := m.save(operation); err != nil {
		return Operation{}, err
	}
	m.publish("operation.created", operation)

	runCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.running[operation.ID] = cancel
	m.mu.Unlock()
	go m.run(runCtx, operation, request)
	return operation, nil
}

func (m *OperationManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.running))
	for _, cancel := range m.running {
		cancels = append(cancels, cancel)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *OperationManager) run(ctx context.Context, operation Operation, request ActionRequest) {
	operation.Status = OperationRunning
	operation.StartedAt = m.now()
	operation.UpdatedAt = operation.StartedAt
	_ = m.save(operation)
	m.publish("operation.running", operation)

	result, runErr := m.catalog.Execute(ctx, operation.ActionID, request)
	finishedAt := m.now()
	operation.FinishedAt = finishedAt
	operation.UpdatedAt = finishedAt
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		operation.Status = OperationCancelled
		operation.Error = "operation cancelled"
	case runErr != nil:
		operation.Status = OperationFailed
		operation.Error = runErr.Error()
	default:
		operation.Status = OperationSucceeded
		operation.Summary = result.Summary
		operation.Result = result.Data
	}
	_ = m.save(operation)
	m.mu.Lock()
	delete(m.running, operation.ID)
	m.mu.Unlock()
	m.publish("operation."+string(operation.Status), operation)
}

func (m *OperationManager) Cancel(id string) (Operation, error) {
	operation, err := m.Load(id)
	if err != nil {
		return Operation{}, err
	}
	if operation.Status != OperationPending && operation.Status != OperationRunning {
		return Operation{}, fmt.Errorf("operation %q is already %s", id, operation.Status)
	}
	m.mu.Lock()
	cancel := m.running[id]
	m.mu.Unlock()
	if cancel == nil {
		return Operation{}, fmt.Errorf("operation %q is not owned by this process", id)
	}
	cancel()
	return operation, nil
}

func (m *OperationManager) Load(id string) (Operation, error) {
	if err := validateOperationID(id); err != nil {
		return Operation{}, err
	}
	var operation Operation
	data, err := os.ReadFile(filepath.Join(m.root, id+".json"))
	if err != nil {
		return Operation{}, err
	}
	if err := json.Unmarshal(data, &operation); err != nil {
		return Operation{}, err
	}
	if operation.ID != id || operation.SchemaVersion != 1 {
		return Operation{}, errors.New("operation identity or schema mismatch")
	}
	return operation, nil
}

func (m *OperationManager) List(limit int) ([]Operation, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return nil, err
	}
	operations := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		operation, loadErr := m.Load(id)
		if loadErr != nil {
			return nil, loadErr
		}
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].CreatedAt.After(operations[right].CreatedAt)
	})
	if limit > 0 && len(operations) > limit {
		operations = operations[:limit]
	}
	return operations, nil
}

func (m *OperationManager) Subscribe(buffer int) (<-chan OperationEvent, func()) {
	if buffer <= 0 {
		buffer = 32
	}
	channel := make(chan OperationEvent, buffer)
	m.mu.Lock()
	id := m.nextSubID
	m.nextSubID++
	m.subscribers[id] = channel
	m.mu.Unlock()
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			m.mu.Lock()
			if existing, ok := m.subscribers[id]; ok {
				delete(m.subscribers, id)
				close(existing)
			}
			m.mu.Unlock()
		})
	}
}

func (m *OperationManager) publish(eventType string, operation Operation) {
	event := OperationEvent{Type: eventType, Operation: operation, Time: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, subscriber := range m.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (m *OperationManager) recoverInterrupted() error {
	operations, err := m.List(0)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.Status != OperationPending && operation.Status != OperationRunning {
			continue
		}
		operation.Status = OperationFailed
		operation.Error = "control plane restarted while operation was running"
		operation.FinishedAt = m.now()
		operation.UpdatedAt = operation.FinishedAt
		if err := m.save(operation); err != nil {
			return err
		}
	}
	return nil
}

func (m *OperationManager) save(operation Operation) error {
	if err := validateOperationID(operation.ID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(operation, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(m.root, ".operation-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, filepath.Join(m.root, operation.ID+".json"))
}

func redactOperationInput(spec ActionSpec, input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	sensitive := map[string]bool{}
	for _, field := range spec.Inputs {
		sensitive[field.Name] = field.Sensitive || field.Type == FieldSecret
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitive[key] {
			result[key] = "[REDACTED]"
		} else {
			result[key] = value
		}
	}
	return result
}

func validateOperationID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return errors.New("invalid operation id")
	}
	return nil
}

func newOperationID(now time.Time) string {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("op_%d", now.UnixNano())
	}
	return fmt.Sprintf("op_%d_%s", now.UnixMilli(), hex.EncodeToString(random[:]))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
