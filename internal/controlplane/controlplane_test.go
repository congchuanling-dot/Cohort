package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
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

func TestPreparationResolvesEntityAndRejectsChangedVersion_BitsUT(t *testing.T) {
	entities := &memoryEntityProvider{entity: EntityDescriptor{
		Kind: EntityDelivery, ID: "delivery-1", Title: "Delivery One",
		Status: "approved", Version: "v1",
	}}
	catalog, err := NewCatalog(ActionSpec{
		ID: "delivery.merge", Category: "delivery", Label: "Merge",
		Description: "Merge an approved delivery.", Risk: RiskDanger, ConfirmationText: "MERGE",
		Inputs: []InputField{{
			Name: "delivery_id", Label: "Delivery", Type: FieldEntity, Required: true,
			Entity: &EntitySelector{Kind: EntityDelivery, Status: []string{"approved"}},
		}},
		Handler: func(context.Context, ActionRequest) (ActionResult, error) {
			return ActionResult{Summary: "merged"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPreparationManager(catalog, entities)
	request := ActionRequest{
		ProjectRoot: t.TempDir(), Actor: "tester",
		Input: map[string]any{"delivery_id": "delivery-1"},
	}
	prepared, err := manager.Prepare(context.Background(), "delivery.merge", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Entities) != 1 || prepared.Entities[0].Title != "Delivery One" {
		t.Fatalf("preparation = %#v", prepared)
	}
	entities.entity.Version = "v2"
	if _, err := manager.Consume(context.Background(), prepared.Token, "delivery.merge"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed entity consume err = %v", err)
	}
	prepared, err = manager.Prepare(context.Background(), "delivery.merge", request)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.Consume(context.Background(), prepared.Token, "delivery.merge")
	if err != nil {
		t.Fatal(err)
	}
	resolved.Confirmation = "MERGE"
	if _, err := catalog.Execute(context.Background(), "delivery.merge", resolved); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Consume(context.Background(), prepared.Token, "delivery.merge"); err == nil {
		t.Fatal("preparation token was reusable")
	}
}

type memoryEntityProvider struct {
	entity EntityDescriptor
}

func (p *memoryEntityProvider) ListEntities(context.Context, EntityKind, url.Values) ([]EntityDescriptor, error) {
	return []EntityDescriptor{p.entity}, nil
}

func (p *memoryEntityProvider) GetEntity(_ context.Context, kind EntityKind, id string) (EntityDescriptor, error) {
	if p.entity.Kind != kind || p.entity.ID != id {
		return EntityDescriptor{}, os.ErrNotExist
	}
	return p.entity, nil
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
			{Name: "source", Label: "来源", Type: FieldString, Sensitive: true},
		},
		Handler: func(ctx context.Context, request ActionRequest) (ActionResult, error) {
			ReportProgress(ctx, "working", map[string]any{"step": 1})
			if request.Input["task"] == "cancel" {
				<-ctx.Done()
				return ActionResult{}, ctx.Err()
			}
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
		Input:       map[string]any{"task": "inspect", "api_key": "secret", "source": "https://token@example.com/repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Input["api_key"] != "[REDACTED]" {
		t.Fatalf("operation leaked secret: %#v", operation.Input)
	}
	if operation.Input["source"] != "[REDACTED]" {
		t.Fatalf("operation leaked sensitive field: %#v", operation.Input)
	}
	waitForOperationStatus(t, manager, operation.ID, OperationRunning)
	close(release)
	completed := waitForOperationStatus(t, manager, operation.ID, OperationSucceeded)
	if completed.Summary != "done" {
		t.Fatalf("completed = %#v", completed)
	}
	seenRunning := false
	seenProgress := false
	for len(events) > 0 {
		event := <-events
		if event.Operation.Status == OperationRunning {
			seenRunning = true
		}
		if event.Type == "operation.progress" && event.Operation.Summary == "working" {
			seenProgress = true
		}
	}
	if !seenRunning {
		t.Fatal("operation stream omitted running state")
	}
	if !seenProgress {
		t.Fatal("operation stream omitted progress state")
	}
	cancellable, err := manager.Start(context.Background(), "session.run", ActionRequest{
		ProjectRoot: projectRoot,
		Input:       map[string]any{"task": "cancel", "api_key": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForOperationStatus(t, manager, cancellable.ID, OperationRunning)
	if _, err := manager.Cancel(cancellable.ID); err != nil {
		t.Fatal(err)
	}
	waitForOperationStatus(t, manager, cancellable.ID, OperationCancelled)

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
	rootInfo, err := os.Stat(filepath.Join(projectRoot, ".cohort", "control", "operations"))
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0700 {
		t.Fatalf("operation directory mode=%v, want 0700", rootInfo.Mode().Perm())
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

func TestServerEnforcesBootstrapSessionOriginAndCSRF_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	catalog, err := NewCatalog(ActionSpec{
		ID: "system.ping", Category: "system", Label: "连通测试",
		Description: "验证控制面执行链路。", Risk: RiskExecute,
		Handler: func(context.Context, ActionRequest) (ActionResult, error) {
			return ActionResult{Summary: "pong", Data: map[string]any{"ok": true}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		ProjectRoot: projectRoot,
		Listen:      "127.0.0.1:0",
		Catalog:     catalog,
		DataSources: staticDataSourceProvider{},
		StaticFS: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html>control center</html>")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running, err := server.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close(context.Background())
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}

	response := mustControlRequest(t, client, http.MethodGet, running.URL+"/", nil, nil)
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("static response status=%d headers=%v", response.StatusCode, response.Header)
	}
	_ = response.Body.Close()
	response = mustControlRequest(t, client, http.MethodGet, running.URL+"/api/v1/catalog", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated catalog status = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	bootstrapURL, err := url.Parse(running.BootstrapURL)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(bootstrapURL.Fragment, "token=")
	token, err = url.QueryUnescape(token)
	if err != nil {
		t.Fatal(err)
	}
	response = mustControlRequest(t, client, http.MethodPost, running.URL+"/api/v1/auth/bootstrap", []byte("{}"), map[string]string{
		"Content-Type":       "application/json",
		"Origin":             "https://evil.invalid",
		"X-Cohort-Bootstrap": token,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin bootstrap status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = mustControlRequest(t, client, http.MethodPost, running.URL+"/api/v1/auth/bootstrap", []byte("{}"), map[string]string{
		"Content-Type":       "application/json",
		"Origin":             running.URL,
		"X-Cohort-Bootstrap": token,
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("bootstrap status=%d body=%s", response.StatusCode, body)
	}
	var bootstrap struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if bootstrap.CSRF == "" {
		t.Fatal("bootstrap omitted CSRF token")
	}
	response = mustControlRequest(t, client, http.MethodGet, running.URL+"/api/v1/data-sources", nil, nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("data sources status=%d body=%s", response.StatusCode, body)
	}
	_ = response.Body.Close()

	response = mustControlRequest(t, client, http.MethodPost, running.URL+"/api/v1/actions/system.ping/execute", []byte(`{}`), map[string]string{
		"Content-Type": "application/json", "Origin": running.URL,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = mustControlRequest(t, client, http.MethodPost, running.URL+"/api/v1/actions/system.ping/execute", []byte(`{"input":{}}`), map[string]string{
		"Content-Type": "application/json", "Origin": running.URL, "X-CSRF-Token": bootstrap.CSRF,
	})
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("execute status=%d body=%s", response.StatusCode, body)
	}
	var operation Operation
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	completed := waitForOperationStatus(t, server.operations, operation.ID, OperationSucceeded)
	if completed.Summary != "pong" {
		t.Fatalf("operation = %#v", completed)
	}
}

type staticDataSourceProvider struct{}

func (staticDataSourceProvider) Sources(context.Context, bool) ([]SourceHealth, error) {
	return []SourceHealth{{Kind: "sessions", Label: "Sessions", State: SourceReady, Count: 2}}, nil
}

func TestServerRejectsNonLoopbackListen_BitsUT(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(ServerConfig{ProjectRoot: t.TempDir(), Listen: "0.0.0.0:18779", Catalog: catalog}); err == nil {
		t.Fatal("non-loopback listen unexpectedly accepted")
	}
}

func TestOperationManagerRejectsSymlinkStorageRoot_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(projectRoot, ".cohort")); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewOperationManager(projectRoot, catalog); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink storage err = %v", err)
	}
}

func TestProjectRegistryPersistsAndDeduplicates_BitsUT(t *testing.T) {
	registry, err := NewProjectRegistry(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	first, err := registry.Register(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(secondRoot); err != nil {
		t.Fatal(err)
	}
	again, err := registry.Register(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("project id changed: %s != %s", again.ID, first.ID)
	}
	projects, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].Root != firstRoot {
		t.Fatalf("projects = %#v", projects)
	}
}

func mustControlRequest(t *testing.T, client *http.Client, method string, target string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
