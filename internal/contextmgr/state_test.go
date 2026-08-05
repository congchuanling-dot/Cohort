package contextmgr

import (
	"path/filepath"
	"testing"
)

func TestContextStateRoundTrip_BitsUT(t *testing.T) {
	sessionDir := t.TempDir()
	state := ContextState{
		AutoCompactAttempts:         2,
		AutoCompactConsecutiveFails: 1,
		LastAutoCompactError:        "model unavailable",
	}
	if err := SaveContextState(sessionDir, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadContextState(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.AutoCompactAttempts != 2 || loaded.AutoCompactConsecutiveFails != 1 {
		t.Fatalf("loaded = %#v", loaded)
	}
	if filepath.Base(contextStatePath(sessionDir)) != ContextStateFileName {
		t.Fatalf("context state path = %s", contextStatePath(sessionDir))
	}
}
