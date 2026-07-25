package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"cohert/internal/agent"
	"cohert/internal/desktop"
	"cohert/internal/llm"
)

type desktopActionRisk string

const (
	desktopRiskReversible desktopActionRisk = "R1_reversible"
	desktopRiskExternal   desktopActionRisk = "R2_external_side_effect"
	desktopRiskHigh       desktopActionRisk = "R3_high_risk"
)

const (
	desktopActionSnapshotDepth = 12
	desktopActionSnapshotNodes = 500
	maxDesktopTypeTextRunes    = 2000
)

// DesktopAXPress 对一个刚刚发现且语义仍匹配的 AX 节点执行 AXPress。
// 它不提供坐标点击降级路径，避免 M2.1 引入未经验证的物理输入。
type DesktopAXPress struct {
	driver        desktop.Driver
	confirmations *ConfirmationStore
}

func NewDesktopAXPress(driver desktop.Driver, confirmations *ConfirmationStore) *DesktopAXPress {
	return &DesktopAXPress{driver: driver, confirmations: confirmations}
}

func (t *DesktopAXPress) Name() string { return ToolNameDesktopAXPress }

func (t *DesktopAXPress) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Press one current macOS Accessibility node using AXPress. The target PID must already be frontmost. Provide the node ID and exact role/title/description from an immediately preceding desktop_ax_snapshot. R2 actions require a one-time confirmation_token issued by ask_user; R3 actions are refused for manual completion. The tool automatically refreshes the AX tree and returns an error when it cannot verify a state change.",
		Parameters: objectSchema(map[string]any{
			"pid":                  intProp("Target application PID from desktop_windows.", 0),
			"node_id":              stringProp("Exact node ID from the immediately preceding desktop_ax_snapshot."),
			"expected_role":        stringProp("Exact role from that node, for example AXButton."),
			"expected_title":       stringProp("Exact title from that node. Pass an empty string when the snapshot title is empty."),
			"expected_description": stringProp("Exact description from that node. Pass an empty string when the snapshot description is empty."),
			"reason":               stringProp("Concrete user-facing reason for this action."),
			"confirmation_token":   stringProp("Required only for R2 actions. Obtain it from ask_user with an approval binding for this exact pid, node_id, and reason."),
		}, "pid", "node_id", "expected_role", "expected_title", "expected_description", "reason"),
	}}
}

func (t *DesktopAXPress) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	nodeID, nodeIDErr := requiredDesktopActionString(call.Args, "node_id")
	if nodeIDErr != nil {
		return agent.Outcome{Data: *nodeIDErr, NextPrompt: "\n"}, nil
	}
	role, roleErr := requiredDesktopActionString(call.Args, "expected_role")
	if roleErr != nil {
		return agent.Outcome{Data: *roleErr, NextPrompt: "\n"}, nil
	}
	title, titleErr := requiredDesktopActionField(call.Args, "expected_title")
	if titleErr != nil {
		return agent.Outcome{Data: *titleErr, NextPrompt: "\n"}, nil
	}
	description, descriptionErr := requiredDesktopActionField(call.Args, "expected_description")
	if descriptionErr != nil {
		return agent.Outcome{Data: *descriptionErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}

	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	snapshotRequest := desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        desktopActionSnapshotDepth,
		MaxNodes:        desktopActionSnapshotNodes,
		IncludeZeroSize: false,
	}
	before, err := t.driver.AXSnapshot(ctx, snapshotRequest)
	if err != nil {
		return desktopToolError(err), nil
	}
	node, found := findAXNode(before.Root, nodeID)
	if !found {
		return desktopAXPressError(
			"desktop_ax_node_stale",
			"the requested AX node is absent from the current snapshot",
			"界面可能已变化；请重新调用 desktop_ax_snapshot，选择当前可见节点后再操作。",
		), nil
	}
	if node.Role != role || node.Title != title || node.Description != description {
		return desktopAXPressError(
			"desktop_ax_node_stale",
			"the requested AX node no longer matches its expected role, title, or description",
			"界面可能已变化；请重新读取 AX 快照，不要沿用旧 node_id。",
		), nil
	}
	if node.Enabled != nil && !*node.Enabled {
		return desktopAXPressError(
			"desktop_ax_node_disabled",
			"the requested AX node is disabled",
			"请重新读取 AX 快照，选择 enabled=true 的节点。",
		), nil
	}
	if !containsAXAction(node.Actions, "AXPress") {
		return desktopAXPressError(
			"desktop_ax_press_unsupported",
			"the requested AX node does not support AXPress",
			"请选择支持 AXPress 的节点；如必须点击该节点，请改用受控 desktop_click，并继续使用当前 AX 节点 metadata。",
		), nil
	}

	risk := classifyDesktopAXRisk(node)
	if risk == desktopRiskHigh {
		return desktopAXPressError(
			"desktop_action_high_risk_refused",
			"this AX action is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 desktop_ax_press 自动执行。",
		), nil
	}
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: desktopAXPressOperation,
			PID:       pid,
			NodeID:    nodeID,
			Reason:    reason,
		}
		token := strings.TrimSpace(asString(call.Args["confirmation_token"]))
		if !t.confirmations.Consume(token, approval) {
			return desktopApprovalRequiredOutcome(approval, node, risk), nil
		}
	}

	result, err := t.driver.AXPress(ctx, desktop.AXPressRequest{
		PID:                 pid,
		NodeID:              nodeID,
		ExpectedRole:        role,
		ExpectedTitle:       title,
		ExpectedDescription: description,
	})
	if err != nil {
		return desktopToolError(err), nil
	}

	after, err := t.driver.AXSnapshot(ctx, snapshotRequest)
	if err != nil {
		return desktopAXPressError(
			"desktop_ax_press_verification_failed",
			"AXPress was sent but the post-action AX snapshot could not be read: "+err.Error(),
			"动作已经发生，但无法验证结果；请停止后续自动操作并重新检查目标窗口。",
		), nil
	}
	afterNode, targetStillPresent := findAXNode(after.Root, nodeID)
	treeChanged := !reflect.DeepEqual(before.Root, after.Root)
	targetChanged := !targetStillPresent || !reflect.DeepEqual(node, afterNode)
	if !treeChanged && !targetChanged {
		return agent.Outcome{
			Data: map[string]any{
				"status":           agent.ToolStatusError,
				"code":             "desktop_ax_press_unverified",
				"message":          "AXPress was sent but the bounded AX snapshot did not show a state change",
				"hint":             "请停止重复点击，重新读取目标窗口和 AX 快照，确认是否有遮挡、异步延迟或无效目标。",
				"pid":              pid,
				"node_id":          nodeID,
				"action_performed": result.Performed,
				"risk":             risk,
				"verified":         false,
			},
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":               agent.ToolStatusSuccess,
			"pid":                  result.PID,
			"node_id":              result.NodeID,
			"action":               result.Action,
			"action_performed":     result.Performed,
			"risk":                 risk,
			"verified":             true,
			"tree_changed":         treeChanged,
			"target_still_present": targetStillPresent,
			"target_changed":       targetChanged,
		},
		NextPrompt: "\n",
	}, nil
}

