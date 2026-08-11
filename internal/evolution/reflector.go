package evolution

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cohort/internal/llm"
	"cohort/internal/observability"
	"cohort/internal/session"
)

const (
	ReflectTaskSessionArchive      = "session-archive"
	ReflectTaskMineSOPCandidates   = "mine-sop-candidates"
	ReflectTaskMineSkillCandidates = "mine-skill-candidates"
	ReflectTaskMemoryQualityReport = "memory-quality-report"
	ReflectTaskToolFailureReport   = "tool-failure-report"

	RawSessionArchivePath   = "memory/raw_sessions/all_histories.md"
	FailurePatternsPath     = "memory/reflection/failure_patterns.md"
	MemoryQualityReportPath = "memory/reflection/quality_reports/memory_quality.md"
	SkillCandidatesPath     = "memory/reflection/skill_candidates.md"
)

// ReflectionResult summarizes a single offline reflection task.
type ReflectionResult struct {
	Task                 string
	OutputPaths          []string
	SessionsScanned      int
	HistoryMessages      int
	ToolFailures         int
	SOPCandidatesWritten int
	MemoryHitSessions    int
}

type reflectionDataset struct {
	SessionRoot string
	Sessions    []reflectedSession
	Failures    []reflectedToolFailure
	Memory      []reflectedMemorySession
}

type reflectedSession struct {
	ID              string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Model           string
	TitleHash       string
	CWDHash         string
	HistoryMessages []reflectedHistoryMessage
	RoleCounts      map[string]int
	ToolCounts      map[string]int
	ToolCallCount   int
	ToolResultCount int
	ParseErrors     []string
}

type reflectedHistoryMessage struct {
	Index        int
	Role         string
	ContentChars int
	ContentLines int
	ContentHash  string
	ToolNames    []string
	ToolResult   string
}

type reflectedToolFailure struct {
	SessionID string
	Turn      int
	Index     int
	Tool      string
	Status    string
	ErrorCode string
	ArgsHash  string
	Source    string
	Time      time.Time
}

type reflectedMemorySession struct {
	SessionID             string
	ContextBuiltEvents    int
	RelevantHitEvents     int
	MaxRelevantHitCount   int
	InjectedMemoryEvents  int
	CheckpointAfterHit    bool
	MemoryUpdateAfterHit  bool
	MemoryApplySuccesses  int
	RelevantMemoryUsed    bool
	ObservationParseError []string
}

type legacyRunLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id,omitempty"`
	Turn      int       `json:"turn"`
	Index     int       `json:"index"`
	Event     string    `json:"event"`
	Tool      string    `json:"tool"`
	Status    string    `json:"status"`
	ErrorCode string    `json:"error_code,omitempty"`
	ArgsHash  string    `json:"args_hash,omitempty"`
}

// ReflectOnce runs one deterministic offline reflection task over local session logs.
//
// It never calls an LLM and never writes promoted SOP files or indexes. Outputs stay under
// memory/raw_sessions or memory/reflection for later human review.
func (m Manager) ReflectOnce(task string, sessionRoot string) (ReflectionResult, error) {
	task = strings.TrimSpace(task)
	if task == ReflectTaskDeliveryOutcomeReport {
		paths, err := m.writeDeliveryOutcomeReport(sessionRoot)
		if err != nil {
			return ReflectionResult{}, err
		}
		sort.Strings(paths)
		return ReflectionResult{Task: task, OutputPaths: paths}, nil
	}
	if sessionRoot == "" {
		sessionRoot = session.DefaultRootDir
	}
	dataset, err := m.loadReflectionDataset(sessionRoot)
	if err != nil {
		return ReflectionResult{}, err
	}
	result := ReflectionResult{
		Task:            task,
		SessionsScanned: len(dataset.Sessions),
	}
	for _, sess := range dataset.Sessions {
		result.HistoryMessages += len(sess.HistoryMessages)
	}

	switch task {
	case ReflectTaskSessionArchive:
		path, err := m.writeSessionArchive(dataset)
		if err != nil {
			return ReflectionResult{}, err
		}
		result.OutputPaths = []string{path}
	case ReflectTaskToolFailureReport:
		path, err := m.writeToolFailureReport(dataset)
		if err != nil {
			return ReflectionResult{}, err
		}
		result.OutputPaths = []string{path}
		result.ToolFailures = len(dataset.Failures)
	case ReflectTaskMemoryQualityReport:
		path, hitSessions, err := m.writeMemoryQualityReport(dataset)
		if err != nil {
			return ReflectionResult{}, err
		}
		result.OutputPaths = []string{path}
		result.MemoryHitSessions = hitSessions
	case ReflectTaskMineSOPCandidates:
		path, written, err := m.mineSOPCandidates(dataset)
		if err != nil {
			return ReflectionResult{}, err
		}
		result.OutputPaths = []string{path}
		result.ToolFailures = len(dataset.Failures)
		result.SOPCandidatesWritten = written
	case ReflectTaskMineSkillCandidates:
		path, written, err := m.writeSkillCandidateReport(dataset)
		if err != nil {
			return ReflectionResult{}, err
		}
		result.OutputPaths = []string{path}
		result.SOPCandidatesWritten = written
	default:
		return ReflectionResult{}, fmt.Errorf("unknown reflection task %q", task)
	}
	sort.Strings(result.OutputPaths)
	return result, nil
}

