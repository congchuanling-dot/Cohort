package evolution

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/hooks"
)

const (
	ReflectionRunModeInteractive = "interactive"
	reflectionHookName           = "session_end_reflection_queue"
)

type SessionEndReflectionConfig struct {
	Enabled         bool
	ProjectRoot     string
	MemoryWorkspace string
	SessionRoot     string
	Debounce        time.Duration
	MaxAttempts     int
}

// SessionEndReflectionHandler 只负责将合法 SessionEnd 事件转换为持久 trigger。
// 它不读取会话正文，也不在 Hook 主路径执行任何反思任务。
type SessionEndReflectionHandler struct {
	Queue  ReflectionQueue
	Config SessionEndReflectionConfig
}

func NewSessionEndReflectionHandler(
	queue ReflectionQueue,
	cfg SessionEndReflectionConfig,
) *SessionEndReflectionHandler {
	return &SessionEndReflectionHandler{Queue: queue, Config: cfg}
}

func (h *SessionEndReflectionHandler) Name() string {
	return reflectionHookName
}

func (h *SessionEndReflectionHandler) HandleHook(_ context.Context, event hooks.Event) error {
	if h == nil || !h.Config.Enabled || event.Type != hooks.EventSessionEnd {
		return nil
	}
	runMode := strings.TrimSpace(stringValue(event.Data["run_mode"]))
	if runMode == "" {
		runMode = ReflectionRunModeInteractive
	}
	if runMode != ReflectionRunModeInteractive {
		return nil
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return nil
	}
	historyLen, ok := intValue(event.Data["history_len"])
	if !ok || historyLen <= 0 {
		return nil
	}
	projectRoot, err := absoluteCleanPath(h.Config.ProjectRoot)
	if err != nil {
		return fmt.Errorf("reflection project root: %w", err)
	}
	if queueRoot := h.Queue.ProjectRoot; queueRoot != "" {
		projectRoot = queueRoot
	}
	memoryWorkspace, err := absoluteCleanPath(h.Config.MemoryWorkspace)
	if err != nil {
		return fmt.Errorf("reflection memory workspace: %w", err)
	}
	sessionRoot, err := absoluteCleanPath(h.Config.SessionRoot)
	if err != nil {
		return fmt.Errorf("reflection session root: %w", err)
	}
	debounce := h.Config.Debounce
	if debounce < 0 {
		debounce = 0
	}
	now := h.Queue.nowUTC()
	_, _, err = h.Queue.Enqueue(ReflectionTrigger{
		ProjectRoot:     projectRoot,
		MemoryWorkspace: memoryWorkspace,
		SessionRoot:     sessionRoot,
		SessionID:       event.SessionID,
		RunID:           event.RunID,
		HistoryLen:      historyLen,
		RunStatus:       strings.TrimSpace(stringValue(event.Data["status"])),
		CreatedAt:       now,
		AvailableAt:     now.Add(debounce),
		MaxAttempts:     h.Config.MaxAttempts,
	})
	return err
}

func absoluteCleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		return 0, false
	}
}
