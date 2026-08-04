package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/computeruse"
	"cohort/internal/desktop"
	"cohort/internal/llm"
	"cohort/internal/vision"
)

const (
	defaultComputerFindLimit = 5
	maxComputerFindLimit     = 20
	defaultComputerWaitMS    = 5000
	maxComputerWaitMS        = 30000
	defaultComputerPollMS    = 250
	minComputerPollMS        = 100
)

// ComputerSee 返回当前电脑窗口、截图和 AX 候选目标，是 computer_* 闭环入口。
type ComputerSee struct {
	driver   desktop.Driver
	store    *computeruse.Store
	runner   vision.OCRRunner
	detector computerUIDetector
	workspaceTool
}

// NewComputerSee 创建电脑状态观察工具。
func NewComputerSee(driver desktop.Driver, store *computeruse.Store, workspace string) *ComputerSee {
	return NewComputerSeeWithOCRRunner(driver, store, workspace, newComputerOCRRunner(workspace))
}

// NewComputerSeeWithOCRRunner 允许测试注入 OCR runner；runner 为 nil 时只返回 AX 候选。
func NewComputerSeeWithOCRRunner(driver desktop.Driver, store *computeruse.Store, workspace string, runner vision.OCRRunner) *ComputerSee {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerSee{driver: driver, store: store, runner: runner, detector: defaultComputerUIDetector(), workspaceTool: newWorkspaceTool(workspace)}
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
	if t.detector == nil {
		t.detector = defaultComputerUIDetector()
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
	uiDetectorName := ""
	uiDetectorStatus := "skipped"
	uiDetectorCandidateCount := 0
	uiDetectorWarnings := []string{}
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
		ocrTargets, ocrStatus, ocrText, ocrLineCount, ocrLines, ocrErr := t.collectOCRTargets(ctx, state.ActiveWindow, state.ScreenshotRef, state.ScreenshotManifestRef)
		state.OCRStatus = ocrStatus
		state.OCRText = ocrText
		state.OCRLineCount = ocrLineCount
		state.OCRError = ocrErr
		state.Candidates = append(state.Candidates, ocrTargets...)
		uiResult := t.detector.Detect(ctx, computerUIDetectorRequest{
			Window:          state.ActiveWindow,
			ScreenshotRef:   state.ScreenshotRef,
			ManifestRef:     state.ScreenshotManifestRef,
			Width:           state.ScreenshotWidth,
			Height:          state.ScreenshotHeight,
			AXTargets:       state.Candidates,
			OCRText:         state.OCRText,
			OCRLines:        ocrLines,
			ExistingTargets: state.Candidates,
		})
		uiDetectorName = uiResult.Name
		uiDetectorStatus = uiResult.Status
		uiDetectorCandidateCount = len(uiResult.Targets)
		uiDetectorWarnings = uiResult.Warnings
		state.Candidates = append(state.Candidates, uiResult.Targets...)
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
			"ocr_status":              state.OCRStatus,
			"ocr_line_count":          state.OCRLineCount,
			"ocr_error":               state.OCRError,
			"ui_detector_name":        uiDetectorName,
			"ui_detector_status":      uiDetectorStatus,
			"ui_detector_candidates":  uiDetectorCandidateCount,
			"ui_detector_warnings":    uiDetectorWarnings,
			"candidates":              summarizeComputerTargets(state.Candidates, 40),
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

func (t *ComputerSee) collectOCRTargets(ctx context.Context, window computeruse.WindowRef, imagePath string, manifestPath string) ([]computeruse.ComputerTarget, string, string, int, []vision.OCRLine, string) {
	if t.runner == nil || imagePath == "" {
		return nil, "skipped", "", 0, nil, ""
	}
	result, err := t.runner.Run(ctx, vision.OCRRequest{
		ImagePath:     imagePath,
		MinConfidence: defaultDesktopOCRMinConfidence,
		Enhance:       false,
	})
	if err != nil {
		var ocrErr *vision.ToolError
		if errors.As(err, &ocrErr) {
			return nil, "error", "", 0, nil, strings.Replace(ocrErr.Code, "browser_ocr_", "desktop_ocr_", 1) + ": " + ocrErr.Message
		}
		return nil, "error", "", 0, nil, err.Error()
	}
	targets := collectComputerOCRTargets(result.Lines, window, imagePath, manifestPath)
	return targets, "success", result.Text, len(result.Lines), result.Lines, ""
}

// computerUIDetector 是截图级 UI 检测协议。AX 和 OCR 保留为独立信号源，
// detector 只负责把截图/OCR/布局上下文补成可操作的视觉候选。
type computerUIDetector interface {
	Detect(ctx context.Context, req computerUIDetectorRequest) computerUIDetectorResult
}

type computerUIDetectorRequest struct {
	Window          computeruse.WindowRef
	ScreenshotRef   string
	ManifestRef     string
	Width           int
	Height          int
	AXTargets       []computeruse.ComputerTarget
	OCRText         string
	OCRLines        []vision.OCRLine
	ExistingTargets []computeruse.ComputerTarget
}

type computerUIDetectorResult struct {
	Name     string
	Status   string
	Targets  []computeruse.ComputerTarget
	Warnings []string
}

func defaultComputerUIDetector() computerUIDetector {
	return heuristicComputerUIDetector{}
}

// heuristicComputerUIDetector 是第一版真实 detector 适配层：它消费真实截图、
// OCR 行与 AX 上下文，输出统一的 SourceVision 候选。后续可在该接口下替换
// 成模型/SDK detector，而不改变 computer_see/find/click 的调用协议。
type heuristicComputerUIDetector struct{}

func (heuristicComputerUIDetector) Detect(ctx context.Context, req computerUIDetectorRequest) computerUIDetectorResult {
	_ = ctx
	result := computerUIDetectorResult{Name: "heuristic_ui_detector", Status: "skipped"}
	if req.ScreenshotRef == "" || req.ManifestRef == "" || req.Width <= 0 || req.Height <= 0 {
		result.Warnings = append(result.Warnings, "missing screenshot metadata")
		return result
	}
	result.Status = "success"
	targets := collectComputerOCRVisionTargets(req.OCRLines, req.Window, req.ScreenshotRef, req.ManifestRef)
	targets = append(targets, collectComputerHeuristicVisionTargets(
		req.Window,
		req.ScreenshotRef,
		req.ManifestRef,
		req.Width,
		req.Height,
		firstNonNilComputerTargets(req.ExistingTargets, req.AXTargets),
		req.OCRText,
	)...)
	result.Targets = targets
	if len(targets) == 0 {
		result.Status = "empty"
	}
	return result
}

func firstNonNilComputerTargets(values ...[]computeruse.ComputerTarget) []computeruse.ComputerTarget {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
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

// ComputerVisualSnapshot 返回最近 computer_see 中可用于视觉路径的候选 target。
type ComputerVisualSnapshot struct {
	store *computeruse.Store
}

// NewComputerVisualSnapshot 创建只读视觉候选快照工具。
func NewComputerVisualSnapshot(store *computeruse.Store) *ComputerVisualSnapshot {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerVisualSnapshot{store: store}
}

func (t *ComputerVisualSnapshot) Name() string { return ToolNameComputerVisualSnapshot }

func (t *ComputerVisualSnapshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Return a compact visual snapshot from the latest computer_see state: OCR text targets and detector vision targets by default, detector-only candidates with mode=ui_detect, or all cached candidates. It returns target_id and bbox metadata only, never screenshot bytes.",
		Parameters: objectSchema(map[string]any{
			"mode":       stringProp("Candidate mode: ocr (default, OCR + vision), ui_detect (detector vision only), or all (AX + OCR + vision)."),
			"query":      stringProp("Optional natural-language filter. When set, candidates are ranked like computer_find."),
			"include_ax": boolProp("Whether to include AX targets together with OCR/vision candidates. Default false.", false),
			"limit":      intProp("Maximum candidates to return. Default 40, max 100.", 40),
		}),
	}}
}

func (t *ComputerVisualSnapshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_visual_snapshot", err), nil
	}
	if state.ActivePID <= 0 || state.ScreenshotRef == "" || state.ScreenshotManifestRef == "" {
		return computerToolError("computer_visual_snapshot_no_observation", "latest computer state has no screenshot-backed active window", "请先调用 computer_see，让 Cohort 捕获当前窗口截图和候选目标。"), nil
	}
	mode := strings.ToLower(strings.TrimSpace(asString(call.Args["mode"])))
	if mode == "" {
		mode = "ocr"
	}
	if mode != "ocr" && mode != "ui_detect" && mode != "all" {
		return computerToolError("computer_visual_snapshot_bad_mode", "mode must be ocr, ui_detect, or all", "computer_visual_snapshot 支持 mode=ocr、mode=ui_detect 或 mode=all。"), nil
	}
	includeAX := mode == "all" || (mode != "ui_detect" && asBool(call.Args["include_ax"], false))
	limit := clampDesktopLimit(asInt(call.Args["limit"], 40), 40, 100)
	query := strings.TrimSpace(asString(call.Args["query"]))
	candidates := filterComputerVisualSnapshotTargets(state.Candidates, includeAX, mode == "ui_detect")
	sourceCounts := countComputerTargetSources(candidates)
	totalCandidates := len(candidates)

	var returned any
	returnedCount := 0
	if query != "" {
		matches := rankComputerTargets(query, candidates)
		if len(matches) > limit {
			matches = matches[:limit]
		}
		returned = matches
		returnedCount = len(matches)
	} else {
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		returned = candidates
		returnedCount = len(candidates)
	}
	return agent.Outcome{Data: map[string]any{
		"status":                  agent.ToolStatusSuccess,
		"state_id":                state.ID,
		"mode":                    mode,
		"include_ax":              includeAX,
		"query":                   query,
		"active_app":              state.ActiveApp,
		"active_pid":              state.ActivePID,
		"active_window":           state.ActiveWindow,
		"screenshot_ref":          state.ScreenshotRef,
		"screenshot_manifest_ref": state.ScreenshotManifestRef,
		"screenshot_width":        state.ScreenshotWidth,
		"screenshot_height":       state.ScreenshotHeight,
		"coordinate_spaces":       []string{desktop.CoordinateSpaceScreenshotLocal, desktop.CoordinateSpaceScreenPhysical},
		"ocr_status":              state.OCRStatus,
		"ocr_line_count":          state.OCRLineCount,
		"ocr_error":               state.OCRError,
		"source_counts":           sourceCounts,
		"candidates":              returned,
		"candidate_count":         returnedCount,
		"total_visual_candidates": totalCandidates,
		"contains_screenshot":     false,
		"expires_at":              state.ExpiresAt.Format(time.RFC3339Nano),
	}, NextPrompt: "\n"}, nil
}

