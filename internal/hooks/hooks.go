package hooks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type EventType string

const (
	EventSessionStart EventType = "SessionStart"
	EventSessionEnd   EventType = "SessionEnd"
	EventPreToolUse   EventType = "PreToolUse"
	EventPostToolUse  EventType = "PostToolUse"
	EventFileChanged  EventType = "FileChanged"
	EventPreCompact   EventType = "PreCompact"
	EventPostCompact  EventType = "PostCompact"
)

type Event struct {
	Type      EventType      `json:"type"`
	Time      time.Time      `json:"time"`
	RunID     string         `json:"run_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Turn      int            `json:"turn,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Handler interface {
	Name() string
	HandleHook(ctx context.Context, event Event) error
}

type HandlerFunc struct {
	ID string
	Fn func(context.Context, Event) error
}

func (h HandlerFunc) Name() string {
	if strings.TrimSpace(h.ID) == "" {
		return "anonymous"
	}
	return h.ID
}

func (h HandlerFunc) HandleHook(ctx context.Context, event Event) error {
	if h.Fn == nil {
		return nil
	}
	return h.Fn(ctx, event)
}

type Result struct {
	Handler string `json:"handler"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type Registry struct {
	mu       sync.RWMutex
	handlers []Handler
}

func NewRegistry(handlers ...Handler) *Registry {
	r := &Registry{}
	for _, handler := range handlers {
		r.Register(handler)
	}
	return r
}

func (r *Registry) Register(handler Handler) {
	if r == nil || handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, handler)
}

func (r *Registry) Emit(ctx context.Context, event Event) []Result {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	handlers := append([]Handler(nil), r.handlers...)
	r.mu.RUnlock()
	if len(handlers) == 0 {
		return nil
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	results := make([]Result, 0, len(handlers))
	for _, handler := range handlers {
		name := strings.TrimSpace(handler.Name())
		if name == "" {
			name = "anonymous"
		}
		result := Result{Handler: name, OK: true}
		if err := handler.HandleHook(ctx, event); err != nil {
			result.OK = false
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

func ResultsSummary(results []Result) map[string]any {
	if len(results) == 0 {
		return nil
	}
	failed := 0
	for _, result := range results {
		if !result.OK {
			failed++
		}
	}
	return map[string]any{
		"handlers": len(results),
		"failed":   failed,
		"results":  results,
	}
}

func RequireEventType(value string) (EventType, error) {
	eventType := EventType(strings.TrimSpace(value))
	switch eventType {
	case EventSessionStart, EventSessionEnd, EventPreToolUse, EventPostToolUse, EventFileChanged, EventPreCompact, EventPostCompact:
		return eventType, nil
	default:
		return "", fmt.Errorf("unsupported hook event type %q", value)
	}
}
