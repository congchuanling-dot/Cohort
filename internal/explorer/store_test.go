package explorer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateExplorerTaskIsReadOnly_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	task, err := store.Create("verify whether plan mode requires evidence")
	if err != nil {
		t.Fatal(err)
	}
	if !task.ReadOnly || task.Status != "open" || !strings.Contains(task.ID, "verify") {
		t.Fatalf("task = %#v", task)
	}
	data, err := os.ReadFile(task.TaskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Do not edit files") || !strings.Contains(string(data), task.ResultPath) {
		t.Fatalf("task markdown = %s", string(data))
	}
	tasks, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("tasks = %#v", tasks)
	}
}

func TestRunExplorerTaskWritesResult_BitsUT(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(`#!/bin/sh
if [ "$1" = "status" ]; then echo " M internal/foo.go"; exit 0; fi
if [ "$1" = "diff" ]; then echo "internal/foo.go"; exit 0; fi
exit 2
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	store := NewStore(dir)
	task, err := store.Create("verify diff is visible")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Run(context.Background(), task.ID, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Status != "completed" || len(result.Checks) != 3 {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(task.ResultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# Explorer Result") || !strings.Contains(string(data), "git_status") {
		t.Fatalf("result markdown = %s", string(data))
	}
	updated, err := store.Find(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "completed" || updated.CompletedAt.IsZero() {
		t.Fatalf("updated task = %#v", updated)
	}
}

func TestRunAgentUsesQuestionAndPersistsFindings_BitsUT(t *testing.T) {
	store := NewStore(t.TempDir())
	task, err := store.Create("where is the runtime assembled?")
	if err != nil {
		t.Fatal(err)
	}
	var received string
	result, err := store.RunAgent(context.Background(), task.ID, func(_ context.Context, task Task) (string, error) {
		received = task.Question
		return "Runner is assembled in internal/app/app.go:40.", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if received != task.Question || result.Task.Status != "completed" || !strings.Contains(result.Findings, "app.go:40") {
		t.Fatalf("result = %#v received=%q", result, received)
	}
	data, err := os.ReadFile(task.ResultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## Findings") || !strings.Contains(string(data), "app.go:40") {
		t.Fatalf("result markdown = %s", string(data))
	}
}

func TestRunExplorerBatchWritesAggregateReport_BitsUT(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(`#!/bin/sh
if [ "$1" = "status" ]; then exit 0; fi
if [ "$1" = "diff" ]; then exit 0; fi
exit 2
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	store := NewStore(dir)
	first, err := store.Create("verify first lane")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create("verify second lane")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RunBatch(context.Background(), []string{first.ID, second.ID}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Failed != 0 || result.ReportPath == "" {
		t.Fatalf("batch result = %#v", result)
	}
	data, err := os.ReadFile(result.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Explorer Aggregate Result") || !strings.Contains(string(data), first.ID) || !strings.Contains(string(data), second.ID) {
		t.Fatalf("aggregate = %s", string(data))
	}
}
