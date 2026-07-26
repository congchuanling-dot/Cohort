package tools

import (
	"context"
	"strings"
	"time"

	"cohert/internal/agent"
	"cohert/internal/computeruse"
	"cohert/internal/desktop"
	"cohert/internal/llm"
)

// ComputerClick 点击由 computer_see/find 缓存的目标，不接受裸坐标或底层 node_id。
type ComputerClick struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
	visualFocuses *VisualFocusStore
}

// NewComputerClick 创建受 target cache 约束的点击工具。
func NewComputerClick(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerClick {
	return NewComputerClickWithVisualFocus(driver, store, confirmations, NewVisualFocusStore())
}

// NewComputerClickWithVisualFocus 创建共享视觉焦点令牌存储的点击工具。
func NewComputerClickWithVisualFocus(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore, visualFocuses *VisualFocusStore) *ComputerClick {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	if visualFocuses == nil {
		visualFocuses = NewVisualFocusStore()
	}
	return &ComputerClick{driver: driver, store: store, confirmations: confirmations, visualFocuses: visualFocuses}
}

func (t *ComputerClick) Name() string { return ToolNameComputerClick }

func (t *ComputerClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Click a cached computer target_id from computer_see/computer_find. The tool refreshes the AX snapshot, rejects stale targets, refuses R3 actions, and asks for one-time confirmation for R2 side effects. Never pass coordinates or raw AX node IDs.",
		Parameters: objectSchema(map[string]any{
			"target_id":          stringProp("Cached target_id returned by computer_find or computer_see candidates."),
			"reason":             stringProp("Concrete user-facing reason for this click."),
			"expected_change":    stringProp("Optional expected UI change after the click, used for audit and follow-up check guidance."),
			"confirmation_token": stringProp("Required only for R2 actions. Obtain it from ask_user with the returned approval_request."),
		}, "target_id", "reason"),
	}}
}

