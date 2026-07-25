package tools

import (
	"context"
	"reflect"
	"slices"
	"strings"

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
			"当前 M2.1 不会退化为坐标点击；请选择支持 AXPress 的节点或等待 desktop_click。",
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
