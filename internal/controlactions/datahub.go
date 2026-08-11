package controlactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cohort/internal/controlplane"
	"cohort/internal/delivery"
	"cohort/internal/evaluation"
	"cohort/internal/evolution"
	"cohort/internal/hermes"
	"cohort/internal/session"
	"cohort/internal/traceview"
)

const dataHubRefreshInterval = 3 * time.Second

type ProjectDataHub struct {
	projectRoot string
	indexPath   string
	now         func() time.Time

	mu       sync.RWMutex
	sources  []controlplane.SourceHealth
	lastScan time.Time
}

type persistedDataIndex struct {
	Version   int                         `json:"version"`
	Project   string                      `json:"project"`
	ScannedAt time.Time                   `json:"scanned_at"`
	Sources   []controlplane.SourceHealth `json:"sources"`
}

func NewProjectDataHub(projectRoot string) (*ProjectDataHub, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(projectRoot))
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("project data hub root must be a directory")
	}
	hub := &ProjectDataHub{
		projectRoot: absolute,
		indexPath:   filepath.Join(absolute, ".cohort", "control", "index-v1.json"),
		now:         func() time.Time { return time.Now().UTC() },
	}
	hub.loadCachedIndex()
	return hub, nil
}

func (h *ProjectDataHub) Sources(ctx context.Context, force bool) ([]controlplane.SourceHealth, error) {
	h.mu.RLock()
	cached := append([]controlplane.SourceHealth(nil), h.sources...)
	fresh := !h.lastScan.IsZero() && h.now().Sub(h.lastScan) < dataHubRefreshInterval
	h.mu.RUnlock()
	if !force && fresh {
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scannedAt := h.now()
	sources := []controlplane.SourceHealth{
		h.scanSessions(scannedAt),
		h.scanEvaluations(scannedAt),
		h.scanDeliveries(scannedAt),
		h.scanHermes(scannedAt),
		h.scanTraces(scannedAt),
		h.scanReflections(scannedAt),
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Kind < sources[j].Kind })
	h.mu.Lock()
	h.sources = append([]controlplane.SourceHealth(nil), sources...)
	h.lastScan = scannedAt
	h.mu.Unlock()
	if err := h.persist(sources, scannedAt); err != nil {
		return nil, fmt.Errorf("persist project data index: %w", err)
	}
	return sources, nil
}

func (h *ProjectDataHub) scanSessions(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("sessions", "Agent Sessions", session.DefaultRootDir, scannedAt)
	items, err := session.NewStore(filepath.Join(h.projectRoot, session.DefaultRootDir)).List()
	if err != nil {
		return sourceFailure(health, err)
	}
	health.Count = len(items)
	for _, item := range items {
		health.UpdatedAt = laterTime(health.UpdatedAt, item.Session.UpdatedAt)
	}
	return completeSource(health)
}

func (h *ProjectDataHub) scanEvaluations(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("evaluations", "Agent Evaluations", filepath.Join(".cohort", "evals"), scannedAt)
	items, err := evaluation.NewStore(h.projectRoot).ListResults()
	if err != nil {
		return sourceFailure(health, err)
	}
	health.Count = len(items)
	for _, item := range items {
		health.UpdatedAt = laterTime(health.UpdatedAt, item.FinishedAt)
	}
	return completeSource(health)
}

func (h *ProjectDataHub) scanDeliveries(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("deliveries", "Deliveries", filepath.Join(".cohort", "deliveries"), scannedAt)
	items, err := delivery.NewStore(h.projectRoot).List()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return sourceFailure(health, err)
	}
	health.Count = len(items)
	for _, item := range items {
		health.UpdatedAt = laterTime(health.UpdatedAt, item.UpdatedAt)
	}
	return completeSource(health)
}

func (h *ProjectDataHub) scanHermes(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("hermes", "Hermes Actions", filepath.Join(".cohort", "hermes"), scannedAt)
	queue, err := hermes.NewStore(h.projectRoot).LoadQueue()
	if err != nil {
		return sourceFailure(health, err)
	}
	health.Count = len(queue.Actions)
	health.UpdatedAt = queue.UpdatedAt
	for _, item := range queue.Actions {
		health.UpdatedAt = laterTime(health.UpdatedAt, item.LastSeenAt)
	}
	return completeSource(health)
}

func (h *ProjectDataHub) scanTraces(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("traces", "Causal Traces", session.DefaultRootDir, scannedAt)
	root := filepath.Join(h.projectRoot, session.DefaultRootDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return completeSource(health)
	}
	if err != nil {
		return sourceFailure(health, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), traceview.ObservationLogFileName)
		info, statErr := os.Stat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return sourceFailure(health, statErr)
		}
		health.Count++
		health.UpdatedAt = laterTime(health.UpdatedAt, info.ModTime())
	}
	return completeSource(health)
}

func (h *ProjectDataHub) scanReflections(scannedAt time.Time) controlplane.SourceHealth {
	health := h.baseHealth("reflections", "Reflection Queue", filepath.Join(".cohort", "reflection"), scannedAt)
	queue := evolution.NewReflectionQueue(h.projectRoot)
	if _, err := os.Stat(queue.RootDir); errors.Is(err, os.ErrNotExist) {
		return completeSource(health)
	} else if err != nil {
		return sourceFailure(health, err)
	}
	status, err := queue.Status()
	if err != nil {
		return sourceFailure(health, err)
	}
	health.Count = status.Pending + status.Running + status.Dead
	health.UpdatedAt = status.LastUpdatedAt
	return completeSource(health)
}

func (h *ProjectDataHub) baseHealth(kind, label, relativePath string, scannedAt time.Time) controlplane.SourceHealth {
	return controlplane.SourceHealth{
		Kind: kind, Label: label, State: controlplane.SourceEmpty,
		RelativePath: filepath.ToSlash(relativePath), ScannedAt: scannedAt,
	}
}

func completeSource(health controlplane.SourceHealth) controlplane.SourceHealth {
	if health.Count > 0 {
		health.State = controlplane.SourceReady
	} else {
		health.State = controlplane.SourceEmpty
	}
	return health
}

func sourceFailure(health controlplane.SourceHealth, err error) controlplane.SourceHealth {
	health.State = controlplane.SourceError
	health.ErrorCode = "source_read_failed"
	health.Error = err.Error()
	return health
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func (h *ProjectDataHub) loadCachedIndex() {
	data, err := os.ReadFile(h.indexPath)
	if err != nil {
		return
	}
	var index persistedDataIndex
	if json.Unmarshal(data, &index) != nil || index.Version != 1 || index.Project != h.projectRoot {
		return
	}
	for item := range index.Sources {
		index.Sources[item].State = controlplane.SourceStale
	}
	h.sources = append([]controlplane.SourceHealth(nil), index.Sources...)
}

func (h *ProjectDataHub) persist(sources []controlplane.SourceHealth, scannedAt time.Time) error {
	index := persistedDataIndex{
		Version: 1, Project: h.projectRoot, ScannedAt: scannedAt,
		Sources: sources,
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(h.indexPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".index-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, h.indexPath)
}