// ComputerExecuteStep 执行一个单步 Observe-Act-Verify GUI 动作。
type ComputerExecuteStep struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
	visualFocuses *VisualFocusStore
}

// NewComputerExecuteStep 创建单步 OAV 执行器。
func NewComputerExecuteStep(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore, visualFocuses *VisualFocusStore) *ComputerExecuteStep {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	if visualFocuses == nil {
		visualFocuses = NewVisualFocusStore()
	}
	return &ComputerExecuteStep{driver: driver, store: store, confirmations: confirmations, visualFocuses: visualFocuses}
}

func (t *ComputerExecuteStep) Name() string { return ToolNameComputerExecuteStep }

func (t *ComputerExecuteStep) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Execute one Observe-Act-Verify GUI step from the latest computer_see state. It finds a target by query for click/type actions, delegates to the existing safe computer tools, then optionally waits for visible verification text. It does not bypass confirmation rules.",
		Parameters: objectSchema(map[string]any{
			"action":               stringProp("Action to execute: click, type, or press_key."),
			"target_query":         stringProp("Required for click/type. Natural-language target query, for example 'message input box' or 'Save button'."),
			"text":                 stringProp("Required for action=type. Text is drafted only; this tool does not send/submit."),
			"key":                  stringProp("Required for action=press_key. Uses the same allowlist and confirmation rules as computer_press."),
			"reason":               stringProp("Concrete user-facing reason for this step."),
			"expected_change":      stringProp("Optional expected UI change after the action."),
			"verify_contains_text": stringProp("Optional exact text to wait for after the action."),
			"timeout_ms":           intProp("Verification wait timeout in milliseconds. Default 5000, max 30000.", defaultComputerWaitMS),
			"confirmation_token":   stringProp("Optional one-time confirmation token required when the delegated action is R2."),
		}, "action", "reason"),
	}}
}