func (m Manager) loadReflectionDataset(sessionRoot string) (reflectionDataset, error) {
	if _, err := m.EnsureStructure(); err != nil {
		return reflectionDataset{}, err
	}
	root := filepath.Clean(sessionRoot)
	dataset := reflectionDataset{SessionRoot: root}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dataset, nil
		}
		return dataset, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		sessionDir := filepath.Join(root, sessionID)
		reflected, err := loadReflectedSession(sessionDir, sessionID)
		if err != nil {
			reflected = reflectedSession{
				ID:          sessionID,
				RoleCounts:  map[string]int{},
				ToolCounts:  map[string]int{},
				ParseErrors: []string{err.Error()},
			}
		}
		dataset.Sessions = append(dataset.Sessions, reflected)
		dataset.Failures = append(dataset.Failures, loadLegacyFailures(filepath.Join(sessionDir, "run.log"), sessionID)...)
		obsFailures, memory := loadObservationReflection(filepath.Join(sessionDir, "run.log.jsonl"), sessionID)
		dataset.Failures = append(dataset.Failures, obsFailures...)
		if memory.SessionID != "" {
			dataset.Memory = append(dataset.Memory, memory)
		}
	}
	sort.Slice(dataset.Sessions, func(i, j int) bool {
		return dataset.Sessions[i].ID < dataset.Sessions[j].ID
	})
	sort.Slice(dataset.Failures, func(i, j int) bool {
		a, b := dataset.Failures[i], dataset.Failures[j]
		return strings.Join([]string{a.Tool, a.ErrorCode, a.Status, a.SessionID, fmt.Sprint(a.Turn), fmt.Sprint(a.Index), a.ArgsHash}, "\x00") <
			strings.Join([]string{b.Tool, b.ErrorCode, b.Status, b.SessionID, fmt.Sprint(b.Turn), fmt.Sprint(b.Index), b.ArgsHash}, "\x00")
	})
	sort.Slice(dataset.Memory, func(i, j int) bool {
		return dataset.Memory[i].SessionID < dataset.Memory[j].SessionID
	})
	return dataset, nil
}

