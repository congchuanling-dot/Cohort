package tools

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cohort/internal/agent"
	"cohort/internal/computeruse"
	"cohort/internal/desktop"
	"cohort/internal/llm"
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

// ComputerDoubleClick 双击由 computer_see/find 缓存的目标，不接受裸坐标。
type ComputerDoubleClick struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerDoubleClick 创建受 target cache 约束的双击工具。
func NewComputerDoubleClick(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerDoubleClick {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerDoubleClick{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerDoubleClick) Name() string { return ToolNameComputerDoubleClick }

func (t *ComputerDoubleClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Double-click a cached computer target_id from computer_see/computer_find. The tool refreshes AX targets or validates screenshot manifest targets, refuses R3 actions, and asks for one-time confirmation for R2 side effects. Never pass raw coordinates.",
		Parameters: objectSchema(map[string]any{
			"target_id":          stringProp("Cached target_id returned by computer_find or computer_see candidates."),
			"reason":             stringProp("Concrete user-facing reason for this double click."),
			"expected_change":    stringProp("Optional expected UI change after the double click."),
			"confirmation_token": stringProp("Required only for R2 actions. Obtain it from ask_user with the returned approval_request."),
		}, "target_id", "reason"),
	}}
}

func (t *ComputerDoubleClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
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
		return computerCacheError("computer_double_click", err), nil
	}
	resolved, errOutcome, ok := resolveComputerPointerTarget(ctx, t.driver, target, "computer_double_click")
	if !ok {
		return errOutcome, nil
	}
	risk := desktopRiskReversible
	if resolved.hasNode {
		risk = classifyDesktopClickRisk(resolved.node)
	} else {
		risk = classifyDesktopVisualClickRisk(firstNonEmpty(target.Label, target.Description, target.Role), reason)
	}
	if risk == desktopRiskHigh {
		return desktopActionError(
			"computer_action_high_risk_refused",
			"this double click is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 computer_double_click 自动执行。",
		), nil
	}
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: computerDoubleClickOperation,
			PID:       target.Window.PID,
			BBox:      resolved.binding,
			Reason:    reason,
		}
		if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
			return computerApprovalRequiredOutcome(approval, risk, "computer_double_click"), nil
		}
	}
	result, err := t.driver.DoubleClick(ctx, desktop.DoubleClickRequest{
		PID:             target.Window.PID,
		X:               resolved.x,
		Y:               resolved.y,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"target_id":        target.ID,
		"source":           target.Source,
		"pid":              result.PID,
		"action":           result.Action,
		"risk":             risk,
		"reason":           reason,
		"expected_change":  strings.TrimSpace(asString(call.Args["expected_change"])),
		"x":                result.X,
		"y":                result.Y,
		"coordinate_space": result.CoordinateSpace,
		"performed":        result.Performed,
		"active_before":    result.ActiveBefore,
		"active_after":     result.ActiveAfter,
		"verified":         verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_double_click_unverified"
		data["message"] = "computer double click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续双击，重新调用 computer_see 和 computer_find 确认目标窗口状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerRightClick 右键点击由 computer_see/find 缓存的目标，用于打开上下文菜单。
type ComputerRightClick struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerRightClick 创建受 target cache 约束的右键工具。
func NewComputerRightClick(driver desktop.Driver, store *computeruse.Store) *ComputerRightClick {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerRightClick{driver: driver, store: store}
}

func (t *ComputerRightClick) Name() string { return ToolNameComputerRightClick }

func (t *ComputerRightClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Right-click a cached computer target_id from computer_see/computer_find to open a context menu. This is R1 navigation and never accepts raw coordinates.",
		Parameters: objectSchema(map[string]any{
			"target_id":       stringProp("Cached target_id returned by computer_find or computer_see candidates."),
			"reason":          stringProp("Concrete user-facing reason for opening the context menu."),
			"expected_change": stringProp("Optional expected UI change after the right click."),
		}, "target_id", "reason"),
	}}
}

