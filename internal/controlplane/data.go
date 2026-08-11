package controlplane

import (
	"context"
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
