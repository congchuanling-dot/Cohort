package computeruse

import (
	"time"

	"cohert/internal/desktop"
)

const (
	// SourceAX 表示目标来自平台可访问性树。
	SourceAX = "ax"
	// SourceOCR 预留给 OCR 文本目标。
	SourceOCR = "ocr"
	// SourceVision 预留给视觉检测目标。
	SourceVision = "vision"
)

const (
	// SuggestedActionClick 表示目标适合点击或 AXPress。
	SuggestedActionClick = "click"
	// SuggestedActionType 表示目标适合聚焦后起草文本。
	SuggestedActionType = "type_text"
	// SuggestedActionInspect 表示目标只适合观察或校验。
	SuggestedActionInspect = "inspect"
)

// DefaultTargetTTL 是 computer_* 目标缓存的默认有效期。
const DefaultTargetTTL = 45 * time.Second

// WindowRef 是副作用动作绑定的窗口身份。
type WindowRef struct {
	OS       string         `json:"os"`
	AppName  string         `json:"app_name"`
	PID      int            `json:"pid"`
	WindowID string         `json:"window_id"`
	Title    string         `json:"title"`
	Bounds   desktop.Bounds `json:"bounds"`
}

// ComputerState 是一次 computer_see 产生的电脑状态快照。
type ComputerState struct {
	ID                    string           `json:"id"`
	OS                    string           `json:"os"`
	ActiveApp             string           `json:"active_app"`
	ActivePID             int              `json:"active_pid"`
	ActiveWindow          WindowRef        `json:"active_window"`
	Windows               []desktop.Window `json:"windows"`
	ScreenshotRef         string           `json:"screenshot_ref,omitempty"`
	ScreenshotManifestRef string           `json:"screenshot_manifest_ref,omitempty"`
	ScreenshotWidth       int              `json:"screenshot_width,omitempty"`
	ScreenshotHeight      int              `json:"screenshot_height,omitempty"`
	AXRoot                desktop.AXNode   `json:"ax_root,omitempty"`
	AXNodeCount           int              `json:"ax_node_count,omitempty"`
	AXTruncated           bool             `json:"ax_truncated,omitempty"`
	OCRStatus             string           `json:"ocr_status,omitempty"`
	OCRText               string           `json:"ocr_text,omitempty"`
	OCRLineCount          int              `json:"ocr_line_count,omitempty"`
	OCRError              string           `json:"ocr_error,omitempty"`
	Candidates            []ComputerTarget `json:"candidates"`
	CreatedAt             time.Time        `json:"created_at"`
	ExpiresAt             time.Time        `json:"expires_at"`
}

// ComputerTarget 是模型可引用的 GUI 目标。低层 node_id/bbox 只由 runtime 使用。
type ComputerTarget struct {
	ID                  string         `json:"id"`
	Label               string         `json:"label"`
	Role                string         `json:"role"`
	Value               string         `json:"value,omitempty"`
	Description         string         `json:"description,omitempty"`
	Confidence          float64        `json:"confidence"`
	Source              string         `json:"source"`
	Bounds              desktop.Bounds `json:"bounds"`
	CoordinateSpace     string         `json:"coordinate_space"`
	Window              WindowRef      `json:"window"`
	SuggestedAction     string         `json:"suggested_action"`
	RiskHint            string         `json:"risk_hint,omitempty"`
	AXNodeID            string         `json:"-"`
	ExpectedRole        string         `json:"-"`
	ExpectedTitle       string         `json:"-"`
	ExpectedDescription string         `json:"-"`
	Actions             []string       `json:"actions,omitempty"`
	ScreenshotRef       string         `json:"-"`
	ScreenshotManifest  string         `json:"-"`
	BBox                [4]int         `json:"-"`
	CreatedAt           time.Time      `json:"created_at"`
	ExpiresAt           time.Time      `json:"expires_at"`
}

// ComputerActionResult 是 computer_click/type/check 的统一结果骨架。
type ComputerActionResult struct {
	Status            string         `json:"status"`
	ExecutedAction    string         `json:"executed_action,omitempty"`
	TargetID          string         `json:"target_id,omitempty"`
	Target            ComputerTarget `json:"target,omitempty"`
	Verified          bool           `json:"verified"`
	Evidence          []string       `json:"evidence,omitempty"`
	NeedsUserDecision bool           `json:"needs_user_decision,omitempty"`
}
