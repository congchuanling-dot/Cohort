package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSchedulerRunsDependencyDAGAndPublishesArtifacts_BitsUT(t *testing.T) {
	root := initRunnableDeliveryRepo(t)
	store, item := createRunnableDelivery(t, root)
	var mu sync.Mutex
	seenDependencies := map[string][]string{}
	worker := func(_ context.Context, _ Delivery, _ AcceptanceContract, node TaskNode, candidate Candidate) (WorkerResult, error) {
		mu.Lock()
		seenDependencies[node.ID] = append([]string(nil), candidate.DependencyCommits...)
		mu.Unlock()
		actualWrites := []string{"internal/delivery/generated.go"}
		if node.ID == "delivery-docs" {
			actualWrites = []string{"docs/generated.md"}
		}
		return WorkerResult{
			Summary:      "completed " + node.ID,
			Commit:       "commit-" + node.ID,
			TreeHash:     "tree-" + node.ID,
			ActualWrites: actualWrites,
			Diff:         []byte("diff --git a/file b/file\n"),
			Result:       []byte(`{"status":"done"}`),
			DurationMS:   10,
		}, nil
	}
	scheduler := Scheduler{Store: store, MaxParallel: 2, OwnerID: "test-scheduler", LeaseTTL: time.Minute}
	runtime, err := scheduler.Run(context.Background(), item.ID, worker)
	if err != nil {
		t.Fatal(err)
	}
	if !RuntimeComplete(runtime) {
		t.Fatalf("runtime = %#v", runtime)
	}
	if got := seenDependencies["delivery-docs"]; len(got) != 1 || got[0] != "commit-delivery-core" {
		t.Fatalf("dependent candidate commits = %#v", got)
	}
	stored, err := store.Load(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusIntegrating {
		t.Fatalf("delivery status = %s, want integrating", stored.Status)
	}
	for _, node := range runtime.Nodes {
		if node.SelectedID == "" {
			t.Fatalf("node has no selected candidate: %#v", node)
		}
		for _, candidate := range node.Candidates {
			if candidate.ID == node.SelectedID {
				if candidate.DiffArtifact == "" || candidate.ResultArtifact == "" {
					t.Fatalf("selected candidate artifacts = %#v", candidate)
				}
				if _, _, err := store.ReadArtifact(item.ID, candidate.DiffArtifact); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestStoreRecoversExpiredNodeLease_BitsUT(t *testing.T) {
	root := initRunnableDeliveryRepo(t)
	store, item := createRunnableDelivery(t, root)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.InitializeRuntime(item.ID); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{ID: "delivery-core-c1", BaseCommit: item.BaseCommit, Branch: "cohort/test", WorktreePath: filepath.Join(store.WorktreeDir, "test")}
	if _, err := store.ClaimNode(item.ID, "delivery-core", "dead-worker", -1, []Candidate{candidate}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	recovered, err := store.RecoverExpiredLeases(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	runtime, err := store.LoadRuntime(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Nodes["delivery-core"].Status != NodeReady {
		t.Fatalf("recovered node = %#v", runtime.Nodes["delivery-core"])
	}
}

func TestArtifactBoardRejectsTamperedPayload_BitsUT(t *testing.T) {
	root := initRunnableDeliveryRepo(t)
	store, item := createRunnableDelivery(t, root)
	meta, err := store.PublishArtifact(item.ID, ArtifactMeta{Kind: "report", Producer: "test"}, []byte("verified"))
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimPrefix(meta.ID, "sha256:")
	payloadPath := filepath.Join(store.RootDir, item.ID, artifactsDir, hash, artifactPayloadFile)
	if err := os.WriteFile(payloadPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadArtifact(item.ID, meta.ID); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("ReadArtifact err = %v, want hash mismatch", err)
	}
}

func createRunnableDelivery(t *testing.T, root string) (Store, Delivery) {
	t.Helper()
	store := NewStore(root)
	base, dirty, err := RepositoryState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("fixture repository is dirty")
	}
	item, err := store.CreateDraft("run dependency DAG", base, false, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	contract, graph := validPlanFixture(item)
	item, err = store.SavePlan(item.ID, contract, graph)
	if err != nil {
		t.Fatal(err)
	}
	return store, item
}

func initRunnableDeliveryRepo(t *testing.T) string {
	t.Helper()
	root := initDeliveryGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.cohort/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", ".gitignore")
	runTestGit(t, root, "commit", "-m", "ignore runtime")
	return root
}
