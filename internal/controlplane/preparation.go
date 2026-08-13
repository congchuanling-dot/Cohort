package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const preparationTTL = 5 * time.Minute

type EntityBinding struct {
	Field   string     `json:"field"`
	Kind    EntityKind `json:"kind"`
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Status  string     `json:"status,omitempty"`
	Version string     `json:"version"`
}

type ActionPreparation struct {
	Token            string          `json:"preparation_token"`
	ActionID         string          `json:"action_id"`
	Input            map[string]any  `json:"resolved_input"`
	Entities         []EntityBinding `json:"entities,omitempty"`
	Impact           string          `json:"impact"`
	ConfirmationText string          `json:"confirmation_text,omitempty"`
	ExpiresAt        time.Time       `json:"expires_at"`
}

type preparationRecord struct {
	preparation ActionPreparation
	request     ActionRequest
}

type PreparationManager struct {
	catalog  *Catalog
	entities EntityProvider
	now      func() time.Time

	mu      sync.Mutex
	records map[string]preparationRecord
}

func NewPreparationManager(catalog *Catalog, entities EntityProvider) *PreparationManager {
	return &PreparationManager{
		catalog: catalog, entities: entities,
		now:     func() time.Time { return time.Now().UTC() },
		records: map[string]preparationRecord{},
	}
}

func (m *PreparationManager) Prepare(ctx context.Context, actionID string, request ActionRequest) (ActionPreparation, error) {
	if m == nil || m.catalog == nil {
		return ActionPreparation{}, errors.New("action preparation is unavailable")
	}
	spec, normalized, err := m.catalog.PrepareRequest(actionID, request)
	if err != nil {
		return ActionPreparation{}, err
	}
	var bindings []EntityBinding
	for _, field := range spec.Inputs {
		if field.Type != FieldEntity {
			continue
		}
		if m.entities == nil {
			return ActionPreparation{}, errors.New("entity provider is unavailable")
		}
		raw, exists := normalized.Input[field.Name]
		if !exists {
			continue
		}
		id, _ := raw.(string)
		entity, loadErr := m.entities.GetEntity(ctx, field.Entity.Kind, id)
		if loadErr != nil {
			if field.Entity.AllowMissing {
				continue
			}
			return ActionPreparation{}, fmt.Errorf("resolve input %q: %w", field.Name, loadErr)
		}
		if len(field.Entity.Status) > 0 && !containsString(field.Entity.Status, entity.Status) {
			return ActionPreparation{}, fmt.Errorf(
				"input %q entity %q has status %q, want one of %s",
				field.Name, entity.ID, entity.Status, strings.Join(field.Entity.Status, ", "),
			)
		}
		for relation, sourceField := range field.Entity.DependsOn {
			parentValue := strings.TrimSpace(fmt.Sprint(normalized.Input[sourceField]))
			if parentValue == "" || entity.Relations[relation] != parentValue {
				return ActionPreparation{}, fmt.Errorf(
					"input %q entity %q does not belong to selected %s %q",
					field.Name, entity.ID, sourceField, parentValue,
				)
			}
		}
		normalized.Input[field.Name] = entity.ID
		bindings = append(bindings, EntityBinding{
			Field: field.Name, Kind: entity.Kind, ID: entity.ID, Title: entity.Title,
			Status: entity.Status, Version: entity.Version,
		})
	}
	now := m.now()
	preparation := ActionPreparation{
		Token: randomToken(24), ActionID: actionID, Input: normalized.Input,
		Entities: bindings, Impact: preparationImpact(spec, bindings),
		ConfirmationText: spec.ConfirmationText, ExpiresAt: now.Add(preparationTTL),
	}
	m.mu.Lock()
	m.removeExpiredLocked(now)
	m.records[preparation.Token] = preparationRecord{preparation: preparation, request: normalized}
	m.mu.Unlock()
	return preparation, nil
}

func (m *PreparationManager) Consume(ctx context.Context, token string, actionID string) (ActionRequest, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ActionRequest{}, errors.New("preparation_token is required")
	}
	m.mu.Lock()
	record, exists := m.records[token]
	if exists {
		delete(m.records, token)
	}
	m.mu.Unlock()
	if !exists || record.preparation.ActionID != actionID {
		return ActionRequest{}, errors.New("invalid or expired preparation_token")
	}
	if !m.now().Before(record.preparation.ExpiresAt) {
		return ActionRequest{}, errors.New("preparation_token expired")
	}
	for _, binding := range record.preparation.Entities {
		entity, err := m.entities.GetEntity(ctx, binding.Kind, binding.ID)
		if err != nil {
			return ActionRequest{}, fmt.Errorf("prepared entity %q is unavailable: %w", binding.ID, err)
		}
		if entity.Version != binding.Version {
			return ActionRequest{}, fmt.Errorf("prepared entity %q changed; prepare the action again", binding.ID)
		}
	}
	return record.request, nil
}

func (m *PreparationManager) removeExpiredLocked(now time.Time) {
	for token, record := range m.records {
		if !now.Before(record.preparation.ExpiresAt) {
			delete(m.records, token)
		}
	}
}

func ActionRequiresPreparation(spec ActionSpec) bool {
	if spec.Risk == RiskConfirm || spec.Risk == RiskDanger {
		return true
	}
	for _, field := range spec.Inputs {
		if field.Type == FieldEntity {
			return true
		}
	}
	return false
}

func preparationImpact(spec ActionSpec, bindings []EntityBinding) string {
	if len(bindings) == 0 {
		return spec.Description
	}
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		label := binding.Title
		if label == "" {
			label = binding.ID
		}
		if binding.Status != "" {
			label += " (" + binding.Status + ")"
		}
		parts = append(parts, label)
	}
	return spec.Label + ": " + strings.Join(parts, ", ")
}
