package cli

import (
	"testing"

	"cohort/internal/replay"
)

func TestParseReplayCLIOptionsFork(t *testing.T) {
	options, err := parseReplayCLIOptions([]string{
		"fork", "session-1",
		"--run", "run-1",
		"--fork-turn", "3",
		"--repeat=5",
		"--model", "candidate-model",
		"--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.mode != replay.ModeFork ||
		options.sessionID != "session-1" ||
		options.runID != "run-1" ||
		options.forkTurn != 3 ||
		options.repeat != 5 ||
		options.model != "candidate-model" ||
		!options.jsonOutput {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestParseReplayCLIOptionsRequiresForkTurn(t *testing.T) {
	_, err := parseReplayCLIOptions([]string{"fork", "session-1", "--run", "run-1"})
	if err == nil {
		t.Fatal("expected missing fork turn error")
	}
}
