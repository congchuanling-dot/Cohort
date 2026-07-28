package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"cohort/internal/agent"
	"cohort/internal/computeruse"
	"cohort/internal/desktop"
	"cohort/internal/vision"
)

func TestComputerSeeAndFindCachesAXTargets_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot("hello draft"))
	seeOutcome, err := NewComputerSeeWithOCRRunner(driver, store, t.TempDir(), nil).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "WeChat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seeData := seeOutcome.Data.(map[string]any)
	if seeData["status"] != agent.ToolStatusSuccess || seeData["candidate_count"].(int) == 0 {
		t.Fatalf("see outcome = %#v", seeData)
	}
	if driver.activateRequest.PID != 123 || driver.screenshotRequest.WindowID != "w1" {
		t.Fatalf("driver requests = activate:%#v screenshot:%#v", driver.activateRequest, driver.screenshotRequest)
	}

	findOutcome, err := NewComputerFind(store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"query": "消息输入框"},
	})
	if err != nil {
		t.Fatal(err)
	}
	findData := findOutcome.Data.(map[string]any)
	targets := findData["targets"].([]computerTargetMatch)
	if len(targets) == 0 {
		t.Fatalf("expected target matches: %#v", findData)
	}
	if targets[0].ID == "" || targets[0].Source != computeruse.SourceAX || targets[0].SuggestedAction != computeruse.SuggestedActionType {
		t.Fatalf("target = %#v", targets[0])
	}
}

func TestComputerSeeAndFindCachesOCRVisionTargets_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	runner := &fakeOCRRunner{result: vision.OCRResult{
		Status: "success",
		Text:   "搜索联系人",
		Lines: []vision.OCRLine{{
			Index:      1,
			Text:       "搜索联系人",
			Confidence: 0.96,
			BBox:       []int{10, 10, 120, 32},
		}},
	}}
	seeOutcome, err := NewComputerSeeWithOCRRunner(driver, store, t.TempDir(), runner).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "WeChat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seeData := seeOutcome.Data.(map[string]any)
	if seeData["ocr_status"] != "success" || seeData["ocr_line_count"] != 1 {
		t.Fatalf("see outcome = %#v", seeData)
	}

	findOutcome, err := NewComputerFind(store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"query": "搜索输入框"},
	})
	if err != nil {
		t.Fatal(err)
	}
	targets := findOutcome.Data.(map[string]any)["targets"].([]computerTargetMatch)
	if len(targets) == 0 {
		t.Fatalf("expected OCR/vision target")
	}
	foundVisionInput := false
	for _, target := range targets {
		if target.Source == computeruse.SourceVision && target.SuggestedAction == computeruse.SuggestedActionType {
			foundVisionInput = true
		}
	}
	if !foundVisionInput {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestComputerVisualSnapshotReturnsOCRAndVisionCandidates_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	runner := &fakeOCRRunner{result: vision.OCRResult{
		Status: "success",
		Text:   "搜索联系人",
		Lines: []vision.OCRLine{{
			Index:      1,
			Text:       "搜索联系人",
			Confidence: 0.96,
			BBox:       []int{10, 10, 120, 32},
		}},
	}}
	if _, err := NewComputerSeeWithOCRRunner(driver, store, t.TempDir(), runner).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "WeChat"},
	}); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewComputerVisualSnapshot(store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"mode": "ocr", "query": "搜索", "limit": 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["contains_screenshot"] != false {
		t.Fatalf("visual snapshot outcome = %#v", data)
	}
	if data["screenshot_ref"] == "" || data["screenshot_manifest_ref"] == "" {
		t.Fatalf("missing screenshot refs: %#v", data)
	}
	matches := data["candidates"].([]computerTargetMatch)
	if len(matches) == 0 {
		t.Fatalf("expected visual matches: %#v", data)
	}
	for _, match := range matches {
		if match.Source != computeruse.SourceOCR && match.Source != computeruse.SourceVision {
			t.Fatalf("unexpected source in mode=ocr: %#v", match)
		}
		if match.ID == "" || match.CoordinateSpace != desktop.CoordinateSpaceScreenshotLocal {
			t.Fatalf("bad visual target metadata: %#v", match)
		}
	}
	counts := data["source_counts"].(map[string]int)
	if counts[computeruse.SourceOCR] == 0 {
		t.Fatalf("source counts = %#v", counts)
	}
}

