package controlactions

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cohort/internal/app"
	"cohort/internal/capability"
	"cohort/internal/delivery"
	"cohort/internal/evaluation"
	"cohort/internal/hermes"
	"cohort/internal/lsp"
	"cohort/internal/mcp"
	"cohort/internal/plugin"
	"cohort/internal/session"
	"cohort/internal/skill"
	"cohort/internal/traceview"
)

func NewResourceProvider(configPath string) func(context.Context, string, string, url.Values) (any, error) {
	return func(ctx context.Context, projectRoot string, resource string, query url.Values) (any, error) {
		return resourceProvider(ctx, projectRoot, configPath, resource, query)
	}
}

func resourceProvider(ctx context.Context, projectRoot string, configPath string, resource string, query url.Values) (any, error) {
	switch resource {
	case "deliveries":
		return deliveryResource(projectRoot, strings.TrimSpace(query.Get("id")))
	case "hermes":
		return hermesResource(projectRoot)
	case "evaluations":
		return evaluationResource(projectRoot)
	case "traces":
		return traceResource(projectRoot, strings.TrimSpace(query.Get("session_id")), strings.TrimSpace(query.Get("run_id")))
	case "sessions":
		return sessionResource(projectRoot, strings.TrimSpace(query.Get("id")))
	case "capabilities":
		return capabilityResource(projectRoot)
	case "skills":
		return skillResource(projectRoot, strings.TrimSpace(query.Get("id")))
	case "mcp":
		return mcpResource(projectRoot)
	case "lsp":
		return lspResource(ctx, projectRoot)
	case "plugins":
		return pluginResource(projectRoot)
	case "settings":
		return settingsResource(configPath)
	default:
		return nil, os.ErrNotExist
	}
}

func sessionResource(projectRoot string, id string) (any, error) {
	store := session.NewStore(filepath.Join(projectRoot, session.DefaultRootDir))
	if id == "" {
		summaries, err := store.List()
		if errors.Is(err, os.ErrNotExist) {
			summaries = nil
			err = nil
		}
		return map[string]any{"sessions": flattenSessions(summaries)}, err
	}
	meta, err := store.LoadMeta(id)
	if err != nil {
		return nil, err
	}
	history, err := store.LoadHistory(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"session": meta, "history": history}, nil
}

func capabilityResource(projectRoot string) (any, error) {
	store := capability.NewStore(projectRoot)
	registry, err := store.Load()
	if err != nil {
		return nil, err
	}
	suggestions, err := store.Suggestions()
	if err != nil {
		return nil, err
	}
	dependencies, err := store.LoadDependencies()
	if err != nil {
		return nil, err
	}
	adapters, err := store.ListEnabledAdapters()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"registry": registry, "suggestions": suggestions,
		"dependencies": dependencies, "enabled_adapters": adapters,
	}, nil
}

func skillResource(projectRoot string, id string) (any, error) {
	store := skill.NewStore(projectRoot, "")
	if err := store.Reload(); err != nil {
		return nil, err
	}
	if id == "" {
		return map[string]any{"skills": store.Skills()}, nil
	}
	item, err := store.Read(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill": item}, nil
}

func mcpResource(projectRoot string) (any, error) {
	store := mcp.NewStore(projectRoot)
	servers, err := store.LoadEffectiveWithScopes()
	if err != nil {
		return nil, err
	}
	type summary struct {
		Name       string    `json:"name"`
		Scope      mcp.Scope `json:"scope"`
		Type       string    `json:"type"`
		Command    string    `json:"command,omitempty"`
		ArgCount   int       `json:"arg_count,omitempty"`
		URL        string    `json:"url,omitempty"`
		EnvKeys    []string  `json:"env_keys,omitempty"`
		HeaderKeys []string  `json:"header_keys,omitempty"`
	}
	result := make([]summary, 0, len(servers))
	for _, entry := range servers {
		item := summary{
			Name: entry.Server.Name, Scope: entry.Scope, Type: entry.Server.Type,
			Command: entry.Server.Command, ArgCount: len(entry.Server.Args), URL: redactURL(entry.Server.URL),
		}
		for name := range entry.Server.Env {
			item.EnvKeys = append(item.EnvKeys, name)
		}
		for name := range entry.Server.Headers {
			item.HeaderKeys = append(item.HeaderKeys, name)
		}
		sort.Strings(item.EnvKeys)
		sort.Strings(item.HeaderKeys)
		result = append(result, item)
	}
	permissions, err := store.LoadPermissions()
	if err != nil {
		return nil, err
	}
	return map[string]any{"servers": result, "permissions": permissions}, nil
}

func lspResource(ctx context.Context, projectRoot string) (any, error) {
	client := lsp.Diagnostics{Root: projectRoot}
	return map[string]any{
		"doctor":  client.Doctor(ctx, lsp.LanguageAll),
		"servers": lsp.ServerStatuses(projectRoot),
	}, nil
}

func pluginResource(projectRoot string) (any, error) {
	plugins, err := plugin.Discover(projectRoot)
	if err != nil {
		return nil, err
	}
	doctors := make([]plugin.DoctorResult, 0, len(plugins))
	for _, item := range plugins {
		doctors = append(doctors, plugin.Doctor(item))
	}
	return map[string]any{"plugins": plugins, "doctor": doctors}, nil
}

func settingsResource(configPath string) (any, error) {
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	type profile struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		APIBase       string `json:"api_base"`
		APIKeyPresent bool   `json:"api_key_present"`
	}
	profiles := make([]profile, 0, len(cfg.LLM.Profiles))
	for id, item := range cfg.LLM.Profiles {
		profiles = append(profiles, profile{
			ID: id, Name: item.Name, Provider: item.Provider, Model: item.Model,
			APIBase: redactURL(item.APIBase), APIKeyPresent: strings.TrimSpace(item.APIKey) != "",
		})
	}
	sort.Slice(profiles, func(left, right int) bool { return profiles[left].ID < profiles[right].ID })
	return map[string]any{
		"config_path": configPath, "language": cfg.Language, "workspace": cfg.Workspace,
		"max_turns": cfg.MaxTurns, "active_profile": cfg.LLM.ActiveProfile,
		"fallback_profiles": cfg.LLM.FallbackProfiles, "profiles": profiles,
		"tools": cfg.Tools, "reflection": cfg.Reflection, "context": cfg.Context,
	}, nil
}

func redactURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func deliveryResource(projectRoot string, id string) (any, error) {
	store := delivery.NewStore(projectRoot)
	if id == "" {
		items, err := store.List()
		if errors.Is(err, os.ErrNotExist) {
			items = nil
			err = nil
		}
		return map[string]any{"deliveries": items}, err
	}
	item, err := store.Load(id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"delivery": item}
	if contract, graph, loadErr := store.LoadPlan(id); loadErr == nil {
		result["contract"] = contract
		result["graph"] = graph
	}
	if runtime, loadErr := store.LoadRuntime(id); loadErr == nil {
		result["runtime"] = runtime
	}
	if integration, loadErr := store.LoadIntegration(id); loadErr == nil {
		result["integration"] = integration
	}
	if verification, loadErr := store.LoadVerification(id); loadErr == nil {
		result["verification"] = verification
	}
	if revisions, loadErr := store.LoadRevisions(id); loadErr == nil {
		result["revisions"] = revisions
	}
	if approval, loadErr := store.LoadApproval(id); loadErr == nil {
		result["approval"] = approval
	}
	if merge, loadErr := store.LoadMerge(id); loadErr == nil {
		result["merge"] = merge
	}
	return result, nil
}

func hermesResource(projectRoot string) (any, error) {
	store := hermes.NewStore(projectRoot)
	result := map[string]any{}
	if _, statErr := os.Stat(store.StatusPath()); statErr == nil {
		if status, err := store.LoadStatus(); err == nil {
			result["status"] = status
		}
	}
	if queue, err := store.LoadQueue(); err == nil {
		result["actions"] = queue.Actions
	} else {
		result["actions"] = []hermes.QueueAction{}
	}
	if repairs, err := store.LoadRepairs(); err == nil {
		result["repairs"] = repairs.Repairs
	} else {
		result["repairs"] = []hermes.RepairTask{}
	}
	if jobs, err := store.LoadJobs(); err == nil {
		result["jobs"] = jobs.Jobs
	} else {
		result["jobs"] = []hermes.Job{}
	}
	if events, err := store.LoadEvents(100); err == nil {
		result["events"] = events
	} else {
		result["events"] = []hermes.Event{}
	}
	return result, nil
}

func evaluationResource(projectRoot string) (any, error) {
	results, err := evaluation.NewStore(projectRoot).ListResults()
	if errors.Is(err, os.ErrNotExist) {
		results = nil
		err = nil
	}
	if len(results) > 50 {
		results = results[:50]
	}
	return map[string]any{"runs": results}, err
}

func traceResource(projectRoot string, sessionID string, runID string) (any, error) {
	sessionRoot := filepath.Join(projectRoot, session.DefaultRootDir)
	if sessionID == "" {
		sessions, err := session.NewStore(sessionRoot).List()
		if errors.Is(err, os.ErrNotExist) {
			sessions = nil
			err = nil
		}
		return map[string]any{"sessions": flattenSessions(sessions)}, err
	}
	view, err := traceview.LoadSessionRun(sessionRoot, sessionID, runID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"view":    view,
		"summary": view.Summary(),
		"graph":   view.CausalGraph(),
	}, nil
}

func flattenSessions(summaries []session.Summary) []map[string]any {
	result := make([]map[string]any, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, map[string]any{
			"id": summary.Session.ID, "title": summary.Session.Title,
			"cwd": summary.Session.CWD, "model": summary.Session.Model,
			"created_at": summary.Session.CreatedAt, "updated_at": summary.Session.UpdatedAt,
			"message_count": summary.MessageCount,
		})
	}
	return result
}
