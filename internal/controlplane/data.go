package controlplane

import (
	"context"
	"net/url"
	"time"
)

type SourceState string

const (
	SourceReady       SourceState = "ready"
	SourceEmpty       SourceState = "empty"
	SourceUnavailable SourceState = "unavailable"
	SourceError       SourceState = "error"
	SourceStale       SourceState = "stale"
)

type SourceHealth struct {
	Kind         string      `json:"kind"`
	Label        string      `json:"label"`
	State        SourceState `json:"state"`
	RelativePath string      `json:"relative_path"`
	Count        int         `json:"count"`
	UpdatedAt    time.Time   `json:"updated_at,omitempty"`
	ScannedAt    time.Time   `json:"scanned_at"`
	ErrorCode    string      `json:"error_code,omitempty"`
	Error        string      `json:"error,omitempty"`
}

type DataSourceProvider interface {
	Sources(context.Context, bool) ([]SourceHealth, error)
}

type EntityKind string

const (
	EntitySession      EntityKind = "session"
	EntityEvalRun      EntityKind = "eval_run"
	EntityDelivery     EntityKind = "delivery"
	EntityHermesAction EntityKind = "hermes_action"
	EntitySkill        EntityKind = "skill"
	EntityCapability   EntityKind = "capability"
	EntityMCPServer    EntityKind = "mcp_server"
	EntityModelProfile EntityKind = "model_profile"
)

type ContextAction struct {
	ActionID       string    `json:"action_id"`
	Label          string    `json:"label"`
	Risk           RiskLevel `json:"risk"`
	Enabled        bool      `json:"enabled"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
}

type EntityDescriptor struct {
	Kind       EntityKind      `json:"kind"`
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Subtitle   string          `json:"subtitle,omitempty"`
	Status     string          `json:"status,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
	SearchText string          `json:"search_text,omitempty"`
	Version    string          `json:"version"`
	Badges     []string        `json:"badges,omitempty"`
	Actions    []ContextAction `json:"actions,omitempty"`
}

type EntityProvider interface {
	ListEntities(context.Context, EntityKind, url.Values) ([]EntityDescriptor, error)
	GetEntity(context.Context, EntityKind, string) (EntityDescriptor, error)
}
