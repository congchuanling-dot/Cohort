package cli

import "testing"

func TestParseEvalRunOptionsV3_BitsUT(t *testing.T) {
	opts, err := parseEvalRunOptions([]string{
		"stateful",
		"--profile", "deepseek,local",
		"--workers=3",
		"--repeat", "2",
		"--min-score=90",
		"--min-pass-rate", "100%",
		"--min-stability=95",
		"--max-regressions", "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.SuitePath != "stateful" || opts.Workers != 3 || opts.Repeat != 2 {
		t.Fatalf("basic opts = %#v", opts)
	}
	if len(opts.Profiles) != 2 || opts.Profiles[0] != "deepseek" || opts.Profiles[1] != "local" {
		t.Fatalf("profiles = %#v", opts.Profiles)
	}
	if opts.Gate.MinScore != 90 || opts.Gate.MinPassRate != 100 || opts.Gate.MinStability != 95 || opts.Gate.MaxRegressions != 0 {
		t.Fatalf("gate = %#v", opts.Gate)
	}
}

func TestParseEvalStabilityOptions_BitsUT(t *testing.T) {
	opts, err := parseEvalStabilityOptions([]string{"--window=50", "--suite", "stateful", "--profile=deepseek", "--model", "model-a", "--open", "--flaky"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Window != 50 || opts.SuiteID != "stateful" || opts.Profile != "deepseek" || opts.Model != "model-a" || !opts.Open || !opts.OnlyFlaky {
		t.Fatalf("opts = %#v", opts)
	}
}
