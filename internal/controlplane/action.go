package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type RiskLevel string

const (
	RiskRead    RiskLevel = "read"
	RiskExecute RiskLevel = "execute"
	RiskConfirm RiskLevel = "confirm"
	RiskDanger  RiskLevel = "danger"
)

type FieldType string

const (
	FieldString   FieldType = "string"
	FieldText     FieldType = "text"
	FieldBoolean  FieldType = "boolean"
	FieldInteger  FieldType = "integer"
	FieldSelect   FieldType = "select"
	FieldPath     FieldType = "path"
	FieldSecret   FieldType = "secret"
	FieldDuration FieldType = "duration"
)

type InputField struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required,omitempty"`
	Default     any       `json:"default,omitempty"`
	Options     []string  `json:"options,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Sensitive   bool      `json:"sensitive,omitempty"`
}

type ActionSpec struct {
	ID               string        `json:"id"`
	Category         string        `json:"category"`
	Label            string        `json:"label"`
	Description      string        `json:"description"`
	Keywords         []string      `json:"keywords,omitempty"`
	Risk             RiskLevel     `json:"risk"`
	Async            bool          `json:"async,omitempty"`
	ConfirmationText string        `json:"confirmation_text,omitempty"`
	Inputs           []InputField  `json:"inputs,omitempty"`
	Handler          ActionHandler `json:"-"`
}

type ActionRequest struct {
	ProjectRoot  string         `json:"project_root"`
	Actor        string         `json:"actor"`
	Input        map[string]any `json:"input,omitempty"`
	Confirmation string         `json:"confirmation,omitempty"`
}

type ActionResult struct {
	Summary string `json:"summary"`
	Data    any    `json:"data,omitempty"`
}

type ActionHandler func(context.Context, ActionRequest) (ActionResult, error)

type Catalog struct {
	actions map[string]ActionSpec
}

func NewCatalog(specs ...ActionSpec) (*Catalog, error) {
	catalog := &Catalog{actions: make(map[string]ActionSpec, len(specs))}
	for _, spec := range specs {
		if err := catalog.Register(spec); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func (c *Catalog) Register(spec ActionSpec) error {
	if c == nil {
		return errors.New("action catalog is nil")
	}
	spec = normalizeActionSpec(spec)
	if err := validateActionSpec(spec); err != nil {
		return err
	}
	if _, exists := c.actions[spec.ID]; exists {
		return fmt.Errorf("action %q is already registered", spec.ID)
	}
	c.actions[spec.ID] = spec
	return nil
}

func (c *Catalog) List() []ActionSpec {
	if c == nil {
		return nil
	}
	result := make([]ActionSpec, 0, len(c.actions))
	for _, spec := range c.actions {
		spec.Handler = nil
		result = append(result, spec)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Category == result[right].Category {
			return result[left].ID < result[right].ID
		}
		return result[left].Category < result[right].Category
	})
	return result
}

func (c *Catalog) Get(id string) (ActionSpec, bool) {
	if c == nil {
		return ActionSpec{}, false
	}
	spec, exists := c.actions[strings.TrimSpace(id)]
	return spec, exists
}

func (c *Catalog) Execute(ctx context.Context, id string, request ActionRequest) (ActionResult, error) {
	spec, request, err := c.ValidateRequest(id, request)
	if err != nil {
		return ActionResult{}, err
	}
	return spec.Handler(ctx, request)
}

func (c *Catalog) ValidateRequest(id string, request ActionRequest) (ActionSpec, ActionRequest, error) {
	spec, exists := c.Get(id)
	if !exists {
		return ActionSpec{}, ActionRequest{}, fmt.Errorf("unknown action %q", id)
	}
	if spec.Handler == nil {
		return ActionSpec{}, ActionRequest{}, fmt.Errorf("action %q is unavailable", id)
	}
	request.ProjectRoot = strings.TrimSpace(request.ProjectRoot)
	request.Actor = strings.TrimSpace(request.Actor)
	if request.ProjectRoot == "" {
		return ActionSpec{}, ActionRequest{}, errors.New("project_root is required")
	}
	if request.Actor == "" {
		request.Actor = "local-user"
	}
	normalized, err := validateActionInput(spec.Inputs, request.Input)
	if err != nil {
		return ActionSpec{}, ActionRequest{}, err
	}
	request.Input = normalized
	if spec.Risk == RiskConfirm || spec.Risk == RiskDanger {
		if request.Confirmation != spec.ConfirmationText {
			return ActionSpec{}, ActionRequest{}, fmt.Errorf("action %q requires exact confirmation %q", id, spec.ConfirmationText)
		}
	}
	return spec, request, nil
}

func normalizeActionSpec(spec ActionSpec) ActionSpec {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Category = strings.TrimSpace(spec.Category)
	spec.Label = strings.TrimSpace(spec.Label)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.ConfirmationText = strings.TrimSpace(spec.ConfirmationText)
	for index := range spec.Keywords {
		spec.Keywords[index] = strings.TrimSpace(spec.Keywords[index])
	}
	for index := range spec.Inputs {
		field := &spec.Inputs[index]
		field.Name = strings.TrimSpace(field.Name)
		field.Label = strings.TrimSpace(field.Label)
		field.Description = strings.TrimSpace(field.Description)
		field.Placeholder = strings.TrimSpace(field.Placeholder)
		for optionIndex := range field.Options {
			field.Options[optionIndex] = strings.TrimSpace(field.Options[optionIndex])
		}
		if field.Type == FieldSecret {
			field.Sensitive = true
		}
	}
	if spec.Risk == "" {
		spec.Risk = RiskRead
	}
	return spec
}

func validateActionSpec(spec ActionSpec) error {
	if spec.ID == "" || strings.ContainsAny(spec.ID, " /\\") {
		return errors.New("action id must be a non-empty dotted identifier")
	}
	if spec.Category == "" || spec.Label == "" || spec.Description == "" {
		return fmt.Errorf("action %q requires category, label, and description", spec.ID)
	}
	switch spec.Risk {
	case RiskRead, RiskExecute, RiskConfirm, RiskDanger:
	default:
		return fmt.Errorf("action %q has invalid risk %q", spec.ID, spec.Risk)
	}
	if (spec.Risk == RiskConfirm || spec.Risk == RiskDanger) && spec.ConfirmationText == "" {
		return fmt.Errorf("action %q requires confirmation text", spec.ID)
	}
	seen := map[string]bool{}
	for _, field := range spec.Inputs {
		if field.Name == "" || seen[field.Name] {
			return fmt.Errorf("action %q has invalid or duplicate input %q", spec.ID, field.Name)
		}
		seen[field.Name] = true
		switch field.Type {
		case FieldString, FieldText, FieldBoolean, FieldInteger, FieldSelect, FieldPath, FieldSecret, FieldDuration:
		default:
			return fmt.Errorf("action %q input %q has invalid type %q", spec.ID, field.Name, field.Type)
		}
		if field.Type == FieldSelect && len(field.Options) == 0 {
			return fmt.Errorf("action %q select input %q requires options", spec.ID, field.Name)
		}
	}
	return nil
}

func validateActionInput(fields []InputField, input map[string]any) (map[string]any, error) {
	if input == nil {
		input = map[string]any{}
	}
	allowed := make(map[string]InputField, len(fields))
	for _, field := range fields {
		allowed[field.Name] = field
	}
	for name := range input {
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("unknown input %q", name)
		}
	}
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		value, exists := input[field.Name]
		if !exists && field.Default != nil {
			value, exists = field.Default, true
		}
		if !exists {
			if field.Required {
				return nil, fmt.Errorf("input %q is required", field.Name)
			}
			continue
		}
		normalized, err := normalizeFieldValue(field, value)
		if err != nil {
			return nil, err
		}
		result[field.Name] = normalized
	}
	return result, nil
}

func normalizeFieldValue(field InputField, value any) (any, error) {
	switch field.Type {
	case FieldString, FieldText, FieldPath, FieldSecret, FieldDuration, FieldSelect:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("input %q must be a string", field.Name)
		}
		text = strings.TrimSpace(text)
		if field.Required && text == "" {
			return nil, fmt.Errorf("input %q is required", field.Name)
		}
		if field.Type == FieldSelect && !containsString(field.Options, text) {
			return nil, fmt.Errorf("input %q must be one of %s", field.Name, strings.Join(field.Options, ", "))
		}
		return text, nil
	case FieldBoolean:
		switch typed := value.(type) {
		case bool:
			return typed, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
			if err == nil {
				return parsed, nil
			}
		}
		return nil, fmt.Errorf("input %q must be a boolean", field.Name)
	case FieldInteger:
		switch typed := value.(type) {
		case int:
			return typed, nil
		case float64:
			if typed == float64(int(typed)) {
				return int(typed), nil
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil {
				return parsed, nil
			}
		}
		return nil, fmt.Errorf("input %q must be an integer", field.Name)
	default:
		return nil, fmt.Errorf("input %q has unsupported type %q", field.Name, field.Type)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
