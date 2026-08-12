package agent

import "testing"

func TestToolFailureCircuitBlocksThirdIdenticalFailure_BitsUT(t *testing.T) {
	circuit := newToolFailureCircuit(2)
	if _, blocked := circuit.Before("code_run", "sha256:a"); blocked {
		t.Fatal("first invocation was blocked")
	}
	circuit.Observe("code_run", "sha256:a", false)
	circuit.Observe("code_run", "sha256:a", false)
	decision, blocked := circuit.Before("code_run", "sha256:a")
	if !blocked || decision.Action != "circuit_break" || decision.FailureCount != 2 {
		t.Fatalf("decision = %#v blocked=%t", decision, blocked)
	}
	if _, blocked := circuit.Before("code_run", "sha256:b"); blocked {
		t.Fatal("different arguments must not share a circuit")
	}
	circuit.Observe("code_run", "sha256:a", true)
	if _, blocked := circuit.Before("code_run", "sha256:a"); blocked {
		t.Fatal("success must close the circuit")
	}
}