func TestComputerExecuteStepTypesAndVerifiesText_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:               "消息输入框",
			Role:                "AXTextArea",
			Confidence:          0.9,
			Source:              computeruse.SourceAX,
			Bounds:              desktop.Bounds{X: 20, Y: 500, Width: 500, Height: 80},
			CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
			Window:              computerTestWindowRef(),
			SuggestedAction:     computeruse.SuggestedActionType,
			AXNodeID:            "ax:0/0",
			ExpectedRole:        "AXTextArea",
			ExpectedTitle:       "消息输入框",
			ExpectedDescription: "输入消息",
		}},
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.axSnapshots = []desktop.AXSnapshotResult{
		{PID: 123, Root: computerAXRoot("")},
		{PID: 123, Root: computerAXRoot("hello oav")},
	}
	driver.axFocus = desktop.AXFocusResult{PID: 123, NodeID: "ax:0/0", Action: "AXFocus", Performed: true, Focused: true}
	driver.typeText = desktop.TypeTextResult{PID: 123, Action: "TypeText", Performed: true, ActiveBefore: true, ActiveAfter: true, TextLength: len("hello oav"), LineCount: 1}

	outcome, err := NewComputerExecuteStep(driver, store, NewConfirmationStore(), NewVisualFocusStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"action":               "type",
			"target_query":         "消息输入框",
			"text":                 "hello oav",
			"reason":               "起草测试内容",
			"verify_contains_text": "hello oav",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.axFocusRequest.NodeID != "ax:0/0" || driver.typeTextRequest.Text != "hello oav" {
		t.Fatalf("requests = focus:%#v type:%#v state:%#v", driver.axFocusRequest, driver.typeTextRequest, state)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["action"] != "type" {
		t.Fatalf("outcome = %#v", data)
	}
	actionData := data["action_outcome"].(map[string]any)
	if actionData["executor"] != "computer_execute_step" || actionData["target_id"] != state.Candidates[0].ID {
		t.Fatalf("action outcome = %#v", actionData)
	}
	verificationData := data["verification_outcome"].(map[string]any)
	if verificationData["verified"] != true {
		t.Fatalf("verification = %#v", verificationData)
	}
}

func TestComputerExecutePlanRunsObserveTypeAndVerify_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.axSnapshots = []desktop.AXSnapshotResult{
		{PID: 123, Root: computerAXRoot("")},
		{PID: 123, Root: computerAXRoot("")},
		{PID: 123, Root: computerAXRoot("hello plan")},
	}
	driver.axFocus = desktop.AXFocusResult{PID: 123, NodeID: "ax:0/0", Action: "AXFocus", Performed: true, Focused: true}
	driver.typeText = desktop.TypeTextResult{PID: 123, Action: "TypeText", Performed: true, ActiveBefore: true, ActiveAfter: true, TextLength: len("hello plan"), LineCount: 1}

	outcome, err := NewComputerExecutePlanWithOCRRunner(driver, store, NewConfirmationStore(), NewVisualFocusStore(), t.TempDir(), nil).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"reason": "执行起草计划",
			"steps": []any{
				map[string]any{"action": "observe", "app_name": "WeChat", "reason": "观察聊天窗口"},
				map[string]any{"action": "type", "target_query": "消息输入框", "text": "hello plan", "reason": "起草文本", "verify_contains_text": "hello plan"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("plan outcome = %#v", data)
	}
	steps := data["executed_steps"].([]map[string]any)
	if len(steps) != 2 || steps[0]["step_action"] != "observe" || steps[1]["step_action"] != "type" {
		t.Fatalf("executed steps = %#v", steps)
	}
	if driver.typeTextRequest.Text != "hello plan" {
		t.Fatalf("type request = %#v", driver.typeTextRequest)
	}
}

func TestComputerExecutePlanRecoversMissingStateWithObserve_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.axSnapshots = []desktop.AXSnapshotResult{
		{PID: 123, Root: computerAXRoot("")},
		{PID: 123, Root: computerAXRoot("")},
		{PID: 123, Root: computerAXRoot("hello recover")},
	}
	driver.axFocus = desktop.AXFocusResult{PID: 123, NodeID: "ax:0/0", Action: "AXFocus", Performed: true, Focused: true}
	driver.typeText = desktop.TypeTextResult{PID: 123, Action: "TypeText", Performed: true, ActiveBefore: true, ActiveAfter: true, TextLength: len("hello recover"), LineCount: 1}

	outcome, err := NewComputerExecutePlanWithOCRRunner(driver, store, NewConfirmationStore(), NewVisualFocusStore(), t.TempDir(), nil).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"reason":         "执行恢复计划",
			"max_recoveries": 1,
			"steps": []any{
				map[string]any{"action": "type", "target_query": "消息输入框", "text": "hello recover", "reason": "起草文本", "verify_contains_text": "hello recover"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("plan outcome = %#v", data)
	}
	steps := data["executed_steps"].([]map[string]any)
	if len(steps) != 1 || steps[0]["attempt"] != 1 {
		t.Fatalf("expected recovered retry on attempt 1: %#v", steps)
	}
	recovery := steps[0]["recovery"].(map[string]any)
	if recovery["strategy"] != "computer_see_then_retry" || recovery["status"] != agent.ToolStatusSuccess {
		t.Fatalf("recovery = %#v", recovery)
	}
}

func TestComputerExecutePlanHandoffsOnRepeatedClick_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	enabled := true
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:               "表情",
			Role:                "AXButton",
			Confidence:          0.9,
			Source:              computeruse.SourceAX,
			Bounds:              desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
			CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
			Window:              computerTestWindowRef(),
			SuggestedAction:     computeruse.SuggestedActionClick,
			AXNodeID:            "ax:0/1",
			ExpectedRole:        "AXButton",
			ExpectedTitle:       "打开表情",
			ExpectedDescription: "",
		}},
	})
	root := computerAXRoot("")
	root.Children = append(root.Children, desktop.AXNode{ID: "ax:0/1", Role: "AXButton", Title: "打开表情", Enabled: &enabled, Bounds: desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50}})
	driver := fakeComputerDriver(root)
	driver.click = desktop.ClickResult{PID: 123, NodeID: "ax:0/1", Action: "Click", Performed: true, ActiveBefore: true, ActiveAfter: true, X: 435, Y: 45, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical}

	outcome, err := NewComputerExecutePlanWithOCRRunner(driver, store, NewConfirmationStore(), NewVisualFocusStore(), t.TempDir(), nil).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"reason": "测试重复点击阻断",
			"steps": []any{
				map[string]any{"action": "click", "target_query": "表情", "reason": "打开表情面板"},
				map[string]any{"action": "click", "target_query": "表情", "reason": "再次打开表情面板"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusError || data["code"] != "computer_execute_plan_repeated_click_blocked" {
		t.Fatalf("plan outcome = %#v state=%#v", data, state)
	}
	if data["failed_step_index"] != 1 {
		t.Fatalf("failed index = %#v", data["failed_step_index"])
	}
}

func TestComputerSeeAddsHeuristicChatInputForWebView_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerOffscreenTextFieldRoot())

	seeOutcome, err := NewComputerSeeWithOCRRunner(driver, store, t.TempDir(), nil).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "WeChat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seeData := seeOutcome.Data.(map[string]any)
	if seeData["candidate_count"].(int) == 0 {
		t.Fatalf("see outcome = %#v", seeData)
	}

	findOutcome, err := NewComputerFind(store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"query": "聊天输入框 底部 输入消息"},
	})
	if err != nil {
		t.Fatal(err)
	}
	targets := findOutcome.Data.(map[string]any)["targets"].([]computerTargetMatch)
	if len(targets) == 0 {
		t.Fatalf("expected heuristic visual input target")
	}
	top := targets[0]
	if top.Source != computeruse.SourceVision || top.Role != "VisualInputRegion" || top.SuggestedAction != computeruse.SuggestedActionType {
		t.Fatalf("top target = %#v", top)
	}
	if top.BBox != [4]int{224, 491, 768, 579} {
		t.Fatalf("bbox = %#v", top.BBox)
	}
}