func (t *ComputerRightClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
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
		return computerCacheError("computer_right_click", err), nil
	}
	resolved, errOutcome, ok := resolveComputerPointerTarget(ctx, t.driver, target, "computer_right_click")
	if !ok {
		return errOutcome, nil
	}
	result, err := t.driver.RightClick(ctx, desktop.RightClickRequest{
		PID:             target.Window.PID,
		X:               resolved.x,
		Y:               resolved.y,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"target_id":        target.ID,
		"source":           target.Source,
		"pid":              result.PID,
		"action":           result.Action,
		"risk":             desktopRiskReversible,
		"reason":           reason,
		"expected_change":  strings.TrimSpace(asString(call.Args["expected_change"])),
		"x":                result.X,
		"y":                result.Y,
		"coordinate_space": result.CoordinateSpace,
		"performed":        result.Performed,
		"active_before":    result.ActiveBefore,
		"active_after":     result.ActiveAfter,
		"verified":         verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_right_click_unverified"
		data["message"] = "computer right click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续右键，重新调用 computer_see 和 computer_find 确认目标窗口状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

type resolvedComputerPointerTarget struct {
	x       int
	y       int
	binding string
	node    desktop.AXNode
	hasNode bool
}

func resolveComputerPointerTarget(ctx context.Context, driver desktop.Driver, target computeruse.ComputerTarget, operation string) (resolvedComputerPointerTarget, agent.Outcome, bool) {
	if target.Window.PID <= 0 {
		return resolvedComputerPointerTarget{}, computerToolError(operation+"_bad_target", "target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), false
	}
	if err := activateDesktopTarget(ctx, driver, target.Window.PID); err != nil {
		return resolvedComputerPointerTarget{}, desktopToolError(err), false
	}
	if target.Source == computeruse.SourceAX && target.AXNodeID != "" {
		node, errOutcome, ok := (&ComputerClick{driver: driver}).refreshComputerTargetNode(ctx, target)
		if !ok {
			return resolvedComputerPointerTarget{}, errOutcome, false
		}
		if node.Enabled != nil && !*node.Enabled {
			return resolvedComputerPointerTarget{}, desktopActionError(
				operation+"_target_disabled",
				"the requested computer target is disabled",
				"请重新调用 computer_see/computer_find，选择 enabled=true 的可操作目标。",
			), false
		}
		if node.Bounds.Width <= 0 || node.Bounds.Height <= 0 {
			return resolvedComputerPointerTarget{}, desktopActionError(
				operation+"_bad_bounds",
				"the requested computer target has no clickable bounds",
				"请重新选择带有效 bounds 的当前可见目标。",
			), false
		}
		return resolvedComputerPointerTarget{
			x:       node.Bounds.X + node.Bounds.Width/2,
			y:       node.Bounds.Y + node.Bounds.Height/2,
			binding: target.ID + "|" + target.AXNodeID,
			node:    node,
			hasNode: true,
		}, agent.Outcome{}, true
	}
	if target.Source == computeruse.SourceOCR || target.Source == computeruse.SourceVision {
		manifest, manifestErr := readDesktopVisualManifest(target.ScreenshotManifest)
		if manifestErr != nil {
			return resolvedComputerPointerTarget{}, agent.Outcome{Data: *manifestErr, NextPrompt: "\n"}, false
		}
		if validationErr := validateDesktopVisualManifest(manifest, target.Window.PID, target.ScreenshotRef, target.BBox); validationErr != nil {
			return resolvedComputerPointerTarget{}, agent.Outcome{Data: *validationErr, NextPrompt: "\n"}, false
		}
		x, y := mapScreenshotBBoxCenterToScreen(manifest, target.BBox)
		return resolvedComputerPointerTarget{
			x:       x,
			y:       y,
			binding: strings.Join([]string{target.ID, target.Source, target.ScreenshotRef, canonicalDesktopBBox(target.BBox)}, "|"),
		}, agent.Outcome{}, true
	}
	return resolvedComputerPointerTarget{}, computerToolError(
		operation+"_unsupported_target",
		operation+" only supports AX, OCR, or vision targets from computer_see",
		"请重新调用 computer_see/computer_find 选择当前候选目标，不要手写 target_id。",
	), false
}

func computerApprovalRequiredOutcome(approval ActionApproval, risk desktopActionRisk, operation string) agent.Outcome {
	return agent.Outcome{Data: map[string]any{
		"status":  agent.ToolStatusError,
		"code":    "desktop_action_confirmation_required",
		"message": operation + " requires one-time user confirmation",
		"hint":    "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，用同一参数和 token 重试一次 " + operation + "。",
		"approval_request": map[string]any{
			"operation": approval.Operation,
			"pid":       approval.PID,
			"bbox":      approval.BBox,
			"reason":    approval.Reason,
		},
		"risk": risk,
	}, NextPrompt: "\n"}
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
	windowID := state.ActiveWindow.WindowID
	activated, activateErr := t.driver.Activate(ctx, desktop.ActivateRequest{PID: state.ActivePID, WindowID: windowID})
	if activateErr != nil {
		return desktopToolError(activateErr), nil
	}
	if windowID != "" && !activated.WindowVerified {
		return computerToolError("computer_press_window_unverified", "target window focus was not verified before key press", "请重新调用 computer_see 或 computer_window_switch 获取最新窗口状态。"), nil
	}
	result, err := t.driver.PressKey(ctx, desktop.PressKeyRequest{PID: state.ActivePID, WindowID: windowID, Key: normalizedKey})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter && (windowID == "" || result.WindowVerified)
	data := map[string]any{
		"status":          agent.ToolStatusSuccess,
		"state_id":        state.ID,
		"pid":             result.PID,
		"window_id":       result.WindowID,
		"window_verified": result.WindowVerified,
		"key":             result.Key,
		"intent":          intent,
		"action":          result.Action,
		"risk":            risk,
		"performed":       result.Performed,
		"active_before":   result.ActiveBefore,
		"active_after":    result.ActiveAfter,
		"verified":        verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_press_unverified"
		data["message"] = "computer key event was sent but target foreground state was not verified"
		data["hint"] = "请停止连续按键，重新调用 computer_see 确认目标窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerScroll 在最近 computer_see 的目标窗口中执行受限滚动。
type ComputerScroll struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerScroll 创建滚动工具。
func NewComputerScroll(driver desktop.Driver, store *computeruse.Store) *ComputerScroll {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerScroll{driver: driver, store: store}
}

func (t *ComputerScroll) Name() string { return ToolNameComputerScroll }

func (t *ComputerScroll) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Scroll the latest computer_see target window. This is R1 navigation only and requires a recent computer_see state; it does not accept raw screen coordinates.",
		Parameters: objectSchema(map[string]any{
			"direction": stringProp("Scroll direction: up, down, left, or right."),
			"ticks":     intProp("Scroll strength from 1 to 10. Default 3.", 3),
			"reason":    stringProp("Concrete user-facing reason for scrolling."),
		}, "direction", "reason"),
	}}
}

func (t *ComputerScroll) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_scroll", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_scroll_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择目标窗口。"), nil
	}
	direction := strings.ToLower(strings.TrimSpace(asString(call.Args["direction"])))
	ticks := clampDesktopLimit(asInt(call.Args["ticks"], 3), 3, 10)
	reason, reasonErr := requiredComputerString(call.Args, "reason", "computer_scroll_bad_request", "请说明滚动的具体目的。")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	deltaX, deltaY, ok := computerScrollDelta(direction, ticks)
	if !ok {
		return computerToolError("computer_scroll_bad_direction", "direction must be one of: up, down, left, right", "请使用明确方向，不要传入坐标。"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, state.ActivePID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.Scroll(ctx, desktop.ScrollRequest{PID: state.ActivePID, DeltaX: deltaX, DeltaY: deltaY})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":        agent.ToolStatusSuccess,
		"state_id":      state.ID,
		"pid":           result.PID,
		"action":        result.Action,
		"risk":          desktopRiskReversible,
		"direction":     direction,
		"ticks":         ticks,
		"delta_x":       result.DeltaX,
		"delta_y":       result.DeltaY,
		"reason":        reason,
		"performed":     result.Performed,
		"active_before": result.ActiveBefore,
		"active_after":  result.ActiveAfter,
		"verified":      verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_scroll_unverified"
		data["message"] = "computer scroll was sent but target foreground state was not verified"
		data["hint"] = "请停止连续滚动，重新调用 computer_see 确认目标窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func computerScrollDelta(direction string, ticks int) (int, int, bool) {
	delta := ticks * 120
	switch direction {
	case "up":
		return 0, delta, true
	case "down":
		return 0, -delta, true
	case "left":
		return delta, 0, true
	case "right":
		return -delta, 0, true
	default:
		return 0, 0, false
	}
}

// ComputerDrag 从缓存 target 拖拽到另一个缓存 target 或相对偏移。
type ComputerDrag struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerDrag 创建受确认保护的拖拽工具。
func NewComputerDrag(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerDrag {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerDrag{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerDrag) Name() string { return ToolNameComputerDrag }

func (t *ComputerDrag) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Drag from a cached computer target to another cached target or by a bounded relative offset. Dragging may move files or reorder items, so it always requires a one-time confirmation_token. Never pass raw screen coordinates.",
		Parameters: objectSchema(map[string]any{
			"target_id":          stringProp("Cached source target_id from computer_find or computer_see candidates."),
			"to_target_id":       stringProp("Optional cached destination target_id. If omitted, provide delta_x and/or delta_y."),
			"delta_x":            intProp("Optional relative horizontal drag offset in screen pixels. Used only when to_target_id is empty. Max absolute value 1200.", 0),
			"delta_y":            intProp("Optional relative vertical drag offset in screen pixels. Used only when to_target_id is empty. Max absolute value 1200.", 0),
			"reason":             stringProp("Concrete user-facing reason for this drag."),
			"expected_change":    stringProp("Optional expected UI change after the drag, used for audit and follow-up check guidance."),
			"confirmation_token": stringProp("Required. Obtain it from ask_user with the returned approval_request."),
		}, "target_id", "reason"),
	}}
}

