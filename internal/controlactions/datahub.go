package controlactions

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cohort/internal/app"
	"cohort/internal/capability"
	"cohort/internal/controlplane"
	"cohort/internal/delivery"
	"cohort/internal/evaluation"
	"cohort/internal/evolution"
	"cohort/internal/hermes"
	"cohort/internal/mcp"
	"cohort/internal/session"
	"cohort/internal/skill"
	"cohort/internal/traceview"
)

const dataHubRefreshInterval = 3 * time.Second

type ProjectDataHub struct {
	projectRoot string
	configPath  string
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

func NewProjectDataHub(projectRoot string, configPaths ...string) (*ProjectDataHub, error) {
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
	if len(configPaths) > 0 {
		hub.configPath = strings.TrimSpace(configPaths[0])
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

func (h *ProjectDataHub) ListEntities(ctx context.Context, kind controlplane.EntityKind, query url.Values) ([]controlplane.EntityDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entities, err := h.loadEntities(kind)
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(query.Get("q")))
	statuses := map[string]bool{}
	for _, status := range query["status"] {
		for _, part := range strings.Split(status, ",") {
			if part = strings.TrimSpace(part); part != "" {
				statuses[part] = true
			}
		}
	}
	limit := 100
	if parsed, parseErr := strconv.Atoi(query.Get("limit")); parseErr == nil && parsed > 0 {
		limit = min(parsed, 500)
	}
	filtered := make([]controlplane.EntityDescriptor, 0, min(len(entities), limit))
	for _, entity := range entities {
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
			entity.ID, entity.Title, entity.Subtitle, entity.SearchText,
		}, " ")), search) {
			continue
		}
		if len(statuses) > 0 && !statuses[entity.Status] {
			continue
		}
		filtered = append(filtered, entity)
		if len(filtered) == limit {
			break
		}
	}
	return filtered, nil
}

func (h *ProjectDataHub) GetEntity(ctx context.Context, kind controlplane.EntityKind, id string) (controlplane.EntityDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return controlplane.EntityDescriptor{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return controlplane.EntityDescriptor{}, os.ErrNotExist
	}
	entities, err := h.loadEntities(kind)
	if err != nil {
		return controlplane.EntityDescriptor{}, err
	}
	for _, entity := range entities {
		if entity.ID == id {
			return entity, nil
		}
	}
	return controlplane.EntityDescriptor{}, os.ErrNotExist
}

func (h *ProjectDataHub) loadEntities(kind controlplane.EntityKind) ([]controlplane.EntityDescriptor, error) {
	switch kind {
	case controlplane.EntitySession:
		return h.sessionEntities()
	case controlplane.EntityEvalRun:
		return h.evalEntities()
	case controlplane.EntityDelivery:
		return h.deliveryEntities()
	case controlplane.EntityHermesAction:
		return h.hermesEntities()
	case controlplane.EntitySkill:
		return h.skillEntities()
	case controlplane.EntityCapability:
		return h.capabilityEntities()
	case controlplane.EntityMCPServer:
		return h.mcpEntities()
	case controlplane.EntityModelProfile:
		return h.profileEntities()
	default:
		return nil, fmt.Errorf("unknown entity kind %q", kind)
	}
}

func (h *ProjectDataHub) sessionEntities() ([]controlplane.EntityDescriptor, error) {
	roots := []string{
		filepath.Join(h.projectRoot, session.DefaultRootDir),
		evaluation.NewStore(h.projectRoot).SessionsDir(),
	}
	seen := map[string]bool{}
	var result []controlplane.EntityDescriptor
	for _, root := range roots {
		items, err := session.NewStore(root).List()
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if seen[item.Session.ID] {
				continue
			}
			seen[item.Session.ID] = true
			title := strings.TrimSpace(item.Session.Title)
			if title == "" {
				title = "Untitled session"
			}
			result = append(result, entityDescriptor(
				controlplane.EntitySession, item.Session.ID, title,
				fmt.Sprintf("%s · %d messages", item.Session.Model, item.MessageCount),
				"saved", item.Session.UpdatedAt,
				[]string{item.Session.Model, item.Session.CWD},
				[]controlplane.ContextAction{
					contextAction("agent.continue", "继续 Session", controlplane.RiskExecute, true, ""),
				},
			))
		}
	}
	return result, nil
}