func loadReflectedSession(sessionDir string, sessionID string) (reflectedSession, error) {
	reflected := reflectedSession{
		ID:         sessionID,
		RoleCounts: map[string]int{},
		ToolCounts: map[string]int{},
	}
	if data, err := os.ReadFile(filepath.Join(sessionDir, session.MetaFileName)); err == nil {
		var meta session.Session
		if err := json.Unmarshal(data, &meta); err == nil {
			reflected.ID = defaultString(meta.ID, sessionID)
			reflected.CreatedAt = meta.CreatedAt
			reflected.UpdatedAt = meta.UpdatedAt
			reflected.Model = meta.Model
			reflected.TitleHash = safeHash(meta.Title)
			reflected.CWDHash = safeHash(meta.CWD)
		} else {
			reflected.ParseErrors = append(reflected.ParseErrors, "meta.json: "+err.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		reflected.ParseErrors = append(reflected.ParseErrors, "meta.json: "+err.Error())
	}
	historyPath := filepath.Join(sessionDir, session.HistoryFileName)
	file, err := os.Open(historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return reflected, nil
		}
		return reflected, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	index := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry session.HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			reflected.ParseErrors = append(reflected.ParseErrors, fmt.Sprintf("history line %d: %v", index+1, err))
			index++
			continue
		}
		message := entry.Message
		role := defaultString(entry.Role, message.Role)
		item := reflectedHistoryMessage{
			Index:        index,
			Role:         role,
			ContentChars: len([]rune(message.Content)),
			ContentLines: lineCount(message.Content),
			ContentHash:  safeHash(message.Content),
		}
		if len(message.ToolCalls) > 0 {
			item.ToolNames = toolCallNames(message.ToolCalls)
			reflected.ToolCallCount += len(item.ToolNames)
			for _, name := range item.ToolNames {
				reflected.ToolCounts[name]++
			}
		}
		if role == llm.RoleTool {
			item.ToolResult = message.Name
			reflected.ToolResultCount++
			if message.Name != "" {
				reflected.ToolCounts[message.Name]++
			}
		}
		reflected.HistoryMessages = append(reflected.HistoryMessages, item)
		reflected.RoleCounts[role]++
		index++
	}
	if err := scanner.Err(); err != nil {
		reflected.ParseErrors = append(reflected.ParseErrors, "history.jsonl: "+err.Error())
	}
	return reflected, nil
}

func loadLegacyFailures(path string, fallbackSessionID string) []reflectedToolFailure {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var failures []reflectedToolFailure
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry legacyRunLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if !isFailureStatus(entry.Status, entry.ErrorCode) {
			continue
		}
		failures = append(failures, reflectedToolFailure{
			SessionID: defaultString(entry.SessionID, fallbackSessionID),
			Turn:      entry.Turn,
			Index:     entry.Index,
			Tool:      entry.Tool,
			Status:    entry.Status,
			ErrorCode: entry.ErrorCode,
			ArgsHash:  entry.ArgsHash,
			Source:    "run.log",
			Time:      entry.Timestamp,
		})
	}
	_ = scanner.Err()
	return failures
}

func loadObservationReflection(path string, fallbackSessionID string) ([]reflectedToolFailure, reflectedMemorySession) {
	file, err := os.Open(path)
	if err != nil {
		return nil, reflectedMemorySession{}
	}
	defer file.Close()

	var failures []reflectedToolFailure
	memory := reflectedMemorySession{SessionID: fallbackSessionID}
	seenHit := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event observability.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			memory.ObservationParseError = append(memory.ObservationParseError, err.Error())
			continue
		}
		sessionID := defaultString(event.SessionID, fallbackSessionID)
		switch event.EventType {
		case observability.EventToolFinished:
			tool := stringFromAny(event.Data["tool"])
			status := stringFromAny(event.Data["status"])
			errorCode := stringFromAny(event.Data["error_code"])
			if isFailureStatus(status, errorCode) {
				failures = append(failures, reflectedToolFailure{
					SessionID: sessionID,
					Turn:      event.Turn,
					Tool:      tool,
					Status:    status,
					ErrorCode: errorCode,
					Source:    "run.log.jsonl",
					Time:      event.Time,
				})
			}
			if tool == "memory_apply_update" && status == "success" {
				memory.MemoryApplySuccesses++
				if seenHit {
					memory.MemoryUpdateAfterHit = true
				}
			}
		case observability.EventToolStarted:
			tool := stringFromAny(event.Data["tool"])
			if seenHit && tool == "update_working_checkpoint" {
				memory.CheckpointAfterHit = true
			}
			if seenHit && strings.HasPrefix(tool, "memory_") {
				memory.MemoryUpdateAfterHit = true
			}
		case observability.EventContextBuilt:
			memory.ContextBuiltEvents++
			hitCount := intFromAny(event.Data["relevant_memory_hit_count"])
			if hitCount > memory.MaxRelevantHitCount {
				memory.MaxRelevantHitCount = hitCount
			}
			if boolFromAny(event.Data["injected_relevant_memory"]) {
				memory.InjectedMemoryEvents++
			}
			if hitCount > 0 || boolFromAny(event.Data["injected_relevant_memory"]) {
				seenHit = true
				memory.RelevantHitEvents++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		memory.ObservationParseError = append(memory.ObservationParseError, err.Error())
	}
	memory.RelevantMemoryUsed = memory.CheckpointAfterHit || memory.MemoryUpdateAfterHit
	return failures, memory
}

