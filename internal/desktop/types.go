package desktop

import "context"

const (
	// CoordinateSpaceScreenPhysical 表示 macOS 显示器物理像素坐标。
	CoordinateSpaceScreenPhysical = "screen-physical"
	// CoordinateSpaceScreenshotLocal 表示截图左上角为原点的物理像素坐标。
	CoordinateSpaceScreenshotLocal = "screenshot-local"
)

// Bounds 描述一个矩形区域。桌面工具的 Bounds 默认使用 screen-physical 坐标。
type Bounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// PermissionsResult 返回当前平台的桌面自动化前置权限。
// nil 表示该权限无法在当前系统可靠判断。
type PermissionsResult struct {
	Platform        string   `json:"platform"`
	Accessibility   *bool    `json:"accessibility"`
	ScreenRecording *bool    `json:"screen_recording"`
	InputMonitoring *bool    `json:"input_monitoring"`
	Missing         []string `json:"missing"`
	Hints           []string `json:"hints"`
}

// Window 表示一个可见桌面应用窗口。
type Window struct {
	WindowID  string `json:"window_id"`
	PID       int    `json:"pid"`
	AppName   string `json:"app_name"`
	Title     string `json:"title"`
	Bounds    Bounds `json:"bounds"`
	IsVisible bool   `json:"is_visible"`
	IsActive  bool   `json:"is_active"`
}

type ListWindowsRequest struct {
	AppName string `json:"app_name,omitempty"`
	Title   string `json:"title,omitempty"`
	Limit   int    `json:"limit"`
}

type ListWindowsResult struct {
	Windows []Window `json:"windows"`
}

type ActivateRequest struct {
	PID      int    `json:"pid"`
	WindowID string `json:"window_id,omitempty"`
}

type ActivateResult struct {
	PID      int  `json:"pid"`
	Active   bool `json:"active"`
	Verified bool `json:"verified"`
}

type ScreenshotRequest struct {
	PID        int    `json:"pid"`
	WindowID   string `json:"window_id,omitempty"`
	OutputPath string `json:"output_path"`
}

type ScreenshotResult struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	WindowID string `json:"window_id"`
	PID      int    `json:"pid"`
	Bounds   Bounds `json:"bounds"`
}

type AXSnapshotRequest struct {
	PID             int  `json:"pid"`
	MaxDepth        int  `json:"max_depth"`
	MaxNodes        int  `json:"max_nodes"`
	IncludeZeroSize bool `json:"include_zero_size"`
}

// AXNode 是 Accessibility 控件树中的一个序列化节点。
// ID 只在当前快照中有效；未来 M2 操作节点前必须重新读取快照并验证目标状态。
type AXNode struct {
	ID          string   `json:"id"`
	Role        string   `json:"role"`
	Title       string   `json:"title"`
	Value       string   `json:"value"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled"`
	Bounds      Bounds   `json:"bounds"`
	Actions     []string `json:"actions"`
	Children    []AXNode `json:"children"`
}

type AXSnapshotResult struct {
	PID       int    `json:"pid"`
	Root      AXNode `json:"root"`
	NodeCount int    `json:"node_count"`
	Truncated bool   `json:"truncated"`
}

// AXPressRequest 是一次受控 AXPress 动作。Expected 字段用于 helper
// 在执行前再次确认同一路径仍指向模型刚刚检查过的语义控件。
type AXPressRequest struct {
	PID                 int    `json:"pid"`
	NodeID              string `json:"node_id"`
	ExpectedRole        string `json:"expected_role"`
	ExpectedTitle       string `json:"expected_title"`
	ExpectedDescription string `json:"expected_description"`
}

type AXPressResult struct {
	PID       int    `json:"pid"`
	NodeID    string `json:"node_id"`
	Action    string `json:"action"`
	Performed bool   `json:"performed"`
}

type AXFocusRequest struct {
	PID                 int    `json:"pid"`
	NodeID              string `json:"node_id"`
	ExpectedRole        string `json:"expected_role"`
	ExpectedTitle       string `json:"expected_title"`
	ExpectedDescription string `json:"expected_description"`
}

type AXFocusResult struct {
	PID              int    `json:"pid"`
	NodeID           string `json:"node_id"`
	Action           string `json:"action"`
	Performed        bool   `json:"performed"`
	ActiveBefore     bool   `json:"active_before"`
	ActiveAfter      bool   `json:"active_after"`
	Focused          bool   `json:"focused"`
	FocusRole        string `json:"focus_role"`
	FocusTitle       string `json:"focus_title"`
	FocusDescription string `json:"focus_description"`
}

type ClickRequest struct {
	PID                 int    `json:"pid"`
	NodeID              string `json:"node_id"`
	ExpectedRole        string `json:"expected_role"`
	ExpectedTitle       string `json:"expected_title"`
	ExpectedDescription string `json:"expected_description"`
}

type ClickResult struct {
	PID             int    `json:"pid"`
	NodeID          string `json:"node_id"`
	Action          string `json:"action"`
	Performed       bool   `json:"performed"`
	ActiveBefore    bool   `json:"active_before"`
	ActiveAfter     bool   `json:"active_after"`
	X               int    `json:"x"`
	Y               int    `json:"y"`
	CoordinateSpace string `json:"coordinate_space"`
}

type PressKeyRequest struct {
	PID int    `json:"pid"`
	Key string `json:"key"`
}

type PressKeyResult struct {
	PID          int    `json:"pid"`
	Key          string `json:"key"`
	Action       string `json:"action"`
	Performed    bool   `json:"performed"`
	ActiveBefore bool   `json:"active_before"`
	ActiveAfter  bool   `json:"active_after"`
}

type TypeTextRequest struct {
	PID  int    `json:"pid"`
	Text string `json:"text"`
}

type TypeTextResult struct {
	PID              int    `json:"pid"`
	Action           string `json:"action"`
	Performed        bool   `json:"performed"`
	ActiveBefore     bool   `json:"active_before"`
	ActiveAfter      bool   `json:"active_after"`
	TextLength       int    `json:"text_length"`
	LineCount        int    `json:"line_count"`
	FocusRole        string `json:"focus_role"`
	FocusTitle       string `json:"focus_title"`
	FocusDescription string `json:"focus_description"`
}

// Driver 抽象平台相关的桌面感知和受限 AX 语义动作能力。
type Driver interface {
	Permissions(ctx context.Context) (PermissionsResult, error)
	ListWindows(ctx context.Context, req ListWindowsRequest) (ListWindowsResult, error)
	Activate(ctx context.Context, req ActivateRequest) (ActivateResult, error)
	Screenshot(ctx context.Context, req ScreenshotRequest) (ScreenshotResult, error)
	AXSnapshot(ctx context.Context, req AXSnapshotRequest) (AXSnapshotResult, error)
	AXPress(ctx context.Context, req AXPressRequest) (AXPressResult, error)
	AXFocus(ctx context.Context, req AXFocusRequest) (AXFocusResult, error)
	Click(ctx context.Context, req ClickRequest) (ClickResult, error)
	PressKey(ctx context.Context, req PressKeyRequest) (PressKeyResult, error)
	TypeText(ctx context.Context, req TypeTextRequest) (TypeTextResult, error)
}
