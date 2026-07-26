package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreReloadReadsProjectAndUserSkills_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "go-test", SkillFileName), `---
name: Go Test
description: Run focused Go tests before broad verification.
---

# Go Test

Use go test with focused package paths first.
`)
	writeSkill(t, filepath.Join(home, ".cohert", "skills", "release", SkillFileName), `# Release Checks

Verify changelog and smoke tests.
`)

	store := NewStore(workspace, home)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	skills := store.Skills()
	if len(skills) != 2 {
		t.Fatalf("skills = %d, want 2: %#v", len(skills), skills)
	}
	project, err := store.Find("project/go-test")
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "Go Test" || project.Description != "Run focused Go tests before broad verification." {
		t.Fatalf("project summary = %#v", project)
	}
	user, err := store.Find("release")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "user/release" || user.Name != "Release Checks" {
		t.Fatalf("user skill = %#v", user)
	}
	index := store.IndexPrompt()
	for _, want := range []string{"[Skill Index]", "project/go-test", "user/release", "skill_read"} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}
}

func TestStoreReadRejectsAmbiguousAlias_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "same", SkillFileName), "# Project Same\n")
	writeSkill(t, filepath.Join(home, ".cohert", "skills", "same", SkillFileName), "# User Same\n")

	store := NewStore(workspace, home)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Find("same"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Find same error = %v, want ambiguous", err)
	}
	result, err := store.Read("project/same")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "Project Same") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestStoreParsesFoldedFrontMatterAndInvocationMetadata_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "commit", SkillFileName), `---
name: commit
description: >
  Atomic git commit with conventional message.
  Use when the user wants to create a git commit.
user-invocable: true
argument-hint: "[file1] [file2]"
---

# Commit
`)

	store := NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	item, err := store.Find("commit")
	if err != nil {
		t.Fatal(err)
	}
	if item.Description != "Atomic git commit with conventional message. Use when the user wants to create a git commit." {
		t.Fatalf("description = %q", item.Description)
	}
	if !item.UserInvocable || item.ArgumentHint != "[file1] [file2]" {
		t.Fatalf("invocation metadata = %#v", item)
	}
}

func writeSkill(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