func (h *ProjectDataHub) evalEntities() ([]controlplane.EntityDescriptor, error) {
	items, err := evaluation.NewStore(h.projectRoot).ListResults()
	if err != nil {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(items))
	for _, item := range items {
		status := "failed"
		if item.Gate == nil && item.FailedCases == 0 || item.Gate != nil && item.Gate.Passed {
			status = "passed"
		}
		title := item.SuiteName
		if title == "" {
			title = item.SuiteID
		}
		result = append(result, entityDescriptor(
			controlplane.EntityEvalRun, item.RunID, title,
			fmt.Sprintf("%s · %.1f%% pass · score %.1f", item.Model, item.PassRate, item.Score),
			status, item.FinishedAt,
			[]string{item.SuiteID, item.Profile, item.Model},
			nil,
		))
	}
	return result, nil
}

func (h *ProjectDataHub) deliveryEntities() ([]controlplane.EntityDescriptor, error) {
	items, err := delivery.NewStore(h.projectRoot).List()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(items))
	for _, item := range items {
		result = append(result, entityDescriptor(
			controlplane.EntityDelivery, item.ID, item.Requirement,
			"base "+shortValue(item.BaseCommit, 12), string(item.Status), item.UpdatedAt,
			[]string{item.BaseCommit, item.Error},
			deliveryContextActions(string(item.Status)),
		))
	}
	return result, nil
}

func (h *ProjectDataHub) hermesEntities() ([]controlplane.EntityDescriptor, error) {
	queue, err := hermes.NewStore(h.projectRoot).LoadQueue()
	if err != nil {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(queue.Actions))
	for _, item := range queue.Actions {
		enabled := item.Status != hermes.QueueStatusAcknowledged && item.Status != hermes.QueueStatusDismissed
		result = append(result, entityDescriptor(
			controlplane.EntityHermesAction, item.ID, item.Title,
			item.Category+" · "+item.Severity, item.Status, item.LastSeenAt,
			[]string{item.Detail, item.Evidence, item.RunID, item.CaseID},
			[]controlplane.ContextAction{
				contextAction("hermes.action.acknowledge", "确认 Action", controlplane.RiskExecute, enabled, terminalActionReason(enabled)),
				contextAction("hermes.action.dismiss", "忽略 Action", controlplane.RiskConfirm, enabled, terminalActionReason(enabled)),
			},
		))
	}
	return result, nil
}

func (h *ProjectDataHub) skillEntities() ([]controlplane.EntityDescriptor, error) {
	store := skill.NewStore(h.projectRoot, "")
	if err := store.Reload(); err != nil {
		return nil, err
	}
	items := store.Skills()
	result := make([]controlplane.EntityDescriptor, 0, len(items))
	for _, item := range items {
		result = append(result, entityDescriptor(
			controlplane.EntitySkill, item.ID, item.Name, item.Description,
			string(item.Scope), time.Time{}, []string{item.Alias, item.Path},
			[]controlplane.ContextAction{
				contextAction("skill.doctor", "诊断 Skill", controlplane.RiskRead, true, ""),
				contextAction("skill.update.check", "检查更新", controlplane.RiskRead, true, ""),
				contextAction("skill.uninstall", "卸载 Skill", controlplane.RiskDanger, item.Scope != skill.ScopeBuiltin, "内置 Skill 不可卸载"),
			},
		))
	}
	return result, nil
}

func (h *ProjectDataHub) capabilityEntities() ([]controlplane.EntityDescriptor, error) {
	registry, err := capability.NewStore(h.projectRoot).Load()
	if err != nil {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(registry.Capabilities))
	for _, item := range registry.Capabilities {
		status := string(item.Status)
		result = append(result, entityDescriptor(
			controlplane.EntityCapability, item.ID, item.ID, item.Type+" · "+item.Risk,
			status, item.UpdatedAt, item.Triggers,
			[]controlplane.ContextAction{
				contextAction("capability.doctor", "诊断 Capability", controlplane.RiskRead, true, ""),
				contextAction("capability.verify", "验证 Capability", controlplane.RiskExecute, status != "available", "Capability 已可用"),
				contextAction("capability.promote", "晋级 Capability", controlplane.RiskConfirm, status != "available", "Capability 已晋级"),
				contextAction("capability.disable", "禁用 Capability", controlplane.RiskDanger, status != "disabled", "Capability 已禁用"),
			},
		))
	}
	return result, nil
}

