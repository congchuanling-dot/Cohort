package controlactions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/controlplane"
	"cohort/internal/evaluation"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

func TestSnapshotProviderAggregatesProjectWithoutMutatingRepository_BitsUT(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("cohort\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")
	provider := SnapshotProvider(filepath.Join(root, ".cohort", "config.yaml"))
	value, err := provider(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := value.(DashboardSnapshot)
	if !ok {
		t.Fatalf("snapshot type = %T", value)
	}
	if snapshot.Project.Root != root || snapshot.Project.Head == "" || snapshot.Project.Dirty {
		t.Fatalf("project snapshot = %#v", snapshot.Project)
	}
	if snapshot.Counts.Deliveries != 0 || snapshot.Delivery.ByStatus == nil {
		t.Fatalf("resource snapshot = %#v", snapshot)
	}
	for _, resource := range []string{"deliveries", "hermes", "evaluations", "traces"} {
		if _, err := NewResourceProvider(filepath.Join(root, "config.yaml"))(context.Background(), root, resource, nil); err != nil {
			t.Fatalf("resource %s: %v", resource, err)
		}
	}
	command := exec.Command("git", "-C", root, "status", "--porcelain=v1", "--untracked-files=all")
	if output, err := command.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("snapshot mutated repository: %q err=%v", output, err)
	}
}

func TestCatalogExposesStableSystemAction_BitsUT(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	spec, exists := catalog.Get("system.ping")
	if !exists || spec.Risk != controlplane.RiskRead {
		t.Fatalf("system action = %#v exists=%t", spec, exists)
	}
	merge, exists := catalog.Get("delivery.merge")
	if !exists || merge.Risk != controlplane.RiskDanger || merge.ConfirmationText != "MERGE" {
		t.Fatalf("delivery merge action = %#v exists=%t", merge, exists)
	}
	if actions := catalog.List(); len(actions) < 40 {
		t.Fatalf("catalog only exposes %d actions", len(actions))
	}
	root := t.TempDir()
	if _, err := projectPath(root, "../outside.json"); err == nil {
		t.Fatal("expected project path escape to be rejected")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := projectPath(root, "escape/secret.json"); err == nil {
		t.Fatal("expected symlink path escape to be rejected")
	}
}

func TestProjectDataHubDiscoversStoresAndIsolatesSourceErrors_BitsUT(t *testing.T) {
	root := t.TempDir()
	if _, err := session.NewStore(filepath.Join(root, session.DefaultRootDir)).Create("existing session", root, "test-model"); err != nil {
		t.Fatal(err)
	}
	hub, err := NewProjectDataHub(root)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := hub.Sources(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	sessionSource := findSource(sources, "sessions")
	if sessionSource.Count != 1 || sessionSource.State != controlplane.SourceReady {
		t.Fatalf("session source = %#v", sessionSource)
	}
	indexPath := filepath.Join(root, ".cohort", "control", "index-v1.json")
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("index mode = %v, want 0600", info.Mode().Perm())
	}
	entities, err := hub.ListEntities(context.Background(), controlplane.EntitySession, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].Title != "existing session" || entities[0].Actions[0].ActionID != "agent.continue" {
		t.Fatalf("session entities = %#v", entities)
	}

	hermesRoot := filepath.Join(root, ".cohort", "hermes")
	if err := os.MkdirAll(hermesRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermesRoot, "action_queue.json"), []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	sources, err = hub.Sources(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if source := findSource(sources, "hermes"); source.State != controlplane.SourceError || source.ErrorCode == "" {
		t.Fatalf("hermes source = %#v", source)
	}
	if source := findSource(sources, "sessions"); source.State != controlplane.SourceReady || source.Count != 1 {
		t.Fatalf("session source after hermes failure = %#v", source)
	}
}

func TestQualityProviderAndHTMLExportShareEvalModel_BitsUT(t *testing.T) {
	root := t.TempDir()
	store := evaluation.NewStore(root)
	evalSessionStore := session.NewStore(store.SessionsDir())
	evalSession, err := evalSessionStore.Create("eval trace", root, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	traceRunID := "run-eval-trace"
	traceData := `{"schema_version":1,"event_id":"start","event_type":"RunStarted","time":"2026-08-12T00:00:00Z","run_id":"` + traceRunID + `","session_id":"` + evalSession.ID + `","severity":"info","redaction":{}}` + "\n" +
		`{"schema_version":1,"event_id":"finish","event_type":"RunFinished","time":"2026-08-12T00:00:00.010Z","run_id":"` + traceRunID + `","session_id":"` + evalSession.ID + `","severity":"info","data":{"status":"done","duration_ms":10},"redaction":{}}` + "\n"
	tracePath := filepath.Join(evalSessionStore.SessionDir(evalSession.ID), traceview.ObservationLogFileName)
	if writeErr := os.WriteFile(tracePath, []byte(traceData), 0644); writeErr != nil {
		t.Fatal(writeErr)
	}
	result := evaluation.RunResult{
		SchemaVersion: 1, RunID: "run-quality-1", SuiteID: "core", SuiteName: "Core",
		Model: "test-model", StartedAt: time.Now().UTC().Add(-time.Minute), FinishedAt: time.Now().UTC(),
		TotalCases: 1, PassedCases: 1, PassRate: 100, Score: 96,
		Cases: []evaluation.CaseResult{{
			CaseID: "case-1", Name: "passes", Passed: true, Score: 96,
			Attempts: 1, PassedAttempts: 1, StabilityRate: 100,
			SessionID: evalSession.ID, TraceRunID: traceRunID, TracePath: tracePath,
		}},
	}
	if _, saveErr := store.SaveResult(result); saveErr != nil {
		t.Fatal(saveErr)
	}
	value, err := NewQualityProvider()(context.Background(), root, []string{"evals", result.RunID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dashboard, ok := value.(evaluation.DashboardData)
	if !ok || dashboard.Result.RunID != result.RunID || dashboard.Result.PassRate != 100 {
		t.Fatalf("dashboard = %#v", value)
	}
	exported, err := NewExportProvider()(context.Background(), root, []string{"evals", result.RunID + ".html"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exported.ContentType != "text/html; charset=utf-8" || !strings.Contains(string(exported.Data), "Core") || !strings.Contains(string(exported.Data), result.RunID) {
		t.Fatalf("export = %#v", exported)
	}
	traceValue, err := NewQualityProvider()(context.Background(), root, []string{"traces", evalSession.ID, traceRunID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph, ok := traceValue.(map[string]any)["graph"].(traceview.Graph)
	if !ok || graph.SessionID != evalSession.ID || graph.RunID != traceRunID {
		t.Fatalf("eval trace graph = %#v", traceValue)
	}
	traceExport, err := NewExportProvider()(context.Background(), root, []string{"traces", evalSession.ID, traceRunID + ".html"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(traceExport.Data), "Causal Trace Graph") {
		t.Fatal("eval trace HTML export is missing graph content")
	}
	stability, err := NewQualityProvider()(context.Background(), root, []string{"stability"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if index := stability.(evaluation.StabilityIndex); index.Summary.Runs != 1 || index.Summary.AveragePassRate != 100 {
		t.Fatalf("stability = %#v", index)
	}
}

func findSource(sources []controlplane.SourceHealth, kind string) controlplane.SourceHealth {
	for _, source := range sources {
		if source.Kind == kind {
			return source
		}
	}
	return controlplane.SourceHealth{}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
