package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"cohert/internal/agent"
	"cohert/internal/computeruse"
	"cohert/internal/desktop"
	"cohert/internal/llm"
)

const (
	defaultComputerFindLimit = 5
	maxComputerFindLimit     = 20
)

// ComputerSee 返回当前电脑窗口、截图和 AX 候选目标，是 computer_* 闭环入口。
type ComputerSee struct {
	driver desktop.Driver
	store  *computeruse.Store
	workspaceTool
}

// NewComputerSee 创建电脑状态观察工具。
func NewComputerSee(driver desktop.Driver, store *computeruse.Store, workspace string) *ComputerSee {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerSee{driver: driver, store: store, workspaceTool: newWorkspaceTool(workspace)}
}

func (t *ComputerSee) Name() string { return ToolNameComputerSee }

func (t *ComputerSee) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Observe the current computer state for GUI automation. Optionally filter by app or window title. Returns visible windows, a screenshot artifact when a target window exists, bounded AX summary, and cached target candidates for computer_find/click/type/check. This is read-only except for bringing the selected window frontmost before sensing.",
		Parameters: objectSchema(map[string]any{
			"app_name": stringProp("Optional case-insensitive application-name substring, for example WeChat or Doubao."),
			"title":    stringProp("Optional case-insensitive window-title substring."),
			"limit":    intProp("Maximum windows to inspect. Default 50, max 100.", defaultDesktopWindowLimit),
		}),
	}}
}

func (t *ComputerSee) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if t.store == nil {
		t.store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	limit := clampDesktopLimit(asInt(call.Args["limit"], defaultDesktopWindowLimit), defaultDesktopWindowLimit, maxDesktopWindowLimit)
	windowsResult, err := t.driver.ListWindows(ctx, desktop.ListWindowsRequest{
		AppName: strings.TrimSpace(asString(call.Args["app_name"])),
		Title:   strings.TrimSpace(asString(call.Args["title"])),
		Limit:   limit,
	})
	if err != nil {
		return desktopToolError(err), nil
	}

	state := computeruse.ComputerState{
		OS:      runtime.GOOS,
		Windows: windowsResult.Windows,
	}
	window, ok := selectComputerWindow(windowsResult.Windows)
	if ok {
		state.ActiveApp = window.AppName
		state.ActivePID = window.PID
		state.ActiveWindow = computerWindowRef(runtime.GOOS, window)
		if err := activateDesktopTarget(ctx, t.driver, window.PID); err != nil {
			return desktopToolError(err), nil
		}
		screenshot, screenshotErr := t.captureScreenshot(ctx, window)
		if screenshotErr != nil {
			return desktopToolError(screenshotErr), nil
		}
		state.ScreenshotRef = screenshot.imagePath
		state.ScreenshotManifestRef = screenshot.manifestPath
		state.ScreenshotWidth = screenshot.result.Width
		state.ScreenshotHeight = screenshot.result.Height

		ax, axErr := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
			PID:             window.PID,
			MaxDepth:        defaultDesktopAXDepth,
			MaxNodes:        defaultDesktopAXNodes,
			IncludeZeroSize: false,
		})
		if axErr != nil {
			return desktopToolError(axErr), nil
		}
		state.AXRoot = ax.Root
		state.AXNodeCount = ax.NodeCount
		state.AXTruncated = ax.Truncated
		state.Candidates = collectComputerAXTargets(ax.Root, state.ActiveWindow, state.ScreenshotRef, state.ScreenshotManifestRef)
	}

	state = t.store.SaveState(state)
	return agent.Outcome{
		Data: map[string]any{
			"status":                  agent.ToolStatusSuccess,
			"state_id":                state.ID,
			"os":                      state.OS,
			"active_app":              state.ActiveApp,
			"active_pid":              state.ActivePID,
			"active_window":           state.ActiveWindow,
			"windows":                 state.Windows,
			"window_count":            len(state.Windows),
			"screenshot_ref":          state.ScreenshotRef,
			"screenshot_manifest_ref": state.ScreenshotManifestRef,
			"screenshot_width":        state.ScreenshotWidth,
			"screenshot_height":       state.ScreenshotHeight,
			"ax_node_count":           state.AXNodeCount,
			"ax_truncated":            state.AXTruncated,
			"candidates":              summarizeComputerTargets(state.Candidates, 30),
			"candidate_count":         len(state.Candidates),
			"expires_at":              state.ExpiresAt.Format(time.RFC3339Nano),
		},
		NextPrompt: "\n",
	}, nil
}