func requiredDesktopActionString(args map[string]any, key string) (string, *agent.ToolErrorData) {
	value, err := requiredDesktopActionField(args, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		toolErr := agent.NewToolError(
			"desktop_ax_press_bad_request",
			key+" must be a non-empty string",
			"请使用刚刚 desktop_ax_snapshot 返回的节点语义信息。",
		)
		return "", &toolErr
	}
	return strings.TrimSpace(value), nil
}

// requiredDesktopActionField preserves empty title/description as valid values,
// while still rejecting omitted fields that would weaken stale-node validation.
func requiredDesktopActionField(args map[string]any, key string) (string, *agent.ToolErrorData) {
	raw, present := args[key]
	if !present {
		toolErr := agent.NewToolError(
			"desktop_ax_press_bad_request",
			"missing required field: "+key,
			"请从刚刚 desktop_ax_snapshot 选择节点，并原样传入 expected_role、expected_title、expected_description。",
		)
		return "", &toolErr
	}
	value, ok := raw.(string)
	if !ok {
		toolErr := agent.NewToolError(
			"desktop_ax_press_bad_request",
			key+" must be a string",
			"请从刚刚 desktop_ax_snapshot 复制节点字段；空标题或描述请传空字符串。",
		)
		return "", &toolErr
	}
	return value, nil
}

func findAXNode(root desktop.AXNode, id string) (desktop.AXNode, bool) {
	if root.ID == id {
		return root, true
	}
	for _, child := range root.Children {
		if node, found := findAXNode(child, id); found {
			return node, true
		}
	}
	return desktop.AXNode{}, false
}

func containsAXAction(actions []string, action string) bool {
	return slices.Contains(actions, action)
}

func classifyDesktopAXRisk(node desktop.AXNode) desktopActionRisk {
	text := strings.ToLower(strings.Join([]string{node.Role, node.Title, node.Description, node.Value}, " "))
	if containsAny(text, []string{
		"支付", "付款", "转账", "审批", "授权", "允许", "登录", "验证码", "人机验证", "删除", "移除", "清空", "退出登录",
		"pay", "payment", "transfer", "approve", "authorize", "allow", "sign in", "log in", "captcha", "verification", "delete", "remove", "erase", "revoke", "logout",
	}) {
		return desktopRiskHigh
	}
	if containsAny(text, []string{
		"发送", "提交", "上传", "保存", "发布", "分享", "转发", "邀请", "创建", "关闭",
		"send", "submit", "upload", "save", "publish", "share", "forward", "invite", "create", "close", "commit",
	}) {
		return desktopRiskExternal
	}
	if containsAny(text, []string{
		"显示", "打开", "展开", "收起", "菜单", "标签", "下一", "上一", "切换",
		"show", "open", "expand", "collapse", "menu", "tab", "next", "previous", "toggle",
	}) {
		return desktopRiskReversible
	}
	// 无法可靠判断语义的 AXPress 默认需要用户确认。
	return desktopRiskExternal
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func desktopApprovalRequiredOutcome(approval ActionApproval, node desktop.AXNode, risk desktopActionRisk) agent.Outcome {
	return agent.Outcome{
		Data: map[string]any{
			"status":  agent.ToolStatusError,
			"code":    "desktop_action_confirmation_required",
			"message": "this AX action requires one-time user confirmation",
			"hint":    "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，将返回 confirmation_token。然后用同一 pid、node_id、reason 和该 token 重试一次 desktop_ax_press。",
			"risk":    risk,
			"node": map[string]any{
				"id":          node.ID,
				"role":        node.Role,
				"title":       node.Title,
				"description": node.Description,
			},
			"approval_request": map[string]any{
				"operation": approval.Operation,
				"pid":       approval.PID,
				"node_id":   approval.NodeID,
				"reason":    approval.Reason,
			},
		},
		NextPrompt: "\n",
	}
}

func desktopAXPressError(code string, message string, hint string) agent.Outcome {
	return agent.Outcome{
		Data:       agent.NewToolError(code, message, hint),
		NextPrompt: "\n",
	}
}

type DesktopAXFocus struct {
	driver desktop.Driver
}

func NewDesktopAXFocus(driver desktop.Driver) *DesktopAXFocus {
	return &DesktopAXFocus{driver: driver}
}

func (t *DesktopAXFocus) Name() string { return ToolNameDesktopAXFocus }

func (t *DesktopAXFocus) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Focus one current editable macOS Accessibility node. The target PID must already be frontmost. Provide the node ID and exact role/title/description from an immediately preceding desktop_ax_snapshot. This is used before desktop_type_text and does not type or send content.",
		Parameters: objectSchema(map[string]any{
			"pid":                  intProp("Target application PID from desktop_windows.", 0),
			"node_id":              stringProp("Exact editable node ID from the immediately preceding desktop_ax_snapshot."),
			"expected_role":        stringProp("Exact role from that node, for example AXTextArea or AXTextField."),
			"expected_title":       stringProp("Exact title from that node. Pass an empty string when the snapshot title is empty."),
			"expected_description": stringProp("Exact description from that node. Pass an empty string when the snapshot description is empty."),
			"reason":               stringProp("Concrete user-facing reason for focusing this input field."),
		}, "pid", "node_id", "expected_role", "expected_title", "expected_description", "reason"),
	}}
}