func (m Manager) writeSessionArchive(dataset reflectionDataset) (string, error) {
	path, err := m.resolveMemoryPath(RawSessionArchivePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Raw Session Archive\n\n")
	b.WriteString("- raw_content_policy: omitted; only counts, tool names, and sha256 hashes are stored\n")
	b.WriteString("- session_root_hash: ")
	b.WriteString(safeHash(dataset.SessionRoot))
	b.WriteByte('\n')
	fmt.Fprintf(&b, "- sessions_scanned: %d\n\n", len(dataset.Sessions))
	for _, sess := range dataset.Sessions {
		b.WriteString("## Session ")
		b.WriteString(sess.ID)
		b.WriteString("\n\n")
		writeMemoryField(&b, "created_at", formatTime(sess.CreatedAt))
		writeMemoryField(&b, "updated_at", formatTime(sess.UpdatedAt))
		writeMemoryField(&b, "model", sess.Model)
		writeMemoryField(&b, "title_hash", sess.TitleHash)
		writeMemoryField(&b, "cwd_hash", sess.CWDHash)
		writeMemoryField(&b, "role_counts", formatCounts(sess.RoleCounts))
		writeMemoryField(&b, "tool_counts", formatCounts(sess.ToolCounts))
		writeMemoryField(&b, "tool_call_count", fmt.Sprint(sess.ToolCallCount))
		writeMemoryField(&b, "tool_result_count", fmt.Sprint(sess.ToolResultCount))
		if len(sess.ParseErrors) > 0 {
			writeMemoryField(&b, "parse_error_count", fmt.Sprint(len(sess.ParseErrors)))
		}
		b.WriteString("\n| index | role | chars | lines | content_hash | tool_calls | tool_result |\n")
		b.WriteString("| --- | --- | ---: | ---: | --- | --- | --- |\n")
		for _, msg := range sess.HistoryMessages {
			fmt.Fprintf(&b,
				"| %d | %s | %d | %d | `%s` | %s | %s |\n",
				msg.Index,
				markdownCell(msg.Role),
				msg.ContentChars,
				msg.ContentLines,
				msg.ContentHash,
				markdownCell(strings.Join(msg.ToolNames, ", ")),
				markdownCell(msg.ToolResult),
			)
		}
		b.WriteByte('\n')
	}
	return path, os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
}

func (m Manager) writeToolFailureReport(dataset reflectionDataset) (string, error) {
	path, err := m.resolveMemoryPath(FailurePatternsPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return "", err
	}
	groups := groupFailures(dataset.Failures)
	var b strings.Builder
	b.WriteString("# Tool Failure Patterns\n\n")
	b.WriteString("- raw_content_policy: omitted; args are represented only by args_hash when available\n")
	fmt.Fprintf(&b, "- sessions_scanned: %d\n", len(dataset.Sessions))
	fmt.Fprintf(&b, "- tool_failure_count: %d\n\n", len(dataset.Failures))
	if len(groups) == 0 {
		b.WriteString("No tool failures found.\n")
		return path, os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
	}
	for _, group := range groups {
		b.WriteString("## ")
		b.WriteString(group.Title())
		b.WriteString("\n\n")
		writeMemoryField(&b, "count", fmt.Sprint(group.Count))
		writeMemoryField(&b, "sessions", strings.Join(sortedKeys(group.Sessions), ", "))
		writeMemoryField(&b, "distinct_args_hashes", fmt.Sprint(len(group.ArgsHashes)))
		writeMemoryField(&b, "top_args_hashes", strings.Join(formatTopCounts(group.ArgsHashes, 5), ", "))
		writeMemoryField(&b, "first_seen", formatTime(group.FirstSeen))
		writeMemoryField(&b, "last_seen", formatTime(group.LastSeen))
		b.WriteByte('\n')
	}
	return path, os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
}

func (m Manager) writeMemoryQualityReport(dataset reflectionDataset) (string, int, error) {
	path, err := m.resolveMemoryPath(MemoryQualityReportPath)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return "", 0, err
	}
	auditCount := countJSONLLines(filepath.Join(m.MemoryRoot(), "audit.jsonl"))
	candidates, _ := m.ListSOPCandidates()
	hitSessions := 0
	usedAfterHitSessions := 0
	contextEvents := 0
	memoryApplySuccesses := 0
	for _, item := range dataset.Memory {
		contextEvents += item.ContextBuiltEvents
		memoryApplySuccesses += item.MemoryApplySuccesses
		if item.RelevantHitEvents > 0 {
			hitSessions++
			if item.RelevantMemoryUsed {
				usedAfterHitSessions++
			}
		}
	}
	var b strings.Builder
	b.WriteString("# Memory Quality Report\n\n")
	b.WriteString("- raw_content_policy: omitted; report uses event counts and hashes only\n")
	fmt.Fprintf(&b, "- sessions_scanned: %d\n", len(dataset.Sessions))
	fmt.Fprintf(&b, "- observation_sessions: %d\n", len(dataset.Memory))
	fmt.Fprintf(&b, "- context_built_events: %d\n", contextEvents)
	fmt.Fprintf(&b, "- relevant_memory_hit_sessions: %d\n", hitSessions)
	fmt.Fprintf(&b, "- relevant_memory_used_after_hit_sessions: %d\n", usedAfterHitSessions)
	fmt.Fprintf(&b, "- memory_apply_successes: %d\n", memoryApplySuccesses)
	fmt.Fprintf(&b, "- memory_audit_records: %d\n", auditCount)
	fmt.Fprintf(&b, "- sop_candidate_count: %d\n\n", len(candidates))
	b.WriteString("## Relevant Memory Sessions\n\n")
	b.WriteString("| session | context_events | hit_events | max_hit_count | injected_events | checkpoint_after_hit | memory_update_after_hit | used_after_hit |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | --- | --- | --- |\n")
	for _, item := range dataset.Memory {
		if item.RelevantHitEvents == 0 && item.ContextBuiltEvents == 0 {
			continue
		}
		fmt.Fprintf(&b,
			"| %s | %d | %d | %d | %d | %t | %t | %t |\n",
			item.SessionID,
			item.ContextBuiltEvents,
			item.RelevantHitEvents,
			item.MaxRelevantHitCount,
			item.InjectedMemoryEvents,
			item.CheckpointAfterHit,
			item.MemoryUpdateAfterHit,
			item.RelevantMemoryUsed,
		)
	}
	return path, hitSessions, os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
}

