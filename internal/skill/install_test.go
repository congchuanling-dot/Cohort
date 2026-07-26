package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