func (t *ComputerClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	targetID, idErr := requiredDesktopActionString(call.Args, "target_id")
	if idErr != nil {
		return agent.Outcome{Data: *idErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	target, err := t.store.Target(targetID)
	if err != nil {
		return computerCacheError("computer_click", err), nil
	}
	if target.Source != computeruse.SourceAX || target.AXNodeID == "" {
		return t.clickComputerVisualTarget(ctx, target, reason, strings.TrimSpace(asString(call.Args["confirmation_token"])), strings.TrimSpace(asString(call.Args["expected_change"])))
	}
	if target.Window.PID <= 0 {
		return computerToolError("computer_click_bad_target", "target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, target.Window.PID); err != nil {
		return desktopToolError(err), nil
	}
	node, errOutcome, ok := t.refreshComputerTargetNode(ctx, target)
	if !ok {
		return errOutcome, nil
	}
	if node.Enabled != nil && !*node.Enabled {
		return desktopActionError(
			"computer_click_target_disabled",
			"the requested computer target is disabled",
			"请重新调用 computer_see/computer_find，选择 enabled=true 的可操作目标。",
		), nil
	}
	if !containsAXAction(node.Actions, "AXPress") && (node.Bounds.Width <= 0 || node.Bounds.Height <= 0) {
		return desktopActionError(
			"computer_click_bad_bounds",
			"the requested computer target has no click action or clickable bounds",
			"请重新选择支持 AXPress 或带有效 bounds 的目标。",
		), nil
	}

	risk := classifyDesktopClickRisk(node)
	if risk == desktopRiskHigh {
		return desktopActionError(
			"computer_action_high_risk_refused",
			"this click is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 computer_click 自动执行。",
		), nil
	}
	token := strings.TrimSpace(asString(call.Args["confirmation_token"]))
	if containsAXAction(node.Actions, "AXPress") {
		if risk == desktopRiskExternal {
			approval := ActionApproval{Operation: desktopAXPressOperation, PID: target.Window.PID, NodeID: target.AXNodeID, Reason: reason}
			if !t.confirmations.Consume(token, approval) {
				return desktopApprovalRequiredOutcome(approval, node, risk), nil
			}
		}
		result, err := t.driver.AXPress(ctx, desktop.AXPressRequest{
			PID:                 target.Window.PID,
			NodeID:              target.AXNodeID,
			ExpectedRole:        target.ExpectedRole,
			ExpectedTitle:       target.ExpectedTitle,
			ExpectedDescription: target.ExpectedDescription,
		})
		if err == nil {
			return agent.Outcome{
				Data: map[string]any{
					"status":          agent.ToolStatusSuccess,
					"target_id":       target.ID,
					"pid":             result.PID,
					"node_id":         result.NodeID,
					"action":          result.Action,
					"risk":            risk,
					"performed":       result.Performed,
					"verified":        result.Performed,
					"expected_change": strings.TrimSpace(asString(call.Args["expected_change"])),
				},
				NextPrompt: "\n",
			}, nil
		}
		if risk == desktopRiskExternal || node.Bounds.Width <= 0 || node.Bounds.Height <= 0 {
			return desktopToolError(err), nil
		}
	}

	if risk == desktopRiskExternal {
		approval := ActionApproval{Operation: desktopClickOperation, PID: target.Window.PID, NodeID: target.AXNodeID, Reason: reason}
		if !t.confirmations.Consume(token, approval) {
			return desktopClickApprovalRequiredOutcome(approval, node, risk), nil
		}
	}
	result, err := t.driver.Click(ctx, desktop.ClickRequest{
		PID:                 target.Window.PID,
		NodeID:              target.AXNodeID,
		ExpectedRole:        target.ExpectedRole,
		ExpectedTitle:       target.ExpectedTitle,
		ExpectedDescription: target.ExpectedDescription,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"target_id":        target.ID,
		"pid":              result.PID,
		"node_id":          result.NodeID,
		"action":           result.Action,
		"risk":             risk,
		"performed":        result.Performed,
		"active_before":    result.ActiveBefore,
		"active_after":     result.ActiveAfter,
		"x":                result.X,
		"y":                result.Y,
		"coordinate_space": result.CoordinateSpace,
		"verified":         verified,
		"expected_change":  strings.TrimSpace(asString(call.Args["expected_change"])),
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_click_unverified"
		data["message"] = "computer click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续点击，重新调用 computer_see 和 computer_find 确认目标窗口状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *ComputerClick) clickComputerVisualTarget(ctx context.Context, target computeruse.ComputerTarget, reason string, token string, expectedChange string) (agent.Outcome, error) {
	if target.Source != computeruse.SourceOCR && target.Source != computeruse.SourceVision {
		return computerToolError(
			"computer_click_unsupported_target",
			"computer_click only supports AX, OCR, or vision targets from computer_see",
			"请重新调用 computer_see/computer_find 选择当前候选目标，不要手写 target_id。",
		), nil
	}
	if target.Window.PID <= 0 {
		return computerToolError("computer_click_bad_target", "target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), nil
	}
	manifest, manifestErr := readDesktopVisualManifest(target.ScreenshotManifest)
	if manifestErr != nil {
		return agent.Outcome{Data: *manifestErr, NextPrompt: "\n"}, nil
	}
	if validationErr := validateDesktopVisualManifest(manifest, target.Window.PID, target.ScreenshotRef, target.BBox); validationErr != nil {
		return agent.Outcome{Data: *validationErr, NextPrompt: "\n"}, nil
	}
	expectedText := firstNonEmpty(target.Label, target.Description, target.Role)
	risk := classifyDesktopVisualClickRisk(expectedText, reason)
	if risk == desktopRiskHigh {
		return desktopActionError(
			"computer_action_high_risk_refused",
			"this visual click is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 computer_click 自动执行。",
		), nil
	}
	bboxKey := canonicalDesktopBBox(target.BBox)
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: desktopVisualClickOperation,
			PID:       target.Window.PID,
			ImagePath: target.ScreenshotRef,
			BBox:      bboxKey,
			Reason:    reason,
		}
		if !t.confirmations.Consume(token, approval) {
			return desktopVisualClickApprovalRequiredOutcome(approval, expectedText, risk), nil
		}
	}
	if err := activateDesktopTarget(ctx, t.driver, target.Window.PID); err != nil {
		return desktopToolError(err), nil
	}
	screenX, screenY := mapScreenshotBBoxCenterToScreen(manifest, target.BBox)
	result, err := t.driver.VisualClick(ctx, desktop.VisualClickRequest{
		PID:             target.Window.PID,
		X:               screenX,
		Y:               screenY,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveAfter
	data := map[string]any{
		"status":                  agent.ToolStatusSuccess,
		"target_id":               target.ID,
		"source":                  target.Source,
		"pid":                     result.PID,
		"action":                  result.Action,
		"risk":                    risk,
		"expected_text":           expectedText,
		"bbox_key":                bboxKey,
		"x":                       result.X,
		"y":                       result.Y,
		"coordinate_space":        result.CoordinateSpace,
		"source_coordinate_space": desktop.CoordinateSpaceScreenshotLocal,
		"performed":               result.Performed,
		"active_before":           result.ActiveBefore,
		"active_after":            result.ActiveAfter,
		"verified":                verified,
		"expected_change":         expectedChange,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_click_unverified"
		data["message"] = "computer visual click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续点击，重新调用 computer_see 和 computer_find 确认目标窗口状态。"
	} else if target.SuggestedAction == computeruse.SuggestedActionType || shouldIssueDesktopVisualFocusToken(expectedText, reason) {
		focusToken, ttl, issueErr := t.visualFocuses.Issue(VisualFocusGrant{
			PID:       target.Window.PID,
			ImagePath: target.ScreenshotRef,
			BBox:      bboxKey,
			Reason:    reason,
		})
		if issueErr != nil {
			data["status"] = agent.ToolStatusError
			data["code"] = "computer_visual_focus_token_issue_failed"
			data["message"] = "computer visual click succeeded but visual focus token could not be issued"
			data["hint"] = "请重新执行 computer_click；没有 visual_focus_token 时不要依赖视觉焦点继续输入。"
		} else {
			data["visual_focus_token"] = focusToken
			data["visual_focus_expires_in_seconds"] = int(ttl.Seconds())
		}
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *ComputerClick) refreshComputerTargetNode(ctx context.Context, target computeruse.ComputerTarget) (desktop.AXNode, agent.Outcome, bool) {
	snapshot, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
		PID:             target.Window.PID,
		MaxDepth:        desktopActionSnapshotDepth,
		MaxNodes:        desktopActionSnapshotNodes,
		IncludeZeroSize: false,
	})
	if err != nil {
		return desktop.AXNode{}, desktopToolError(err), false
	}
	node, found := findAXNode(snapshot.Root, target.AXNodeID)
	if !found {
		return desktop.AXNode{}, desktopActionError(
			"computer_target_stale",
			"the cached target AX node is absent from the current snapshot",
			"界面可能已变化；请重新调用 computer_see 和 computer_find 获取新的 target_id。",
		), false
	}
	if node.Role != target.ExpectedRole || node.Title != target.ExpectedTitle || node.Description != target.ExpectedDescription {
		return desktop.AXNode{}, desktopActionError(
			"computer_target_stale",
			"the cached target no longer matches its expected role, title, or description",
			"界面可能已变化；请重新调用 computer_see 和 computer_find，不要复用旧 target_id。",
		), false
	}
	return node, agent.Outcome{}, true
}

// ComputerType 聚焦缓存输入目标并起草文本，不负责发送或提交。
type ComputerType struct {
	driver        desktop.Driver
	store         *computeruse.Store
	visualFocuses *VisualFocusStore
}

// NewComputerType 创建文本起草工具。
func NewComputerType(driver desktop.Driver, store *computeruse.Store, visualFocuses ...*VisualFocusStore) *ComputerType {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	var focusStore *VisualFocusStore
	if len(visualFocuses) > 0 {
		focusStore = visualFocuses[0]
	}
	if focusStore == nil {
		focusStore = NewVisualFocusStore()
	}
	return &ComputerType{driver: driver, store: store, visualFocuses: focusStore}
}

func (t *ComputerType) Name() string { return ToolNameComputerType }

func (t *ComputerType) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Draft text into a cached editable computer target. This never sends or submits content and never echoes full text in the result. Use target_id from computer_find; pass current_focus only when the editable field is already verified focused.",
		Parameters: objectSchema(map[string]any{
			"target_id":          stringProp("Cached editable target_id from computer_find, or current_focus to type into the already focused editable field."),
			"text":               stringProp("Text to draft. The result returns only length and line count."),
			"reason":             stringProp("Concrete user-facing reason for drafting this text."),
			"visual_focus_token": stringProp("Optional one-time token returned by computer_click after visually focusing an input/search target. Used only with current_focus or when AX focus cannot prove WebView focus."),
		}, "target_id", "text", "reason"),
	}}
}

func (t *ComputerType) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	targetID, idErr := requiredDesktopActionString(call.Args, "target_id")
	if idErr != nil {
		return agent.Outcome{Data: *idErr, NextPrompt: "\n"}, nil
	}
	text, textErr := requiredDesktopTypeText(call.Args)
	if textErr != nil {
		return agent.Outcome{Data: *textErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}

	pid := 0
	focusedTarget := ""
	usedVisualFocus := false
	visualFocusFallbackUsed := false
	visualFocusToken := strings.TrimSpace(asString(call.Args["visual_focus_token"]))
	if targetID != "current_focus" {
		target, targetErr := t.store.Target(targetID)
		if targetErr != nil {
			return computerCacheError("computer_type", targetErr), nil
		}
		pid = target.Window.PID
		if pid <= 0 {
			return computerToolError("computer_type_bad_target", "target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), nil
		}
		if target.Source == computeruse.SourceAX && target.AXNodeID != "" {
			if activateErr := activateDesktopTarget(ctx, t.driver, pid); activateErr != nil {
				return desktopToolError(activateErr), nil
			}
			node, errOutcome, ok := (&ComputerClick{driver: t.driver}).refreshComputerTargetNode(ctx, target)
			if !ok {
				return errOutcome, nil
			}
			if node.Enabled != nil && !*node.Enabled {
				return desktopActionError("computer_type_target_disabled", "the requested input target is disabled", "请重新选择 enabled=true 的输入目标。"), nil
			}
			if !isEditableAXNode(node) {
				return desktopActionError("computer_type_not_editable", "computer_type only supports editable targets", "请选择 AXTextField、AXTextArea、AXSearchField 或其他可编辑文本节点。"), nil
			}
			focus, focusErr := t.driver.AXFocus(ctx, desktop.AXFocusRequest{
				PID:                 pid,
				NodeID:              target.AXNodeID,
				ExpectedRole:        target.ExpectedRole,
				ExpectedTitle:       target.ExpectedTitle,
				ExpectedDescription: target.ExpectedDescription,
			})
			if focusErr != nil {
				return desktopToolError(focusErr), nil
			}
			if !focus.Performed || !focus.Focused {
				return desktopActionError(
					"computer_type_focus_unverified",
					"computer_type could not verify editable focus before typing",
					"请重新调用 computer_click 聚焦输入框，或重新 computer_see/computer_find 后再试。",
				), nil
			}
		} else {
			focusOutcome, ok, focusErr := t.focusComputerVisualTypeTarget(ctx, target, reason)
			if focusErr != nil {
				return focusOutcome, focusErr
			}
			if !ok {
				return focusOutcome, nil
			}
			usedVisualFocus = true
		}
		focusedTarget = target.ID
	} else {
		state, err := t.store.LatestState()
		if err != nil {
			return computerCacheError("computer_type", err), nil
		}
		pid = state.ActivePID
		if pid <= 0 {
			return computerToolError("computer_type_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择目标窗口。"), nil
		}
		if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
			return desktopToolError(err), nil
		}
	}

	result, err := t.driver.TypeText(ctx, desktop.TypeTextRequest{PID: pid, Text: text})
	if err != nil && usedVisualFocus && isDesktopTypeTextFocusError(err) {
		result, err = t.driver.TypeText(ctx, desktop.TypeTextRequest{PID: pid, Text: text, AllowVisualFocus: true})
		visualFocusFallbackUsed = err == nil
	}
	if err != nil && visualFocusToken != "" && isDesktopTypeTextFocusError(err) {
		if _, ok := t.visualFocuses.Consume(visualFocusToken, pid); !ok {
			return desktopActionError(
				"computer_type_visual_focus_token_invalid",
				"visual_focus_token is invalid, expired, already used, or bound to a different pid",
				"请重新用 computer_click 点击输入框 target，拿到新的 visual_focus_token 后再输入。",
			), nil
		}
		result, err = t.driver.TypeText(ctx, desktop.TypeTextRequest{PID: pid, Text: text, AllowVisualFocus: true})
		visualFocusFallbackUsed = err == nil
	}
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":             agent.ToolStatusSuccess,
		"target_id":          focusedTarget,
		"pid":                result.PID,
		"action":             result.Action,
		"reason":             reason,
		"text_length":        result.TextLength,
		"line_count":         result.LineCount,
		"focus_role":         result.FocusRole,
		"focus_title":        result.FocusTitle,
		"focus_description":  result.FocusDescription,
		"focus_verification": result.FocusVerification,
		"visual_focus_used":  visualFocusFallbackUsed,
		"performed":          result.Performed,
		"active_before":      result.ActiveBefore,
		"active_after":       result.ActiveAfter,
		"verified":           verified,
		"content_returned":   false,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_type_unverified"
		data["message"] = "computer text was typed but target foreground state was not verified"
		data["hint"] = "请停止继续输入或发送，重新调用 computer_see 确认目标应用状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *ComputerType) focusComputerVisualTypeTarget(ctx context.Context, target computeruse.ComputerTarget, reason string) (agent.Outcome, bool, error) {
	if target.Source != computeruse.SourceOCR && target.Source != computeruse.SourceVision {
		return computerToolError("computer_type_unsupported_target", "computer_type supports AX editable targets or OCR/vision input targets only", "请重新选择输入框、搜索框或消息框 target_id。"), false, nil
	}
	expectedText := firstNonEmpty(target.Label, target.Description, target.Role)
	if target.SuggestedAction != computeruse.SuggestedActionType && !shouldIssueDesktopVisualFocusToken(expectedText, reason) {
		return desktopActionError("computer_type_not_editable", "computer_type only supports visual targets that look like input fields", "请重新选择 OCR/vision 识别出的输入框、搜索框或消息框目标。"), false, nil
	}
	manifest, manifestErr := readDesktopVisualManifest(target.ScreenshotManifest)
	if manifestErr != nil {
		return agent.Outcome{Data: *manifestErr, NextPrompt: "\n"}, false, nil
	}
	if validationErr := validateDesktopVisualManifest(manifest, target.Window.PID, target.ScreenshotRef, target.BBox); validationErr != nil {
		return agent.Outcome{Data: *validationErr, NextPrompt: "\n"}, false, nil
	}
	risk := classifyDesktopVisualClickRisk(expectedText, reason)
	if risk != desktopRiskReversible {
		return desktopActionError("computer_type_visual_target_risky", "visual typing target is not classified as a reversible focus action", "请重新选择明确的输入框、搜索框或消息框；发送、保存、提交等目标不能用于 computer_type。"), false, nil
	}
	if err := activateDesktopTarget(ctx, t.driver, target.Window.PID); err != nil {
		return desktopToolError(err), false, nil
	}
	screenX, screenY := mapScreenshotBBoxCenterToScreen(manifest, target.BBox)
	result, err := t.driver.VisualClick(ctx, desktop.VisualClickRequest{
		PID:             target.Window.PID,
		X:               screenX,
		Y:               screenY,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), false, nil
	}
	if !result.Performed || !result.ActiveAfter {
		return agent.Outcome{
			Data: map[string]any{
				"status":        agent.ToolStatusError,
				"code":          "computer_type_visual_focus_unverified",
				"message":       "computer_type could not verify visual focus before typing",
				"hint":          "请重新调用 computer_see/computer_find，选择当前可见输入目标后再试。",
				"target_id":     target.ID,
				"pid":           result.PID,
				"performed":     result.Performed,
				"active_before": result.ActiveBefore,
				"active_after":  result.ActiveAfter,
				"verified":      false,
			},
			NextPrompt: "\n",
		}, false, nil
	}
	return agent.Outcome{}, true, nil
}

// ComputerPress 对最近 computer_see 的目标窗口发送受限按键。
type ComputerPress struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerPress 创建受最新电脑状态约束的按键工具。
func NewComputerPress(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerPress {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerPress{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerPress) Name() string { return ToolNameComputerPress }

func (t *ComputerPress) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Press a restricted key in the latest computer_see target window. Navigation keys run directly. Enter and deletion/submit shortcuts require one-time confirmation unless intent=open_selected_result makes Enter low-risk navigation. This does not type text.",
		Parameters: objectSchema(map[string]any{
			"key":                stringProp("Restricted key or shortcut, for example Escape, Tab, Shift+Tab, ArrowUp, ArrowDown, Enter, Cmd+Enter, Delete, Backspace."),
			"intent":             stringProp("Optional semantic intent. Use open_selected_result only when Enter opens the currently selected transient search/dropdown result."),
			"reason":             stringProp("Concrete user-facing reason for this key press."),
			"confirmation_token": stringProp("Required only for R2 keys. Obtain it from ask_user with the returned approval_request."),
		}, "key", "reason"),
	}}
}

func (t *ComputerPress) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_press", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_press_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择目标窗口。"), nil
	}
	key, keyErr := requiredComputerString(call.Args, "key", "computer_press_bad_request", "请提供受支持的按键，例如 Escape、Tab、Enter 或 Cmd+Enter。")
	if keyErr != nil {
		return agent.Outcome{Data: *keyErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredComputerString(call.Args, "reason", "computer_press_bad_request", "请说明这次按键的具体目的。")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	intent, intentOK := normalizeDesktopPressKeyIntent(asString(call.Args["intent"]))
	if !intentOK {
		return desktopActionError("computer_press_bad_intent", "computer_press intent is not supported", "当前只支持空 intent 或 open_selected_result；发送、提交、删除等副作用不能用 intent 降级。"), nil
	}
	normalizedKey, risk, ok := classifyDesktopKeyRisk(key, intent)
	if !ok {
		return desktopActionError("computer_press_unsupported", "computer_press only supports a restricted key allowlist", "当前只支持 Escape、Tab、Shift+Tab、方向键、PageUp/PageDown、Home/End，以及需要确认的 Enter/Cmd+Enter/Ctrl+Enter/Delete/Backspace。"), nil
	}
	if risk == desktopRiskExternal {
		approval := ActionApproval{Operation: desktopPressKeyOperation, PID: state.ActivePID, Key: normalizedKey, Reason: reason}
		if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
			return desktopPressKeyApprovalRequiredOutcome(approval, risk), nil
		}
	}
	if activateErr := activateDesktopTarget(ctx, t.driver, state.ActivePID); activateErr != nil {
		return desktopToolError(activateErr), nil
	}
	result, err := t.driver.PressKey(ctx, desktop.PressKeyRequest{PID: state.ActivePID, Key: normalizedKey})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":        agent.ToolStatusSuccess,
		"state_id":      state.ID,
		"pid":           result.PID,
		"key":           result.Key,
		"intent":        intent,
		"action":        result.Action,
		"risk":          risk,
		"performed":     result.Performed,
		"active_before": result.ActiveBefore,
		"active_after":  result.ActiveAfter,
		"verified":      verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_press_unverified"
		data["message"] = "computer key event was sent but target foreground state was not verified"
		data["hint"] = "请停止连续按键，重新调用 computer_see 确认目标窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerWait 等待最近 computer_see 的窗口出现文本，或仅短暂等待界面稳定。
type ComputerWait struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerWait 创建等待工具。
func NewComputerWait(driver desktop.Driver, store *computeruse.Store) *ComputerWait {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerWait{driver: driver, store: store}
}

func (t *ComputerWait) Name() string { return ToolNameComputerWait }

func (t *ComputerWait) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Wait for the latest computer_see target window. Prefer contains_text to wait until visible AX text appears. Without contains_text, this only pauses briefly after an action.",
		Parameters: objectSchema(map[string]any{
			"contains_text":    stringProp("Optional exact text expected to appear in the target window."),
			"reason":           stringProp("Concrete reason for waiting."),
			"timeout_ms":       intProp("Maximum wait time in milliseconds. Default 5000, max 30000.", defaultComputerWaitMS),
			"poll_interval_ms": intProp("Polling interval in milliseconds. Default 250, min 100.", defaultComputerPollMS),
		}, "reason"),
	}}
}