type computerScreenshotCapture struct {
	imagePath    string
	manifestPath string
	result       desktop.ScreenshotResult
}

func (t *ComputerSee) captureScreenshot(ctx context.Context, window desktop.Window) (computerScreenshotCapture, error) {
	dir := filepath.Join(t.workspace, defaultDesktopScreenshotDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return computerScreenshotCapture{}, err
	}
	outputPath := filepath.Join(dir, fmt.Sprintf("computer_see_%d.png", time.Now().UnixNano()))
	result, err := t.driver.Screenshot(ctx, desktop.ScreenshotRequest{
		PID:        window.PID,
		WindowID:   window.WindowID,
		OutputPath: outputPath,
	})
	if err != nil {
		return computerScreenshotCapture{}, err
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return computerScreenshotCapture{}, &desktop.ToolError{
			Code:    "computer_see_screenshot_save_failed",
			Message: "desktop helper did not create the requested screenshot",
			Hint:    "请确认已授予屏幕录制权限，并重新调用 desktop_permissions 检查。",
		}
	}
	manifestPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".json"
	manifest := desktopScreenshotManifest{
		Version:               1,
		ImagePath:             outputPath,
		PID:                   result.PID,
		WindowID:              result.WindowID,
		Width:                 result.Width,
		Height:                result.Height,
		WindowBounds:          result.Bounds,
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeDesktopScreenshotManifest(manifestPath, manifest); err != nil {
		return computerScreenshotCapture{}, err
	}
	return computerScreenshotCapture{imagePath: outputPath, manifestPath: manifestPath, result: result}, nil
}

// ComputerFind 在最近一次 computer_see 的候选中查找目标，并返回 target_id。
type ComputerFind struct {
	store *computeruse.Store
}

// NewComputerFind 创建目标查找工具。
func NewComputerFind(store *computeruse.Store) *ComputerFind {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerFind{store: store}
}

func (t *ComputerFind) Name() string { return ToolNameComputerFind }

func (t *ComputerFind) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Find GUI targets from the latest computer_see snapshot. Pass a natural-language query such as 'message input box', 'send button', or visible text. Returns cached target_id values for computer_click/computer_type/check.",
		Parameters: objectSchema(map[string]any{
			"query": stringProp("Natural-language target description or visible text to find in the latest computer_see result."),
			"limit": intProp("Maximum matching targets to return. Default 5, max 20.", defaultComputerFindLimit),
		}, "query"),
	}}
}

func (t *ComputerFind) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	query := strings.TrimSpace(asString(call.Args["query"]))
	if query == "" {
		return computerToolError("computer_find_bad_request", "query must be a non-empty target description", "请先说明要找的控件或文字，例如“消息输入框”或“发送按钮”。"), nil
	}
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_find", err), nil
	}
	limit := clampDesktopLimit(asInt(call.Args["limit"], defaultComputerFindLimit), defaultComputerFindLimit, maxComputerFindLimit)
	matches := rankComputerTargets(query, state.Candidates)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":     agent.ToolStatusSuccess,
			"state_id":   state.ID,
			"query":      query,
			"targets":    matches,
			"count":      len(matches),
			"expires_at": state.ExpiresAt.Format(time.RFC3339Nano),
		},
		NextPrompt: "\n",
	}, nil
}

// ComputerCheck 用最新 AX 状态验证可见文本或目标状态。
type ComputerCheck struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerCheck 创建状态检查工具。
func NewComputerCheck(driver desktop.Driver, store *computeruse.Store) *ComputerCheck {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerCheck{driver: driver, store: store}
}

func (t *ComputerCheck) Name() string { return ToolNameComputerCheck }

func (t *ComputerCheck) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Verify GUI state using a fresh AX snapshot for the latest computer_see target window. Use contains_text for exact draft/message text; expectation is a human-readable description. Returns evidence and a boolean verified result.",
		Parameters: objectSchema(map[string]any{
			"expectation":   stringProp("Human-readable state to verify, for example 'the message input contains the draft'."),
			"contains_text": stringProp("Optional exact text that should appear in AX title/value/description. Prefer this for draft verification."),
			"target_id":     stringProp("Optional cached target_id to inspect more narrowly before searching the whole AX tree."),
		}, "expectation"),
	}}
}

