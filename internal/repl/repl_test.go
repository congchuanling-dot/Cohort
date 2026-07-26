package repl

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/app"
	"cohert/internal/contextmgr"
	"cohert/internal/evolution"
	"cohert/internal/llm"
	"cohert/internal/mcp"
	"cohert/internal/session"
	"cohert/internal/skill"
)

type fakeClient struct {
	// calls 记录测试过程中模型客户端被调用的次数。
	calls    int
	requests []llm.ChatRequest
}

func (c *fakeClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.calls++
	c.requests = append(c.requests, req)
	out := make(chan llm.Event, 2)
	out <- llm.Event{Type: llm.EventText, Text: "ok"}
	out <- llm.Event{Type: llm.EventDone, Response: &llm.Response{Content: "ok"}}
	close(out)
	return out, nil
}

type fakeTools struct{}

func (fakeTools) Schemas() []llm.ToolSchema {
	return []llm.ToolSchema{
		{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "file_read",
				Description: "read file",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}
}

func (fakeTools) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	return agent.Outcome{}, nil
}

func TestStartHandlesSlashCommandsLocally(t *testing.T) {
	// slash 命令必须由 REPL 本地处理，不能发给模型。
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	err := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/model\n/tools\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"Cohert", "model:", "tools:", "file_read", "bye"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStartHandlesMCPCommandsWithoutDefaultServer(t *testing.T) {
	// /mcp 只能读取调用方注入的配置；空配置不能偷偷拉起任何默认 MCP。
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}
	mcpStore := mcp.NewStore(t.TempDir())

	var out bytes.Buffer
	err := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		MCPStore:     &mcpStore,
		In:           strings.NewReader("/mcp list\n/mcp status\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	if got := strings.Count(out.String(), "no MCP servers configured"); got != 2 {
		t.Fatalf("empty MCP output count = %d, want 2:\n%s", got, out.String())
	}
}