func (t *DesktopAXFocus) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	nodeID, nodeIDErr := requiredDesktopActionString(call.Args, "node_id")
	if nodeIDErr != nil {
		return agent.Outcome{Data: *nodeIDErr, NextPrompt: "\n"}, nil
	}
	role, roleErr := requiredDesktopActionString(call.Args, "expected_role")
	if roleErr != nil {
		return agent.Outcome{Data: *roleErr, NextPrompt: "\n"}, nil
	}
	title, titleErr := requiredDesktopActionField(call.Args, "expected_title")
	if titleErr != nil {
		return agent.Outcome{Data: *titleErr, NextPrompt: "\n"}, nil
	}
	description, descriptionErr := requiredDesktopActionField(call.Args, "expected_description")
	if descriptionErr != nil {
		return agent.Outcome{Data: *descriptionErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}

	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	snapshotRequest := desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        desktopActionSnapshotDepth,
		MaxNodes:        desktopActionSnapshotNodes,
		IncludeZeroSize: false,
	}
	before, err := t.driver.AXSnapshot(ctx, snapshotRequest)
	if err != nil {
		return desktopToolError(err), nil
	}
	node, found := findAXNode(before.Root, nodeID)
	if !found {
		return desktopActionError(
			"desktop_ax_node_stale",
			"the requested AX node is absent from the current snapshot",
			"界面可能已变化；请重新调用 desktop_ax_snapshot，选择当前可见输入节点后再操作。",
		), nil
	}
	if node.Role != role || node.Title != title || node.Description != description {
		return desktopActionError(
			"desktop_ax_node_stale",
			"the requested AX node no longer matches its expected role, title, or description",
			"界面可能已变化；请重新读取 AX 快照，不要沿用旧 node_id。",
		), nil
	}
	if node.Enabled != nil && !*node.Enabled {
		return desktopActionError(
			"desktop_ax_node_disabled",
			"the requested AX node is disabled",
			"请重新读取 AX 快照，选择 enabled=true 的输入节点。",
		), nil
	}
	if !isEditableAXNode(node) {
		return desktopActionError(
			"desktop_ax_focus_not_editable",
			"desktop_ax_focus only supports editable AX nodes",
			"请选择 AXTextField、AXTextArea、AXSearchField 或其他可编辑文本节点。",
		), nil
	}

	result, err := t.driver.AXFocus(ctx, desktop.AXFocusRequest{
		PID:                 pid,
		NodeID:              nodeID,
		ExpectedRole:        role,
		ExpectedTitle:       title,
		ExpectedDescription: description,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter && result.Focused
	data := map[string]any{
		"status":            agent.ToolStatusSuccess,
		"pid":               result.PID,
		"node_id":           result.NodeID,
		"action":            result.Action,
		"reason":            reason,
		"performed":         result.Performed,
		"active_before":     result.ActiveBefore,
		"active_after":      result.ActiveAfter,
		"focused":           result.Focused,
		"focus_role":        result.FocusRole,
		"focus_title":       result.FocusTitle,
		"focus_description": result.FocusDescription,
		"verified":          verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "desktop_ax_focus_unverified"
		data["message"] = "desktop focus action was sent but focused editable state was not verified"
		data["hint"] = "请停止输入文本，重新调用 desktop_windows、desktop_activate 和 desktop_ax_snapshot 确认当前焦点。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

type DesktopClick struct {
	driver        desktop.Driver
	confirmations *ConfirmationStore
}

func NewDesktopClick(driver desktop.Driver, confirmations *ConfirmationStore) *DesktopClick {
	return &DesktopClick{driver: driver, confirmations: confirmations}
}

func (t *DesktopClick) Name() string { return ToolNameDesktopClick }

func (t *DesktopClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Click the center of one current macOS Accessibility node. This is a controlled fallback when AXPress is unavailable. The target PID must already be frontmost. Provide the node ID and exact role/title/description from an immediately preceding desktop_ax_snapshot. R2 clicks require a one-time confirmation_token from ask_user; R3 clicks are refused.",
		Parameters: objectSchema(map[string]any{
			"pid":                  intProp("Target application PID from desktop_windows.", 0),
			"node_id":              stringProp("Exact node ID from the immediately preceding desktop_ax_snapshot."),
			"expected_role":        stringProp("Exact role from that node."),
			"expected_title":       stringProp("Exact title from that node. Pass an empty string when the snapshot title is empty."),
			"expected_description": stringProp("Exact description from that node. Pass an empty string when the snapshot description is empty."),
			"reason":               stringProp("Concrete user-facing reason for this click."),
			"confirmation_token":   stringProp("Required only for R2 clicks. Obtain it from ask_user with an approval binding for this exact pid, node_id, and reason."),
		}, "pid", "node_id", "expected_role", "expected_title", "expected_description", "reason"),
	}}
}

func (t *DesktopClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	nodeID, nodeIDErr := requiredDesktopActionString(call.Args, "node_id")
	if nodeIDErr != nil {
		return agent.Outcome{Data: *nodeIDErr, NextPrompt: "\n"}, nil
	}
	role, roleErr := requiredDesktopActionString(call.Args, "expected_role")
	if roleErr != nil {
		return agent.Outcome{Data: *roleErr, NextPrompt: "\n"}, nil
	}
	title, titleErr := requiredDesktopActionField(call.Args, "expected_title")
	if titleErr != nil {
		return agent.Outcome{Data: *titleErr, NextPrompt: "\n"}, nil
	}
	description, descriptionErr := requiredDesktopActionField(call.Args, "expected_description")
	if descriptionErr != nil {
		return agent.Outcome{Data: *descriptionErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}

	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	before, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        desktopActionSnapshotDepth,
		MaxNodes:        desktopActionSnapshotNodes,
		IncludeZeroSize: false,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	node, found := findAXNode(before.Root, nodeID)
	if !found {
		return desktopActionError(
			"desktop_ax_node_stale",
			"the requested AX node is absent from the current snapshot",
			"界面可能已变化；请重新调用 desktop_ax_snapshot，选择当前可见节点后再点击。",
		), nil
	}
	if node.Role != role || node.Title != title || node.Description != description {
		return desktopActionError(
			"desktop_ax_node_stale",
			"the requested AX node no longer matches its expected role, title, or description",
			"界面可能已变化；请重新读取 AX 快照，不要沿用旧 node_id。",
		), nil
	}
	if node.Enabled != nil && !*node.Enabled {
		return desktopActionError(
			"desktop_ax_node_disabled",
			"the requested AX node is disabled",
			"请重新读取 AX 快照，选择 enabled=true 的节点。",
		), nil
	}
	if node.Bounds.Width <= 0 || node.Bounds.Height <= 0 {
		return desktopActionError(
			"desktop_click_bad_bounds",
			"the requested AX node has no clickable bounds",
			"请选择带有有效 bounds 的可见节点；不要把 OCR bbox 传给 desktop_click。",
		), nil
	}

	risk := classifyDesktopClickRisk(node)
	if risk == desktopRiskHigh {
		return desktopActionError(
			"desktop_action_high_risk_refused",
			"this click is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 desktop_click 自动执行。",
		), nil
	}
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: desktopClickOperation,
			PID:       pid,
			NodeID:    nodeID,
			Reason:    reason,
		}
		token := strings.TrimSpace(asString(call.Args["confirmation_token"]))
		if !t.confirmations.Consume(token, approval) {
			return desktopClickApprovalRequiredOutcome(approval, node, risk), nil
		}
	}

	result, err := t.driver.Click(ctx, desktop.ClickRequest{
		PID:                 pid,
		NodeID:              nodeID,
		ExpectedRole:        role,
		ExpectedTitle:       title,
		ExpectedDescription: description,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":           agent.ToolStatusSuccess,
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
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "desktop_click_unverified"
		data["message"] = "desktop click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续点击，重新调用 desktop_windows 和 desktop_activate 确认目标应用状态。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func isEditableAXNode(node desktop.AXNode) bool {
	role := strings.ToLower(node.Role)
	switch node.Role {
	case "AXTextField", "AXTextArea", "AXSearchField", "AXComboBox":
		return true
	}
	return strings.Contains(role, "text") && !strings.Contains(role, "static")
}

func classifyDesktopClickRisk(node desktop.AXNode) desktopActionRisk {
	risk := classifyDesktopAXRisk(node)
	if risk != desktopRiskExternal {
		return risk
	}
	if isEditableAXNode(node) {
		return desktopRiskReversible
	}
	return risk
}

func desktopClickApprovalRequiredOutcome(approval ActionApproval, node desktop.AXNode, risk desktopActionRisk) agent.Outcome {
	return agent.Outcome{
		Data: map[string]any{
			"status":  agent.ToolStatusError,
			"code":    "desktop_action_confirmation_required",
			"message": "this desktop click requires one-time user confirmation",
			"hint":    "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，将返回 confirmation_token。然后用同一 pid、node_id、reason 和该 token 重试一次 desktop_click。",
			"risk":    risk,
			"node": map[string]any{
				"id":          node.ID,
				"role":        node.Role,
				"title":       node.Title,
				"description": node.Description,
				"bounds":      node.Bounds,
			},
			"approval_request": map[string]any{
				"operation": approval.Operation,
				"pid":       approval.PID,
				"node_id":   approval.NodeID,
				"reason":    approval.Reason,
			},
		},
		NextPrompt: "\n",
	}
}

type DesktopVisualClick struct {
	driver        desktop.Driver
	confirmations *ConfirmationStore
	workspaceTool
}

func NewDesktopVisualClick(driver desktop.Driver, confirmations *ConfirmationStore, workspace string) *DesktopVisualClick {
	return &DesktopVisualClick{
		driver:        driver,
		confirmations: confirmations,
		workspaceTool: newWorkspaceTool(workspace),
	}
}

func (t *DesktopVisualClick) Name() string { return ToolNameDesktopVisualClick }

func (t *DesktopVisualClick) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Click a target described by OCR/UI-detection bbox from a desktop_screenshot image. This tool reads the screenshot manifest, converts screenshot-local bbox to a screen-physical point, automatically activates the PID, then performs one controlled mouse click. Never pass arbitrary screen coordinates. R2 visual clicks require a one-time confirmation_token; R3 clicks are refused.",
		Parameters: objectSchema(map[string]any{
			"pid":                intProp("Target application PID from desktop_windows and matching the screenshot manifest.", 0),
			"image_path":         stringProp("Screenshot path returned by desktop_screenshot and then used by desktop_ocr."),
			"manifest_path":      stringProp("Optional manifest path returned by desktop_screenshot. If empty, the sidecar JSON next to image_path is used."),
			"bbox":               map[string]any{"type": "array", "description": "OCR/UI bbox in screenshot-local coordinates: [x1, y1, x2, y2].", "items": map[string]any{"type": "integer"}, "minItems": 4, "maxItems": 4},
			"expected_text":      stringProp("Text or semantic label observed at this bbox, used for risk classification and audit."),
			"reason":             stringProp("Concrete user-facing reason for this visual click."),
			"confirmation_token": stringProp("Required only for R2 visual clicks. Obtain it from ask_user with the returned approval_request."),
		}, "pid", "image_path", "bbox", "expected_text", "reason"),
	}}
}