func (t *ComputerExecuteStep) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	action := normalizeComputerExecuteAction(asString(call.Args["action"]))
	if action == "" {
		return computerToolError("computer_execute_step_bad_action", "action must be click, type, or press_key", "请明确传入 action=click/type/press_key。"), nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_execute_step", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_execute_step_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 观察并选择目标窗口。"), nil
	}

	var selected *computerTargetMatch
	actionArgs := map[string]any{
		"reason":             reason,
		"expected_change":    strings.TrimSpace(asString(call.Args["expected_change"])),
		"confirmation_token": strings.TrimSpace(asString(call.Args["confirmation_token"])),
	}
	switch action {
	case "click", "type":
		target, targetOutcome, ok := selectComputerExecuteTarget(state, asString(call.Args["target_query"]), action)
		if !ok {
			return targetOutcome, nil
		}
		selected = &target
		actionArgs["target_id"] = target.ID
		if action == "type" {
			text, textErr := requiredDesktopTypeText(call.Args)
			if textErr != nil {
				return agent.Outcome{Data: *textErr, NextPrompt: "\n"}, nil
			}
			actionArgs["text"] = text
		}
	case "press_key":
		key, keyErr := requiredDesktopActionString(call.Args, "key")
		if keyErr != nil {
			return agent.Outcome{Data: *keyErr, NextPrompt: "\n"}, nil
		}
		actionArgs["key"] = key
	default:
		return computerToolError("computer_execute_step_bad_action", "action must be click, type, or press_key", "请明确传入 action=click/type/press_key。"), nil
	}

	actionOutcome, err := t.runComputerExecuteAction(ctx, action, actionArgs)
	if err != nil {
		return agent.Outcome{}, err
	}
	actionData, ok := actionOutcome.Data.(map[string]any)
	if !ok {
		return agent.Outcome{Data: map[string]any{
			"status":         agent.ToolStatusError,
			"code":           "computer_execute_step_action_failed",
			"message":        "delegated action returned a non-map outcome",
			"action":         action,
			"action_outcome": actionOutcome.Data,
		}, NextPrompt: "\n"}, nil
	}
	actionData["executor"] = "computer_execute_step"
	actionData["executor_stage"] = "action"
	actionData["requested_action"] = action
	if selected != nil {
		actionData["target_query"] = strings.TrimSpace(asString(call.Args["target_query"]))
		actionData["selected_target"] = *selected
	}
	if actionData["status"] != agent.ToolStatusSuccess {
		return agent.Outcome{Data: actionData, NextPrompt: "\n"}, nil
	}

	verifyText := strings.TrimSpace(asString(call.Args["verify_contains_text"]))
	if verifyText == "" {
		return agent.Outcome{Data: map[string]any{
			"status":                agent.ToolStatusSuccess,
			"action":                action,
			"reason":                reason,
			"target_query":          strings.TrimSpace(asString(call.Args["target_query"])),
			"selected_target":       selected,
			"action_outcome":        actionData,
			"verification_skipped":  true,
			"verified":              actionData["verified"] == true,
			"next_recommended_tool": "computer_check",
		}, NextPrompt: "\n"}, nil
	}

	timeoutMS := clampDesktopLimit(asInt(call.Args["timeout_ms"], defaultComputerWaitMS), defaultComputerWaitMS, maxComputerWaitMS)
	verificationOutcome, err := NewComputerWait(t.driver, t.store).Run(ctx, agent.ToolCallContext{Args: map[string]any{
		"contains_text":    verifyText,
		"reason":           "verify computer_execute_step: " + reason,
		"timeout_ms":       timeoutMS,
		"poll_interval_ms": defaultComputerPollMS,
	}})
	if err != nil {
		return agent.Outcome{}, err
	}
	verificationData, ok := verificationOutcome.Data.(map[string]any)
	if !ok {
		return agent.Outcome{Data: map[string]any{
			"status":               agent.ToolStatusError,
			"code":                 "computer_execute_step_verification_failed",
			"message":              "verification returned a non-map outcome",
			"action":               action,
			"action_outcome":       actionData,
			"verification_outcome": verificationOutcome.Data,
			"verified":             false,
		}, NextPrompt: "\n"}, nil
	}
	verified := verificationData["verified"] == true
	status := agent.ToolStatusSuccess
	if !verified {
		status = agent.ToolStatusError
	}
	result := map[string]any{
		"status":               status,
		"action":               action,
		"reason":               reason,
		"target_query":         strings.TrimSpace(asString(call.Args["target_query"])),
		"selected_target":      selected,
		"verify_contains_text": verifyText,
		"action_outcome":       actionData,
		"verification_outcome": verificationData,
		"verified":             verified,
	}
	if !verified {
		result["code"] = "computer_execute_step_unverified"
		result["message"] = "action ran but verify_contains_text was not observed before timeout"
		result["hint"] = "请重新调用 computer_see 检查当前窗口，再决定是否重试或换目标。"
	}
	return agent.Outcome{Data: result, NextPrompt: "\n"}, nil
}

func (t *ComputerExecuteStep) runComputerExecuteAction(ctx context.Context, action string, args map[string]any) (agent.Outcome, error) {
	switch action {
	case "click":
		return NewComputerClickWithVisualFocus(t.driver, t.store, t.confirmations, t.visualFocuses).Run(ctx, agent.ToolCallContext{Args: args})
	case "type":
		return NewComputerType(t.driver, t.store, t.visualFocuses).Run(ctx, agent.ToolCallContext{Args: args})
	case "press_key":
		return NewComputerPress(t.driver, t.store, t.confirmations).Run(ctx, agent.ToolCallContext{Args: args})
	default:
		return computerToolError("computer_execute_step_bad_action", "action must be click, type, or press_key", "请明确传入 action=click/type/press_key。"), nil
	}
}

func normalizeComputerExecuteAction(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "click":
		return "click"
	case "type", "type_text":
		return "type"
	case "press", "press_key":
		return "press_key"
	default:
		return ""
	}
}

func selectComputerExecuteTarget(state computeruse.ComputerState, query string, action string) (computerTargetMatch, agent.Outcome, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return computerTargetMatch{}, computerToolError("computer_execute_step_bad_target_query", "target_query is required for click/type actions", "请传入要操作的目标描述，例如“消息输入框”或“保存按钮”。"), false
	}
	matches := rankComputerTargets(query, state.Candidates)
	for _, match := range matches {
		if action == "type" && match.SuggestedAction != computeruse.SuggestedActionType && !strings.Contains(strings.ToLower(match.Role), "text") {
			continue
		}
		if action == "click" && match.SuggestedAction != computeruse.SuggestedActionClick {
			continue
		}
		return match, agent.Outcome{}, true
	}
	return computerTargetMatch{}, computerToolError("computer_execute_step_target_not_found", "no cached target matched target_query and requested action", "请先调用 computer_see/computer_find 或 computer_visual_snapshot 刷新候选目标。"), false
}

