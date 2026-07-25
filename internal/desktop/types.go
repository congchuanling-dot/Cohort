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
	// X 是矩形左边缘的横坐标。
	X int `json:"x"`
	// Y 是矩形上边缘的纵坐标。
	Y int `json:"y"`
	// Width 是矩形宽度，单位与坐标空间一致。
	Width int `json:"width"`
	// Height 是矩形高度，单位与坐标空间一致。
	Height int `json:"height"`
}

// PermissionsResult 返回当前平台的桌面自动化前置权限。
// nil 表示该权限无法在当前系统可靠判断。
type PermissionsResult struct {
	// Platform 是 helper 实际运行的平台标识。
	Platform string `json:"platform"`
	// Accessibility 是辅助功能权限状态；nil 表示当前无法可靠探测。
	Accessibility *bool `json:"accessibility"`
	// ScreenRecording 是屏幕录制权限状态；截图能力依赖此权限。
	ScreenRecording *bool `json:"screen_recording"`
	// InputMonitoring 是输入监控权限状态；部分系统可能要求它才能注入按键。
	InputMonitoring *bool `json:"input_monitoring"`
	// Missing 列出已确认缺少的权限名称。
	Missing []string `json:"missing"`
	// Hints 给出用户可执行的授权或排障建议。
	Hints []string `json:"hints"`
}

// Window 表示一个可见桌面应用窗口。
type Window struct {
	// WindowID 是平台窗口标识，激活或截图时可用于消除同进程多窗口歧义。
	WindowID string `json:"window_id"`
	// PID 是拥有该窗口的操作系统进程标识。
	PID int `json:"pid"`
	// AppName 是应用显示名称。
	AppName string `json:"app_name"`
	// Title 是当前窗口标题，可能随文档或页面内容变化。
	Title string `json:"title"`
	// Bounds 是窗口在物理屏幕坐标系中的位置和尺寸。
	Bounds Bounds `json:"bounds"`
	// IsVisible 表示窗口是否当前可见。
	IsVisible bool `json:"is_visible"`
	// IsActive 表示窗口是否处于前台激活状态。
	IsActive bool `json:"is_active"`
}

// ListWindowsRequest 是按应用或标题筛选窗口列表的请求。
type ListWindowsRequest struct {
	// AppName 是可选的应用名称筛选条件。
	AppName string `json:"app_name,omitempty"`
	// Title 是可选的窗口标题筛选条件。
	Title string `json:"title,omitempty"`
	// Limit 限制返回条数，避免枚举过多窗口。
	Limit int `json:"limit"`
}

// ListWindowsResult 包装可见窗口列表，便于后续扩展分页或诊断字段。
type ListWindowsResult struct {
	// Windows 是满足筛选条件的窗口快照。
	Windows []Window `json:"windows"`
}

// ActivateRequest 指定要切到前台的进程及可选窗口。
type ActivateRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// WindowID 可选地指定同一进程中的一个窗口。
	WindowID string `json:"window_id,omitempty"`
}

// ActivateResult 说明激活动作是否被系统接受并经 helper 验证。
type ActivateResult struct {
	// PID 是被操作的目标进程。
	PID int `json:"pid"`
	// Active 是 helper 观察到的前台状态。
	Active bool `json:"active"`
	// Verified 表示 helper 是否能够可靠验证 Active。
	Verified bool `json:"verified"`
}

// ScreenshotRequest 指定要截取的窗口与由上层控制的输出路径。
type ScreenshotRequest struct {
	// PID 是目标窗口所属进程。
	PID int `json:"pid"`
	// WindowID 可选地锁定目标窗口。
	WindowID string `json:"window_id,omitempty"`
	// OutputPath 是受 workspace 校验后的图片落盘位置。
	OutputPath string `json:"output_path"`
}

// ScreenshotResult 描述实际生成的截图尺寸以及对应窗口边界。
type ScreenshotResult struct {
	// Width 是截图像素宽度。
	Width int `json:"width"`
	// Height 是截图像素高度。
	Height int `json:"height"`
	// WindowID 是实际被截取的窗口标识。
	WindowID string `json:"window_id"`
	// PID 是实际被截取窗口的进程。
	PID int `json:"pid"`
	// Bounds 将截图局部坐标映射回物理屏幕时所需的窗口边界。
	Bounds Bounds `json:"bounds"`
}

// AXSnapshotRequest 控制辅助功能树的读取范围，避免递归遍历大型界面。
type AXSnapshotRequest struct {
	// PID 是要读取其辅助功能树的应用进程。
	PID int `json:"pid"`
	// MaxDepth 限制树遍历层数。
	MaxDepth int `json:"max_depth"`
	// MaxNodes 限制最多返回的节点数。
	MaxNodes int `json:"max_nodes"`
	// IncludeZeroSize 控制是否保留不可见或零尺寸节点。
	IncludeZeroSize bool `json:"include_zero_size"`
}