func (t *DesktopVisualClick) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	imagePath, pathErr := t.resolveDesktopVisualFile(strings.TrimSpace(asString(call.Args["image_path"])), "desktop_visual_click_image_required", "desktop_visual_click_path_outside_workspace", "desktop_visual_click_image_not_found")
	if pathErr != nil {
		return agent.Outcome{Data: *pathErr, NextPrompt: "\n"}, nil
	}
	manifestPathRaw := strings.TrimSpace(asString(call.Args["manifest_path"]))
	if manifestPathRaw == "" {
		manifestPathRaw = strings.TrimSuffix(imagePath, filepath.Ext(imagePath)) + ".json"
	}
	manifestPath, manifestPathErr := t.resolveDesktopVisualFile(manifestPathRaw, "desktop_visual_click_manifest_required", "desktop_visual_click_path_outside_workspace", "desktop_visual_click_manifest_not_found")
	if manifestPathErr != nil {
		return agent.Outcome{Data: *manifestPathErr, NextPrompt: "\n"}, nil
	}
	bbox, bboxErr := requiredDesktopVisualBBox(call.Args["bbox"])
	if bboxErr != nil {
		return agent.Outcome{Data: *bboxErr, NextPrompt: "\n"}, nil
	}
	expectedText, expectedTextErr := requiredDesktopActionString(call.Args, "expected_text")
	if expectedTextErr != nil {
		return agent.Outcome{Data: *expectedTextErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	manifest, manifestErr := readDesktopVisualManifest(manifestPath)
	if manifestErr != nil {
		return agent.Outcome{Data: *manifestErr, NextPrompt: "\n"}, nil
	}
	if validationErr := validateDesktopVisualManifest(manifest, pid, imagePath, bbox); validationErr != nil {
		return agent.Outcome{Data: *validationErr, NextPrompt: "\n"}, nil
	}
	screenX, screenY := mapScreenshotBBoxCenterToScreen(manifest, bbox)
	risk := classifyDesktopVisualClickRisk(expectedText, reason)
	if risk == desktopRiskHigh {
		return desktopActionError(
			"desktop_action_high_risk_refused",
			"this visual click is classified as high risk and must be completed manually",
			"支付、审批、授权、登录验证、删除等操作不能由 desktop_visual_click 自动执行。",
		), nil
	}
	bboxKey := canonicalDesktopBBox(bbox)
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: desktopVisualClickOperation,
			PID:       pid,
			ImagePath: imagePath,
			BBox:      bboxKey,
			Reason:    reason,
		}
		token := strings.TrimSpace(asString(call.Args["confirmation_token"]))
		if !t.confirmations.Consume(token, approval) {
			return desktopVisualClickApprovalRequiredOutcome(approval, expectedText, risk), nil
		}
	}
	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.VisualClick(ctx, desktop.VisualClickRequest{
		PID:             pid,
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
		"pid":                     result.PID,
		"action":                  result.Action,
		"risk":                    risk,
		"image_path":              imagePath,
		"manifest_path":           manifestPath,
		"bbox":                    bbox,
		"bbox_key":                bboxKey,
		"expected_text":           expectedText,
		"window_bounds":           manifest.WindowBounds,
		"x":                       result.X,
		"y":                       result.Y,
		"coordinate_space":        result.CoordinateSpace,
		"source_coordinate_space": desktop.CoordinateSpaceScreenshotLocal,
		"performed":               result.Performed,
		"active_before":           result.ActiveBefore,
		"active_after":            result.ActiveAfter,
		"verified":                verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "desktop_visual_click_unverified"
		data["message"] = "desktop visual click was sent but target foreground state was not verified"
		data["hint"] = "请停止连续点击，重新截图和 OCR，确认目标应用仍在前台且视觉目标未变化。"
	}
	return agent.Outcome{Data: data, NextPrompt: "\n"}, nil
}