func TestComputerClickUsesCachedTargetMapping_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	target := saveComputerButtonTarget(store)
	driver.click = desktop.ClickResult{
		PID:             123,
		NodeID:          "ax:0/1",
		Action:          "Click",
		Performed:       true,
		ActiveBefore:    true,
		ActiveAfter:     true,
		X:               435,
		Y:               45,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	}

	outcome, err := NewComputerClick(driver, store, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "reason": "打开表情面板"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.clickRequest.NodeID != "ax:0/1" || driver.clickRequest.ExpectedTitle != "打开表情" {
		t.Fatalf("click request = %#v", driver.clickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["target_id"] != target.ID || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerClickUsesVisualTargetMapping_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "w1",
		Width:                 200,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 100, Y: 200, Width: 400, Height: 200},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	store := computeruse.NewStore(time.Minute)
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:              "打开设置",
			Role:               "OCRText",
			Source:             computeruse.SourceOCR,
			Window:             computerTestWindowRef(),
			SuggestedAction:    computeruse.SuggestedActionClick,
			ScreenshotRef:      imagePath,
			ScreenshotManifest: manifestPath,
			BBox:               [4]int{40, 20, 60, 40},
		}},
	})
	target := state.Candidates[0]
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.visualClick = desktop.VisualClickResult{
		PID:             123,
		Action:          "VisualClick",
		Performed:       true,
		ActiveBefore:    true,
		ActiveAfter:     true,
		X:               200,
		Y:               260,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	}

	outcome, err := NewComputerClick(driver, store, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "reason": "打开设置面板"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.visualClickRequest.X != 200 || driver.visualClickRequest.Y != 260 {
		t.Fatalf("visual click request = %#v", driver.visualClickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["source"] != computeruse.SourceOCR || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerClickRejectsStaleTarget_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(desktop.AXNode{
		ID:   "ax:0",
		Role: "AXApplication",
		Children: []desktop.AXNode{{
			ID:     "ax:0/1",
			Role:   "AXButton",
			Title:  "新表情",
			Bounds: desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
		}},
	})
	target := saveComputerButtonTarget(store)

	outcome, err := NewComputerClick(driver, store, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "reason": "打开表情面板"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "computer_target_stale" || driver.clickRequest.NodeID != "" {
		t.Fatalf("error = %#v, click request = %#v", toolErr, driver.clickRequest)
	}
}

func TestComputerTypeDraftsIntoEditableTargetWithoutEcho_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	state := computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:               "消息输入框",
			Role:                "AXTextArea",
			Source:              computeruse.SourceAX,
			SuggestedAction:     computeruse.SuggestedActionType,
			Window:              computerTestWindowRef(),
			AXNodeID:            "ax:0/0",
			ExpectedRole:        "AXTextArea",
			ExpectedTitle:       "消息输入框",
			ExpectedDescription: "输入消息",
		}},
	}
	state = store.SaveState(state)
	target := state.Candidates[0]
	driver.axFocus = desktop.AXFocusResult{
		PID:          123,
		NodeID:       "ax:0/0",
		Action:       "AXFocus",
		Performed:    true,
		ActiveBefore: true,
		ActiveAfter:  true,
		Focused:      true,
		FocusRole:    "AXTextArea",
		FocusTitle:   "消息输入框",
	}
	driver.typeText = desktop.TypeTextResult{
		PID:               123,
		Action:            "TypeText",
		Performed:         true,
		ActiveBefore:      true,
		ActiveAfter:       true,
		TextLength:        5,
		LineCount:         1,
		FocusRole:         "AXTextArea",
		FocusTitle:        "消息输入框",
		FocusVerification: "ax_focus",
	}

	outcome, err := NewComputerType(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "text": "hello", "reason": "起草回复"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.axFocusRequest.NodeID != "ax:0/0" || driver.typeTextRequest.Text != "hello" {
		t.Fatalf("requests = focus:%#v type:%#v", driver.axFocusRequest, driver.typeTextRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["content_returned"] != false {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["text"]; exists {
		t.Fatalf("computer_type must not echo content: %#v", data)
	}
}

func TestComputerTypeUsesVisualTargetFocusFallback_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "w1",
		Width:                 100,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 0, Y: 0, Width: 100, Height: 100},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	store := computeruse.NewStore(time.Minute)
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:              "消息输入框",
			Role:               "VisualCandidate",
			Source:             computeruse.SourceVision,
			Window:             computerTestWindowRef(),
			SuggestedAction:    computeruse.SuggestedActionType,
			ScreenshotRef:      imagePath,
			ScreenshotManifest: manifestPath,
			BBox:               [4]int{10, 20, 30, 40},
		}},
	})
	target := state.Candidates[0]
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.visualClick = desktop.VisualClickResult{PID: 123, Action: "VisualClick", Performed: true, ActiveBefore: true, ActiveAfter: true, X: 20, Y: 30, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical}
	driver.typeTextErrs = []error{
		&desktop.ToolError{Code: "desktop_type_text_focus_unavailable", Message: "no AX focus", Hint: "retry with visual focus"},
		nil,
	}
	driver.typeTexts = []desktop.TypeTextResult{
		{},
		{PID: 123, Action: "TypeText", Performed: true, ActiveBefore: true, ActiveAfter: true, TextLength: 2, LineCount: 1, FocusVerification: "visual_token"},
	}

	outcome, err := NewComputerType(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "text": "你好", "reason": "起草消息"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.visualClickRequest.X != 20 || driver.visualClickRequest.Y != 30 {
		t.Fatalf("visual click request = %#v", driver.visualClickRequest)
	}
	if len(driver.typeTextRequests) != 2 || driver.typeTextRequests[0].AllowVisualFocus || !driver.typeTextRequests[1].AllowVisualFocus {
		t.Fatalf("type requests = %#v", driver.typeTextRequests)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["visual_focus_used"] != true || data["content_returned"] != false {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["text"]; exists {
		t.Fatalf("computer_type must not echo content: %#v", data)
	}
}

func TestComputerCheckFindsAXTextEvidence_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot("hello draft"))
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})

	outcome, err := NewComputerCheck(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"expectation": "输入框中已有草稿", "contains_text": "hello draft"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	evidence := data["evidence"].([]string)
	if len(evidence) == 0 {
		t.Fatalf("missing evidence: %#v", data)
	}
}

