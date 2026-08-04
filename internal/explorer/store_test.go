package explorer

import (
	"os"
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