// AXNode 是 Accessibility 控件树中的一个序列化节点。
// ID 只在当前快照中有效；未来 M2 操作节点前必须重新读取快照并验证目标状态。
type AXNode struct {
	// ID 是当前快照内的节点路径标识，不能跨快照复用。
	ID string `json:"id"`
	// Role 是平台辅助功能角色，例如按钮、文本框或菜单项。
	Role string `json:"role"`
	// Title 是控件可见标题或辅助功能标题。
	Title string `json:"title"`
	// Value 是控件当前值，例如输入框文本。
	Value string `json:"value"`
	// Description 是补充语义说明，常用于无标题图标控件。
	Description string `json:"description"`
	// Enabled 是控件是否可操作；nil 表示平台没有提供该信息。
	Enabled *bool `json:"enabled"`
	// Bounds 是控件在物理屏幕坐标中的边界。
	Bounds Bounds `json:"bounds"`
	// Actions 是平台声明该节点支持的辅助功能动作。
	Actions []string `json:"actions"`
	// Children 是该控件的直接子节点。
	Children []AXNode `json:"children"`
}

// AXSnapshotResult 返回一棵受节点数与深度限制的辅助功能树。
type AXSnapshotResult struct {
	// PID 是快照所属应用进程。
	PID int `json:"pid"`
	// Root 是树的根节点。
	Root AXNode `json:"root"`
	// NodeCount 是实际返回的节点数量。
	NodeCount int `json:"node_count"`
	// Truncated 表示因 MaxDepth 或 MaxNodes 截断了树。
	Truncated bool `json:"truncated"`
}

// AXPressRequest 是一次受控 AXPress 动作。Expected 字段用于 helper
// 在执行前再次确认同一路径仍指向模型刚刚检查过的语义控件。
type AXPressRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// NodeID 是刚从 AXSnapshot 中读取到的当前节点标识。
	NodeID string `json:"node_id"`
	// ExpectedRole 是调用前观察到的角色，供 helper 防止节点漂移。
	ExpectedRole string `json:"expected_role"`
	// ExpectedTitle 是调用前观察到的标题，供 helper 二次校验。
	ExpectedTitle string `json:"expected_title"`
	// ExpectedDescription 是调用前观察到的描述，补足无标题控件语义。
	ExpectedDescription string `json:"expected_description"`
}

// AXPressResult 描述平台执行受限 AXPress 动作后的状态。
type AXPressResult struct {
	// PID 是被操作的进程。
	PID int `json:"pid"`
	// NodeID 是被执行动作的快照节点。
	NodeID string `json:"node_id"`
	// Action 是 helper 实际执行的辅助功能动作名称。
	Action string `json:"action"`
	// Performed 表示系统是否接受并执行了该动作。
	Performed bool `json:"performed"`
}

// AXFocusRequest 使用与 AXPress 相同的快照校验字段安全聚焦可编辑节点。
type AXFocusRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// NodeID 是待聚焦的当前快照节点。
	NodeID string `json:"node_id"`
	// ExpectedRole 用于执行前确认目标没有变成其他控件。
	ExpectedRole string `json:"expected_role"`
	// ExpectedTitle 用于执行前确认目标标题未变化。
	ExpectedTitle string `json:"expected_title"`
	// ExpectedDescription 用于执行前确认无标题控件的语义。
	ExpectedDescription string `json:"expected_description"`
}

// AXFocusResult 包含聚焦后的应用状态与当前焦点控件元数据。
type AXFocusResult struct {
	// PID 是被操作的进程。
	PID int `json:"pid"`
	// NodeID 是请求中的目标节点。
	NodeID string `json:"node_id"`
	// Action 是 helper 执行的聚焦动作名称。
	Action string `json:"action"`
	// Performed 表示调用是否执行成功。
	Performed bool `json:"performed"`
	// ActiveBefore 是操作前目标应用是否处于前台。
	ActiveBefore bool `json:"active_before"`
	// ActiveAfter 是操作后目标应用是否处于前台。
	ActiveAfter bool `json:"active_after"`
	// Focused 表示 helper 是否确认焦点到达目标。
	Focused bool `json:"focused"`
	// FocusRole 是操作后焦点控件的角色。
	FocusRole string `json:"focus_role"`
	// FocusTitle 是操作后焦点控件的标题。
	FocusTitle string `json:"focus_title"`
	// FocusDescription 是操作后焦点控件的描述。
	FocusDescription string `json:"focus_description"`
}

// ClickRequest 指定一个已经由 AX 快照验证的控件点击目标。
type ClickRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// NodeID 是待点击节点的当前快照标识。
	NodeID string `json:"node_id"`
	// ExpectedRole 是执行前的角色校验条件。
	ExpectedRole string `json:"expected_role"`
	// ExpectedTitle 是执行前的标题校验条件。
	ExpectedTitle string `json:"expected_title"`
	// ExpectedDescription 是执行前的描述校验条件。
	ExpectedDescription string `json:"expected_description"`
}