func (m Manager) mineSOPCandidates(dataset reflectionDataset) (string, int, error) {
	groups := groupFailures(dataset.Failures)
	existing, err := m.ListSOPCandidates()
	if err != nil {
		return "", 0, err
	}
	existingIDs := make(map[string]bool, len(existing))
	for _, candidate := range existing {
		existingIDs[candidate.ID] = true
	}
	written := 0
	for _, group := range groups {
		if group.Count < 2 {
			continue
		}
		candidate := failureGroupSOPCandidate(group)
		id := sopCandidateID(SOPCandidate{
			Title:           candidateSOPTitle(candidate),
			Scene:           candidate.Scene,
			TriggerKeywords: cleanStringSlice(candidate.TriggerKeywords),
			ProposedSOPPath: candidateSOPPath(candidate),
			Why:             candidateMemoryText(candidate),
			DraftSteps:      cleanStringSlice(candidate.RecommendedSteps),
		})
		if existingIDs[id] {
			continue
		}
		if _, err := m.appendSOPCandidate(candidate, "offline-reflection"); err != nil {
			return "", written, err
		}
		existingIDs[id] = true
		written++
	}
	path, err := m.resolveMemoryPath(SOPCandidateMemoryPath)
	if err != nil {
		return "", written, err
	}
	return path, written, nil
}