func (t *DesktopVisualClick) resolveDesktopVisualFile(rawPath string, requiredCode string, outsideCode string, notFoundCode string) (string, *agent.ToolErrorData) {
	if rawPath == "" {
		err := agent.NewToolError(
			requiredCode,
			"desktop_visual_click requires paths returned by desktop_screenshot",
			"请先调用 desktop_screenshot，并使用其 image_path/manifest_path。",
		)
		return "", &err
	}
	path := t.resolve(rawPath)
	rel, err := filepath.Rel(t.workspace, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		toolErr := agent.NewToolError(
			outsideCode,
			"desktop visual click paths must stay inside the configured workspace",
			"请提供 desktop_screenshot 返回的 workspace 内路径。",
		)
		return "", &toolErr
	}
	if _, err := os.Stat(path); err != nil {
		toolErr := agent.NewToolError(
			notFoundCode,
			fmt.Sprintf("desktop visual click file is unavailable: %v", err),
			"请确认截图和 manifest 仍存在，或重新调用 desktop_screenshot。",
		)
		return "", &toolErr
	}
	realWorkspace, workspaceErr := filepath.EvalSymlinks(t.workspace)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if workspaceErr == nil && pathErr == nil {
		realRel, err := filepath.Rel(realWorkspace, realPath)
		if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			toolErr := agent.NewToolError(
				outsideCode,
				"desktop visual click path resolves outside the configured workspace",
				"路径不能通过符号链接逃出 workspace；请重新截图后再操作。",
			)
			return "", &toolErr
		}
	}
	return path, nil
}