// ComputerExecutePlan 执行多步 Observe-Act-Verify GUI 计划。
type ComputerExecutePlan struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
	visualFocuses *VisualFocusStore
	runner        vision.OCRRunner
	workspaceTool
}

// NewComputerExecutePlan 创建多步 OAV 执行器。
func NewComputerExecutePlan(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore, visualFocuses *VisualFocusStore, workspace string) *ComputerExecutePlan {
	return NewComputerExecutePlanWithOCRRunner(driver, store, confirmations, visualFocuses, workspace, newComputerOCRRunner(workspace))
}

// NewComputerExecutePlanWithOCRRunner 允许测试注入 OCR runner；runner 为 nil 时自动 observe 不做 OCR。
func NewComputerExecutePlanWithOCRRunner(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore, visualFocuses *VisualFocusStore, workspace string, runner vision.OCRRunner) *ComputerExecutePlan {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	if visualFocuses == nil {
		visualFocuses = NewVisualFocusStore()
	}
	return &ComputerExecutePlan{
		driver:        driver,
		store:         store,
		confirmations: confirmations,
		visualFocuses: visualFocuses,
		runner:        runner,
		workspaceTool: newWorkspaceTool(workspace),
	}
}

func (t *ComputerExecutePlan) Name() string { return ToolNameComputerExecutePlan }

func (t *ComputerExecutePlan) Schema() llm.ToolSchema {
	stepSchema := map[string]any{
		"type":        "array",
		"description": "Ordered OAV steps. Each item is an object with action=observe/find/click/type/press_key/wait_text/check_text plus action-specific fields.",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"properties": map[string]any{
				"action":               stringProp("Step action: observe, find, click, type, press_key, wait_text, or check_text."),
				"target_query":         stringProp("Target query for find/click/type steps."),
				"text":                 stringProp("Draft text for type, or expected text for check_text when contains_text is omitted."),
				"key":                  stringProp("Key for press_key."),
				"reason":               stringProp("Step-specific reason. Defaults to plan reason when omitted."),
				"expected_change":      stringProp("Optional expected UI change after click/type/press_key."),
				"verify_contains_text": stringProp("Optional text verification after click/type/press_key."),
				"contains_text":        stringProp("Text to wait for or check in wait_text/check_text."),
				"app_name":             stringProp("Optional observe app filter."),
				"title":                stringProp("Optional observe title filter."),
				"timeout_ms":           intProp("Wait timeout for text verification. Default 5000, max 30000.", defaultComputerWaitMS),
				"confirmation_token":   stringProp("Optional token for a risky step."),
			},
		},
	}
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Execute a bounded multi-step Observe-Act-Verify computer plan. It logs every step, refreshes once on stale/missing targets, blocks on R2 confirmation, refuses R3 through delegated tools, and returns a handoff payload when recovery fails.",
		Parameters: objectSchema(map[string]any{
			"reason":              stringProp("Plan-level user-facing reason."),
			"steps":               stepSchema,
			"start_step_index":    intProp("Resume from this zero-based step index. Default 0.", 0),
			"max_recoveries":      intProp("Maximum automatic recoveries per failed step. Default 1, max 2.", 1),
			"confirmation_tokens": objectProp("Optional map from step index string to one-time confirmation_token for resuming a blocked R2 step."),
		}, "reason", "steps"),
	}}
}

type computerPlanStep struct {
	Index              int
	Action             string
	TargetQuery        string
	Text               string
	Key                string
	Reason             string
	ExpectedChange     string
	VerifyContainsText string
	ContainsText       string
	AppName            string
	Title              string
	TimeoutMS          int
	ConfirmationToken  string
}

func (t *ComputerExecutePlan) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	steps, stepsErr := parseComputerPlanSteps(call.Args["steps"], reason)
	if stepsErr != nil {
		return agent.Outcome{Data: *stepsErr, NextPrompt: "\n"}, nil
	}
	startIndex := asInt(call.Args["start_step_index"], 0)
	if startIndex < 0 || startIndex >= len(steps) {
		return computerToolError("computer_execute_plan_bad_start", "start_step_index is outside the plan step range", "请传入有效的起始 step index，或从 0 开始执行。"), nil
	}
	maxRecoveries := clampDesktopLimit(asInt(call.Args["max_recoveries"], 1), 1, 2)
	tokens := asStringMap(call.Args["confirmation_tokens"])
	executed := make([]map[string]any, 0, len(steps))
	lastClickBinding := ""

	for i := startIndex; i < len(steps); i++ {
		step := steps[i]
		if token := strings.TrimSpace(tokens[fmt.Sprint(i)]); token != "" && step.ConfirmationToken == "" {
			step.ConfirmationToken = token
		}
		var stepResult map[string]any
		var err error
		var recovery map[string]any
		for attempt := 0; attempt <= maxRecoveries; attempt++ {
			if step.Action == "click" {
				binding, duplicate := t.computerPlanClickBinding(step)
				if duplicate && binding == lastClickBinding {
					return t.computerPlanHandoff(reason, steps, executed, step, "computer_execute_plan_repeated_click_blocked", "plan attempted to click the same target twice in a row", "请重新观察界面或把重复点击改成明确的 double_click/等待/检查步骤。"), nil
				}
			}
			outcome, runErr := t.runComputerPlanStep(ctx, step)
			if runErr != nil {
				return agent.Outcome{}, runErr
			}
			stepResult = computerOutcomeDataMap(outcome.Data)
			stepResult["step_index"] = i
			stepResult["step_action"] = step.Action
			stepResult["attempt"] = attempt
			if recovery != nil {
				stepResult["recovery"] = recovery
			}
			err = nil
			if stepResult["status"] == agent.ToolStatusSuccess {
				break
			}
			if isComputerPlanConfirmationRequired(stepResult) {
				return agent.Outcome{Data: map[string]any{
					"status":                 agent.ToolStatusError,
					"code":                   "computer_execute_plan_confirmation_required",
					"message":                "plan paused because one step requires user confirmation",
					"reason":                 reason,
					"blocked_step_index":     i,
					"blocked_step":           step,
					"approval_request":       stepResult["approval_request"],
					"resume_from_step_index": i,
					"executed_steps":         executed,
					"hint":                   "请调用 ask_user 获取 token，然后用 start_step_index 和 confirmation_tokens 只恢复当前 step。",
				}, NextPrompt: "\n"}, nil
			}
			if attempt >= maxRecoveries || !isComputerPlanRecoverable(stepResult) {
				break
			}
			recovery = t.recoverComputerPlanStep(ctx, step, stepResult, i, attempt)
			stepResult["recovery"] = recovery
			if recovery["status"] != agent.ToolStatusSuccess {
				break
			}
		}
		if err != nil {
			return agent.Outcome{}, err
		}
		executed = append(executed, stepResult)
		if stepResult["status"] != agent.ToolStatusSuccess {
			return t.computerPlanHandoff(reason, steps, executed, step, "computer_execute_plan_handoff_required", "plan step failed after bounded recovery", "请根据 handoff 中的最后一步和失败原因接管或调整 plan 后重试。"), nil
		}
		if step.Action == "click" {
			if binding, ok := stepResult["target_binding"].(string); ok && binding != "" {
				lastClickBinding = binding
			} else if targetID, ok := stepResult["target_id"].(string); ok {
				lastClickBinding = targetID
			}
		} else {
			lastClickBinding = ""
		}
	}
	return agent.Outcome{Data: map[string]any{
		"status":         agent.ToolStatusSuccess,
		"reason":         reason,
		"step_count":     len(steps),
		"executed_steps": executed,
		"verified":       computerPlanVerified(executed),
		"handoff":        nil,
	}, NextPrompt: "\n"}, nil
}