func (t *ComputerDrag) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	targetID, idErr := requiredDesktopActionString(call.Args, "target_id")
	if idErr != nil {
		return agent.Outcome{Data: *idErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	source, err := t.store.Target(targetID)
	if err != nil {
		return computerCacheError("computer_drag", err), nil
	}
	if source.Window.PID <= 0 {
		return computerToolError("computer_drag_bad_target", "source target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), nil
	}
	startX, startY, startErr := computerTargetScreenCenter(source)
	if startErr != nil {
		return agent.Outcome{Data: *startErr, NextPrompt: "\n"}, nil
	}
	endX, endY, destinationID, endErr := t.computerDragEndPoint(call.Args, source)
	if endErr != nil {
		return agent.Outcome{Data: *endErr, NextPrompt: "\n"}, nil
	}
	approval := ActionApproval{
		Operation: computerDragOperation,
		PID:       source.Window.PID,
		BBox:      strings.Join([]string{targetID, destinationID, asString(call.Args["delta_x"]), asString(call.Args["delta_y"])}, "|"),
		Reason:    reason,
	}
	if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
		return agent.Outcome{Data: map[string]any{
			"status":  agent.ToolStatusError,
			"code":    "desktop_action_confirmation_required",
			"message": "this computer drag requires one-time user confirmation",
			"hint":    "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，用同一 target_id、to_target_id/delta、reason 和 token 重试一次 computer_drag。",
			"approval_request": map[string]any{
				"operation": approval.Operation,
				"pid":       approval.PID,
				"bbox":      approval.BBox,
				"reason":    approval.Reason,
			},
			"risk": desktopRiskExternal,
		}, NextPrompt: "\n"}, nil
	}
	if err := activateDesktopTarget(ctx, t.driver, source.Window.PID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.Drag(ctx, desktop.DragRequest{
		PID:             source.Window.PID,
		StartX:          startX,
		StartY:          startY,
		EndX:            endX,
		EndY:            endY,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"target_id":        source.ID,
		"to_target_id":     destinationID,
		"pid":              result.PID,
		"action":           result.Action,
		"risk":             desktopRiskExternal,
		"reason":           reason,
		"expected_change":  strings.TrimSpace(asString(call.Args["expected_change"])),
		"start_x":          result.StartX,
		"start_y":          result.StartY,
		"end_x":            result.EndX,
		"end_y":            result.EndY,
		"coordinate_space": result.CoordinateSpace,
		"performed":        result.Performed,
		"active_before":    result.ActiveBefore,
		"active_after":     result.ActiveAfter,
		"verified":         verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_drag_unverified"
		data["message"] = "computer drag was sent but target foreground state was not verified"
		data["hint"] = "请停止连续拖拽，重新调用 computer_see 确认目标窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *ComputerDrag) computerDragEndPoint(args map[string]any, source computeruse.ComputerTarget) (int, int, string, *agent.ToolErrorData) {
	toTargetID := strings.TrimSpace(asString(args["to_target_id"]))
	if toTargetID != "" {
		destination, err := t.store.Target(toTargetID)
		if err != nil {
			outcome := computerCacheError("computer_drag", err)
			if toolErr, ok := outcome.Data.(agent.ToolErrorData); ok {
				return 0, 0, "", &toolErr
			}
			generic := agent.NewToolError("computer_drag_bad_destination", "destination target is unavailable", "请重新调用 computer_see/computer_find 获取目标。")
			return 0, 0, "", &generic
		}
		if destination.Window.PID != source.Window.PID {
			err := agent.NewToolError("computer_drag_cross_window_unsupported", "source and destination targets are in different windows", "当前拖拽只允许同一目标窗口内的缓存 target。")
			return 0, 0, "", &err
		}
		endX, endY, endErr := computerTargetScreenCenter(destination)
		return endX, endY, destination.ID, endErr
	}
	deltaX := asInt(args["delta_x"], 0)
	deltaY := asInt(args["delta_y"], 0)
	if deltaX == 0 && deltaY == 0 {
		err := agent.NewToolError("computer_drag_bad_destination", "computer_drag requires to_target_id or non-zero delta_x/delta_y", "请使用缓存目标作为终点，或提供受限相对偏移。")
		return 0, 0, "", &err
	}
	if absInt(deltaX) > 1200 || absInt(deltaY) > 1200 {
		err := agent.NewToolError("computer_drag_delta_too_large", "computer_drag delta exceeds the maximum absolute value", "请把拖拽拆成更小步骤，单次 delta_x/delta_y 绝对值不能超过 1200。")
		return 0, 0, "", &err
	}
	startX, startY, startErr := computerTargetScreenCenter(source)
	if startErr != nil {
		return 0, 0, "", startErr
	}
	return startX + deltaX, startY + deltaY, "", nil
}

// ComputerDrop 从一个缓存 target 拖放到另一个缓存 target。
type ComputerDrop struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerDrop 创建受确认保护的拖放工具。
func NewComputerDrop(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerDrop {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerDrop{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerDrop) Name() string { return ToolNameComputerDrop }

func (t *ComputerDrop) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Drop a cached source target onto a cached destination target in the same computer_see window. This is stricter than computer_drag: it never accepts raw coordinates or relative deltas, and always requires one-time user confirmation.",
		Parameters: objectSchema(map[string]any{
			"source_target_id":      stringProp("Cached source target_id from computer_find or computer_see candidates."),
			"destination_target_id": stringProp("Cached destination target_id from the same target window."),
			"reason":                stringProp("Concrete user-facing reason for this drop."),
			"expected_change":       stringProp("Optional expected UI change after the drop, used for audit and follow-up check guidance."),
			"confirmation_token":    stringProp("Required. Obtain it from ask_user with the returned approval_request."),
		}, "source_target_id", "destination_target_id", "reason"),
	}}
}

func (t *ComputerDrop) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	sourceID, sourceErr := requiredDesktopActionString(call.Args, "source_target_id")
	if sourceErr != nil {
		return agent.Outcome{Data: *sourceErr, NextPrompt: "\n"}, nil
	}
	destinationID, destinationErr := requiredDesktopActionString(call.Args, "destination_target_id")
	if destinationErr != nil {
		return agent.Outcome{Data: *destinationErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	source, err := t.store.Target(sourceID)
	if err != nil {
		return computerCacheError("computer_drop", err), nil
	}
	destination, err := t.store.Target(destinationID)
	if err != nil {
		return computerCacheError("computer_drop", err), nil
	}
	if source.Window.PID <= 0 || destination.Window.PID <= 0 {
		return computerToolError("computer_drop_bad_target", "source and destination targets must be bound to valid PIDs", "请重新调用 computer_see/computer_find 刷新目标。"), nil
	}
	if source.Window.PID != destination.Window.PID || source.Window.WindowID != destination.Window.WindowID {
		return computerToolError("computer_drop_cross_window_unsupported", "source and destination targets must be in the same window", "当前拖放只允许同一目标窗口内的缓存 target，跨窗口拖放暂不自动执行。"), nil
	}
	resolvedSource, sourceOutcome, ok := resolveComputerPointerTarget(ctx, t.driver, source, "computer_drop")
	if !ok {
		return sourceOutcome, nil
	}
	resolvedDestination, destinationOutcome, ok := resolveComputerPointerTarget(ctx, t.driver, destination, "computer_drop")
	if !ok {
		return destinationOutcome, nil
	}
	approval := ActionApproval{
		Operation: computerDropOperation,
		PID:       source.Window.PID,
		BBox:      resolvedSource.binding + "->" + resolvedDestination.binding,
		Reason:    reason,
	}
	if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
		return computerApprovalRequiredOutcome(approval, desktopRiskExternal, "computer_drop"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, source.Window.PID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.Drag(ctx, desktop.DragRequest{
		PID:             source.Window.PID,
		StartX:          resolvedSource.x,
		StartY:          resolvedSource.y,
		EndX:            resolvedDestination.x,
		EndY:            resolvedDestination.y,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":                agent.ToolStatusSuccess,
		"source_target_id":      source.ID,
		"destination_target_id": destination.ID,
		"pid":                   result.PID,
		"action":                "Drop",
		"driver_action":         result.Action,
		"risk":                  desktopRiskExternal,
		"reason":                reason,
		"expected_change":       strings.TrimSpace(asString(call.Args["expected_change"])),
		"start_x":               result.StartX,
		"start_y":               result.StartY,
		"end_x":                 result.EndX,
		"end_y":                 result.EndY,
		"coordinate_space":      result.CoordinateSpace,
		"performed":             result.Performed,
		"active_before":         result.ActiveBefore,
		"active_after":          result.ActiveAfter,
		"verified":              verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_drop_unverified"
		data["message"] = "computer drop was sent but target foreground state was not verified"
		data["hint"] = "请停止连续拖放，重新调用 computer_see 检查当前窗口和目标位置。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func computerTargetScreenCenter(target computeruse.ComputerTarget) (int, int, *agent.ToolErrorData) {
	if target.CoordinateSpace == desktop.CoordinateSpaceScreenPhysical && target.Bounds.Width > 0 && target.Bounds.Height > 0 {
		return target.Bounds.X + target.Bounds.Width/2, target.Bounds.Y + target.Bounds.Height/2, nil
	}
	if (target.Source == computeruse.SourceOCR || target.Source == computeruse.SourceVision) && target.ScreenshotManifest != "" {
		manifest, manifestErr := readDesktopVisualManifest(target.ScreenshotManifest)
		if manifestErr != nil {
			return 0, 0, manifestErr
		}
		if validationErr := validateDesktopVisualManifest(manifest, target.Window.PID, target.ScreenshotRef, target.BBox); validationErr != nil {
			return 0, 0, validationErr
		}
		x, y := mapScreenshotBBoxCenterToScreen(manifest, target.BBox)
		return x, y, nil
	}
	err := agent.NewToolError("computer_drag_bad_target_bounds", "target does not have screen-physical bounds or a valid screenshot manifest", "请重新调用 computer_see/computer_find 选择当前可见目标。")
	return 0, 0, &err
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ComputerClipboardWrite 写入系统剪贴板，不读取或返回原剪贴板内容。
type ComputerClipboardWrite struct {
	driver desktop.Driver
}

// NewComputerClipboardWrite 创建剪贴板写入工具。
func NewComputerClipboardWrite(driver desktop.Driver) *ComputerClipboardWrite {
	return &ComputerClipboardWrite{driver: driver}
}

func (t *ComputerClipboardWrite) Name() string { return ToolNameComputerClipboardWrite }

func (t *ComputerClipboardWrite) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Write text to the system clipboard without reading or returning previous clipboard content. Use this before computer_paste when paste is more reliable than keystroke typing.",
		Parameters: objectSchema(map[string]any{
			"text":   stringProp("Text to write to clipboard. The result returns only length and line count."),
			"reason": stringProp("Concrete user-facing reason for writing clipboard content."),
		}, "text", "reason"),
	}}
}

func (t *ComputerClipboardWrite) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	text, textErr := requiredDesktopTypeText(call.Args)
	if textErr != nil {
		return agent.Outcome{Data: *textErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	result, err := t.driver.ClipboardWrite(ctx, desktop.ClipboardWriteRequest{Text: text})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{Data: map[string]any{
		"status":           agent.ToolStatusSuccess,
		"action":           result.Action,
		"risk":             desktopRiskReversible,
		"reason":           reason,
		"performed":        result.Performed,
		"text_length":      result.TextLength,
		"line_count":       result.LineCount,
		"content_returned": false,
		"reads_clipboard":  false,
	}, NextPrompt: "\n"}, nil
}

// ComputerPaste 将可选文本写入剪贴板后粘贴到最新目标窗口。
type ComputerPaste struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerPaste 创建粘贴工具。
func NewComputerPaste(driver desktop.Driver, store *computeruse.Store) *ComputerPaste {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerPaste{driver: driver, store: store}
}

func (t *ComputerPaste) Name() string { return ToolNameComputerPaste }

func (t *ComputerPaste) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Paste into the latest computer_see target window. If text is provided, the tool first writes that text to clipboard and then sends Cmd+V. It never reads clipboard content and never sends/submits.",
		Parameters: objectSchema(map[string]any{
			"text":   stringProp("Optional text to write before pasting. If omitted, paste the current clipboard without reading it."),
			"reason": stringProp("Concrete user-facing reason for pasting into the target window."),
		}, "reason"),
	}}
}

func (t *ComputerPaste) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_paste", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_paste_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 选择目标窗口。"), nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	textWritten := false
	textLength := 0
	lineCount := 0
	if _, hasText := call.Args["text"]; hasText {
		text, textErr := requiredDesktopTypeText(call.Args)
		if textErr != nil {
			return agent.Outcome{Data: *textErr, NextPrompt: "\n"}, nil
		}
		writeResult, writeErr := t.driver.ClipboardWrite(ctx, desktop.ClipboardWriteRequest{Text: text})
		if writeErr != nil {
			return desktopToolError(writeErr), nil
		}
		textWritten = writeResult.Performed
		textLength = writeResult.TextLength
		lineCount = writeResult.LineCount
	}
	if err := activateDesktopTarget(ctx, t.driver, state.ActivePID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.ClipboardPaste(ctx, desktop.ClipboardPasteRequest{PID: state.ActivePID})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"state_id":         state.ID,
		"pid":              result.PID,
		"action":           result.Action,
		"risk":             desktopRiskReversible,
		"reason":           reason,
		"text_written":     textWritten,
		"text_length":      textLength,
		"line_count":       lineCount,
		"content_returned": false,
		"reads_clipboard":  false,
		"performed":        result.Performed,
		"active_before":    result.ActiveBefore,
		"active_after":     result.ActiveAfter,
		"verified":         verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_paste_unverified"
		data["message"] = "computer paste was sent but target foreground state was not verified"
		data["hint"] = "请停止继续输入或发送，重新调用 computer_see 确认目标应用状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerWindowSwitch 切换到匹配的可见窗口，并更新最小 active state。
type ComputerWindowSwitch struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerWindowSwitch 创建窗口切换工具。
func NewComputerWindowSwitch(driver desktop.Driver, store *computeruse.Store) *ComputerWindowSwitch {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerWindowSwitch{driver: driver, store: store}
}

func (t *ComputerWindowSwitch) Name() string { return ToolNameComputerWindowSwitch }

func (t *ComputerWindowSwitch) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Switch to a visible desktop window by app_name/title/window_id, or to the first non-active visible window when no filter is provided. This is R1 navigation and updates the latest active computer state.",
		Parameters: objectSchema(map[string]any{
			"app_name": stringProp("Optional case-insensitive application-name substring filter, for example Finder or Chrome."),
			"title":    stringProp("Optional case-insensitive window-title substring filter."),
			"window_id": stringProp("Optional exact window_id from computer_see or desktop_windows. " +
				"When provided, it disambiguates multiple windows of the same app."),
			"index":  intProp("Zero-based index among matched visible windows. Default 0.", 0),
			"reason": stringProp("Concrete user-facing reason for switching windows."),
		}, "reason"),
	}}
}

