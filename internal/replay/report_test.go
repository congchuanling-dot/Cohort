package replay

import "testing"

func TestFinalizeReportIsIdempotent(t *testing.T) {
	report := ExperimentReport{Trials: []TrialResult{
		{Status: "done", Metrics: RunMetrics{InputTokens: 80, OutputTokens: 20, DurationMS: 100}},
		{Status: "failed", Error: "boom", Metrics: RunMetrics{InputTokens: 40, OutputTokens: 10, DurationMS: 300}},
	}}
	FinalizeReport(&report)
	firstHash := report.ProofHash
	FinalizeReport(&report)
	if report.Successful != 1 || report.SuccessRate != 0.5 {
		t.Fatalf("success aggregate = %d %.2f", report.Successful, report.SuccessRate)
	}
	if report.MeanTokens != 75 || report.MeanDurationMS != 200 {
		t.Fatalf("mean aggregate = %.2f tokens %.2f ms", report.MeanTokens, report.MeanDurationMS)
	}
	if report.ProofHash != firstHash {
		t.Fatalf("proof hash changed after idempotent finalize: %s != %s", report.ProofHash, firstHash)
	}
}