func requiredDesktopVisualBBox(raw any) ([4]int, *agent.ToolErrorData) {
	var bbox [4]int
	values, ok := raw.([]any)
	if !ok || len(values) != 4 {
		err := agent.NewToolError(
			"desktop_visual_click_bad_bbox",
			"bbox must be an array of four numbers: [x1, y1, x2, y2]",
			"请使用 desktop_ocr 返回的 bbox 原样传入，不要传入中心点或屏幕坐标。",
		)
		return bbox, &err
	}
	for index, value := range values {
		bbox[index] = asInt(value, -1)
	}
	if bbox[0] < 0 || bbox[1] < 0 || bbox[2] <= bbox[0] || bbox[3] <= bbox[1] {
		err := agent.NewToolError(
			"desktop_visual_click_bad_bbox",
			"bbox must be a positive screenshot-local rectangle [x1, y1, x2, y2]",
			"请使用 desktop_ocr 返回的 bbox 原样传入，不要传入中心点或屏幕坐标。",
		)
		return bbox, &err
	}
	return bbox, nil
}

func readDesktopVisualManifest(path string) (desktopScreenshotManifest, *agent.ToolErrorData) {
	data, err := os.ReadFile(path)
	if err != nil {
		toolErr := agent.NewToolError(
			"desktop_visual_click_manifest_unreadable",
			fmt.Sprintf("cannot read desktop screenshot manifest: %v", err),
			"请重新调用 desktop_screenshot，确保 manifest 存在且可读。",
		)
		return desktopScreenshotManifest{}, &toolErr
	}
	var manifest desktopScreenshotManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		toolErr := agent.NewToolError(
			"desktop_visual_click_manifest_invalid",
			fmt.Sprintf("desktop screenshot manifest is invalid JSON: %v", err),
			"请重新调用 desktop_screenshot；不要手写或修改 manifest。",
		)
		return desktopScreenshotManifest{}, &toolErr
	}
	return manifest, nil
}

func validateDesktopVisualManifest(manifest desktopScreenshotManifest, pid int, imagePath string, bbox [4]int) *agent.ToolErrorData {
	if manifest.Version != 1 || manifest.CoordinateSpace != desktop.CoordinateSpaceScreenshotLocal || manifest.ScreenCoordinateSpace != desktop.CoordinateSpaceScreenPhysical {
		err := agent.NewToolError(
			"desktop_visual_click_manifest_invalid",
			"desktop screenshot manifest has an unsupported version or coordinate space",
			"请重新调用 desktop_screenshot，使用工具生成的最新 manifest。",
		)
		return &err
	}
	if manifest.PID != pid {
		err := agent.NewToolError(
			"desktop_visual_click_pid_mismatch",
			"desktop screenshot manifest PID does not match the requested PID",
			"请重新枚举窗口并截图，不要把其他应用或旧 PID 的截图用于点击。",
		)
		return &err
	}
	if filepath.Clean(manifest.ImagePath) != filepath.Clean(imagePath) {
		err := agent.NewToolError(
			"desktop_visual_click_image_mismatch",
			"desktop screenshot manifest does not belong to the requested image_path",
			"请使用 desktop_screenshot 同一次返回的 image_path 和 manifest_path。",
		)
		return &err
	}
	if manifest.Width <= 0 || manifest.Height <= 0 || manifest.WindowBounds.Width <= 0 || manifest.WindowBounds.Height <= 0 {
		err := agent.NewToolError(
			"desktop_visual_click_manifest_invalid",
			"desktop screenshot manifest has invalid dimensions",
			"请重新调用 desktop_screenshot，确认目标窗口可见且尺寸有效。",
		)
		return &err
	}
	if bbox[2] > manifest.Width || bbox[3] > manifest.Height {
		err := agent.NewToolError(
			"desktop_visual_click_bbox_outside_image",
			"bbox is outside the screenshot dimensions recorded in the manifest",
			"请使用同一张截图的 OCR bbox；不要复用旧截图或其他窗口的 bbox。",
		)
		return &err
	}
	return nil
}

func mapScreenshotBBoxCenterToScreen(manifest desktopScreenshotManifest, bbox [4]int) (int, int) {
	centerX := (bbox[0] + bbox[2]) / 2
	centerY := (bbox[1] + bbox[3]) / 2
	screenX := manifest.WindowBounds.X + centerX*manifest.WindowBounds.Width/manifest.Width
	screenY := manifest.WindowBounds.Y + centerY*manifest.WindowBounds.Height/manifest.Height
	return screenX, screenY
}

func canonicalDesktopBBox(bbox [4]int) string {
	return fmt.Sprintf("%d,%d,%d,%d", bbox[0], bbox[1], bbox[2], bbox[3])
}

func classifyDesktopVisualClickRisk(expectedText string, reason string) desktopActionRisk {
	text := strings.ToLower(strings.Join([]string{expectedText, reason}, " "))
	if containsAny(text, []string{
		"支付", "付款", "转账", "审批", "授权", "允许", "登录", "验证码", "人机验证", "删除", "移除", "清空", "退出登录",
		"pay", "payment", "transfer", "approve", "authorize", "allow", "sign in", "log in", "captcha", "verification", "delete", "remove", "erase", "revoke", "logout",
	}) {
		return desktopRiskHigh
	}
	if containsAny(text, []string{
		"发送", "提交", "上传", "保存", "发布", "分享", "转发", "邀请", "创建", "关闭",
		"send", "submit", "upload", "save", "publish", "share", "forward", "invite", "create", "close", "commit",
	}) {
		return desktopRiskExternal
	}
	if containsAny(text, []string{
		"输入", "发消息", "搜索", "编辑", "聚焦", "光标", "文本框", "输入框", "message", "input", "search", "edit", "focus", "cursor", "textbox", "text field",
		"显示", "打开", "展开", "收起", "菜单", "标签", "下一", "上一", "切换",
		"show", "open", "expand", "collapse", "menu", "tab", "next", "previous", "toggle",
	}) {
		return desktopRiskReversible
	}
	return desktopRiskExternal
}