func (t *ComputerWindowSwitch) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	appName := strings.TrimSpace(asString(call.Args["app_name"]))
	title := strings.TrimSpace(asString(call.Args["title"]))
	windowID := strings.TrimSpace(asString(call.Args["window_id"]))
	index := asInt(call.Args["index"], 0)
	if index < 0 {
		return computerToolError("computer_window_switch_bad_index", "index must be non-negative", "请传入从 0 开始的窗口序号，或使用 window_id 消除歧义。"), nil
	}
	result, err := t.driver.ListWindows(ctx, desktop.ListWindowsRequest{AppName: appName, Title: title, Limit: 50})
	if err != nil {
		return desktopToolError(err), nil
	}
	matches := filterComputerSwitchWindows(result.Windows, appName, title, windowID, appName == "" && title == "" && windowID == "")
	if len(matches) == 0 {
		return agent.Outcome{Data: agent.NewToolError(
			"computer_window_switch_no_match",
			"no visible window matched the requested switch target",
			"请先调用 computer_see 或 desktop_windows 查看当前可见窗口，再使用 app_name/title/window_id 精确切换。",
		), NextPrompt: "\n"}, nil
	}
	if index >= len(matches) {
		return agent.Outcome{Data: agent.NewToolError(
			"computer_window_switch_index_out_of_range",
			"requested index is outside the matched window list",
			"请使用工具返回的 matched_count 范围内序号，或用 window_id 精确指定窗口。",
		), NextPrompt: "\n"}, nil
	}
	target := matches[index]
	activated, err := t.driver.Activate(ctx, desktop.ActivateRequest{PID: target.PID, WindowID: target.WindowID})
	if err != nil {
		return desktopToolError(err), nil
	}
	state := t.store.SaveState(computeruse.ComputerState{
		OS:           runtime.GOOS,
		ActiveApp:    target.AppName,
		ActivePID:    target.PID,
		ActiveWindow: computerWindowRef(runtime.GOOS, target),
		Windows:      result.Windows,
	})
	verified := activated.Active && activated.Verified && (target.WindowID == "" || activated.WindowVerified)
	data := map[string]any{
		"status":            agent.ToolStatusSuccess,
		"state_id":          state.ID,
		"risk":              desktopRiskReversible,
		"reason":            reason,
		"matched_count":     len(matches),
		"selected_index":    index,
		"active":            activated.Active,
		"window_verified":   activated.WindowVerified,
		"verified":          verified,
		"pid":               activated.PID,
		"window_id":         target.WindowID,
		"app_name":          target.AppName,
		"title":             target.Title,
		"active_window":     state.ActiveWindow,
		"candidate_windows": matches,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_window_switch_unverified"
		data["message"] = "window activation was requested but the target foreground state was not verified"
		data["hint"] = "请重新调用 computer_see 检查当前前台窗口，避免继续对错误窗口操作。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func filterComputerSwitchWindows(windows []desktop.Window, appName string, title string, windowID string, preferNonActive bool) []desktop.Window {
	appName = strings.ToLower(strings.TrimSpace(appName))
	title = strings.ToLower(strings.TrimSpace(title))
	matches := make([]desktop.Window, 0, len(windows))
	for _, window := range windows {
		if !window.IsVisible {
			continue
		}
		if appName != "" && !strings.Contains(strings.ToLower(window.AppName), appName) {
			continue
		}
		if title != "" && !strings.Contains(strings.ToLower(window.Title), title) {
			continue
		}
		if windowID != "" && window.WindowID != windowID {
			continue
		}
		if preferNonActive && window.IsActive {
			continue
		}
		matches = append(matches, window)
	}
	if len(matches) == 0 && preferNonActive {
		for _, window := range windows {
			if window.IsVisible && (appName == "" || strings.Contains(strings.ToLower(window.AppName), appName)) && (title == "" || strings.Contains(strings.ToLower(window.Title), title)) {
				matches = append(matches, window)
			}
		}
	}
	return matches
}

