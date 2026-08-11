package traceview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cohort/internal/observability"
	"cohort/internal/session"
)

const ObservationLogFileName = "run.log.jsonl"

type RunView struct {
	SessionID string
	RunID     string
	Path      string
	Events    []observability.Event
}

type RunIndex struct {
	SessionID string
	RunID     string
	LastTime  time.Time
}

type Summary struct {
	SessionID             string
	RunID                 string
	Status                string
	StartedAt             time.Time
	FinishedAt            time.Time
	DurationMS            int64
	EventCount            int
	TurnCount             int
	WarningCount          int
	ErrorCount            int
	ContextBuilds         int
	LastFinalTokens       int64
	LastFinalChars        int64
	LastRequestChars      int64
	LastToolSchemaCount   int64
	LastFullSchemaCount   int64
	LastToolRouteMode     string
	LastSchemaBytes       int64
	LastSavedSchemaBytes  int64
	TotalSavedSchemaBytes int64
	AdaptiveRouteTurns    int
	ToolRouteEscalations  int
	LLMCalls              int
	LLMDurationMS         int64
	ToolCalls             int
	ToolFailures          int
	ToolDurationMS        int64
	TotalTokens           int64
	InputTokens           int64
	OutputTokens          int64
	CacheReadTokens       int64
	Timeline              []TimelineItem
	LLMs                  []LLMItem
	Tools                 []ToolItem
	Gaps                  []GapItem
}

type TimelineItem struct {
	Time          time.Time
	OffsetMS      int64
	Turn          int
	EventType     string
	Severity      string
	Summary       string
	SincePrevious int64
}

type LLMItem struct {
	Turn          int
	DurationMS    int64
	ToolCallCount int64
	ContentChars  int64
	RawChars      int64
	TotalTokens   int64
}

type ToolItem struct {
	Turn       int
	Name       string
	Status     string
	DurationMS int64
	ErrorCode  string
}

type GapItem struct {
	FromEvent string
	ToEvent   string
	GapMS     int64
	Turn      int
}

func LoadLatest(root string) (RunView, error) {
	views, err := LoadRecentRuns(root, 1)
	if err != nil {
		return RunView{}, err
	}
	if len(views) == 0 {
		return RunView{}, errors.New("no session with run.log.jsonl found")
	}
	return views[0], nil
}

func LoadRecentRuns(root string, limit int) ([]RunView, error) {
	store := session.NewStore(root)
	candidates, err := listSessionCandidates(store)
	if err != nil {
		return nil, err
	}
	indexes := make([]RunIndex, 0, len(candidates))
	for _, candidate := range candidates {
		sessionIndexes, err := listSessionRuns(root, candidate.ID)
		if err != nil {
			continue
		}
		indexes = append(indexes, sessionIndexes...)
	}
	sort.Slice(indexes, func(i, j int) bool {
		return indexes[i].LastTime.After(indexes[j].LastTime)
	})
	if limit > 0 && len(indexes) > limit {
		indexes = indexes[:limit]
	}
	views := make([]RunView, 0, len(indexes))
	for _, index := range indexes {
		view, err := LoadSessionRun(root, index.SessionID, index.RunID)
		if err != nil {
			continue
		}
		views = append(views, view)
	}
	return views, nil
}

type sessionCandidate struct {
	ID        string
	UpdatedAt time.Time
}