func desktopVisualClickApprovalRequiredOutcome(approval ActionApproval, expectedText string, risk desktopActionRisk) agent.Outcome {
	return agent.Outcome{
		Data: map[string]any{
			"status":        agent.ToolStatusError,
			"code":          "desktop_action_confirmation_required",
			"message":       "this desktop visual click requires one-time user confirmation",
			"hint":          "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，将返回 confirmation_token。然后用同一 pid、image_path、bbox、reason 和该 token 重试一次 desktop_visual_click。",
			"risk":          risk,
			"expected_text": expectedText,
			"approval_request": map[string]any{
				"operation":  approval.Operation,
				"pid":        approval.PID,
				"image_path": approval.ImagePath,
				"bbox":       approval.BBox,
				"reason":     approval.Reason,
			},
		},
		NextPrompt: "\n",
	}
}

type DesktopPressKey struct {
	driver        desktop.Driver
	confirmations *ConfirmationStore
}

func NewDesktopPressKey(driver desktop.Driver, confirmations *ConfirmationStore) *DesktopPressKey {
	return &DesktopPressKey{driver: driver, confirmations: confirmations}
}

func (t *DesktopPressKey) Name() string { return ToolNameDesktopPressKey }

func (t *DesktopPressKey) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Press a restricted desktop key for the frontmost target PID. Low-risk navigation keys such as Escape, Tab, Shift+Tab, arrows, PageUp/PageDown, Home, and End can run directly. Enter, Cmd+Enter, Ctrl+Enter, Delete, Backspace, and related deletion/submit keys require a one-time confirmation_token from ask_user. This tool does not type text and does not support arbitrary shortcuts.",
		Parameters: objectSchema(map[string]any{
			"pid":                intProp("Target application PID from desktop_windows. The helper refuses to press the key unless this PID is frontmost.", 0),
			"key":                stringProp("Restricted key or shortcut, for example Escape, Tab, Shift+Tab, ArrowUp, ArrowDown, Enter, Cmd+Enter, Delete, Backspace."),
			"reason":             stringProp("Concrete user-facing reason for this key press."),
			"confirmation_token": stringProp("Required only for R2 keys. Obtain it from ask_user with an approval binding for this exact pid, key, and reason."),
		}, "pid", "key", "reason"),
	}}
}

func (t *DesktopPressKey) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	key, keyErr := requiredDesktopActionString(call.Args, "key")
	if keyErr != nil {
		return agent.Outcome{Data: *keyErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}
	normalizedKey, risk, ok := classifyDesktopKeyRisk(key)
	if !ok {
		return desktopActionError(
			"desktop_press_key_unsupported",
			"desktop_press_key only supports a restricted key allowlist",
			"当前只支持 Escape、Tab、Shift+Tab、方向键、PageUp/PageDown、Home/End，以及需要确认的 Enter/Cmd+Enter/Ctrl+Enter/Delete/Backspace。",
		), nil
	}
	if risk == desktopRiskExternal {
		approval := ActionApproval{
			Operation: desktopPressKeyOperation,
			PID:       pid,
			Key:       normalizedKey,
			Reason:    reason,
		}
		token := strings.TrimSpace(asString(call.Args["confirmation_token"]))
		if !t.confirmations.Consume(token, approval) {
			return desktopPressKeyApprovalRequiredOutcome(approval, risk), nil
		}
	}

	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.PressKey(ctx, desktop.PressKeyRequest{PID: pid, Key: normalizedKey})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":        agent.ToolStatusSuccess,
		"pid":           result.PID,
		"key":           result.Key,
		"action":        result.Action,
		"risk":          risk,
		"performed":     result.Performed,
		"active_before": result.ActiveBefore,
		"active_after":  result.ActiveAfter,
		"verified":      verified,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "desktop_press_key_unverified"
		data["message"] = "desktop key event was sent but target foreground state was not verified"
		data["hint"] = "请停止连续按键，重新调用 desktop_windows 和 desktop_activate 确认目标应用状态。"
	}
	return agent.Outcome{
		Data:       data,
		NextPrompt: "\n",
	}, nil
}

func classifyDesktopKeyRisk(key string) (string, desktopActionRisk, bool) {
	normalized := normalizeDesktopKey(key)
	switch normalized {
	case "Escape", "Tab", "Shift+Tab", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "PageUp", "PageDown", "Home", "End":
		return normalized, desktopRiskReversible, true
	case "Enter", "Cmd+Enter", "Ctrl+Enter", "Delete", "Backspace", "Cmd+Backspace", "Ctrl+Backspace":
		return normalized, desktopRiskExternal, true
	default:
		return "", "", false
	}
}