// ComputerMenu 通过 AX 菜单栏选择目标应用菜单项。
type ComputerMenu struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerMenu 创建菜单选择工具。
func NewComputerMenu(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerMenu {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerMenu{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerMenu) Name() string { return ToolNameComputerMenu }

func (t *ComputerMenu) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Select a menu item in the latest computer target app using a semantic menu path such as File > Open. R2 side-effect menu paths require one-time confirmation and R3 paths are refused.",
		Parameters: objectSchema(map[string]any{
			"menu_path":          stringProp("Menu path separated by >, for example File > Open Recent > Project."),
			"reason":             stringProp("Concrete user-facing reason for selecting this menu item."),
			"confirmation_token": stringProp("Required only for R2 menu actions. Obtain it from ask_user with the returned approval_request."),
		}, "menu_path", "reason"),
	}}
}

func (t *ComputerMenu) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_menu", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_menu_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 或 computer_window_switch 选择目标窗口。"), nil
	}
	menuPath, pathErr := parseComputerMenuPath(asString(call.Args["menu_path"]))
	if pathErr != nil {
		return agent.Outcome{Data: *pathErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	risk := classifyComputerTextRisk(strings.Join(menuPath, " "), reason)
	if risk == desktopRiskHigh {
		return desktopActionError("computer_menu_high_risk_refused", "this menu action is classified as high risk and must be completed manually", "删除、支付、授权、登录验证等菜单动作不能由 computer_menu 自动执行。"), nil
	}
	binding := strings.Join(menuPath, ">")
	if risk == desktopRiskExternal {
		approval := ActionApproval{Operation: computerMenuOperation, PID: state.ActivePID, BBox: binding, Reason: reason}
		if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
			return computerApprovalRequiredOutcome(approval, risk, "computer_menu"), nil
		}
	}
	activated, activateErr := t.driver.Activate(ctx, desktop.ActivateRequest{
		PID: state.ActivePID, WindowID: state.ActiveWindow.WindowID,
	})
	if activateErr != nil {
		return desktopToolError(activateErr), nil
	}
	if state.ActiveWindow.WindowID != "" && !activated.WindowVerified {
		return computerToolError("computer_menu_window_unverified", "target window focus was not verified before menu selection", "请重新调用 computer_see 获取最新窗口状态。"), nil
	}
	if !activated.Active || !activated.Verified {
		return computerToolError("computer_menu_target_not_active", "target application was not verified active before menu selection", "请重新调用 computer_see 获取最新窗口状态。"), nil
	}
	result, err := t.driver.MenuSelect(ctx, desktop.MenuSelectRequest{PID: state.ActivePID, MenuPath: menuPath})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":        agent.ToolStatusSuccess,
		"state_id":      state.ID,
		"pid":           result.PID,
		"action":        result.Action,
		"risk":          risk,
		"reason":        reason,
		"menu_path":     result.MenuPath,
		"performed":     result.Performed,
		"active_before": result.ActiveBefore,
		"active_after":  result.ActiveAfter,
		"verified":      verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_menu_unverified"
		data["message"] = "menu action was sent but target foreground state was not verified"
		data["hint"] = "请停止连续菜单操作，重新调用 computer_see 检查当前前台窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerFileDialog 在 macOS 文件对话框中跳转到指定路径，并可选确认。
type ComputerFileDialog struct {
	driver        desktop.Driver
	store         *computeruse.Store
	confirmations *ConfirmationStore
}

// NewComputerFileDialog 创建文件对话框工具。
func NewComputerFileDialog(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerFileDialog {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerFileDialog{driver: driver, store: store, confirmations: confirmations}
}

func (t *ComputerFileDialog) Name() string { return ToolNameComputerFileDialog }

func (t *ComputerFileDialog) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Operate the currently active macOS file dialog by jumping to an absolute path. With confirm=false it only fills/navigates the path. confirm=true presses Enter again and requires one-time confirmation.",
		Parameters: objectSchema(map[string]any{
			"path":               stringProp("Absolute file or folder path to enter in the active macOS file dialog."),
			"confirm":            boolProp("Whether to confirm the dialog after entering the path. Default false.", false),
			"reason":             stringProp("Concrete user-facing reason for operating this file dialog."),
			"confirmation_token": stringProp("Required when confirm=true. Obtain it from ask_user with the returned approval_request."),
		}, "path", "reason"),
	}}
}

