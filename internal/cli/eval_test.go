package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

func TestEvalBrowserFixturesAreLoopbackAndDeterministic_BitsUT(t *testing.T) {
	for _, fixture := range []string{"form", "ocr-canvas"} {
		server, err := startEvalBrowserFixture(fixture)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Get(server.URL)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		server.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", fixture, response.StatusCode)
		}
		text := string(body)
		if fixture == "form" && !strings.Contains(text, `name="custname"`) {
			t.Fatalf("form fixture=%s", text)
		}
		if fixture == "ocr-canvas" && (!strings.Contains(text, "fromCharCode") || strings.Contains(text, "COHORT OCR READY")) {
			t.Fatalf("ocr fixture leaks target text: %s", text)
		}
	}
}

func TestFinalEvalOutputUsesTerminalResponseOnly_BitsUT(t *testing.T) {
	result := agent.RunResult{Status: agent.RunStatusDone, Response: &llm.Response{Content: "COHORT_DOM_READY"}}
	streamed := "准备读取页面\n执行工具\n复读 DOM\nCOHORT_DOM_READY"
	if actual := finalEvalOutput(result, streamed); actual != "COHORT_DOM_READY" {
		t.Fatalf("output=%q", actual)
	}
}

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
		"--no-stability",
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
	if !opts.SkipStability {
		t.Fatal("--no-stability was not parsed")
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