func listSessionCandidates(store session.Store) ([]sessionCandidate, error) {
	entries, err := os.ReadDir(store.RootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	candidates := make([]sessionCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, err := store.LoadMeta(id)
		if err != nil {
			continue
		}
		candidates = append(candidates, sessionCandidate{
			ID:        id,
			UpdatedAt: meta.UpdatedAt,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})
	return candidates, nil
}

func LoadSessionRun(root string, sessionID string, runID string) (RunView, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return RunView{}, errors.New("session id is required")
	}
	path := filepath.Join(session.NewStore(root).SessionDir(sessionID), ObservationLogFileName)
	events, err := readEvents(path)
	if err != nil {
		return RunView{}, err
	}
	selectedRunID := strings.TrimSpace(runID)
	if selectedRunID == "" {
		selectedRunID = latestRunID(events)
	}
	if selectedRunID == "" {
		return RunView{}, fmt.Errorf("no run_id found in %s", path)
	}
	filtered := make([]observability.Event, 0, len(events))
	for _, event := range events {
		if event.RunID == selectedRunID {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) == 0 {
		return RunView{}, fmt.Errorf("run %q not found in session %s", selectedRunID, sessionID)
	}
	return RunView{
		SessionID: sessionID,
		RunID:     selectedRunID,
		Path:      path,
		Events:    filtered,
	}, nil
}

func listSessionRuns(root string, sessionID string) ([]RunIndex, error) {
	path := filepath.Join(session.NewStore(root).SessionDir(sessionID), ObservationLogFileName)
	events, err := readEvents(path)
	if err != nil {
		return nil, err
	}
	byRun := map[string]time.Time{}
	for _, event := range events {
		runID := strings.TrimSpace(event.RunID)
		if runID == "" {
			continue
		}
		if event.Time.After(byRun[runID]) {
			byRun[runID] = event.Time
		}
	}
	indexes := make([]RunIndex, 0, len(byRun))
	for runID, lastTime := range byRun {
		indexes = append(indexes, RunIndex{
			SessionID: sessionID,
			RunID:     runID,
			LastTime:  lastTime,
		})
	}
	sort.Slice(indexes, func(i, j int) bool {
		return indexes[i].LastTime.After(indexes[j].LastTime)
	})
	return indexes, nil
}

func (v RunView) Summary() Summary {
	events := append([]observability.Event(nil), v.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time.Before(events[j].Time)
	})
	summary := Summary{
		SessionID:  v.SessionID,
		RunID:      v.RunID,
		EventCount: len(events),
		Status:     "incomplete",
	}
	turns := map[int]bool{}
	for i, event := range events {
		if i == 0 || (event.EventType == observability.EventRunStarted && summary.StartedAt.IsZero()) {
			summary.StartedAt = event.Time
		}
		if event.Turn > 0 {
			turns[event.Turn] = true
		}
		if event.Severity == observability.SeverityWarn {
			summary.WarningCount++
		}
		if event.Severity == observability.SeverityError {
			summary.ErrorCount++
		}
		summary.applyEvent(event)
		summary.Timeline = append(summary.Timeline, timelineItem(events, i))
		if i > 0 {
			gap := event.Time.Sub(events[i-1].Time).Milliseconds()
			if gap > 0 {
				summary.Gaps = append(summary.Gaps, GapItem{
					FromEvent: string(events[i-1].EventType),
					ToEvent:   string(event.EventType),
					GapMS:     gap,
					Turn:      event.Turn,
				})
			}
		}
	}
	if !summary.StartedAt.IsZero() {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].EventType == observability.EventRunFinished || i == len(events)-1 {
				summary.FinishedAt = events[i].Time
				break
			}
		}
	}
	if summary.DurationMS == 0 && !summary.StartedAt.IsZero() && !summary.FinishedAt.IsZero() {
		summary.DurationMS = summary.FinishedAt.Sub(summary.StartedAt).Milliseconds()
	}
	summary.TurnCount = len(turns)
	sort.Slice(summary.Gaps, func(i, j int) bool {
		return summary.Gaps[i].GapMS > summary.Gaps[j].GapMS
	})
	return summary
}

func (s *Summary) applyEvent(event observability.Event) {
	switch event.EventType {
	case observability.EventRunFinished:
		if status, ok := stringValue(event.Data, "status"); ok {
			s.Status = status
		}
		s.DurationMS = intValue(event.Data, "duration_ms")
		s.applyUsageMap(valueMap(event.Data["usage"]))
	case observability.EventContextBuilt:
		s.ContextBuilds++
		s.LastFinalTokens = intValue(event.Data, "final_tokens")
		s.LastFinalChars = intValue(event.Data, "final_chars")
	case observability.EventToolRouteSelected:
		s.LastToolRouteMode = firstString(event.Data, "mode")
		s.LastFullSchemaCount = intValue(event.Data, "full_schema_count")
		s.LastSchemaBytes = intValue(event.Data, "selected_schema_bytes")
		s.LastSavedSchemaBytes = intValue(event.Data, "saved_schema_bytes")
		s.TotalSavedSchemaBytes += s.LastSavedSchemaBytes
		if s.LastToolRouteMode == "adaptive" {
			s.AdaptiveRouteTurns++
		}
		if s.LastToolRouteMode == "escalating" || boolValue(event.Data, "escalated") {
			s.ToolRouteEscalations++
		}
	case observability.EventLLMRequestStarted:
		s.LastRequestChars = intValue(event.Data, "request_chars")
		s.LastToolSchemaCount = intValue(event.Data, "tool_schema_count")
	case observability.EventLLMResponseFinished:
		item := LLMItem{
			Turn:          event.Turn,
			DurationMS:    intValue(event.Data, "duration_ms"),
			ToolCallCount: intValue(event.Data, "tool_call_count"),
			ContentChars:  intValue(event.Data, "content_chars"),
			RawChars:      intValue(event.Data, "raw_chars"),
		}
		usage := valueMap(event.Data["usage"])
		item.TotalTokens = intValue(usage, "total_tokens")
		s.applyUsageMap(usage)
		s.LLMCalls++
		s.LLMDurationMS += item.DurationMS
		s.LLMs = append(s.LLMs, item)
	case observability.EventToolFinished:
		item := ToolItem{
			Turn:       event.Turn,
			Name:       firstString(event.Data, "tool", "name"),
			Status:     firstStringDefault(event.Data, "status", "unknown"),
			DurationMS: intValue(event.Data, "duration_ms"),
			ErrorCode:  firstString(event.Data, "error_code", "code"),
		}
		s.ToolCalls++
		s.ToolDurationMS += item.DurationMS
		if item.Status != "" && item.Status != "success" && !expectedControlErrorCode(item.ErrorCode) {
			s.ToolFailures++
		}
		s.Tools = append(s.Tools, item)
	}
}

