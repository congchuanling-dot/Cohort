package controlplane

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatalogValidatesInputsAndConfirmation_BitsUT(t *testing.T) {
	catalog, err := NewCatalog(ActionSpec{
		ID: "delivery.accept", Category: "delivery", Label: "批准并合并",
		Description: "批准当前交付并执行事务合并。", Risk: RiskDanger,
		ConfirmationText: "ACCEPT",
		Inputs: []InputField{
			{Name: "delivery_id", Label: "Delivery", Type: FieldString, Required: true},
			{Name: "mode", Label: "模式", Type: FieldSelect, Options: []string{"safe", "fast"}, Default: "safe"},
		},
		Handler: func(_ context.Context, request ActionRequest) (ActionResult, error) {
			return ActionResult{Summary: request.Input["delivery_id"].(string), Data: request.Input}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ActionRequest{
		ProjectRoot: t.TempDir(), Actor: "tester",
		Input: map[string]any{"delivery_id": "delivery-1"},
	}
	if _, err := catalog.Execute(context.Background(), "delivery.accept", request); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("missing confirmation err = %v", err)
	}
	request.Confirmation = "ACCEPT"
	result, err := catalog.Execute(context.Background(), "delivery.accept", request)
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if data["mode"] != "safe" || result.Summary != "delivery-1" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := catalog.Execute(context.Background(), "delivery.accept", ActionRequest{
		ProjectRoot: t.TempDir(), Confirmation: "ACCEPT",
		Input: map[string]any{"delivery_id": "x", "unknown": true},
	}); err == nil || !strings.Contains(err.Error(), "unknown input") {
		t.Fatalf("unknown input err = %v", err)
	}
}

func TestOperationManagerPersistsRedactsStreamsAndRecovers_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	release := make(chan struct{})
	catalog, err := NewCatalog(ActionSpec{
		ID: "session.run", Category: "session", Label: "运行任务",
		Description: "运行一个 Agent 任务。", Risk: RiskExecute, Async: true,
		Inputs: []InputField{
			{Name: "task", Label: "任务", Type: FieldText, Required: true},
			{Name: "api_key", Label: "密钥", Type: FieldSecret, Required: true},
		},
		Handler: func(ctx context.Context, request ActionRequest) (ActionResult, error) {
			select {
			case <-ctx.Done():
				return ActionResult{}, ctx.Err()
			case <-release:
				return ActionResult{Summary: "done", Data: map[string]any{"task": request.Input["task"]}}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewOperationManager(projectRoot, catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	events, unsubscribe := manager.Subscribe(8)
	defer unsubscribe()
	operation, err := manager.Start(context.Background(), "session.run", ActionRequest{
		ProjectRoot: projectRoot,
		Input:       map[string]any{"task": "inspect", "api_key": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Input["api_key"] != "[REDACTED]" {
		t.Fatalf("operation leaked secret: %#v", operation.Input)
	}
	waitForOperationStatus(t, manager, operation.ID, OperationRunning)
	close(release)
	completed := waitForOperationStatus(t, manager, operation.ID, OperationSucceeded)
	if completed.Summary != "done" {
		t.Fatalf("completed = %#v", completed)
	}
	seenRunning := false
	for len(events) > 0 {
		if event := <-events; event.Operation.Status == OperationRunning {
			seenRunning = true
		}
	}
	if !seenRunning {
		t.Fatal("operation stream omitted running state")
	}

	interrupted := completed
	interrupted.ID = "op_interrupted"
	interrupted.Status = OperationRunning
	interrupted.FinishedAt = time.Time{}
	interrupted.Error = ""
	if err := manager.save(interrupted); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewOperationManager(projectRoot, catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recovered, err := restarted.Load(interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != OperationFailed || !strings.Contains(recovered.Error, "restarted") {
		t.Fatalf("recovered = %#v", recovered)
	}
	info, err := os.Stat(filepath.Join(projectRoot, ".cohort", "control", "operations", operation.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("operation file mode=%v, want 0600", info.Mode().Perm())
	}
}

func waitForOperationStatus(t *testing.T, manager *OperationManager, id string, status OperationStatus) Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := manager.Load(id)
		if err == nil && operation.Status == status {
			return operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	operation, err := manager.Load(id)
	t.Fatalf("operation status = %#v err=%v, want %s", operation, err, status)
	return Operation{}
}
