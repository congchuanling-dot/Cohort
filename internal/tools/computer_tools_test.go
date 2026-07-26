package tools

import (
	"context"
	"testing"
	"time"

	"cohert/internal/agent"
	"cohert/internal/computeruse"
	"cohert/internal/desktop"
	"cohert/internal/vision"
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