func (m Manager) writeSkillCandidateReport(dataset reflectionDataset) (string, int, error) {
	path, err := m.resolveMemoryPath(SkillCandidatesPath)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return "", 0, err
	}
	type candidate struct {
		Name     string
		Reason   string
		Sessions map[string]int
		Tools    map[string]int
		Count    int
	}
	grouped := map[string]*candidate{}
	for _, sess := range dataset.Sessions {
		if sess.ToolCallCount < 3 {
			continue
		}
		keyTools := topToolNames(sess.ToolCounts, 4)
		if len(keyTools) < 2 {
			continue
		}
		key := strings.Join(keyTools, "+")
		item := grouped[key]
		if item == nil {
			item = &candidate{
				Name:     "skill-" + slugify(key),
				Reason:   "Repeated multi-tool workflow observed in session histories.",
				Sessions: map[string]int{},
				Tools:    map[string]int{},
			}
			grouped[key] = item
		}
		item.Count++
		item.Sessions[sess.ID]++
		for _, tool := range keyTools {
			item.Tools[tool] += sess.ToolCounts[tool]
		}
	}
	for _, group := range groupFailures(dataset.Failures) {
		if group.Count < 2 {
			continue
		}
		key := "recover-" + group.Tool + "-" + defaultString(group.ErrorCode, group.Status)
		item := grouped[key]
		if item == nil {
			item = &candidate{
				Name:     "skill-" + slugify(key),
				Reason:   "Repeated failure pattern needs a reusable recovery workflow.",
				Sessions: map[string]int{},
				Tools:    map[string]int{group.Tool: group.Count},
			}
			grouped[key] = item
		}
		item.Count += group.Count
		for sessionID, count := range group.Sessions {
			item.Sessions[sessionID] += count
		}
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := grouped[keys[i]], grouped[keys[j]]
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		return left.Name < right.Name
	})
	var b strings.Builder
	b.WriteString("# Skill Candidates\n\n")
	b.WriteString("- raw_content_policy: omitted; candidates are mined from tool names, counts, session IDs, and failure codes\n")
	fmt.Fprintf(&b, "- sessions_scanned: %d\n", len(dataset.Sessions))
	fmt.Fprintf(&b, "- candidate_count: %d\n\n", len(keys))
	if len(keys) == 0 {
		b.WriteString("No reusable Skill candidates found.\n")
		return path, 0, os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
	}
	for _, key := range keys {
		item := grouped[key]
		b.WriteString("## ")
		b.WriteString(item.Name)
		b.WriteString("\n\n")
		writeMemoryField(&b, "reason", item.Reason)
		writeMemoryField(&b, "score", fmt.Sprint(item.Count))
		writeMemoryField(&b, "sessions", strings.Join(sortedKeys(item.Sessions), ", "))
		writeMemoryField(&b, "tools", strings.Join(formatTopCounts(item.Tools, 8), ", "))
		b.WriteString("\nSuggested SKILL.md outline:\n\n")
		b.WriteString("```md\n")
		b.WriteString("---\n")
		b.WriteString("name: " + item.Name + "\n")
		b.WriteString("description: TODO: summarize the repeated workflow and when to invoke it.\n")
		b.WriteString("user-invocable: false\n")
		b.WriteString("permissions:\n")
		b.WriteString("  allow-tools: [" + strings.Join(topToolNames(item.Tools, 8), ", ") + "]\n")
		b.WriteString("---\n\n")
		b.WriteString("# " + item.Name + "\n\n")
		b.WriteString("TODO: convert the observed pattern into verified steps, validation checks, and stop conditions.\n")
		b.WriteString("```\n\n")
	}
	return path, len(keys), os.WriteFile(path, []byte(b.String()), defaultMemoryFilePerm)
}

