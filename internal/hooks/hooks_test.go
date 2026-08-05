package hooks

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryEmitCollectsHandlerResults_BitsUT(t *testing.T) {
	called := 0
	registry := NewRegistry(
		HandlerFunc{ID: "ok", Fn: func(context.Context, Event) error {
			called++
			return nil
		}},
		HandlerFunc{ID: "fail", Fn: func(context.Context, Event) error {
			return errors.New("hook failed")
		}},
	)

	results := registry.Emit(context.Background(), Event{Type: EventPreToolUse})
	if called != 1 || len(results) != 2 {
		t.Fatalf("called=%d results=%#v", called, results)
	}
	summary := ResultsSummary(results)
	if summary["handlers"] != 2 || summary["failed"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}