// ClickResult 记录语义节点点击转换得到的屏幕坐标和前台状态。
type ClickResult struct {
	// PID 是被操作的进程。
	PID int `json:"pid"`
	// NodeID 是点击的辅助功能节点。
	NodeID string `json:"node_id"`
	// Action 是 helper 采用的实际动作。
	Action string `json:"action"`
	// Performed 表示点击是否执行。
	Performed bool `json:"performed"`
	// ActiveBefore 是操作前的前台状态。
	ActiveBefore bool `json:"active_before"`
	// ActiveAfter 是操作后的前台状态。
	ActiveAfter bool `json:"active_after"`
	// X 是最终点击横坐标。
	X int `json:"x"`
	// Y 是最终点击纵坐标。
	Y int `json:"y"`
	// CoordinateSpace 说明 X、Y 所在坐标系，通常为 screen-physical。
	CoordinateSpace string `json:"coordinate_space"`
}

// VisualClickRequest 指定已由截图 manifest 映射过的物理屏幕坐标。
type VisualClickRequest struct {
	// PID 是必须先被激活的目标应用进程。
	PID int `json:"pid"`
	// X 是物理屏幕横坐标。
	X int `json:"x"`
	// Y 是物理屏幕纵坐标。
	Y int `json:"y"`
	// CoordinateSpace 明确坐标语义，拒绝把截图局部坐标误传给 helper。
	CoordinateSpace string `json:"coordinate_space"`
}

// VisualClickResult 描述受控视觉点击实际作用的位置和前台状态。
type VisualClickResult struct {
	// PID 是被操作的目标进程。
	PID int `json:"pid"`
	// Action 是 helper 执行的动作名称。
	Action string `json:"action"`
	// Performed 表示点击是否成功执行。
	Performed bool `json:"performed"`
	// ActiveBefore 是操作前的前台状态。
	ActiveBefore bool `json:"active_before"`
	// ActiveAfter 是操作后的前台状态。
	ActiveAfter bool `json:"active_after"`
	// X 是最终点击横坐标。
	X int `json:"x"`
	// Y 是最终点击纵坐标。
	Y int `json:"y"`
	// CoordinateSpace 是最终坐标的空间标识。
	CoordinateSpace string `json:"coordinate_space"`
}

// PressKeyRequest 将受限按键发送给已激活的应用进程。
type PressKeyRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// Key 是经过上层白名单校验的按键或组合键。
	Key string `json:"key"`
}

// PressKeyResult 返回按键执行与目标应用前后台状态。
type PressKeyResult struct {
	// PID 是被操作的进程。
	PID int `json:"pid"`
	// Key 是实际发送的按键。
	Key string `json:"key"`
	// Action 是 helper 记录的动作名称。
	Action string `json:"action"`
	// Performed 表示系统是否接受输入。
	Performed bool `json:"performed"`
	// ActiveBefore 是输入前的前台状态。
	ActiveBefore bool `json:"active_before"`
	// ActiveAfter 是输入后的前台状态。
	ActiveAfter bool `json:"active_after"`
}

// TypeTextRequest 提供向已聚焦编辑控件起草的文本。
type TypeTextRequest struct {
	// PID 是目标应用进程。
	PID int `json:"pid"`
	// Text 是待输入文本；上层不会把它回显到工具结果。
	Text string `json:"text"`
	// AllowVisualFocus 仅在已消费视觉聚焦令牌时允许无 AX 焦点证明的输入。
	AllowVisualFocus bool `json:"allow_visual_focus"`
}

// TypeTextResult 返回输入动作及 helper 对目标焦点的验证信息。
type TypeTextResult struct {
	// PID 是被操作的进程。
	PID int `json:"pid"`
	// Action 是 helper 实际执行的输入动作。
	Action string `json:"action"`
	// Performed 表示输入是否成功执行。
	Performed bool `json:"performed"`
	// ActiveBefore 是输入前的前台状态。
	ActiveBefore bool `json:"active_before"`
	// ActiveAfter 是输入后的前台状态。
	ActiveAfter bool `json:"active_after"`
	// TextLength 仅返回文本长度，避免把可能敏感的正文回传。
	TextLength int `json:"text_length"`
	// LineCount 是输入文本的行数。
	LineCount int `json:"line_count"`
	// FocusRole 是输入时焦点控件的角色。
	FocusRole string `json:"focus_role"`
	// FocusTitle 是输入时焦点控件的标题。
	FocusTitle string `json:"focus_title"`
	// FocusDescription 是输入时焦点控件的描述。
	FocusDescription string `json:"focus_description"`
	// FocusVerification 表示焦点由 AX 快照还是视觉令牌证明。
	FocusVerification string `json:"focus_verification"`
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
	VisualClick(ctx context.Context, req VisualClickRequest) (VisualClickResult, error)
	PressKey(ctx context.Context, req PressKeyRequest) (PressKeyResult, error)
	TypeText(ctx context.Context, req TypeTextRequest) (TypeTextResult, error)
}