func (t *ComputerFileDialog) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, err := t.store.LatestState()
	if err != nil {
		return computerCacheError("computer_file_dialog", err), nil
	}
	if state.ActivePID <= 0 {
		return computerToolError("computer_file_dialog_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 或 computer_window_switch 选择目标窗口。"), nil
	}
	path := strings.TrimSpace(asString(call.Args["path"]))
	if path == "" || !filepath.IsAbs(path) {
		return computerToolError("computer_file_dialog_bad_path", "path must be a non-empty absolute path", "请传入绝对路径，避免在未知目录下误选文件。"), nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	confirm := asBool(call.Args["confirm"], false)
	risk := desktopRiskReversible
	if confirm {
		risk = classifyComputerTextRisk(path, reason)
		if risk == desktopRiskReversible {
			risk = desktopRiskExternal
		}
		if risk == desktopRiskHigh {
			return desktopActionError("computer_file_dialog_high_risk_refused", "this file dialog confirmation is classified as high risk and must be completed manually", "删除、授权、支付、登录验证等动作不能由 computer_file_dialog 自动确认。"), nil
		}
		approval := ActionApproval{Operation: computerFileDialogOperation, PID: state.ActivePID, BBox: path + "|confirm", Reason: reason}
		if !t.confirmations.Consume(strings.TrimSpace(asString(call.Args["confirmation_token"])), approval) {
			return computerApprovalRequiredOutcome(approval, risk, "computer_file_dialog"), nil
		}
	}
	activated, activateErr := t.driver.Activate(ctx, desktop.ActivateRequest{
		PID: state.ActivePID, WindowID: state.ActiveWindow.WindowID,
	})
	if activateErr != nil {
		return desktopToolError(activateErr), nil
	}
	if state.ActiveWindow.WindowID != "" && !activated.WindowVerified {
		return computerToolError("computer_file_dialog_window_unverified", "target dialog focus was not verified before file dialog operation", "请重新调用 computer_see 获取最新对话框窗口。"), nil
	}
	if !activated.Active || !activated.Verified {
		return computerToolError("computer_file_dialog_target_not_active", "target application was not verified active before file dialog operation", "请重新调用 computer_see 获取最新对话框窗口。"), nil
	}
	result, err := t.driver.FileDialog(ctx, desktop.FileDialogRequest{PID: state.ActivePID, Path: path, Confirm: confirm})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":        agent.ToolStatusSuccess,
		"state_id":      state.ID,
		"pid":           result.PID,
		"action":        result.Action,
		"risk":          risk,
		"reason":        reason,
		"confirm":       result.Confirm,
		"path_length":   result.PathLength,
		"performed":     result.Performed,
		"active_before": result.ActiveBefore,
		"active_after":  result.ActiveAfter,
		"verified":      verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "computer_file_dialog_unverified"
		data["message"] = "file dialog operation was sent but target foreground state was not verified"
		data["hint"] = "请停止继续确认文件对话框，重新调用 computer_see 检查当前窗口。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

// ComputerWindowMove 移动最新或指定窗口。
type ComputerWindowMove struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerWindowMove 创建窗口移动工具。
func NewComputerWindowMove(driver desktop.Driver, store *computeruse.Store) *ComputerWindowMove {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerWindowMove{driver: driver, store: store}
}

func (t *ComputerWindowMove) Name() string { return ToolNameComputerWindowMove }

func (t *ComputerWindowMove) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Move the latest active computer window or a known window_id to a bounded screen-physical position. This is R1 reversible window management.",
		Parameters: objectSchema(map[string]any{
			"window_id": stringProp("Optional window_id from computer_see/window_switch. Defaults to latest active window."),
			"x":         intProp("Target screen-physical X. Must be 0..20000.", 0),
			"y":         intProp("Target screen-physical Y. Must be 0..20000.", 0),
			"reason":    stringProp("Concrete user-facing reason for moving the window."),
		}, "x", "y", "reason"),
	}}
}