func (t *ComputerCheck) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	expectation := strings.TrimSpace(asString(call.Args["expectation"]))
	if expectation == "" {
		return computerToolError("computer_check_bad_request", "expectation must be non-empty", "请描述要验证的界面状态。"), nil
	}
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_check", err), nil
	}
	pid := state.ActivePID
	targetID := strings.TrimSpace(asString(call.Args["target_id"]))
	var target computeruse.ComputerTarget
	if targetID != "" {
		target, err = t.store.Target(targetID)
		if err != nil {
			return computerCacheError("computer_check", err), nil
		}
		pid = target.Window.PID
	}
	if pid <= 0 {
		return computerToolError("computer_check_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择一个可见窗口。"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	ax, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        desktopActionSnapshotDepth,
		MaxNodes:        desktopActionSnapshotNodes,
		IncludeZeroSize: false,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	needle := strings.TrimSpace(asString(call.Args["contains_text"]))
	if needle == "" {
		needle = expectation
	}
	evidence := []string{}
	verified := false
	if targetID != "" && target.AXNodeID != "" {
		if node, found := findAXNode(ax.Root, target.AXNodeID); found {
			if computerNodeContains(node, needle) || computerNodeContains(node, expectation) {
				verified = true
				evidence = append(evidence, computerNodeEvidence(node))
			}
		}
	}
	if !verified {
		evidence = findComputerTextEvidence(ax.Root, needle, 5)
		verified = len(evidence) > 0
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":        agent.ToolStatusSuccess,
			"expectation":   expectation,
			"contains_text": needle,
			"target_id":     targetID,
			"verified":      verified,
			"evidence":      evidence,
			"pid":           pid,
			"source":        computeruse.SourceAX,
		},
		NextPrompt: "\n",
	}, nil
}

func selectComputerWindow(windows []desktop.Window) (desktop.Window, bool) {
	for _, window := range windows {
		if window.IsVisible && window.IsActive {
			return window, true
		}
	}
	for _, window := range windows {
		if window.IsVisible {
			return window, true
		}
	}
	if len(windows) == 0 {
		return desktop.Window{}, false
	}
	return windows[0], true
}

func computerWindowRef(osName string, window desktop.Window) computeruse.WindowRef {
	return computeruse.WindowRef{
		OS:       osName,
		AppName:  window.AppName,
		PID:      window.PID,
		WindowID: window.WindowID,
		Title:    window.Title,
		Bounds:   window.Bounds,
	}
}

