package skill

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/mcp"
)

func TestInstallCopiesLocalSkillToProjectScope_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source-skill")
	writeSkill(t, filepath.Join(source, SkillFileName), `---
name: Source Skill
description: Install me.
---

# Source Skill
`)
	if err := os.WriteFile(filepath.Join(source, "helper.txt"), []byte("asset"), 0644); err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()

	result, err := Install(context.Background(), InstallOptions{
		Source:      source,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.ID != "project/source-skill" || result.Files != 2 {
		t.Fatalf("result = %#v", result)
	}
	dest := filepath.Join(projectRoot, ".cohort", "skills", "source-skill")
	for _, path := range []string{filepath.Join(dest, SkillFileName), filepath.Join(dest, "helper.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing installed file %s: %v", path, err)
		}
	}
	store := NewStore(projectRoot, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	found, err := store.Find("project/source-skill")
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != "Source Skill" || found.Description != "Install me." {
		t.Fatalf("found = %#v", found)
	}
}

func TestInstallRequiresNameWhenSourceContainsMultipleSkills_BitsUT(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "skills", "first", SkillFileName), "# First\n")
	writeSkill(t, filepath.Join(root, "skills", "second", SkillFileName), "# Second\n")

	_, err := Install(context.Background(), InstallOptions{
		Source:      root,
		ProjectRoot: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "multiple skills found") {
		t.Fatalf("error = %v, want multiple skills", err)
	}

	result, err := Install(context.Background(), InstallOptions{
		Source:      root,
		Name:        "second",
		ProjectRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.ID != "project/second" {
		t.Fatalf("result = %#v", result)
	}
}

func TestInstallForceReplacesExistingSkill_BitsUT(t *testing.T) {
	projectRoot := t.TempDir()
	first := filepath.Join(t.TempDir(), "replace-me")
	second := filepath.Join(t.TempDir(), "replace-me")
	writeSkill(t, filepath.Join(first, SkillFileName), "# First\n")
	writeSkill(t, filepath.Join(second, SkillFileName), "# Second\n")

	if _, err := Install(context.Background(), InstallOptions{Source: first, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{Source: second, ProjectRoot: projectRoot}); err == nil {
		t.Fatal("expected duplicate install to fail")
	}
	result, err := Install(context.Background(), InstallOptions{Source: second, ProjectRoot: projectRoot, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replaced {
		t.Fatalf("replaced = false, result = %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".cohort", "skills", "replace-me", SkillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Second") {
		t.Fatalf("installed content = %s", data)
	}
}

func TestInstallUserScopeUsesHomeDir_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "global-skill")
	writeSkill(t, filepath.Join(source, SkillFileName), "# Global Skill\n")
	home := t.TempDir()

	result, err := Install(context.Background(), InstallOptions{
		Source:  source,
		Scope:   ScopeUser,
		HomeDir: home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.ID != "user/global-skill" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(home, ".cohert", "skills", "global-skill", SkillFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestInstallDryRunDoesNotWriteAndInstallRecordsHash_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "preview-me")
	writeSkill(t, filepath.Join(source, SkillFileName), "# Preview Me\n")
	if err := os.WriteFile(filepath.Join(source, "asset.txt"), []byte("asset"), 0644); err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()

	preview, err := Install(context.Background(), InstallOptions{
		Source:      source,
		ProjectRoot: projectRoot,
		DryRun:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || preview.Files != 2 || preview.ContentHash == "" || preview.SourceType != "local-dir" {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".cohort", "skills", "preview-me")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote destination or stat failed differently: %v", err)
	}

	result, err := Install(context.Background(), InstallOptions{
		Source:      source,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentHash != preview.ContentHash {
		t.Fatalf("install hash = %s, preview hash = %s", result.ContentHash, preview.ContentHash)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".cohort", "skills", "preview-me", manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var meta manifest
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ContentHash != preview.ContentHash || meta.SourceType != "local-dir" || meta.Source != source {
		t.Fatalf("manifest = %#v", meta)
	}
}

func TestInstallDryRunReportsRequires_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "preview-deps")
	writeSkill(t, filepath.Join(source, SkillFileName), `---
name: Preview Deps
requires:
  mcp:
    - docs
  env:
    - COHORT_PREVIEW_TOKEN
  commands:
    - git
---

# Preview Deps
`)

	preview, err := Install(context.Background(), InstallOptions{
		Source:      source,
		ProjectRoot: t.TempDir(),
		DryRun:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Skill.Requires.Summary(); got != "mcp:docs env:COHORT_PREVIEW_TOKEN commands:git" {
		t.Fatalf("requires summary = %q", got)
	}
}

func TestStoreUninstallRemovesSkillDirectory_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "remove-me")
	writeSkill(t, filepath.Join(source, SkillFileName), "# Remove Me\n")
	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())

	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	result, err := store.Uninstall("remove-me")
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.ID != "project/remove-me" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("skill dir still exists or stat failed differently: %v", err)
	}
	if _, err := store.Find("remove-me"); err == nil {
		t.Fatal("removed skill still found")
	}
}

func TestStoreUpdateUsesInstallManifestSource_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "update-me")
	writeSkill(t, filepath.Join(source, SkillFileName), "# First\n")
	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())

	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(source, SkillFileName), "# Second\n")
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	result, err := store.Update(context.Background(), "update-me", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.Name != "Second" || result.Previous.Name != "First" {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".cohort", "skills", "update-me", SkillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Second") {
		t.Fatalf("updated content = %s", data)
	}
}

func TestStoreCheckUpdateDoesNotWrite_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "check-me")
	writeSkill(t, filepath.Join(source, SkillFileName), "# First\n")
	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())

	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(source, SkillFileName), "# Second\n")
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	result, err := store.CheckUpdate(context.Background(), UpdateOptions{ID: "check-me"})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpToDate || result.CurrentHash == result.CandidateHash {
		t.Fatalf("check result = %#v, want update available", result)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".cohort", "skills", "check-me", SkillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "First") {
		t.Fatalf("check update wrote installed skill: %s", data)
	}
}