func topToolNames(counts map[string]int, limit int) []string {
	items := sortedKeys(counts)
	sort.Slice(items, func(i, j int) bool {
		if counts[items[i]] != counts[items[j]] {
			return counts[items[i]] > counts[items[j]]
		}
		return items[i] < items[j]
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

type failureGroup struct {
	Tool       string
	Status     string
	ErrorCode  string
	Count      int
	Sessions   map[string]int
	ArgsHashes map[string]int
	FirstSeen  time.Time
	LastSeen   time.Time
}

func (g failureGroup) Title() string {
	code := defaultString(g.ErrorCode, "status:"+defaultString(g.Status, "unknown"))
	return g.Tool + " / " + code
}

func groupFailures(failures []reflectedToolFailure) []failureGroup {
	grouped := map[string]*failureGroup{}
	for _, failure := range failures {
		tool := defaultString(failure.Tool, "unknown_tool")
		code := defaultString(failure.ErrorCode, "status:"+defaultString(failure.Status, "unknown"))
		key := tool + "\x00" + code
		group, ok := grouped[key]
		if !ok {
			group = &failureGroup{
				Tool:       tool,
				Status:     failure.Status,
				ErrorCode:  failure.ErrorCode,
				Sessions:   map[string]int{},
				ArgsHashes: map[string]int{},
			}
			grouped[key] = group
		}
		group.Count++
		group.Sessions[failure.SessionID]++
		if failure.ArgsHash != "" {
			group.ArgsHashes[failure.ArgsHash]++
		}
		if !failure.Time.IsZero() && (group.FirstSeen.IsZero() || failure.Time.Before(group.FirstSeen)) {
			group.FirstSeen = failure.Time
		}
		if !failure.Time.IsZero() && (group.LastSeen.IsZero() || failure.Time.After(group.LastSeen)) {
			group.LastSeen = failure.Time
		}
	}
	groups := make([]failureGroup, 0, len(grouped))
	for _, group := range grouped {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Title() < groups[j].Title()
	})
	return groups
}

func failureGroupSOPCandidate(group failureGroup) Candidate {
	code := defaultString(group.ErrorCode, "status:"+defaultString(group.Status, "unknown"))
	title := "Handle repeated " + group.Tool + " " + code + " failures"
	path := "sops/" + slugify(title) + ".md"
	return Candidate{
		Type:            "sop_candidate",
		Target:          SOPCandidateMemoryPath,
		Scene:           fmt.Sprintf("Tool %s repeatedly fails with %s across %d run-log entries.", group.Tool, code, group.Count),
		TriggerKeywords: []string{"tool failure", group.Tool, code, "offline reflection"},
		Lesson:          fmt.Sprintf("Repeated %s failures with %s should be handled as a pattern: validate fresh inputs, avoid retrying the same args_hash, and stop for diagnosis when the same failure repeats.", group.Tool, code),
		RecommendedSteps: []string{
			"Re-read the relevant SOP or Skill when one exists, then update_working_checkpoint with the current constraint.",
			"Inspect the most recent successful observation before retrying " + group.Tool + "; do not reuse stale targets or stale args_hash values.",
			"If the same error code or args_hash repeats, switch to diagnostic evidence collection instead of another blind retry.",
			"Ask the user when the failure depends on external permission, unavailable application state, or a risky side effect.",
		},
		PromoteToSOP: true,
		SOPTitle:     title,
		SOPPath:      path,
		EvidenceIDs:  []string{"reflect:" + stableHashID("failure", group.Tool+" "+code)},
		Risk:         "low",
		Action:       "append",
	}
}

func toolCallNames(calls []llm.ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if name := strings.TrimSpace(call.Function.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func isFailureStatus(status string, errorCode string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return errorCode != "" || (status != "" && status != "success" && status != "ok")
}

func safeHash(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func lineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := sortedKeys(counts)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func formatTopCounts(counts map[string]int, limit int) []string {
	if len(counts) == 0 || limit <= 0 {
		return nil
	}
	keys := sortedKeys(counts)
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return parts
}

func markdownCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "|", `\|`)
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func countJSONLLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return count
	}
	return count
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return false
}