func normalizeDesktopKey(key string) string {
	parts := strings.Split(strings.TrimSpace(key), "+")
	for index := range parts {
		parts[index] = strings.ToLower(strings.TrimSpace(parts[index]))
	}
	if len(parts) == 1 {
		switch parts[0] {
		case "esc", "escape":
			return "Escape"
		case "tab":
			return "Tab"
		case "enter", "return":
			return "Enter"
		case "delete", "forwarddelete", "forward_delete":
			return "Delete"
		case "backspace":
			return "Backspace"
		case "arrowup", "up":
			return "ArrowUp"
		case "arrowdown", "down":
			return "ArrowDown"
		case "arrowleft", "left":
			return "ArrowLeft"
		case "arrowright", "right":
			return "ArrowRight"
		case "pageup":
			return "PageUp"
		case "pagedown":
			return "PageDown"
		case "home":
			return "Home"
		case "end":
			return "End"
		}
		return ""
	}
	if len(parts) != 2 {
		return ""
	}
	modifier := ""
	switch parts[0] {
	case "shift":
		modifier = "Shift"
	case "cmd", "command", "meta":
		modifier = "Cmd"
	case "ctrl", "control":
		modifier = "Ctrl"
	default:
		return ""
	}
	base := normalizeDesktopKey(parts[1])
	switch modifier + "+" + base {
	case "Shift+Tab", "Cmd+Enter", "Ctrl+Enter", "Cmd+Backspace", "Ctrl+Backspace":
		return modifier + "+" + base
	default:
		return ""
	}
}

func desktopPressKeyApprovalRequiredOutcome(approval ActionApproval, risk desktopActionRisk) agent.Outcome {
	return agent.Outcome{
		Data: map[string]any{
			"status":  agent.ToolStatusError,
			"code":    "desktop_action_confirmation_required",
			"message": "this desktop key requires one-time user confirmation",
			"hint":    "请调用 ask_user，并在 approval 中原样传入 approval_request；用户明确确认后，将返回 confirmation_token。然后用同一 pid、key、reason 和该 token 重试一次 desktop_press_key。",
			"risk":    risk,
			"approval_request": map[string]any{
				"operation": approval.Operation,
				"pid":       approval.PID,
				"key":       approval.Key,
				"reason":    approval.Reason,
			},
		},
		NextPrompt: "\n",
	}
}

func desktopActionError(code string, message string, hint string) agent.Outcome {
	return agent.Outcome{
		Data:       agent.NewToolError(code, message, hint),
		NextPrompt: "\n",
	}
}

func activateDesktopTarget(ctx context.Context, driver desktop.Driver, pid int) error {
	_, err := driver.Activate(ctx, desktop.ActivateRequest{PID: pid})
	return err
}

type DesktopTypeText struct {
	driver desktop.Driver
}

func NewDesktopTypeText(driver desktop.Driver) *DesktopTypeText {
	return &DesktopTypeText{driver: driver}
}

func (t *DesktopTypeText) Name() string { return ToolNameDesktopTypeText }

func (t *DesktopTypeText) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Type text into the currently focused editable desktop field for the frontmost target PID. This is for drafting only and never sends the message. The tool refuses empty or overly long text, does not return the typed content, and send/submit must be handled separately with desktop_press_key plus confirmation.",
		Parameters: objectSchema(map[string]any{
			"pid":    intProp("Target application PID from desktop_windows. The helper refuses to type unless this PID is frontmost.", 0),
			"text":   stringProp("Text to type into the current focused editable field. The tool result will not echo this text."),
			"reason": stringProp("Concrete user-facing reason for typing this draft."),
		}, "pid", "text", "reason"),
	}}
}

func (t *DesktopTypeText) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	text, textErr := requiredDesktopTypeText(call.Args)
	if textErr != nil {
		return agent.Outcome{Data: *textErr, NextPrompt: "\n"}, nil
	}
	reason, reasonErr := requiredDesktopActionString(call.Args, "reason")
	if reasonErr != nil {
		return agent.Outcome{Data: *reasonErr, NextPrompt: "\n"}, nil
	}

	if err := activateDesktopTarget(ctx, t.driver, pid); err != nil {
		return desktopToolError(err), nil
	}
	result, err := t.driver.TypeText(ctx, desktop.TypeTextRequest{PID: pid, Text: text})
	if err != nil {
		return desktopToolError(err), nil
	}
	verified := result.Performed && result.ActiveBefore && result.ActiveAfter
	data := map[string]any{
		"status":            agent.ToolStatusSuccess,
		"pid":               result.PID,
		"action":            result.Action,
		"reason":            reason,
		"text_length":       result.TextLength,
		"line_count":        result.LineCount,
		"focus_role":        result.FocusRole,
		"focus_title":       result.FocusTitle,
		"focus_description": result.FocusDescription,
		"performed":         result.Performed,
		"active_before":     result.ActiveBefore,
		"active_after":      result.ActiveAfter,
		"verified":          verified,
		"content_returned":  false,
	}
	if !verified {
		data["status"] = agent.ToolStatusError
		data["code"] = "desktop_type_text_unverified"
		data["message"] = "desktop text was typed but target foreground state was not verified"
		data["hint"] = "请停止继续输入或发送，重新调用 desktop_windows 和 desktop_activate 确认目标应用状态。"
	}
	return agent.Outcome{
		Data:       data,
		NextPrompt: "\n",
	}, nil
}

func requiredDesktopTypeText(args map[string]any) (string, *agent.ToolErrorData) {
	raw, present := args["text"]
	if !present {
		err := agent.NewToolError(
			"desktop_type_text_bad_request",
			"missing required field: text",
			"请提供需要起草的文本；发送动作必须单独使用 desktop_press_key。",
		)
		return "", &err
	}
	text, ok := raw.(string)
	if !ok {
		err := agent.NewToolError(
			"desktop_type_text_bad_request",
			"text must be a string",
			"请提供字符串文本；工具结果不会回显完整内容。",
		)
		return "", &err
	}
	if text == "" {
		err := agent.NewToolError(
			"desktop_type_text_bad_request",
			"text must be non-empty",
			"空文本没有可执行动作；如需发送请使用 desktop_press_key 并确认。",
		)
		return "", &err
	}
	if utf8.RuneCountInString(text) > maxDesktopTypeTextRunes {
		err := agent.NewToolError(
			"desktop_type_text_too_long",
			"desktop_type_text text exceeds the maximum length",
			"请缩短单次输入文本，分段起草并在发送前让用户确认。",
		)
		return "", &err
	}
	return text, nil
}