func TestGitInstallPinLocksResolvedCommit_BitsUT(t *testing.T) {
	repo := newGitSkillRepo(t)
	writeSkill(t, filepath.Join(repo, SkillFileName), "# First\n")
	commit1 := gitCommitAll(t, repo, "first")
	writeSkill(t, filepath.Join(repo, SkillFileName), "# Second\n")
	commit2 := gitCommitAll(t, repo, "second")

	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())
	source := "file://" + repo
	result, err := Install(context.Background(), InstallOptions{
		Source:      source,
		Name:        "repo",
		Pin:         commit1,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pinned || result.SourceRef != commit1 || result.ResolvedRef != commit1 || result.RequestedRef != commit1 {
		t.Fatalf("install result = %#v", result)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckUpdate(context.Background(), UpdateOptions{ID: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !check.UpToDate || !check.Pinned || check.SourceRef != commit1 {
		t.Fatalf("pinned check = %#v, want up-to-date at commit1", check)
	}
	check, err = store.CheckUpdate(context.Background(), UpdateOptions{ID: "repo", Pin: commit2})
	if err != nil {
		t.Fatal(err)
	}
	if check.UpToDate || check.SourceRef != commit2 || check.ResolvedRef != commit2 {
		t.Fatalf("repin check = %#v, want update available at commit2", check)
	}
	update, err := store.UpdateWithOptions(context.Background(), UpdateOptions{ID: "repo", Pin: commit2})
	if err != nil {
		t.Fatal(err)
	}
	if !update.Pinned || update.SourceRef != commit2 || update.ResolvedRef != commit2 {
		t.Fatalf("update = %#v", update)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".cohort", "skills", "repo", SkillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Second") {
		t.Fatalf("updated content = %s", data)
	}
}

func TestStoreDoctorDetectsContentHashDrift_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "doctor-me")
	writeSkill(t, filepath.Join(source, SkillFileName), `---
name: Doctor Me
description: Diagnose me.
---

# Doctor Me
`)
	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())
	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	result, err := store.Doctor("doctor-me")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || result.ErrorCount() != 0 || result.Manifest == nil || result.Manifest.ContentHash == "" {
		t.Fatalf("initial doctor result = %#v", result)
	}
	installed := filepath.Join(projectRoot, ".cohort", "skills", "doctor-me", SkillFileName)
	if err := os.WriteFile(installed, []byte("# Changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err = store.Doctor("doctor-me")
	if err != nil {
		t.Fatal(err)
	}
	if result.Healthy || result.ErrorCount() == 0 {
		t.Fatalf("doctor did not detect drift: %#v", result)
	}
	foundHashError := false
	for _, check := range result.Checks {
		if check.Code == "content_hash" && check.Severity == DiagnosticError {
			foundHashError = true
		}
	}
	if !foundHashError {
		t.Fatalf("doctor checks missing content_hash error: %#v", result.Checks)
	}
}

func TestStoreDoctorChecksDeclaredRequires_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "doctor-deps")
	writeSkill(t, filepath.Join(source, SkillFileName), `---
name: Doctor Deps
description: Diagnose declared dependencies.
requires:
  mcp:
    - docs
  env:
    - COHORT_DOCTOR_TOKEN
  commands:
    - cohort-test-command
---

# Doctor Deps
`)
	projectRoot := t.TempDir()
	home := t.TempDir()
	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "cohort-test-command")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COHORT_DOCTOR_TOKEN", "")

	mcpStore := mcp.NewStore(projectRoot)
	mcpStore.HomeDir = func() (string, error) { return home, nil }
	if err := mcpStore.Add(mcp.ScopeProject, mcp.ServerConfig{
		Name: "docs",
		Type: mcp.TransportHTTP,
		URL:  "https://example.com/mcp",
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(projectRoot, home)
	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	result, err := store.Doctor("doctor-deps")
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCount() != 0 {
		t.Fatalf("doctor result has errors: %#v", result.Checks)
	}
	for _, code := range []string{"requires_env", "requires_command", "requires_mcp"} {
		if !hasDiagnostic(result.Checks, code, DiagnosticOK) {
			t.Fatalf("doctor checks missing ok %s: %#v", code, result.Checks)
		}
	}
}

func TestStoreDoctorReportsMissingRequires_BitsUT(t *testing.T) {
	source := filepath.Join(t.TempDir(), "doctor-missing-deps")
	writeSkill(t, filepath.Join(source, SkillFileName), `---
name: Doctor Missing Deps
description: Diagnose missing dependencies.
requires:
  mcp:
    - missing-docs
  env:
    - COHORT_MISSING_DOCTOR_TOKEN
  commands:
    - cohort-definitely-missing-command
---

# Doctor Missing Deps
`)
	projectRoot := t.TempDir()
	store := NewStore(projectRoot, t.TempDir())
	if _, err := Install(context.Background(), InstallOptions{Source: source, ProjectRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COHORT_MISSING_DOCTOR_TOKEN", "")
	os.Unsetenv("COHORT_MISSING_DOCTOR_TOKEN")

	result, err := store.Doctor("doctor-missing-deps")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"requires_env", "requires_command", "requires_mcp"} {
		if !hasDiagnostic(result.Checks, code, DiagnosticError) {
			t.Fatalf("doctor checks missing error %s: %#v", code, result.Checks)
		}
	}
	if result.ErrorCount() < 3 {
		t.Fatalf("error count = %d, checks = %#v", result.ErrorCount(), result.Checks)
	}
}

func hasDiagnostic(checks []Diagnostic, code string, severity DiagnosticSeverity) bool {
	for _, check := range checks {
		if check.Code == code && check.Severity == severity {
			return true
		}
	}
	return false
}

func newGitSkillRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "cohert@example.com")
	runGit(t, repo, "config", "user.name", "Cohert Test")
	return repo
}

func gitCommitAll(t *testing.T, repo, message string) string {
	t.Helper()
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", message)
	out := runGit(t, repo, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

func runGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, strings.TrimSpace(string(output)))
	}
	return string(output)
}