func expectedControlErrorCode(code string) bool {
	switch code {
	case "desktop_action_confirmation_required",
		"computer_execute_plan_confirmation_required",
		"computer_execute_plan_handoff_required",
		"mcp_tool_permission_required":
		return true
	default:
		return false
	}
}

func (s *Summary) applyUsageMap(usage map[string]any) {
	if len(usage) == 0 {
		return
	}
	s.TotalTokens = maxInt64(s.TotalTokens, intValue(usage, "total_tokens"))
	s.InputTokens = maxInt64(s.InputTokens, intValue(usage, "input_tokens"))
	s.OutputTokens = maxInt64(s.OutputTokens, intValue(usage, "output_tokens"))
	s.CacheReadTokens = maxInt64(s.CacheReadTokens, intValue(usage, "cache_read_input_tokens"))
}

func timelineItem(events []observability.Event, index int) TimelineItem {
	event := events[index]
	start := events[0].Time
	item := TimelineItem{
		Time:      event.Time,
		OffsetMS:  event.Time.Sub(start).Milliseconds(),
		Turn:      event.Turn,
		EventType: string(event.EventType),
		Severity:  string(event.Severity),
		Summary:   eventSummary(event),
	}
	if index > 0 {
		item.SincePrevious = event.Time.Sub(events[index-1].Time).Milliseconds()
	}
	return item
}

func eventSummary(event observability.Event) string {
	switch event.EventType {
	case observability.EventContextBuilt:
		return fmt.Sprintf("messages=%d tokens=%d chars=%d", intValue(event.Data, "final_messages"), intValue(event.Data, "final_tokens"), intValue(event.Data, "final_chars"))
	case observability.EventToolRouteSelected:
		return fmt.Sprintf(
			"mode=%s reason=%s tools=%d/%d saved=%dB",
			firstStringDefault(event.Data, "mode", "unknown"),
			firstStringDefault(event.Data, "reason", "unknown"),
			intValue(event.Data, "selected_count"),
			intValue(event.Data, "full_schema_count"),
			intValue(event.Data, "saved_schema_bytes"),
		)
	case observability.EventLLMRequestStarted:
		return fmt.Sprintf("messages=%d tools=%d chars=%d", intValue(event.Data, "message_count"), intValue(event.Data, "tool_schema_count"), intValue(event.Data, "request_chars"))
	case observability.EventLLMResponseFinished:
		return fmt.Sprintf("duration=%dms tool_calls=%d tokens=%d", intValue(event.Data, "duration_ms"), intValue(event.Data, "tool_call_count"), intValue(valueMap(event.Data["usage"]), "total_tokens"))
	case observability.EventToolStarted, observability.EventToolFinished:
		tool := firstString(event.Data, "tool", "name")
		status := firstString(event.Data, "status")
		duration := intValue(event.Data, "duration_ms")
		parts := []string{}
		if tool != "" {
			parts = append(parts, "tool="+tool)
		}
		if status != "" {
			parts = append(parts, "status="+status)
		}
		if duration > 0 {
			parts = append(parts, fmt.Sprintf("duration=%dms", duration))
		}
		return strings.Join(parts, " ")
	case observability.EventRunFinished:
		return fmt.Sprintf("status=%s duration=%dms", firstStringDefault(event.Data, "status", "unknown"), intValue(event.Data, "duration_ms"))
	default:
		return ""
	}
}

func readEvents(path string) ([]observability.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []observability.Event
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && len(line) == 0 {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if errors.Is(readErr, os.ErrClosed) {
				return nil, readErr
			}
			return nil, readErr
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if readErr != nil {
				break
			}
			continue
		}
		var event observability.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, readErr
			}
			break
		}
	}
	return events, nil
}

func latestRunID(events []observability.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if strings.TrimSpace(events[i].RunID) != "" {
			return events[i].RunID
		}
	}
	return ""
}

func valueMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func intValue(data map[string]any, key string) int64 {
	if data == nil {
		return 0
	}
	switch value := data[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func stringValue(data map[string]any, key string) (string, bool) {
	if data == nil {
		return "", false
	}
	value, ok := data[key].(string)
	return value, ok
}

func boolValue(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	value, _ := data[key].(bool)
	return value
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := stringValue(data, key); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstStringDefault(data map[string]any, key string, fallback string) string {
	if value, ok := stringValue(data, key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func maxInt64(a int64, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