func (t *ComputerWait) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_wait", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_wait_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择目标窗口。"), nil
	}
	reason, reasonErr := requiredComputerString(call.Args, "reason", "computer_wait_bad_request", "请说明等待的具体目的。")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	timeoutMS := clampDesktopLimit(asInt(call.Args["timeout_ms"], defaultComputerWaitMS), defaultComputerWaitMS, maxComputerWaitMS)
	pollMS := clampDesktopLimit(asInt(call.Args["poll_interval_ms"], defaultComputerPollMS), defaultComputerPollMS, maxComputerWaitMS)
	pollMS = max(pollMS, minComputerPollMS)
	needle := strings.TrimSpace(asString(call.Args["contains_text"]))
	if err := activateDesktopTarget(ctx, t.driver, state.ActivePID); err != nil {
		return desktopToolError(err), nil
	}
	if needle == "" {
		timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return agent.Outcome{}, ctx.Err()
		case <-timer.C:
		}
		return agent.Outcome{Data: map[string]any{
			"status":     agent.ToolStatusSuccess,
			"state_id":   state.ID,
			"pid":        state.ActivePID,
			"reason":     reason,
			"waited_ms":  timeoutMS,
			"verified":   true,
			"wait_mode":  "sleep",
			"target_app": state.ActiveApp,
		}, NextPrompt: "\n"}, nil
	}

	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	attempts := 0
	for {
		attempts++
		ax, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
			PID:             state.ActivePID,
			MaxDepth:        desktopActionSnapshotDepth,
			MaxNodes:        desktopActionSnapshotNodes,
			IncludeZeroSize: false,
		})
		if err != nil {
			return desktopToolError(err), nil
		}
		evidence := findComputerTextEvidence(ax.Root, needle, 5)
		if len(evidence) > 0 {
			return agent.Outcome{Data: map[string]any{
				"status":        agent.ToolStatusSuccess,
				"state_id":      state.ID,
				"pid":           state.ActivePID,
				"reason":        reason,
				"contains_text": needle,
				"verified":      true,
				"evidence":      evidence,
				"attempts":      attempts,
				"wait_mode":     "text",
			}, NextPrompt: "\n"}, nil
		}
		if !time.Now().Add(time.Duration(pollMS) * time.Millisecond).Before(deadline) {
			break
		}
		timer := time.NewTimer(time.Duration(pollMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return agent.Outcome{}, ctx.Err()
		case <-timer.C:
		}
	}
	return agent.Outcome{Data: map[string]any{
		"status":        agent.ToolStatusError,
		"code":          "computer_wait_timeout",
		"message":       "timed out waiting for expected computer text",
		"hint":          "请重新调用 computer_see 检查窗口状态，或延长 timeout_ms 后再等待。",
		"state_id":      state.ID,
		"pid":           state.ActivePID,
		"reason":        reason,
		"contains_text": needle,
		"verified":      false,
		"attempts":      attempts,
		"wait_mode":     "text",
	}, NextPrompt: "\n"}, nil
}

func requiredComputerString(args map[string]any, key string, code string, hint string) (string, *agent.ToolErrorData) {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		err := agent.NewToolError(code, key+" must be a non-empty string", hint)
		return "", &err
	}
	return strings.TrimSpace(value), nil
}
