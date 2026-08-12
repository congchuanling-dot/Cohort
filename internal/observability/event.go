package observability

import (
	"fmt"
	"sync/atomic"
	"time"
)

const SchemaVersion = 1

type EventType string

const (
	EventRunStarted             EventType = "RunStarted"
	EventUserPromptSubmitted    EventType = "UserPromptSubmitted"
	EventTurnStarted            EventType = "TurnStarted"
	EventContextBuilt           EventType = "ContextBuilt"
	EventToolRouteSelected      EventType = "ToolRouteSelected"
	EventSessionStarted         EventType = "SessionStarted"
	EventSessionFinished        EventType = "SessionFinished"
	EventLLMRequestStarted      EventType = "LLMRequestStarted"
	EventLLMResponseFinished    EventType = "LLMResponseFinished"
	EventFinishGuardTriggered   EventType = "FinishGuardTriggered"
	EventTextToolUseParsed      EventType = "TextToolUseParsed"
	EventToolStarted            EventType = "ToolStarted"
	EventToolFinished           EventType = "ToolFinished"
	EventFileChanged            EventType = "FileChanged"
	EventCompactStarted         EventType = "CompactStarted"
	EventCompactFinished        EventType = "CompactFinished"
	EventHookDispatched         EventType = "HookDispatched"
	EventPermissionDecision     EventType = "PermissionDecision"
	EventGovernanceIntervention EventType = "GovernanceIntervention"
	EventCapabilityGapRecorded  EventType = "CapabilityGapRecorded"
	EventRunFinished            EventType = "RunFinished"
)

type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// RedactionSummary 记录事件写入前是否做过脱敏，以及哪些字段被替换。
type RedactionSummary struct {
	Applied bool     `json:"applied"`
	Fields  []string `json:"fields,omitempty"`
}

// Event 是 Cohort 运行时观测的统一事件信封。
//
// Data 只能放可 JSON 序列化的结构化摘要。大文本、密钥、剪贴板正文、
// 截图 base64 等内容应在进入 Event 前就转换为 hash、长度或 artifact ref。
type Event struct {
	SchemaVersion int              `json:"schema_version"`
	EventID       string           `json:"event_id"`
	EventType     EventType        `json:"event_type"`
	Time          time.Time        `json:"time"`
	RunID         string           `json:"run_id,omitempty"`
	SessionID     string           `json:"session_id,omitempty"`
	Turn          int              `json:"turn,omitempty"`
	Workspace     string           `json:"workspace,omitempty"`
	Source        string           `json:"source,omitempty"`
	Severity      Severity         `json:"severity"`
	Data          map[string]any   `json:"data,omitempty"`
	Redaction     RedactionSummary `json:"redaction"`
}

var idCounter atomic.Uint64

// NewRunID 生成一次 Runner.Run 内稳定的运行 ID。
func NewRunID() string {
	return nextID("run")
}

func newEventID() string {
	return nextID("evt")
}

func nextID(prefix string) string {
	seq := idCounter.Add(1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UTC().UnixNano(), seq)
}

// NewEvent 填充事件信封的固定字段，调用方只需要提供生命周期语义和数据。
func NewEvent(eventType EventType, runID string, sessionID string, turn int, workspace string, source string, severity Severity, data map[string]any) Event {
	if severity == "" {
		severity = SeverityInfo
	}
	return Event{
		SchemaVersion: SchemaVersion,
		EventID:       newEventID(),
		EventType:     eventType,
		Time:          time.Now().UTC(),
		RunID:         runID,
		SessionID:     sessionID,
		Turn:          turn,
		Workspace:     workspace,
		Source:        source,
		Severity:      severity,
		Data:          data,
	}
}
