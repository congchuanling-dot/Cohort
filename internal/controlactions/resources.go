package controlactions

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cohort/internal/delivery"
	"cohort/internal/evaluation"
	"cohort/internal/hermes"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

func ResourceProvider(ctx context.Context, projectRoot string, resource string, query url.Values) (any, error) {
	switch resource {
	case "deliveries":
		return deliveryResource(projectRoot, strings.TrimSpace(query.Get("id")))
	case "hermes":
		return hermesResource(projectRoot)
	case "evaluations":
		return evaluationResource(projectRoot)
	case "traces":
		return traceResource(projectRoot, strings.TrimSpace(query.Get("session_id")), strings.TrimSpace(query.Get("run_id")))
	default:
		return nil, os.ErrNotExist
	}
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
		return map[string]any{"sessions": sessions}, err
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