func (h *ProjectDataHub) mcpEntities() ([]controlplane.EntityDescriptor, error) {
	entries, err := mcp.NewStore(h.projectRoot).LoadEffectiveWithScopes()
	if err != nil {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(entries))
	for _, entry := range entries {
		server := entry.Server
		result = append(result, entityDescriptor(
			controlplane.EntityMCPServer, server.Name, server.Name,
			string(entry.Scope)+" · "+server.Type, "configured", time.Time{},
			[]string{server.Command, server.URL},
			[]controlplane.ContextAction{
				contextAction("mcp.probe", "探测 MCP", controlplane.RiskRead, true, ""),
				contextAction("mcp.remove", "移除 MCP", controlplane.RiskDanger, true, ""),
			},
		))
	}
	return result, nil
}

func (h *ProjectDataHub) profileEntities() ([]controlplane.EntityDescriptor, error) {
	if h.configPath == "" {
		return nil, errors.New("config path is unavailable")
	}
	cfg, err := app.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	result := make([]controlplane.EntityDescriptor, 0, len(cfg.LLM.Profiles))
	for id, item := range cfg.LLM.Profiles {
		status := "inactive"
		enabled := true
		if id == cfg.LLM.ActiveProfile {
			status = "active"
			enabled = false
		}
		result = append(result, entityDescriptor(
			controlplane.EntityModelProfile, id, firstNonEmptyValue(item.Name, id),
			item.Provider+" · "+item.Model, status, time.Time{},
			[]string{item.APIBase},
			[]controlplane.ContextAction{
				contextAction("settings.model.activate", "切换到此模型", controlplane.RiskConfirm, enabled, "该模型已激活"),
			},
		))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func entityDescriptor(kind controlplane.EntityKind, id, title, subtitle, status string, updatedAt time.Time, search []string, actions []controlplane.ContextAction) controlplane.EntityDescriptor {
	versionInput := strings.Join([]string{string(kind), id, status, updatedAt.UTC().Format(time.RFC3339Nano), strings.Join(search, "\x00")}, "\x00")
	sum := sha256.Sum256([]byte(versionInput))
	return controlplane.EntityDescriptor{
		Kind: kind, ID: id, Title: title, Subtitle: subtitle, Status: status,
		UpdatedAt: updatedAt, SearchText: strings.Join(search, " "),
		Version: fmt.Sprintf("sha256:%x", sum[:12]), Actions: actions,
	}
}

func contextAction(id, label string, risk controlplane.RiskLevel, enabled bool, disabledReason string) controlplane.ContextAction {
	if enabled {
		disabledReason = ""
	}
	return controlplane.ContextAction{
		ActionID: id, Label: label, Risk: risk, Enabled: enabled, DisabledReason: disabledReason,
	}
}

func deliveryContextActions(status string) []controlplane.ContextAction {
	canIntegrate := status == "planned" || status == "building" || status == "revising"
	canApprove := status == "ready_for_review"
	canMerge := status == "approved"
	canRecover := status == "merging" || status == "merged_unverified"
	terminal := status == "verified" || status == "cancelled"
	return []controlplane.ContextAction{
		contextAction("delivery.review", "查看 Review", controlplane.RiskRead, true, ""),
		contextAction("delivery.integrate", "执行集成验证", controlplane.RiskExecute, canIntegrate, "当前状态不可集成"),
		contextAction("delivery.approve", "批准 Delivery", controlplane.RiskConfirm, canApprove, "尚未达到待审批状态"),
		contextAction("delivery.merge", "合并并复验", controlplane.RiskDanger, canMerge, "Delivery 尚未批准"),
		contextAction("delivery.recover", "恢复合并", controlplane.RiskConfirm, canRecover, "没有可恢复的合并"),
		contextAction("delivery.cancel", "取消 Delivery", controlplane.RiskDanger, !terminal, "Delivery 已结束"),
	}
}

func terminalActionReason(enabled bool) string {
	if enabled {
		return ""
	}
	return "Action 已处理"
}

func shortValue(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