func collectComputerAXTargets(root desktop.AXNode, window computeruse.WindowRef, screenshotRef string, manifestRef string) []computeruse.ComputerTarget {
	targets := []computeruse.ComputerTarget{}
	var walk func(desktop.AXNode)
	walk = func(node desktop.AXNode) {
		if len(targets) >= defaultDesktopAXNodes {
			return
		}
		if target, ok := computerTargetFromAXNode(node, window, screenshotRef, manifestRef); ok {
			targets = append(targets, target)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return targets
}

func computerTargetFromAXNode(node desktop.AXNode, window computeruse.WindowRef, screenshotRef string, manifestRef string) (computeruse.ComputerTarget, bool) {
	label := firstNonEmpty(node.Title, node.Description, node.Value)
	action := computeruse.SuggestedActionInspect
	if isEditableAXNode(node) {
		action = computeruse.SuggestedActionType
	} else if containsAXAction(node.Actions, "AXPress") || (node.Bounds.Width > 0 && node.Bounds.Height > 0) {
		action = computeruse.SuggestedActionClick
	}
	if label == "" && action == computeruse.SuggestedActionInspect {
		return computeruse.ComputerTarget{}, false
	}
	if label == "" {
		label = node.Role
	}
	risk := classifyDesktopClickRisk(node)
	return computeruse.ComputerTarget{
		Label:               label,
		Role:                node.Role,
		Value:               node.Value,
		Description:         node.Description,
		Confidence:          0.9,
		Source:              computeruse.SourceAX,
		Bounds:              node.Bounds,
		CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
		Window:              window,
		SuggestedAction:     action,
		RiskHint:            string(risk),
		AXNodeID:            node.ID,
		ExpectedRole:        node.Role,
		ExpectedTitle:       node.Title,
		ExpectedDescription: node.Description,
		Actions:             append([]string(nil), node.Actions...),
		ScreenshotRef:       screenshotRef,
		ScreenshotManifest:  manifestRef,
	}, true
}

func summarizeComputerTargets(targets []computeruse.ComputerTarget, limit int) []computeruse.ComputerTarget {
	if len(targets) <= limit {
		return targets
	}
	return targets[:limit]
}

type computerTargetMatch struct {
	computeruse.ComputerTarget
	Score float64 `json:"score"`
}

func rankComputerTargets(query string, targets []computeruse.ComputerTarget) []computerTargetMatch {
	matches := []computerTargetMatch{}
	for _, target := range targets {
		score := scoreComputerTarget(query, target)
		if score <= 0 {
			continue
		}
		matches = append(matches, computerTargetMatch{ComputerTarget: target, Score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Confidence > matches[j].Confidence
		}
		return matches[i].Score > matches[j].Score
	})
	return matches
}

func scoreComputerTarget(query string, target computeruse.ComputerTarget) float64 {
	q := normalizeComputerText(query)
	haystack := normalizeComputerText(strings.Join([]string{
		target.Label,
		target.Role,
		target.Value,
		target.Description,
		target.SuggestedAction,
		target.Window.AppName,
		target.Window.Title,
	}, " "))
	score := 0.0
	if strings.Contains(haystack, q) {
		score += 1.0
	}
	for _, token := range strings.Fields(q) {
		if strings.Contains(haystack, token) {
			score += 0.2
		}
	}
	if computerQueryWantsInput(q) && (target.SuggestedAction == computeruse.SuggestedActionType || strings.Contains(strings.ToLower(target.Role), "text")) {
		score += 0.8
	}
	if computerQueryWantsClick(q) && target.SuggestedAction == computeruse.SuggestedActionClick {
		score += 0.4
	}
	return score * target.Confidence
}

func normalizeComputerText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func computerQueryWantsInput(query string) bool {
	return containsAny(query, []string{"输入", "消息", "搜索", "编辑", "草稿", "input", "message", "search", "edit", "draft", "text field", "textbox"})
}

func computerQueryWantsClick(query string) bool {
	return containsAny(query, []string{"点击", "按钮", "打开", "发送", "保存", "click", "button", "open", "send", "save"})
}

func computerNodeContains(node desktop.AXNode, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	haystack := normalizeComputerText(strings.Join([]string{node.Role, node.Title, node.Value, node.Description}, " "))
	return strings.Contains(haystack, normalizeComputerText(text))
}

func findComputerTextEvidence(root desktop.AXNode, text string, limit int) []string {
	evidence := []string{}
	var walk func(desktop.AXNode)
	walk = func(node desktop.AXNode) {
		if len(evidence) >= limit {
			return
		}
		if computerNodeContains(node, text) {
			evidence = append(evidence, computerNodeEvidence(node))
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return evidence
}

func computerNodeEvidence(node desktop.AXNode) string {
	return strings.TrimSpace(fmt.Sprintf("node=%s role=%s title=%q value=%q description=%q", node.ID, node.Role, node.Title, node.Value, node.Description))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func computerToolError(code string, message string, hint string) agent.Outcome {
	return agent.Outcome{Data: agent.NewToolError(code, message, hint), NextPrompt: "\n"}
}

func computerCacheError(tool string, err error) agent.Outcome {
	switch err {
	case computeruse.ErrNoState:
		return computerToolError(tool+"_state_required", tool+" requires a fresh computer_see result", "请先调用 computer_see 观察目标窗口，然后再查找或操作 target_id。")
	case computeruse.ErrTargetExpired:
		return computerToolError(tool+"_target_stale", "target_id is expired and cannot be reused", "界面可能已变化；请重新调用 computer_see 和 computer_find 获取新的 target_id。")
	case computeruse.ErrTargetNotFound:
		return computerToolError(tool+"_target_not_found", "target_id was not found in the computer target cache", "请确认 target_id 来自最近一次 computer_see/computer_find，或重新观察界面。")
	default:
		return computerToolError(tool+"_cache_failed", err.Error(), "请重新调用 computer_see 刷新缓存。")
	}
}
