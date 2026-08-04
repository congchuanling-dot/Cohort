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
	writeSkill(t, filepath.Join(home, ".cohort", "skills", "release", SkillFileName), `# Release Checks

Verify changelog and smoke tests.
`)

	store := NewStore(workspace, home)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	skills := store.Skills()
	if len(skills) < 2 {
		t.Fatalf("skills = %d, want at least 2: %#v", len(skills), skills)
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

func TestStoreIncludesBuiltinSkillsAndProjectAliasWins_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "code-review", SkillFileName), `---
name: project review
description: Project-specific review flow.
---
# Project Review
`)
	store := NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	builtin, err := store.Find("builtin/unit-test")
	if err != nil {
		t.Fatal(err)
	}
	if builtin.Scope != ScopeBuiltin || !builtin.UserInvocable {
		t.Fatalf("builtin = %#v", builtin)
	}
	result, err := store.Read("unit-test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "# Unit Test") {
		t.Fatalf("builtin content = %q", result.Content)
	}
	project, err := store.Find("code-review")
	if err != nil {
		t.Fatal(err)
	}
	if project.ID != "project/code-review" {
		t.Fatalf("code-review alias resolved to %s, want project/code-review", project.ID)
	}
	doctor, err := store.Doctor("builtin/code-review")
	if err != nil {
		t.Fatal(err)
	}
	if !doctor.Healthy {
		t.Fatalf("builtin doctor = %#v", doctor.Checks)
	}
}

func TestStoreReadRejectsAmbiguousAlias_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "same", SkillFileName), "# Project Same\n")
	writeSkill(t, filepath.Join(home, ".cohort", "skills", "same", SkillFileName), "# User Same\n")

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

func TestStoreParsesRequiresFrontMatter_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "deps", SkillFileName), `---
name: deps
description: Skill with dependencies.
requires:
  mcp:
    - docs
    - lark
  env: [COHORT_TOKEN, COHORT_REGION]
  commands:
    - git
    - go
---

# Deps
`)

	store := NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	item, err := store.Find("deps")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(item.Requires.MCP, ","); got != "docs,lark" {
		t.Fatalf("mcp requires = %q", got)
	}
	if got := strings.Join(item.Requires.Env, ","); got != "COHORT_TOKEN,COHORT_REGION" {
		t.Fatalf("env requires = %q", got)
	}
	if got := strings.Join(item.Requires.Commands, ","); got != "git,go" {
		t.Fatalf("command requires = %q", got)
	}
	if summary := item.Requires.Summary(); !strings.Contains(summary, "mcp:docs,lark") || !strings.Contains(summary, "env:COHORT_TOKEN,COHORT_REGION") || !strings.Contains(summary, "commands:git,go") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestStoreParsesPermissionsFrontMatter_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".cohort", "skills", "safe", SkillFileName), `---
name: safe
description: Skill with active policy.
permissions:
  allow-tools: [file_read, code_run]
  deny-tools:
    - mcp_prod_delete
---

# Safe
`)

	store := NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	item, err := store.Find("safe")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(item.Permissions.AllowTools, ","); got != "file_read,code_run" {
		t.Fatalf("allow tools = %q", got)
	}
	if got := strings.Join(item.Permissions.DenyTools, ","); got != "mcp_prod_delete" {
		t.Fatalf("deny tools = %q", got)
	}
	if index := store.IndexPrompt(); !strings.Contains(index, "permissions: allow:file_read,code_run deny:mcp_prod_delete") {
		t.Fatalf("index missing permissions:\n%s", index)
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