func (t *ComputerExecutePlan) runComputerPlanStep(ctx context.Context, step computerPlanStep) (agent.Outcome, error) {
	switch step.Action {
	case "observe":
		return NewComputerSeeWithOCRRunner(t.driver, t.store, t.workspace, t.runner).Run(ctx, agent.ToolCallContext{Args: map[string]any{
			"app_name": step.AppName,
			"title":    step.Title,
			"limit":    defaultDesktopWindowLimit,
		}})
	case "find":
		return NewComputerFind(t.store).Run(ctx, agent.ToolCallContext{Args: map[string]any{"query": step.TargetQuery}})
	case "click", "type", "press_key":
		args := map[string]any{
			"action":               step.Action,
			"target_query":         step.TargetQuery,
			"text":                 step.Text,
			"key":                  step.Key,
			"reason":               step.Reason,
			"expected_change":      step.ExpectedChange,
			"verify_contains_text": step.VerifyContainsText,
			"timeout_ms":           step.TimeoutMS,
			"confirmation_token":   step.ConfirmationToken,
		}
		outcome, err := NewComputerExecuteStep(t.driver, t.store, t.confirmations, t.visualFocuses).Run(ctx, agent.ToolCallContext{Args: args})
		data := computerOutcomeDataMap(outcome.Data)
		if data["status"] == agent.ToolStatusSuccess {
			if actionData, ok := data["action_outcome"].(map[string]any); ok {
				if targetID, ok := actionData["target_id"].(string); ok {
					data["target_id"] = targetID
					data["target_binding"] = targetID
				}
			}
		}
		outcome.Data = data
		return outcome, err
	case "wait_text":
		needle := firstNonEmpty(step.ContainsText, step.VerifyContainsText, step.Text)
		return NewComputerWait(t.driver, t.store).Run(ctx, agent.ToolCallContext{Args: map[string]any{
			"contains_text":    needle,
			"reason":           step.Reason,
			"timeout_ms":       step.TimeoutMS,
			"poll_interval_ms": defaultComputerPollMS,
		}})
	case "check_text":
		needle := firstNonEmpty(step.ContainsText, step.VerifyContainsText, step.Text)
		return NewComputerCheck(t.driver, t.store).Run(ctx, agent.ToolCallContext{Args: map[string]any{
			"expectation":   step.Reason,
			"contains_text": needle,
		}})
	default:
		return computerToolError("computer_execute_plan_bad_step_action", "unsupported plan step action", "请使用 observe/find/click/type/press_key/wait_text/check_text。"), nil
	}
}

