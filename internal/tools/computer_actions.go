package tools

import (
	"context"
	"strings"

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
}

// NewComputerClick 创建受 target cache 约束的点击工具。
func NewComputerClick(driver desktop.Driver, store *computeruse.Store, confirmations *ConfirmationStore) *ComputerClick {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	if confirmations == nil {
		confirmations = NewConfirmationStore()
	}
	return &ComputerClick{driver: driver, store: store, confirmations: confirmations}
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
		return computerToolError(
			"computer_click_unsupported_target",
			"computer_click currently supports AX-backed targets only",
			"请重新调用 computer_see/computer_find 选择 source=ax 的目标；OCR/vision 目标将在后续版本接入受控视觉点击。",
		), nil
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
	driver desktop.Driver
	store  *computeruse.Store
}

// NewComputerType 创建文本起草工具。
func NewComputerType(driver desktop.Driver, store *computeruse.Store) *ComputerType {
	if store == nil {
		store = computeruse.NewStore(computeruse.DefaultTargetTTL)
	}
	return &ComputerType{driver: driver, store: store}
}

func (t *ComputerType) Name() string { return ToolNameComputerType }

func (t *ComputerType) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Draft text into a cached editable computer target. This never sends or submits content and never echoes full text in the result. Use target_id from computer_find; pass current_focus only when the editable field is already verified focused.",
		Parameters: objectSchema(map[string]any{
			"target_id": stringProp("Cached editable target_id from computer_find, or current_focus to type into the already focused editable field."),
			"text":      stringProp("Text to draft. The result returns only length and line count."),
			"reason":    stringProp("Concrete user-facing reason for drafting this text."),
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
	if targetID != "current_focus" {
		target, err := t.store.Target(targetID)
		if err != nil {
			return computerCacheError("computer_type", err), nil
		}
		if target.Source != computeruse.SourceAX || target.AXNodeID == "" {
			return computerToolError("computer_type_unsupported_target", "computer_type currently supports AX-backed editable targets only", "请重新选择 source=ax 的输入框目标。"), nil
		}
		pid = target.Window.PID
		if pid <= 0 {
			return computerToolError("computer_type_bad_target", "target is not bound to a valid PID", "请重新调用 computer_see 刷新目标窗口。"), nil
		}
		if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
			return desktopToolError(err), nil
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
		focus, err := t.driver.AXFocus(ctx, desktop.AXFocusRequest{
			PID:                 pid,
			NodeID:              target.AXNodeID,
			ExpectedRole:        target.ExpectedRole,
			ExpectedTitle:       target.ExpectedTitle,
			ExpectedDescription: target.ExpectedDescription,
		})
		if err != nil {
			return desktopToolError(err), nil
		}
		if !focus.Performed || !focus.Focused {
			return desktopActionError(
				"computer_type_focus_unverified",
				"computer_type could not verify editable focus before typing",
				"请重新调用 computer_click 聚焦输入框，或重新 computer_see/computer_find 后再试。",
			), nil
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