func TestStartPrintsCommandPaletteForSlashInNonTerminal(t *testing.T) {
	// 非真实终端里无法打开上下键选择菜单，因此 "/" 会退化为文本命令面板。
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	err := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if !strings.Contains(output, "Slash commands") {
		t.Fatalf("output does not contain command palette:\n%s", output)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
}

func TestStartResumesSessionWithSlashCommand(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("old task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendHistory(sess.ID, llm.Message{Role: llm.RoleUser, Content: "旧问题"}); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{
		Client: &fakeClient{},
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: store,
		In:           strings.NewReader("/resume " + sess.ID + "\n/session\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if runner.SessionID() != sess.ID {
		t.Fatalf("session id = %q, want %q", runner.SessionID(), sess.ID)
	}
	if runner.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", runner.HistoryLen())
	}
	output := out.String()
	if !strings.Contains(output, "resumed session "+sess.ID) {
		t.Fatalf("output does not contain resume message:\n%s", output)
	}
	if !strings.Contains(output, "messages: 1") {
		t.Fatalf("output does not contain session message count:\n%s", output)
	}
}

func TestStartCompactGeneratesSessionMemory_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:       client,
		Tools:        fakeTools{},
		SessionStore: &store,
	}
	runner.ResumeSession(sess.ID, []llm.Message{
		{Role: llm.RoleUser, Content: "我要继续做 session memory"},
	})

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: store,
		In:           strings.NewReader("/compact\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 1 {
		t.Fatalf("model calls = %d, want 1", client.calls)
	}
	memoryPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryFileName)
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("memory content = %q, want ok", string(data))
	}
	output := out.String()
	for _, want := range []string{"compact:", "status: updated memory.md", "path: " + memoryPath} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStartFullCompactGeneratesCompactSummary_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("full compact task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:       client,
		Tools:        fakeTools{},
		SessionStore: &store,
	}
	runner.ResumeSession(sess.ID, []llm.Message{
		{Role: llm.RoleUser, Content: "我要继续做 full compact"},
	})

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: store,
		In:           strings.NewReader("/full-compact\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 1 {
		t.Fatalf("model calls = %d, want 1", client.calls)
	}
	compactPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.CompactSummaryFileName)
	data, err := os.ReadFile(compactPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("compact content = %q, want ok", string(data))
	}
	output := out.String()
	for _, want := range []string{"full compact:", "status: updated compact.md", "path: " + compactPath} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStartPrintsSessionMemory_BitsUT(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.Create("memory task", "/tmp/project", "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	memoryPath := filepath.Join(store.SessionDir(sess.ID), contextmgr.SessionMemoryFileName)
	if err := os.WriteFile(memoryPath, []byte("# Session Memory\n\n- stable facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &agent.Runner{
		Client:       &fakeClient{},
		Tools:        fakeTools{},
		SessionStore: &store,
	}
	runner.ResumeSession(sess.ID, []llm.Message{{Role: llm.RoleUser, Content: "继续"}})

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: store,
		In:           strings.NewReader("/memory\n/session memory\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	output := out.String()
	for _, want := range []string{"memory:", "path: " + memoryPath, "# Session Memory", "stable facts"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStartPrintsNoSessionMemory_BitsUT(t *testing.T) {
	runner := &agent.Runner{
		Client: &fakeClient{},
		Tools:  fakeTools{},
	}

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       testConfig(),
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/memory\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if !strings.Contains(out.String(), "status: no active session") {
		t.Fatalf("output does not contain no active session:\n%s", out.String())
	}
}

func TestStartHandlesSOPPromotionCommandsLocally_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	manager := evolution.NewManager(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, "sops"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, evolution.SOPIndexPath), []byte("# SOP Index\n\n## Rules\n\n- Read SOPs on demand.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := evolution.Candidate{
		Type:             "project_lesson",
		Target:           manager.ProjectMemoryPath(),
		Scene:            "Local SOP promotion",
		TriggerKeywords:  []string{"sop", "promote"},
		Lesson:           "SOP promotion commands should stay local and require explicit index confirmation.",
		RecommendedSteps: []string{"list candidates", "promote candidate", "confirm index update"},
		PromoteToSOP:     true,
		SOPTitle:         "Local SOP Promotion",
		SOPPath:          "sops/local_sop_promotion.md",
		EvidenceIDs:      []string{"tool:1:0"},
		Action:           "append",
	}
	if _, err := manager.ApplyCandidate(candidate, []evolution.Evidence{{ID: "tool:1:0", Verified: true}}, "session-1"); err != nil {
		t.Fatal(err)
	}
	candidates, err := manager.ListSOPCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client: client,
		Tools:  fakeTools{},
	}
	cfg := testConfig()
	cfg.Workspace = workspace

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/sop candidates\n/sop promote " + candidates[0].ID + " --confirm-index\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"sop candidates:", candidates[0].ID, "sop promote:", "index:     updated"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
	sopData, err := os.ReadFile(filepath.Join(workspace, "sops", "local_sop_promotion.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sopData), "promoted_from: "+candidates[0].ID) {
		t.Fatalf("promoted SOP missing source id:\n%s", sopData)
	}
	indexData, err := os.ReadFile(filepath.Join(workspace, evolution.SOPIndexPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(indexData), "sops/local_sop_promotion.md") ||
		!strings.Contains(string(indexData), "explicit slash command") {
		t.Fatalf("index was not updated with confirmation:\n%s", indexData)
	}
}

func TestStartHandlesSkillCommandsLocally_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	skillPath := filepath.Join(workspace, ".cohort", "skills", "go-test", skill.SkillFileName)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("# Go Test\n\nRun focused tests.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:     client,
		Tools:      fakeTools{},
		SkillStore: store,
	}
	cfg := testConfig()
	cfg.Workspace = workspace

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/skill list\n/skill show project/go-test\n/skill doctor project/go-test\n/skill reload\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"skills:", "project/go-test", "skill:", "Run focused tests.", "skill doctor project/go-test", "skills reloaded: 1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
	if !strings.Contains(runner.SystemPrompt, "[Skill Index]") {
		t.Fatalf("system prompt was not refreshed:\n%s", runner.SystemPrompt)
	}
}

func TestStartRunsSkillByCommandAndInvocableAlias_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	skillPath := filepath.Join(workspace, ".cohort", "skills", "commit", skill.SkillFileName)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(`---
name: commit
description: Commit workflow.
user-invocable: true
argument-hint: "[file]"
---

# Commit
`), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:     client,
		Tools:      fakeTools{},
		SkillStore: store,
	}
	cfg := testConfig()
	cfg.Workspace = workspace

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/skill run commit README.md\n/commit docs/usage.md\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2; output:\n%s", client.calls, out.String())
	}
	for index, want := range []string{"README.md", "docs/usage.md"} {
		messages := client.requests[index].Messages
		var task string
		for _, message := range messages {
			if message.Role == llm.RoleUser && strings.Contains(message.Content, "project/commit") {
				task = message.Content
			}
		}
		if !strings.Contains(task, want) || !strings.Contains(task, "skill_read") {
			t.Fatalf("request %d task = %q", index, task)
		}
	}
}

func TestStartDryRunsSkillInstallLocally_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(t.TempDir(), "repl-skill")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, skill.SkillFileName), []byte("# REPL Skill\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:     client,
		Tools:      fakeTools{},
		SkillStore: store,
	}
	cfg := testConfig()
	cfg.Workspace = workspace

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/skill install --dry-run --name repl-skill " + source + "\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"dry-run skill project/repl-skill", "dry_run:     true"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, ".cohort", "skills", "repl-skill")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote skill directory or stat failed differently: %v", err)
	}
}

func TestStartChecksSkillUpdateLocally_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(t.TempDir(), "check-skill")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, skill.SkillFileName), []byte("# First\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Install(context.Background(), skill.InstallOptions{Source: source, ProjectRoot: workspace}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, skill.SkillFileName), []byte("# Second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := skill.NewStore(workspace, t.TempDir())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	runner := &agent.Runner{
		Client:     client,
		Tools:      fakeTools{},
		SkillStore: store,
	}
	cfg := testConfig()
	cfg.Workspace = workspace

	var out bytes.Buffer
	startErr := Start(context.Background(), Options{
		Config:       cfg,
		Runner:       runner,
		SessionStore: session.NewStore(t.TempDir()),
		In:           strings.NewReader("/skill update --check check-skill\n/exit\n"),
		Out:          &out,
		Err:          &out,
	})
	if startErr != nil {
		t.Fatal(startErr)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
	output := out.String()
	for _, want := range []string{"skill update check project/check-skill", "status:         update-available"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, output)
		}
	}
	data, err := os.ReadFile(filepath.Join(workspace, ".cohort", "skills", "check-skill", skill.SkillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "First") {
		t.Fatalf("check wrote installed skill: %s", data)
	}
}

func testConfig() app.Config {
	return app.Config{
		Language:  "zh",
		Workspace: "./workspace",
		LogDir:    "./temp/model_responses",
		MaxTurns:  100,
		LLM: app.LLMConfig{
			Provider: "openai",
			Name:     "deepseek",
			APIBase:  "https://api.deepseek.com",
			Model:    "deepseek-v4-pro",
			Stream:   true,
		},
	}
}