func (t *ComputerWindowMove) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, windowID, reason, errOutcome, ok := computerWindowActionInputs(t.store, call.Args, "computer_window_move")
	if !ok {
		return errOutcome, nil
	}
	x := asInt(call.Args["x"], -1)
	y := asInt(call.Args["y"], -1)
	if x < 0 || y < 0 || x > 20000 || y > 20000 {
		return computerToolError("computer_window_move_bad_bounds", "x/y must be between 0 and 20000", "请传入受限屏幕物理坐标，避免把窗口移动到不可恢复位置。"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, state.ActivePID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.WindowMove(ctx, desktop.WindowMoveRequest{PID: state.ActivePID, WindowID: windowID, X: x, Y: y, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical})
	if err != nil {
		return desktopToolError(err), nil
	}
	return computerWindowGeometryOutcome("computer_window_move", state.ID, reason, result.PID, result.WindowID, result.Action, result.Performed, result.ActiveBefore, result.ActiveAfter, result.BeforeBounds, result.AfterBounds, result.CoordinateSpace), nil
}

// ComputerWindowResize 调整最新或指定窗口尺寸。
type ComputerWindowResize struct {
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerWindowResize 创建窗口缩放工具。
func NewComputerWindowResize(driver desktop.Driver, store *computeruse.Store) *ComputerWindowResize {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerWindowResize{driver: driver, store: store}
}

func (t *ComputerWindowResize) Name() string { return ToolNameComputerWindowResize }

func (t *ComputerWindowResize) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Resize the latest active computer window or a known window_id to a bounded screen-physical size. This is R1 reversible window management.",
		Parameters: objectSchema(map[string]any{
			"window_id": stringProp("Optional window_id from computer_see/window_switch. Defaults to latest active window."),
			"width":     intProp("Target window width. Must be 160..10000.", 0),
			"height":    intProp("Target window height. Must be 120..10000.", 0),
			"reason":    stringProp("Concrete user-facing reason for resizing the window."),
		}, "width", "height", "reason"),
	}}
}