func parseComputerPlanSteps(raw any, planReason string) ([]computerPlanStep, *agent.ToolErrorData) {
	rawSteps, ok := raw.([]any)
	if !ok || len(rawSteps) == 0 {
		err := agent.NewToolError("computer_execute_plan_bad_steps", "steps must be a non-empty array", "请传入结构化 steps 数组。")
		return nil, &err
	}
	if len(rawSteps) > 20 {
		err := agent.NewToolError("computer_execute_plan_too_many_steps", "steps length exceeds 20", "请把大型任务拆成多个较短 plan。")
		return nil, &err
	}
	steps := make([]computerPlanStep, 0, len(rawSteps))
	for index, rawStep := range rawSteps {
		stepMap, ok := rawStep.(map[string]any)
		if !ok {
			err := agent.NewToolError("computer_execute_plan_bad_step", "each step must be an object", "请把每个 step 写成包含 action 等字段的对象。")
			return nil, &err
		}
		action := normalizeComputerPlanAction(asString(stepMap["action"]))
		if action == "" {
			err := agent.NewToolError("computer_execute_plan_bad_step_action", "step action is missing or unsupported", "请使用 observe/find/click/type/press_key/wait_text/check_text。")
			return nil, &err
		}
		reason := strings.TrimSpace(asString(stepMap["reason"]))
		if reason == "" {
			reason = planReason
		}
		step := computerPlanStep{
			Index:              index,
			Action:             action,
			TargetQuery:        strings.TrimSpace(asString(stepMap["target_query"])),
			Text:               asString(stepMap["text"]),
			Key:                strings.TrimSpace(asString(stepMap["key"])),
			Reason:             reason,
			ExpectedChange:     strings.TrimSpace(asString(stepMap["expected_change"])),
			VerifyContainsText: strings.TrimSpace(asString(stepMap["verify_contains_text"])),
			ContainsText:       strings.TrimSpace(asString(stepMap["contains_text"])),
			AppName:            strings.TrimSpace(asString(stepMap["app_name"])),
			Title:              strings.TrimSpace(asString(stepMap["title"])),
			TimeoutMS:          clampDesktopLimit(asInt(stepMap["timeout_ms"], defaultComputerWaitMS), defaultComputerWaitMS, maxComputerWaitMS),
			ConfirmationToken:  strings.TrimSpace(asString(stepMap["confirmation_token"])),
		}
		if err := validateComputerPlanStep(step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func validateComputerPlanStep(step computerPlanStep) *agent.ToolErrorData {
	switch step.Action {
	case "find", "click", "type":
		if step.TargetQuery == "" {
			err := agent.NewToolError("computer_execute_plan_missing_target_query", step.Action+" step requires target_query", "请为 find/click/type step 提供目标描述。")
			return &err
		}
	case "press_key":
		if step.Key == "" {
			err := agent.NewToolError("computer_execute_plan_missing_key", "press_key step requires key", "请为 press_key step 提供 key。")
			return &err
		}
	case "wait_text", "check_text":
		if firstNonEmpty(step.ContainsText, step.VerifyContainsText, step.Text) == "" {
			err := agent.NewToolError("computer_execute_plan_missing_text", step.Action+" step requires contains_text or text", "请为等待/检查 step 提供要验证的文本。")
			return &err
		}
	}
	if step.Action == "type" && strings.TrimSpace(step.Text) == "" {
		err := agent.NewToolError("computer_execute_plan_missing_text", "type step requires text", "请为 type step 提供要起草的文本。")
		return &err
	}
	return nil
}

func normalizeComputerPlanAction(raw string) string {
	switch normalizeComputerExecuteAction(raw) {
	case "click":
		return "click"
	case "type":
		return "type"
	case "press_key":
		return "press_key"
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "observe", "see":
		return "observe"
	case "find":
		return "find"
	case "wait_text", "wait":
		return "wait_text"
	case "check_text", "check":
		return "check_text"
	default:
		return ""
	}
}

type computerRecoverPolicyDecision struct {
	Strategy         string
	Reason           string
	RetryEligible    bool
	RefreshAppName   string
	RefreshTitle     string
	FallbackOrder    []string
	NextAction       string
	HandoffIfFailed  string
	OriginalCode     string
	OriginalMessage  string
	OriginalVerified any
}

func evaluateComputerRecoverPolicy(step computerPlanStep, failure map[string]any) computerRecoverPolicyDecision {
	code := strings.TrimSpace(asString(failure["code"]))
	message := strings.TrimSpace(asString(failure["message"]))
	decision := computerRecoverPolicyDecision{
		Strategy:         "refresh_observation_then_retry",
		Reason:           "refresh computer_see state before retrying the failed GUI step",
		RetryEligible:    isComputerPlanRecoverable(failure),
		RefreshAppName:   step.AppName,
		RefreshTitle:     step.Title,
		FallbackOrder:    []string{computeruse.SourceAX, computeruse.SourceOCR, computeruse.SourceVision},
		NextAction:       "retry_failed_step",
		HandoffIfFailed:  "ask_user_or_replan",
		OriginalCode:     code,
		OriginalMessage:  message,
		OriginalVerified: failure["verified"],
	}
	switch {
	case strings.Contains(code, "state_required"):
		decision.Reason = "latest computer state is missing; observe target app/window and rebuild target cache"
	case strings.Contains(code, "target_stale"):
		decision.Reason = "cached target expired; refresh observation and rebuild semantic/visual targets"
	case strings.Contains(code, "target_not_found"):
		decision.Reason = "target was not found in cache; refresh observation and allow AX/OCR/vision fallback"
	case strings.Contains(code, "target_window"):
		decision.Reason = "target window is missing or inactive; reselect and activate the requested window"
	case strings.Contains(code, "unverified"):
		decision.Reason = "action result was not verified; refresh observation before deciding whether a retry is safe"
		decision.FallbackOrder = []string{computeruse.SourceAX, computeruse.SourceOCR}
	case code == "":
		decision.Reason = "failed step did not return a typed error code; only one bounded refresh is allowed"
		decision.RetryEligible = false
		decision.NextAction = "handoff"
	}
	if !decision.RetryEligible {
		decision.Strategy = "handoff_without_retry"
		decision.NextAction = "handoff"
	}
	return decision
}

func (d computerRecoverPolicyDecision) Map() map[string]any {
	return map[string]any{
		"strategy":          d.Strategy,
		"reason":            d.Reason,
		"retry_eligible":    d.RetryEligible,
		"refresh_app_name":  d.RefreshAppName,
		"refresh_title":     d.RefreshTitle,
		"fallback_order":    d.FallbackOrder,
		"next_action":       d.NextAction,
		"handoff_if_failed": d.HandoffIfFailed,
		"original_code":     d.OriginalCode,
		"original_message":  d.OriginalMessage,
		"original_verified": d.OriginalVerified,
	}
}

func (t *ComputerExecutePlan) recoverComputerPlanStep(ctx context.Context, step computerPlanStep, failure map[string]any, stepIndex int, attempt int) map[string]any {
	decision := evaluateComputerRecoverPolicy(step, failure)
	if !decision.RetryEligible {
		return map[string]any{
			"status":     agent.ToolStatusError,
			"code":       "computer_execute_plan_recover_not_allowed",
			"message":    "recover policy refused automatic retry",
			"step_index": stepIndex,
			"attempt":    attempt,
			"policy":     decision.Map(),
		}
	}
	outcome, err := NewComputerSeeWithOCRRunner(t.driver, t.store, t.workspace, t.runner).Run(ctx, agent.ToolCallContext{Args: map[string]any{
		"app_name": decision.RefreshAppName,
		"title":    decision.RefreshTitle,
		"limit":    defaultDesktopWindowLimit,
	}})
	data := computerOutcomeDataMap(outcome.Data)
	data["step_index"] = stepIndex
	data["attempt"] = attempt
	data["strategy"] = decision.Strategy
	data["policy"] = decision.Map()
	data["fallback_order"] = decision.FallbackOrder
	data["next_action"] = decision.NextAction
	if err != nil {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_execute_plan_recover_failed"
		data["message"] = err.Error()
	}
	return data
}

func (t *ComputerExecutePlan) computerPlanClickBinding(step computerPlanStep) (string, bool) {
	state, err := t.store.LatestState()
	if err != nil {
		return "", false
	}
	target, _, ok := selectComputerExecuteTarget(state, step.TargetQuery, "click")
	if !ok {
		return "", false
	}
	if target.Source == computeruse.SourceOCR || target.Source == computeruse.SourceVision {
		return strings.Join([]string{target.Source, target.ScreenshotRef, canonicalDesktopBBox(target.BBox)}, "|"), true
	}
	return target.ID, true
}

func (t *ComputerExecutePlan) computerPlanHandoff(reason string, steps []computerPlanStep, executed []map[string]any, failedStep computerPlanStep, code string, message string, hint string) agent.Outcome {
	lastOutcome := map[string]any{}
	if len(executed) > 0 {
		lastOutcome = executed[len(executed)-1]
	}
	return agent.Outcome{Data: map[string]any{
		"status":            agent.ToolStatusError,
		"code":              code,
		"message":           message,
		"reason":            reason,
		"failed_step_index": failedStep.Index,
		"failed_step":       failedStep,
		"executed_steps":    executed,
		"handoff": map[string]any{
			"failed_step_index": failedStep.Index,
			"failed_action":     failedStep.Action,
			"target_query":      failedStep.TargetQuery,
			"last_outcome":      lastOutcome,
			"recommended_next":  "请重新调用 computer_see 检查当前窗口，必要时让用户手动完成当前步骤。",
		},
		"remaining_steps": len(steps) - failedStep.Index - 1,
		"hint":            hint,
	}, NextPrompt: "\n"}
}

func computerOutcomeDataMap(data any) map[string]any {
	switch value := data.(type) {
	case map[string]any:
		return value
	case agent.ToolErrorData:
		return map[string]any{"status": agent.ToolStatusError, "code": value.Code, "message": value.Message, "hint": value.Hint}
	default:
		return map[string]any{"status": agent.ToolStatusError, "code": "computer_execute_plan_bad_outcome", "message": fmt.Sprintf("unexpected outcome type %T", data)}
	}
}

func isComputerPlanConfirmationRequired(data map[string]any) bool {
	return data["code"] == "desktop_action_confirmation_required" || data["code"] == "computer_execute_plan_confirmation_required"
}

func isComputerPlanRecoverable(data map[string]any) bool {
	code := strings.TrimSpace(asString(data["code"]))
	if code == "" {
		return false
	}
	return strings.Contains(code, "state_required") ||
		strings.Contains(code, "target_not_found") ||
		strings.Contains(code, "target_stale") ||
		strings.Contains(code, "target_window") ||
		strings.Contains(code, "unverified")
}

func computerPlanVerified(steps []map[string]any) bool {
	for _, step := range steps {
		if step["status"] != agent.ToolStatusSuccess {
			return false
		}
		if value, ok := step["verified"]; ok && value != true {
			return false
		}
	}
	return len(steps) > 0
}

func asStringMap(value any) map[string]string {
	result := map[string]string{}
	raw, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for key, item := range raw {
		if text := strings.TrimSpace(asString(item)); text != "" {
			result[key] = text
		}
	}
	return result
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
	if activateErr := activateDesktopTarget(ctx, t.driver, pid); activateErr != nil {
		return desktopToolError(activateErr), nil
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

func collectComputerOCRTargets(lines []vision.OCRLine, window computeruse.WindowRef, screenshotRef string, manifestRef string) []computeruse.ComputerTarget {
	targets := []computeruse.ComputerTarget{}
	for _, line := range lines {
		if strings.TrimSpace(line.Text) == "" || len(line.BBox) != 4 {
			continue
		}
		bbox := [4]int{line.BBox[0], line.BBox[1], line.BBox[2], line.BBox[3]}
		if bbox[2] <= bbox[0] || bbox[3] <= bbox[1] {
			continue
		}
		confidence := line.Confidence
		if confidence <= 0 {
			confidence = 0.5
		}
		risk := classifyDesktopVisualClickRisk(line.Text, "")
		targets = append(targets, computeruse.ComputerTarget{
			Label:              line.Text,
			Role:               "OCRText",
			Confidence:         confidence,
			Source:             computeruse.SourceOCR,
			Bounds:             desktop.Bounds{X: bbox[0], Y: bbox[1], Width: bbox[2] - bbox[0], Height: bbox[3] - bbox[1]},
			CoordinateSpace:    desktop.CoordinateSpaceScreenshotLocal,
			Window:             window,
			SuggestedAction:    computeruse.SuggestedActionClick,
			RiskHint:           string(risk),
			ScreenshotRef:      screenshotRef,
			ScreenshotManifest: manifestRef,
			BBox:               bbox,
		})
	}
	return targets
}

func collectComputerOCRVisionTargets(lines []vision.OCRLine, window computeruse.WindowRef, screenshotRef string, manifestRef string) []computeruse.ComputerTarget {
	targets := []computeruse.ComputerTarget{}
	for _, line := range lines {
		if strings.TrimSpace(line.Text) == "" || len(line.BBox) != 4 {
			continue
		}
		bbox := [4]int{line.BBox[0], line.BBox[1], line.BBox[2], line.BBox[3]}
		if bbox[2] <= bbox[0] || bbox[3] <= bbox[1] {
			continue
		}
		confidence := line.Confidence
		if confidence <= 0 {
			confidence = 0.5
		}
		risk := classifyDesktopVisualClickRisk(line.Text, "")
		if action, ok := classifyComputerVisionAction(line.Text); ok {
			targets = append(targets, computeruse.ComputerTarget{
				Label:              line.Text,
				Role:               "VisualCandidate",
				Description:        "heuristic visual target from OCR text",
				Confidence:         confidence * 0.85,
				Source:             computeruse.SourceVision,
				Bounds:             desktop.Bounds{X: bbox[0], Y: bbox[1], Width: bbox[2] - bbox[0], Height: bbox[3] - bbox[1]},
				CoordinateSpace:    desktop.CoordinateSpaceScreenshotLocal,
				Window:             window,
				SuggestedAction:    action,
				RiskHint:           string(risk),
				ScreenshotRef:      screenshotRef,
				ScreenshotManifest: manifestRef,
				BBox:               bbox,
			})
		}
	}
	return targets
}

func collectComputerHeuristicVisionTargets(window computeruse.WindowRef, screenshotRef string, manifestRef string, screenshotWidth int, screenshotHeight int, existing []computeruse.ComputerTarget, ocrText string) []computeruse.ComputerTarget {
	if screenshotRef == "" || manifestRef == "" || screenshotWidth <= 0 || screenshotHeight <= 0 {
		return nil
	}
	if !computerWindowLooksLikeChat(window, ocrText) {
		return nil
	}
	if hasVisibleEditableAXTarget(existing, window) {
		return nil
	}
	bbox, ok := heuristicChatInputBBox(screenshotWidth, screenshotHeight)
	if !ok {
		return nil
	}
	return []computeruse.ComputerTarget{{
		Label:              "底部聊天输入框 消息输入框",
		Role:               "VisualInputRegion",
		Description:        "heuristic bottom input region for WebView or self-rendered chat apps; use for drafting messages only",
		Confidence:         0.66,
		Source:             computeruse.SourceVision,
		Bounds:             desktop.Bounds{X: bbox[0], Y: bbox[1], Width: bbox[2] - bbox[0], Height: bbox[3] - bbox[1]},
		CoordinateSpace:    desktop.CoordinateSpaceScreenshotLocal,
		Window:             window,
		SuggestedAction:    computeruse.SuggestedActionType,
		RiskHint:           string(desktopRiskReversible),
		ScreenshotRef:      screenshotRef,
		ScreenshotManifest: manifestRef,
		BBox:               bbox,
	}}
}

func computerWindowLooksLikeChat(window computeruse.WindowRef, ocrText string) bool {
	appText := normalizeComputerText(strings.Join([]string{
		window.AppName,
		window.Title,
	}, " "))
	if containsAny(appText, []string{
		"微信", "wechat", "weixin",
		"飞书", "lark", "slack", "teams", "discord",
		"qq", "tim", "messages", "message", "chat", "聊天",
	}) {
		return true
	}
	ocr := normalizeComputerText(ocrText)
	return containsAny(strings.Join([]string{
		appText,
		ocr,
	}, " "), []string{"聊天", "发送消息", "输入消息", "发消息"})
}

func hasVisibleEditableAXTarget(targets []computeruse.ComputerTarget, window computeruse.WindowRef) bool {
	for _, target := range targets {
		if target.Source != computeruse.SourceAX {
			continue
		}
		if target.SuggestedAction != computeruse.SuggestedActionType && !strings.Contains(strings.ToLower(target.Role), "text") {
			continue
		}
		if computerTargetVisibleInWindow(target, window) {
			return true
		}
	}
	return false
}

func heuristicChatInputBBox(width int, height int) ([4]int, bool) {
	if width < 240 || height < 180 {
		return [4]int{}, false
	}
	x1Ratio := 0.28
	if width >= 1000 {
		x1Ratio = 0.36
	}
	x1 := int(float64(width) * x1Ratio)
	y1 := int(float64(height) * 0.82)
	x2 := int(float64(width) * 0.96)
	y2 := int(float64(height) * 0.965)
	if x2-x1 < 120 || y2-y1 < 40 {
		return [4]int{}, false
	}
	return [4]int{x1, y1, x2, y2}, true
}

func classifyComputerVisionAction(text string) (string, bool) {
	normalized := normalizeComputerText(text)
	if shouldIssueDesktopVisualFocusToken(normalized, "") {
		return computeruse.SuggestedActionType, true
	}
	if containsAny(normalized, []string{
		"按钮", "打开", "保存", "发送", "提交", "搜索", "下一步", "继续", "取消", "关闭",
		"button", "open", "save", "send", "submit", "search", "next", "continue", "cancel", "close",
	}) {
		return computeruse.SuggestedActionClick, true
	}
	return "", false
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

func filterComputerVisualSnapshotTargets(targets []computeruse.ComputerTarget, includeAX bool, detectorOnly bool) []computeruse.ComputerTarget {
	filtered := make([]computeruse.ComputerTarget, 0, len(targets))
	for _, target := range targets {
		if detectorOnly {
			if target.Source == computeruse.SourceVision {
				filtered = append(filtered, target)
			}
			continue
		}
		switch target.Source {
		case computeruse.SourceOCR, computeruse.SourceVision:
			filtered = append(filtered, target)
		case computeruse.SourceAX:
			if includeAX {
				filtered = append(filtered, target)
			}
		}
	}
	return filtered
}

func countComputerTargetSources(targets []computeruse.ComputerTarget) map[string]int {
	counts := map[string]int{}
	for _, target := range targets {
		source := strings.TrimSpace(target.Source)
		if source == "" {
			source = "unknown"
		}
		counts[source]++
	}
	return counts
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
	if !computerTargetVisibleInWindow(target, target.Window) {
		return 0
	}
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
	axTextRole := target.Source == computeruse.SourceAX && strings.Contains(strings.ToLower(target.Role), "text")
	if computerQueryWantsInput(q) && (target.SuggestedAction == computeruse.SuggestedActionType || axTextRole) {
		score += 0.8
	}
	if computerQueryWantsClick(q) && target.SuggestedAction == computeruse.SuggestedActionClick {
		score += 0.4
	}
	return score * target.Confidence
}

func computerTargetVisibleInWindow(target computeruse.ComputerTarget, window computeruse.WindowRef) bool {
	if target.Bounds.Width <= 0 || target.Bounds.Height <= 0 {
		return true
	}
	switch target.CoordinateSpace {
	case desktop.CoordinateSpaceScreenPhysical:
		return boundsIntersect(target.Bounds, window.Bounds)
	case desktop.CoordinateSpaceScreenshotLocal:
		if window.Bounds.Width <= 0 || window.Bounds.Height <= 0 {
			return true
		}
		return target.Bounds.X < window.Bounds.Width &&
			target.Bounds.Y < window.Bounds.Height &&
			target.Bounds.X+target.Bounds.Width > 0 &&
			target.Bounds.Y+target.Bounds.Height > 0
	default:
		return true
	}
}

func boundsIntersect(a desktop.Bounds, b desktop.Bounds) bool {
	if b.Width <= 0 || b.Height <= 0 {
		return true
	}
	return a.X < b.X+b.Width &&
		a.X+a.Width > b.X &&
		a.Y < b.Y+b.Height &&
		a.Y+a.Height > b.Y
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

func newComputerOCRRunner(workspace string) vision.OCRRunner {
	scriptPath := resolveRuntimeScriptPath(workspace, browserOCRHelperFileName)
	if absolutePath, err := filepath.Abs(scriptPath); err == nil {
		scriptPath = absolutePath
	}
	return vision.NewPythonOCRRunner("python3", scriptPath, vision.DefaultOCRTimeout)
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