func TestComputerPressRequiresConfirmationForSubmitKey_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.pressKey = desktop.PressKeyResult{PID: 123, Key: "Cmd+Enter", Action: "PressKey", Performed: true, ActiveBefore: true, ActiveAfter: true}
	confirmations := NewConfirmationStore()
	tool := NewComputerPress(driver, store, confirmations)
	args := map[string]any{"key": "Cmd+Enter", "reason": "发送已确认消息"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.pressKeyRequest.Key != "" {
		t.Fatalf("outcome = %#v, request = %#v", required, driver.pressKeyRequest)
	}
	token, err := confirmations.Issue(ActionApproval{Operation: desktopPressKeyOperation, PID: state.ActivePID, Key: "Cmd+Enter", Reason: "发送已确认消息"})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if driver.pressKeyRequest.Key != "Cmd+Enter" || data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("outcome = %#v, request = %#v", data, driver.pressKeyRequest)
	}
}

func TestComputerScrollUsesLatestComputerState_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.scroll = desktop.ScrollResult{PID: 123, Action: "Scroll", Performed: true, ActiveBefore: true, ActiveAfter: true, DeltaY: -360}

	outcome, err := NewComputerScroll(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"direction": "down", "ticks": 3, "reason": "查看更多内容"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.scrollRequest.PID != 123 || driver.scrollRequest.DeltaY != -360 {
		t.Fatalf("scroll request = %#v", driver.scrollRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerDragRequiresConfirmation_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	target := saveComputerButtonTarget(store)
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.drag = desktop.DragResult{
		PID:             123,
		Action:          "Drag",
		Performed:       true,
		ActiveBefore:    true,
		ActiveAfter:     true,
		StartX:          435,
		StartY:          45,
		EndX:            535,
		EndY:            45,
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	}
	confirmations := NewConfirmationStore()
	tool := NewComputerDrag(driver, store, confirmations)
	args := map[string]any{"target_id": target.ID, "delta_x": 100, "reason": "调整列表顺序"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.dragRequest.PID != 0 {
		t.Fatalf("outcome = %#v, drag request = %#v", required, driver.dragRequest)
	}
	token, err := confirmations.Issue(ActionApproval{Operation: computerDragOperation, PID: 123, BBox: target.ID + "||100|", Reason: "调整列表顺序"})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if driver.dragRequest.StartX != 435 || driver.dragRequest.EndX != 535 {
		t.Fatalf("drag request = %#v", driver.dragRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerDropRequiresConfirmationAndUsesCachedTargets_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	enabled := true
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{
			{
				Label:               "表情",
				Role:                "AXButton",
				Source:              computeruse.SourceAX,
				Bounds:              desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
				CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
				Window:              computerTestWindowRef(),
				SuggestedAction:     computeruse.SuggestedActionClick,
				AXNodeID:            "ax:0/1",
				ExpectedRole:        "AXButton",
				ExpectedTitle:       "打开表情",
				ExpectedDescription: "",
			},
			{
				Label:               "消息输入框",
				Role:                "AXTextArea",
				Source:              computeruse.SourceAX,
				Bounds:              desktop.Bounds{X: 20, Y: 500, Width: 500, Height: 80},
				CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
				Window:              computerTestWindowRef(),
				SuggestedAction:     computeruse.SuggestedActionType,
				AXNodeID:            "ax:0/0",
				ExpectedRole:        "AXTextArea",
				ExpectedTitle:       "消息输入框",
				ExpectedDescription: "输入消息",
			},
		},
	})
	root := desktop.AXNode{
		ID:   "ax:0",
		Role: "AXApplication",
		Children: []desktop.AXNode{
			{ID: "ax:0/0", Role: "AXTextArea", Title: "消息输入框", Description: "输入消息", Enabled: &enabled, Bounds: desktop.Bounds{X: 20, Y: 500, Width: 500, Height: 80}},
			{ID: "ax:0/1", Role: "AXButton", Title: "打开表情", Enabled: &enabled, Bounds: desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50}},
		},
	}
	driver := fakeComputerDriver(root)
	driver.drag = desktop.DragResult{PID: 123, Action: "Drag", Performed: true, ActiveBefore: true, ActiveAfter: true, StartX: 435, StartY: 45, EndX: 270, EndY: 540, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical}
	confirmations := NewConfirmationStore()
	tool := NewComputerDrop(driver, store, confirmations)
	args := map[string]any{
		"source_target_id":      state.Candidates[0].ID,
		"destination_target_id": state.Candidates[1].ID,
		"reason":                "把表情拖到输入框",
	}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.dragRequest.PID != 0 {
		t.Fatalf("outcome = %#v, drag request = %#v", required, driver.dragRequest)
	}
	request := required["approval_request"].(map[string]any)
	token, err := confirmations.Issue(ActionApproval{
		Operation: request["operation"].(string),
		PID:       request["pid"].(int),
		BBox:      request["bbox"].(string),
		Reason:    request["reason"].(string),
	})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if driver.dragRequest.StartX != 435 || driver.dragRequest.StartY != 45 || driver.dragRequest.EndX != 270 || driver.dragRequest.EndY != 540 {
		t.Fatalf("drop drag request = %#v", driver.dragRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal || data["verified"] != true || data["action"] != "Drop" {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerDoubleClickRequiresConfirmationForExternalTarget_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	enabled := true
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:               "发送",
			Role:                "AXButton",
			Source:              computeruse.SourceAX,
			Bounds:              desktop.Bounds{X: 500, Y: 500, Width: 80, Height: 40},
			CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
			Window:              computerTestWindowRef(),
			SuggestedAction:     computeruse.SuggestedActionClick,
			AXNodeID:            "ax:0/2",
			ExpectedRole:        "AXButton",
			ExpectedTitle:       "发送",
			ExpectedDescription: "",
		}},
	})
	root := computerAXRoot("")
	root.Children = append(root.Children, desktop.AXNode{
		ID:      "ax:0/2",
		Role:    "AXButton",
		Title:   "发送",
		Enabled: &enabled,
		Bounds:  desktop.Bounds{X: 500, Y: 500, Width: 80, Height: 40},
	})
	driver := fakeComputerDriver(root)
	driver.doubleClick = desktop.DoubleClickResult{PID: 123, Action: "DoubleClick", Performed: true, ActiveBefore: true, ActiveAfter: true, X: 540, Y: 520, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical}
	confirmations := NewConfirmationStore()
	tool := NewComputerDoubleClick(driver, store, confirmations)
	args := map[string]any{"target_id": state.Candidates[0].ID, "reason": "打开发送按钮的默认动作"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.doubleClickRequest.PID != 0 {
		t.Fatalf("outcome = %#v, double click request = %#v", required, driver.doubleClickRequest)
	}
	token, err := confirmations.Issue(ActionApproval{Operation: computerDoubleClickOperation, PID: 123, BBox: state.Candidates[0].ID + "|ax:0/2", Reason: "打开发送按钮的默认动作"})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if driver.doubleClickRequest.X != 540 || driver.doubleClickRequest.Y != 520 {
		t.Fatalf("double click request = %#v", driver.doubleClickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerRightClickUsesCachedTargetPoint_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	target := saveComputerButtonTarget(store)
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.rightClick = desktop.RightClickResult{PID: 123, Action: "RightClick", Performed: true, ActiveBefore: true, ActiveAfter: true, X: 435, Y: 45, CoordinateSpace: desktop.CoordinateSpaceScreenPhysical}

	outcome, err := NewComputerRightClick(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"target_id": target.ID, "reason": "打开上下文菜单"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.rightClickRequest.PID != 123 || driver.rightClickRequest.X != 435 || driver.rightClickRequest.Y != 45 {
		t.Fatalf("right click request = %#v", driver.rightClickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerClipboardWriteDoesNotEchoContent_BitsUT(t *testing.T) {
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.clipboardWrite = desktop.ClipboardWriteResult{Action: "ClipboardWrite", Performed: true, TextLength: 5, LineCount: 1}

	outcome, err := NewComputerClipboardWrite(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"text": "hello", "reason": "准备粘贴草稿"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.clipboardWriteRequest.Text != "hello" {
		t.Fatalf("clipboard write request = %#v", driver.clipboardWriteRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["content_returned"] != false || data["reads_clipboard"] != false {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["text"]; exists {
		t.Fatalf("clipboard write must not echo content: %#v", data)
	}
}

func TestComputerPasteWritesTextThenPastes_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.clipboardWrite = desktop.ClipboardWriteResult{Action: "ClipboardWrite", Performed: true, TextLength: 2, LineCount: 1}
	driver.clipboardPaste = desktop.ClipboardPasteResult{PID: 123, Action: "ClipboardPaste", Performed: true, ActiveBefore: true, ActiveAfter: true}

	outcome, err := NewComputerPaste(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"text": "你好", "reason": "粘贴草稿"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.clipboardWriteRequest.Text != "你好" || driver.clipboardPasteRequest.PID != 123 {
		t.Fatalf("requests = write:%#v paste:%#v", driver.clipboardWriteRequest, driver.clipboardPasteRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["text_written"] != true || data["verified"] != true || data["content_returned"] != false {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestAskUserApprovalAcceptsComputerDrag_BitsUT(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		binding   string
		wantText  string
	}{
		{name: "drag", operation: computerDragOperation, binding: "source|dest||", wantText: "桌面拖拽操作"},
		{name: "drop", operation: computerDropOperation, binding: "source->dest", wantText: "桌面拖放操作"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approval, toolErr := parseAskUserApproval(map[string]any{
				"approval": map[string]any{
					"operation": tc.operation,
					"pid":       123,
					"bbox":      tc.binding,
					"reason":    "拖拽文件到目标区域",
				},
			})
			if toolErr != nil {
				t.Fatalf("unexpected error: %#v", toolErr)
			}
			if approval == nil || approval.Operation != tc.operation || approval.BBox != tc.binding {
				t.Fatalf("approval = %#v", approval)
			}
			if !strings.Contains(approvalPrompt(*approval), tc.wantText) {
				t.Fatalf("approval prompt = %q", approvalPrompt(*approval))
			}
		})
	}
}

func TestAskUserApprovalAcceptsComputerDoubleClick_BitsUT(t *testing.T) {
	approval, toolErr := parseAskUserApproval(map[string]any{
		"approval": map[string]any{
			"operation": computerDoubleClickOperation,
			"pid":       123,
			"bbox":      "ct_1|ax:0/2",
			"reason":    "双击打开文件",
		},
	})
	if toolErr != nil {
		t.Fatalf("unexpected error: %#v", toolErr)
	}
	if approval == nil || approval.Operation != computerDoubleClickOperation || approval.BBox != "ct_1|ax:0/2" {
		t.Fatalf("approval = %#v", approval)
	}
	if !strings.Contains(approvalPrompt(*approval), "桌面双击操作") {
		t.Fatalf("approval prompt = %q", approvalPrompt(*approval))
	}
}

func TestComputerWindowSwitchActivatesMatchedWindowAndUpdatesState_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.windows = desktop.ListWindowsResult{Windows: []desktop.Window{
		{WindowID: "w1", PID: 123, AppName: "WeChat", Title: "聊天", Bounds: desktop.Bounds{X: 0, Y: 0, Width: 800, Height: 600}, IsVisible: true, IsActive: true},
		{WindowID: "w2", PID: 456, AppName: "Finder", Title: "Downloads", Bounds: desktop.Bounds{X: 10, Y: 10, Width: 700, Height: 500}, IsVisible: true},
	}}
	driver.activate = desktop.ActivateResult{PID: 456, Active: true, Verified: true}

	outcome, err := NewComputerWindowSwitch(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "Finder", "reason": "切到下载目录"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.listRequest.AppName != "Finder" || driver.activateRequest.PID != 456 || driver.activateRequest.WindowID != "w2" {
		t.Fatalf("requests = list:%#v activate:%#v", driver.listRequest, driver.activateRequest)
	}
	state, err := store.LatestState()
	if err != nil {
		t.Fatal(err)
	}
	if state.ActivePID != 456 || state.ActiveWindow.AppName != "Finder" {
		t.Fatalf("state = %#v", state)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerMenuRequiresConfirmationForExternalMenu_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.menuSelect = desktop.MenuSelectResult{PID: 123, Action: "MenuSelect", Performed: true, ActiveBefore: true, ActiveAfter: true, MenuPath: []string{"File", "Save"}}
	confirmations := NewConfirmationStore()
	tool := NewComputerMenu(driver, store, confirmations)
	args := map[string]any{"menu_path": "File > Save", "reason": "保存当前文档"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || len(driver.menuSelectRequest.MenuPath) != 0 {
		t.Fatalf("outcome = %#v, menu request = %#v", required, driver.menuSelectRequest)
	}
	token, err := confirmations.Issue(ActionApproval{Operation: computerMenuOperation, PID: 123, BBox: "File>Save", Reason: "保存当前文档"})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(driver.menuSelectRequest.MenuPath, ">") != "File>Save" {
		t.Fatalf("menu request = %#v", driver.menuSelectRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestComputerFileDialogConfirmRequiresConfirmation_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.fileDialog = desktop.FileDialogResult{PID: 123, Action: "FileDialog", Performed: true, ActiveBefore: true, ActiveAfter: true, PathLength: len("/tmp/report.txt"), Confirm: true}
	confirmations := NewConfirmationStore()
	tool := NewComputerFileDialog(driver, store, confirmations)
	args := map[string]any{"path": "/tmp/report.txt", "confirm": true, "reason": "选择报告文件"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.fileDialogRequest.Path != "" {
		t.Fatalf("outcome = %#v, file dialog request = %#v", required, driver.fileDialogRequest)
	}
	token, err := confirmations.Issue(ActionApproval{Operation: computerFileDialogOperation, PID: 123, BBox: "/tmp/report.txt|confirm", Reason: "选择报告文件"})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if driver.fileDialogRequest.Path != "/tmp/report.txt" || !driver.fileDialogRequest.Confirm {
		t.Fatalf("file dialog request = %#v", driver.fileDialogRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["path"]; exists {
		t.Fatalf("file dialog result should not echo path: %#v", data)
	}
}

func TestComputerFileDialogWithoutConfirmDoesNotRequireToken_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.fileDialog = desktop.FileDialogResult{PID: 123, Action: "FileDialog", Performed: true, ActiveBefore: true, ActiveAfter: true, PathLength: len("/tmp/report.txt"), Confirm: false}

	outcome, err := NewComputerFileDialog(driver, store, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"path": "/tmp/report.txt", "reason": "跳转到报告文件"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.fileDialogRequest.Path != "/tmp/report.txt" || driver.fileDialogRequest.Confirm {
		t.Fatalf("file dialog request = %#v", driver.fileDialogRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["path"]; exists {
		t.Fatalf("file dialog result should not echo path: %#v", data)
	}
}

func TestComputerWindowMoveAndResizeUseLatestWindow_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.windowMove = desktop.WindowMoveResult{
		PID:             123,
		WindowID:        "w1",
		Action:          "WindowMove",
		Performed:       true,
		ActiveBefore:    true,
		ActiveAfter:     true,
		BeforeBounds:    desktop.Bounds{X: 0, Y: 0, Width: 800, Height: 600},
		AfterBounds:     desktop.Bounds{X: 100, Y: 120, Width: 800, Height: 600},
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	}
	driver.windowResize = desktop.WindowResizeResult{
		PID:             123,
		WindowID:        "w1",
		Action:          "WindowResize",
		Performed:       true,
		ActiveBefore:    true,
		ActiveAfter:     true,
		BeforeBounds:    desktop.Bounds{X: 100, Y: 120, Width: 800, Height: 600},
		AfterBounds:     desktop.Bounds{X: 100, Y: 120, Width: 900, Height: 700},
		CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	}

	moveOutcome, err := NewComputerWindowMove(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"x": 100, "y": 120, "reason": "整理窗口位置"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.windowMoveRequest.PID != 123 || driver.windowMoveRequest.WindowID != "w1" || driver.windowMoveRequest.X != 100 || driver.windowMoveRequest.Y != 120 {
		t.Fatalf("move request = %#v", driver.windowMoveRequest)
	}
	moveData := moveOutcome.Data.(map[string]any)
	if moveData["status"] != agent.ToolStatusSuccess || moveData["risk"] != desktopRiskReversible || moveData["verified"] != true {
		t.Fatalf("move outcome = %#v", moveData)
	}

	resizeOutcome, err := NewComputerWindowResize(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"width": 900, "height": 700, "reason": "扩大窗口"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.windowResizeRequest.PID != 123 || driver.windowResizeRequest.WindowID != "w1" || driver.windowResizeRequest.Width != 900 || driver.windowResizeRequest.Height != 700 {
		t.Fatalf("resize request = %#v", driver.windowResizeRequest)
	}
	resizeData := resizeOutcome.Data.(map[string]any)
	if resizeData["status"] != agent.ToolStatusSuccess || resizeData["risk"] != desktopRiskReversible || resizeData["verified"] != true {
		t.Fatalf("resize outcome = %#v", resizeData)
	}
}

func TestAskUserApprovalAcceptsComputerMenuAndFileDialog_BitsUT(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		binding   string
		wantText  string
	}{
		{name: "menu", operation: computerMenuOperation, binding: "File>Save", wantText: "菜单选择操作"},
		{name: "file_dialog", operation: computerFileDialogOperation, binding: "/tmp/report.txt|confirm", wantText: "文件对话框确认操作"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approval, toolErr := parseAskUserApproval(map[string]any{
				"approval": map[string]any{
					"operation": tc.operation,
					"pid":       123,
					"bbox":      tc.binding,
					"reason":    "确认执行",
				},
			})
			if toolErr != nil {
				t.Fatalf("unexpected error: %#v", toolErr)
			}
			if approval == nil || approval.Operation != tc.operation || approval.BBox != tc.binding {
				t.Fatalf("approval = %#v", approval)
			}
			if !strings.Contains(approvalPrompt(*approval), tc.wantText) {
				t.Fatalf("approval prompt = %q", approvalPrompt(*approval))
			}
		})
	}
}

func TestComputerWaitForTextPollsAXSnapshot_BitsUT(t *testing.T) {
	store := computeruse.NewStore(time.Minute)
	store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
	})
	driver := fakeComputerDriver(computerAXRoot(""))
	driver.axSnapshots = []desktop.AXSnapshotResult{
		{PID: 123, Root: computerAXRoot("处理中")},
		{PID: 123, Root: computerAXRoot("完成")},
	}

	outcome, err := NewComputerWait(driver, store).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"contains_text": "完成", "reason": "等待处理完成", "timeout_ms": 500, "poll_interval_ms": 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["attempts"] != 2 {
		t.Fatalf("outcome = %#v", data)
	}
}

func fakeComputerDriver(root desktop.AXNode) *fakeDesktopDriver {
	return &fakeDesktopDriver{
		windows: desktop.ListWindowsResult{Windows: []desktop.Window{{
			WindowID:  "w1",
			PID:       123,
			AppName:   "WeChat",
			Title:     "聊天",
			Bounds:    desktop.Bounds{X: 0, Y: 0, Width: 800, Height: 600},
			IsVisible: true,
			IsActive:  true,
		}}},
		screenshot: desktop.ScreenshotResult{
			Width:    800,
			Height:   600,
			WindowID: "w1",
			PID:      123,
			Bounds:   desktop.Bounds{X: 0, Y: 0, Width: 800, Height: 600},
		},
		axSnapshot: desktop.AXSnapshotResult{
			PID:       123,
			Root:      root,
			NodeCount: 3,
		},
	}
}

func computerAXRoot(inputValue string) desktop.AXNode {
	enabled := true
	return desktop.AXNode{
		ID:   "ax:0",
		Role: "AXApplication",
		Children: []desktop.AXNode{
			{
				ID:          "ax:0/0",
				Role:        "AXTextArea",
				Title:       "消息输入框",
				Value:       inputValue,
				Description: "输入消息",
				Enabled:     &enabled,
				Bounds:      desktop.Bounds{X: 20, Y: 500, Width: 500, Height: 80},
			},
			{
				ID:      "ax:0/1",
				Role:    "AXButton",
				Title:   "打开表情",
				Enabled: &enabled,
				Bounds:  desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
			},
		},
	}
}

func computerOffscreenTextFieldRoot() desktop.AXNode {
	enabled := true
	return desktop.AXNode{
		ID:   "ax:0",
		Role: "AXApplication",
		Children: []desktop.AXNode{
			{
				ID:      "ax:0/0",
				Role:    "AXTextField",
				Enabled: &enabled,
				Bounds:  desktop.Bounds{X: -2, Y: 900, Width: 684, Height: 52},
			},
			{
				ID:      "ax:0/1",
				Role:    "AXButton",
				Title:   "打开表情",
				Enabled: &enabled,
				Bounds:  desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
			},
		},
	}
}

func saveComputerButtonTarget(store *computeruse.Store) computeruse.ComputerTarget {
	state := store.SaveState(computeruse.ComputerState{
		OS:           "darwin",
		ActivePID:    123,
		ActiveWindow: computerTestWindowRef(),
		Candidates: []computeruse.ComputerTarget{{
			Label:               "表情",
			Role:                "AXButton",
			Source:              computeruse.SourceAX,
			Bounds:              desktop.Bounds{X: 400, Y: 20, Width: 70, Height: 50},
			CoordinateSpace:     desktop.CoordinateSpaceScreenPhysical,
			Window:              computerTestWindowRef(),
			SuggestedAction:     computeruse.SuggestedActionClick,
			AXNodeID:            "ax:0/1",
			ExpectedRole:        "AXButton",
			ExpectedTitle:       "打开表情",
			ExpectedDescription: "",
		}},
	})
	return state.Candidates[0]
}

func computerTestWindowRef() computeruse.WindowRef {
	return computeruse.WindowRef{
		OS:       "darwin",
		AppName:  "WeChat",
		PID:      123,
		WindowID: "w1",
		Title:    "聊天",
		Bounds:   desktop.Bounds{X: 0, Y: 0, Width: 800, Height: 600},
	}
}