func (t *ComputerWindowResize) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	state, windowID, reason, errOutcome, ok := computerWindowActionInputs(t.store, call.Args, "computer_window_resize")
	if !ok {
		return errOutcome, nil
	}
	width := asInt(call.Args["width"], 0)
	height := asInt(call.Args["height"], 0)
	if width < 160 || height < 120 || width > 10000 || height > 10000 {
		return computerToolError("computer_window_resize_bad_size", "width/height are outside the allowed range", "请传入合理窗口尺寸，width 160..10000，height 120..10000。"), nil
	}
	if err := activateDesktopTarget(ctx, t.driver, state.ActivePID); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.WindowResize(ctx, desktop.WindowResizeRequest{PID: state.ActivePID, WindowID: windowID, Width: width, Height: height, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical})
	if err != nil {
		return desktopToolError(err), nil
	}
	return computerWindowGeometryOutcome("computer_window_resize", state.ID, reason, result.PID, result.WindowID, result.Action, result.Performed, result.ActiveBefore, result.ActiveAfter, result.BeforeBounds, result.AfterBounds, result.CoordinateSpace), nil
}

func parseComputerMenuPath(raw string) ([]string, *agent.ToolErrorData) {
	parts := strings.Split(raw, ">")
	path := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			path = append(path, value)
		}
	}
	if len(path) == 0 {
		err := agent.NewToolError("computer_menu_bad_path", "menu_path must be a non-empty path separated by >", "请提供类似 File > Open 的菜单路径。")
		return nil, &err
	}
	if len(path) > 6 {
		err := agent.NewToolError("computer_menu_path_too_deep", "menu_path has too many segments", "请使用明确且较短的菜单路径。")
		return nil, &err
	}
	return path, nil
}

func classifyComputerTextRisk(text string, reason string) desktopActionRisk {
	return classifyDesktopVisualClickRisk(text, reason)
}

func computerWindowActionInputs(store *computeruse.Store, args map[string]any, operation string) (computeruse.ComputerState, string, string, agent.Outcome, bool) {
	state, err := store.LatestState()
	if err != nil {
		return computeruse.ComputerState{}, "", "", computerCacheError(operation, err), false
	}
	if state.ActivePID <= 0 {
		return computeruse.ComputerState{}, "", "", computerToolError(operation+"_no_target_window", "latest computer state has no active target window", "请先调用 computer_see 或 computer_window_switch 选择目标窗口。"), false
	}
	reason, reasonErr := requiredDesktopActionString(args, "reason")
	if reasonErr != nil {
		return computeruse.ComputerState{}, "", "", agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, false
	}
	windowID := strings.TrimSpace(asString(args["window_id"]))
	if windowID == "" {
		windowID = state.ActiveWindow.WindowID
	}
	return state, windowID, reason, agent.Outcome{}, true
}

func computerWindowGeometryOutcome(operation string, stateID string, reason string, pid int, windowID string, action string, performed bool, activeBefore bool, activeAfter bool, before desktop.Bounds, after desktop.Bounds, coordinateSpace string) agent.Outcome {
	verified := performed && activeBefore && activeAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
		"state_id":         stateID,
		"pid":              pid,
		"window_id":        windowID,
		"action":           action,
		"risk":             desktopRiskReversible,
		"reason":           reason,
		"before_bounds":    before,
		"after_bounds":     after,
		"coordinate_space": coordinateSpace,
		"performed":        performed,
		"active_before":    activeBefore,
		"active_after":     activeAfter,
		"verified":         verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = operation + "_unverified"
		data["message"] = operation + " was sent but target foreground state was not verified"
		data["hint"] = "请重新调用 computer_see 检查当前窗口位置和前台状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}
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
