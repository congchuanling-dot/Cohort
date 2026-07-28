package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/desktop"
	"cohert/internal/vision"
)

type fakeDesktopDriver struct {
	permissions    desktop.PermissionsResult
	windows        desktop.ListWindowsResult
	activate       desktop.ActivateResult
	screenshot     desktop.ScreenshotResult
	axSnapshot     desktop.AXSnapshotResult
	axSnapshots    []desktop.AXSnapshotResult
	axPress        desktop.AXPressResult
	axFocus        desktop.AXFocusResult
	click          desktop.ClickResult
	visualClick    desktop.VisualClickResult
	doubleClick    desktop.DoubleClickResult
	rightClick     desktop.RightClickResult
	pressKey       desktop.PressKeyResult
	typeText       desktop.TypeTextResult
	scroll         desktop.ScrollResult
	drag           desktop.DragResult
	clipboardWrite desktop.ClipboardWriteResult
	clipboardPaste desktop.ClipboardPasteResult
	typeTexts      []desktop.TypeTextResult
	typeTextErrs   []error
	err            error

	listRequest           desktop.ListWindowsRequest
	activateRequest       desktop.ActivateRequest
	screenshotRequest     desktop.ScreenshotRequest
	axRequest             desktop.AXSnapshotRequest
	axPressRequest        desktop.AXPressRequest
	axFocusRequest        desktop.AXFocusRequest
	clickRequest          desktop.ClickRequest
	visualClickRequest    desktop.VisualClickRequest
	doubleClickRequest    desktop.DoubleClickRequest
	rightClickRequest     desktop.RightClickRequest
	pressKeyRequest       desktop.PressKeyRequest
	typeTextRequest       desktop.TypeTextRequest
	scrollRequest         desktop.ScrollRequest
	dragRequest           desktop.DragRequest
	clipboardWriteRequest desktop.ClipboardWriteRequest
	clipboardPasteRequest desktop.ClipboardPasteRequest
	typeTextRequests      []desktop.TypeTextRequest
	axSnapshotCalls       int
	typeTextCalls         int
}

func (d *fakeDesktopDriver) Permissions(ctx context.Context) (desktop.PermissionsResult, error) {
	if d.err != nil {
		return desktop.PermissionsResult{}, d.err
	}
	return d.permissions, nil
}

func (d *fakeDesktopDriver) ListWindows(ctx context.Context, req desktop.ListWindowsRequest) (desktop.ListWindowsResult, error) {
	d.listRequest = req
	if d.err != nil {
		return desktop.ListWindowsResult{}, d.err
	}
	return d.windows, nil
}

func (d *fakeDesktopDriver) Activate(ctx context.Context, req desktop.ActivateRequest) (desktop.ActivateResult, error) {
	d.activateRequest = req
	if d.err != nil {
		return desktop.ActivateResult{}, d.err
	}
	return d.activate, nil
}

func (d *fakeDesktopDriver) Screenshot(ctx context.Context, req desktop.ScreenshotRequest) (desktop.ScreenshotResult, error) {
	d.screenshotRequest = req
	if d.err != nil {
		return desktop.ScreenshotResult{}, d.err
	}
	if err := os.WriteFile(req.OutputPath, []byte("fake desktop image"), 0644); err != nil {
		return desktop.ScreenshotResult{}, err
	}
	return d.screenshot, nil
}

func (d *fakeDesktopDriver) AXSnapshot(ctx context.Context, req desktop.AXSnapshotRequest) (desktop.AXSnapshotResult, error) {
	d.axRequest = req
	if d.err != nil {
		return desktop.AXSnapshotResult{}, d.err
	}
	if len(d.axSnapshots) > 0 {
		index := d.axSnapshotCalls
		if index >= len(d.axSnapshots) {
			index = len(d.axSnapshots) - 1
		}
		d.axSnapshotCalls++
		return d.axSnapshots[index], nil
	}
	return d.axSnapshot, nil
}

func (d *fakeDesktopDriver) AXPress(ctx context.Context, req desktop.AXPressRequest) (desktop.AXPressResult, error) {
	d.axPressRequest = req
	if d.err != nil {
		return desktop.AXPressResult{}, d.err
	}
	return d.axPress, nil
}

func (d *fakeDesktopDriver) AXFocus(ctx context.Context, req desktop.AXFocusRequest) (desktop.AXFocusResult, error) {
	d.axFocusRequest = req
	if d.err != nil {
		return desktop.AXFocusResult{}, d.err
	}
	return d.axFocus, nil
}

func (d *fakeDesktopDriver) Click(ctx context.Context, req desktop.ClickRequest) (desktop.ClickResult, error) {
	d.clickRequest = req
	if d.err != nil {
		return desktop.ClickResult{}, d.err
	}
	return d.click, nil
}

func (d *fakeDesktopDriver) VisualClick(ctx context.Context, req desktop.VisualClickRequest) (desktop.VisualClickResult, error) {
	d.visualClickRequest = req
	if d.err != nil {
		return desktop.VisualClickResult{}, d.err
	}
	return d.visualClick, nil
}

func (d *fakeDesktopDriver) DoubleClick(ctx context.Context, req desktop.DoubleClickRequest) (desktop.DoubleClickResult, error) {
	d.doubleClickRequest = req
	if d.err != nil {
		return desktop.DoubleClickResult{}, d.err
	}
	return d.doubleClick, nil
}

func (d *fakeDesktopDriver) RightClick(ctx context.Context, req desktop.RightClickRequest) (desktop.RightClickResult, error) {
	d.rightClickRequest = req
	if d.err != nil {
		return desktop.RightClickResult{}, d.err
	}
	return d.rightClick, nil
}

func (d *fakeDesktopDriver) PressKey(ctx context.Context, req desktop.PressKeyRequest) (desktop.PressKeyResult, error) {
	d.pressKeyRequest = req
	if d.err != nil {
		return desktop.PressKeyResult{}, d.err
	}
	return d.pressKey, nil
}

func (d *fakeDesktopDriver) TypeText(ctx context.Context, req desktop.TypeTextRequest) (desktop.TypeTextResult, error) {
	d.typeTextRequest = req
	d.typeTextRequests = append(d.typeTextRequests, req)
	if d.err != nil {
		return desktop.TypeTextResult{}, d.err
	}
	index := d.typeTextCalls
	d.typeTextCalls++
	if len(d.typeTextErrs) > 0 {
		errIndex := index
		if errIndex >= len(d.typeTextErrs) {
			errIndex = len(d.typeTextErrs) - 1
		}
		if d.typeTextErrs[errIndex] != nil {
			return desktop.TypeTextResult{}, d.typeTextErrs[errIndex]
		}
	}
	if len(d.typeTexts) > 0 {
		resultIndex := index
		if resultIndex >= len(d.typeTexts) {
			resultIndex = len(d.typeTexts) - 1
		}
		return d.typeTexts[resultIndex], nil
	}
	return d.typeText, nil
}

func (d *fakeDesktopDriver) Scroll(ctx context.Context, req desktop.ScrollRequest) (desktop.ScrollResult, error) {
	d.scrollRequest = req
	if d.err != nil {
		return desktop.ScrollResult{}, d.err
	}
	return d.scroll, nil
}

func (d *fakeDesktopDriver) Drag(ctx context.Context, req desktop.DragRequest) (desktop.DragResult, error) {
	d.dragRequest = req
	if d.err != nil {
		return desktop.DragResult{}, d.err
	}
	return d.drag, nil
}

func (d *fakeDesktopDriver) ClipboardWrite(ctx context.Context, req desktop.ClipboardWriteRequest) (desktop.ClipboardWriteResult, error) {
	d.clipboardWriteRequest = req
	if d.err != nil {
		return desktop.ClipboardWriteResult{}, d.err
	}
	return d.clipboardWrite, nil
}

func (d *fakeDesktopDriver) ClipboardPaste(ctx context.Context, req desktop.ClipboardPasteRequest) (desktop.ClipboardPasteResult, error) {
	d.clipboardPasteRequest = req
	if d.err != nil {
		return desktop.ClipboardPasteResult{}, d.err
	}
	return d.clipboardPaste, nil
}

func TestDesktopWindowsClampsLimitAndReportsPhysicalCoordinates_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		windows: desktop.ListWindowsResult{Windows: []desktop.Window{{
			WindowID: "12",
			PID:      123,
			AppName:  "Notes",
			Bounds:   desktop.Bounds{X: 10, Y: 20, Width: 800, Height: 600},
		}}},
	}
	outcome, err := NewDesktopWindows(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"app_name": "notes", "limit": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.listRequest.Limit != maxDesktopWindowLimit || driver.listRequest.AppName != "notes" {
		t.Fatalf("request = %#v", driver.listRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["coordinate_space"] != desktop.CoordinateSpaceScreenPhysical {
		t.Fatalf("coordinate space = %#v", data["coordinate_space"])
	}
}

func TestDesktopActivateRejectsMissingPID_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{}
	outcome, err := NewDesktopActivate(driver).Run(context.Background(), agent.ToolCallContext{})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(agent.ToolErrorData)
	if data.Code != "desktop_bad_pid" {
		t.Fatalf("code = %q, want desktop_bad_pid", data.Code)
	}
}

func TestDesktopScreenshotUsesWorkspaceOwnedPath_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	driver := &fakeDesktopDriver{
		screenshot: desktop.ScreenshotResult{
			Width:    800,
			Height:   600,
			WindowID: "12",
			PID:      123,
			Bounds:   desktop.Bounds{X: 100, Y: 200, Width: 800, Height: 600},
		},
	}
	outcome, err := NewDesktopScreenshot(driver, workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "window_id": "12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.screenshotRequest.PID != 123 {
		t.Fatalf("request = %#v", driver.screenshotRequest)
	}
	rel, err := filepath.Rel(workspace, driver.screenshotRequest.OutputPath)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		t.Fatalf("screenshot path %q is outside workspace %q", driver.screenshotRequest.OutputPath, workspace)
	}
	data := outcome.Data.(map[string]any)
	if data["coordinate_space"] != desktop.CoordinateSpaceScreenshotLocal {
		t.Fatalf("coordinate space = %#v", data["coordinate_space"])
	}
	if _, statErr := os.Stat(driver.screenshotRequest.OutputPath); statErr != nil {
		t.Fatalf("expected screenshot: %v", statErr)
	}
	manifestPath, ok := data["manifest_path"].(string)
	if !ok || manifestPath == "" {
		t.Fatalf("missing manifest path in outcome: %#v", data)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("expected screenshot manifest: %v", err)
	}
	var manifest desktopScreenshotManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ImagePath != driver.screenshotRequest.OutputPath || manifest.PID != 123 || manifest.WindowBounds.Width != 800 {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestDesktopAXSnapshotClampsBounds_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		axSnapshot: desktop.AXSnapshotResult{
			PID:       123,
			NodeCount: 1,
			Root:      desktop.AXNode{ID: "ax:0", Role: "AXApplication"},
		},
	}
	_, err := NewDesktopAXSnapshot(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "max_depth": 99, "max_nodes": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.axRequest.MaxDepth != maxDesktopAXDepth || driver.axRequest.MaxNodes != maxDesktopAXNodes {
		t.Fatalf("request = %#v", driver.axRequest)
	}
}

func TestDesktopOCRReadsWorkspaceImageAndMapsRunnerError_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "desktop.png")
	if err := os.WriteFile(imagePath, []byte("fake image"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeOCRRunner{
		result: vision.OCRResult{
			Status: "success",
			Width:  800,
			Height: 600,
			Lines:  []vision.OCRLine{{Index: 1, Text: "设置", Confidence: 0.99, BBox: []int{1, 2, 3, 4}}},
		},
	}
	outcome, err := NewDesktopOCRWithRunner(workspace, runner).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"image_path": "desktop.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.request.ImagePath != imagePath {
		t.Fatalf("OCR path = %q, want %q", runner.request.ImagePath, imagePath)
	}
	data := outcome.Data.(map[string]any)
	if data["coordinate_space"] != desktop.CoordinateSpaceScreenshotLocal {
		t.Fatalf("coordinate space = %#v", data["coordinate_space"])
	}

	runner.err = &vision.ToolError{Code: "browser_ocr_dependency_missing", Message: "missing", Hint: "install"}
	outcome, err = NewDesktopOCRWithRunner(workspace, runner).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"image_path": "desktop.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_ocr_dependency_missing" {
		t.Fatalf("code = %q, want desktop_ocr_dependency_missing", toolErr.Code)
	}
}

func TestDesktopOCRRejectsOutsideWorkspacePath_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	outcome, err := NewDesktopOCRWithRunner(workspace, &fakeOCRRunner{}).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"image_path": filepath.Join(t.TempDir(), "outside.png")},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_ocr_path_outside_workspace" {
		t.Fatalf("code = %q, want desktop_ocr_path_outside_workspace", toolErr.Code)
	}
}

func TestDesktopAXPressExecutesReversibleActionAndVerifiesTreeChange_BitsUT(t *testing.T) {
	enabled := true
	before := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXButton",
				Title:       "展开侧边栏",
				Description: "显示导航",
				Enabled:     &enabled,
				Actions:     []string{"AXPress"},
			}},
		},
	}
	after := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXButton",
				Title:       "收起侧边栏",
				Description: "显示导航",
				Enabled:     &enabled,
				Actions:     []string{"AXPress"},
			}},
		},
	}
	driver := &fakeDesktopDriver{
		axSnapshots: []desktop.AXSnapshotResult{before, after},
		axPress:     desktop.AXPressResult{PID: 123, NodeID: "ax:0/0", Action: "AXPress", Performed: true},
	}
	tool := NewDesktopAXPress(driver, NewConfirmationStore())
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXButton", "展开侧边栏", "显示导航", "展开导航栏"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if driver.axPressRequest.NodeID != "ax:0/0" || driver.axPressRequest.ExpectedTitle != "展开侧边栏" {
		t.Fatalf("press request = %#v", driver.axPressRequest)
	}
}

func TestDesktopAXPressRequiresBoundOneTimeConfirmation_BitsUT(t *testing.T) {
	enabled := true
	before := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXButton",
				Title:       "发送",
				Description: "发送消息",
				Enabled:     &enabled,
				Actions:     []string{"AXPress"},
			}},
		},
	}
	after := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{
				before.Root.Children[0],
				{ID: "ax:0/1", Role: "AXStaticText", Title: "已发送"},
			},
		},
	}
	driver := &fakeDesktopDriver{
		axSnapshots: []desktop.AXSnapshotResult{before, before, after},
		axPress:     desktop.AXPressResult{PID: 123, NodeID: "ax:0/0", Action: "AXPress", Performed: true},
	}
	store := NewConfirmationStore()
	tool := NewDesktopAXPress(driver, store)
	args := desktopAXPressArgs(123, "ax:0/0", "AXButton", "发送", "发送消息", "向项目群发送已确认的更新")

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.axPressRequest.NodeID != "" {
		t.Fatalf("outcome = %#v, press request = %#v", required, driver.axPressRequest)
	}

	token, err := store.Issue(ActionApproval{
		Operation: desktopAXPressOperation,
		PID:       123,
		NodeID:    "ax:0/0",
		Reason:    "向项目群发送已确认的更新",
	})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal {
		t.Fatalf("outcome = %#v", data)
	}

	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	reused := outcome.Data.(map[string]any)
	if reused["code"] != "desktop_action_confirmation_required" {
		t.Fatalf("reused token outcome = %#v", reused)
	}
}

func TestDesktopAXPressRefusesHighRiskAction_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:      "ax:0/0",
				Role:    "AXButton",
				Title:   "删除项目",
				Enabled: &enabled,
				Actions: []string{"AXPress"},
			}},
		},
	}
	driver := &fakeDesktopDriver{axSnapshots: []desktop.AXSnapshotResult{snapshot}}
	outcome, err := NewDesktopAXPress(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXButton", "删除项目", "", "删除项目"),
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_action_high_risk_refused" || driver.axPressRequest.NodeID != "" {
		t.Fatalf("error = %#v, press request = %#v", toolErr, driver.axPressRequest)
	}
}

func TestDesktopAXPressRejectsStaleNode_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:      "ax:0/0",
				Role:    "AXButton",
				Title:   "展开",
				Enabled: &enabled,
				Actions: []string{"AXPress"},
			}},
		},
	}
	driver := &fakeDesktopDriver{axSnapshots: []desktop.AXSnapshotResult{snapshot}}
	outcome, err := NewDesktopAXPress(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXButton", "旧标题", "", "展开面板"),
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_ax_node_stale" {
		t.Fatalf("code = %q, want desktop_ax_node_stale", toolErr.Code)
	}
}

func TestDesktopAXFocusFocusesEditableNode_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXTextArea",
				Title:       "输入",
				Description: "消息输入框",
				Enabled:     &enabled,
			}},
		},
	}
	driver := &fakeDesktopDriver{
		axSnapshots: []desktop.AXSnapshotResult{snapshot},
		axFocus: desktop.AXFocusResult{
			PID:              123,
			NodeID:           "ax:0/0",
			Action:           "AXFocus",
			Performed:        true,
			ActiveBefore:     true,
			ActiveAfter:      true,
			Focused:          true,
			FocusRole:        "AXTextArea",
			FocusTitle:       "输入",
			FocusDescription: "消息输入框",
		},
	}
	outcome, err := NewDesktopAXFocus(driver).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXTextArea", "输入", "消息输入框", "聚焦微信消息输入框"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.axFocusRequest.NodeID != "ax:0/0" || driver.axFocusRequest.ExpectedRole != "AXTextArea" {
		t.Fatalf("focus request = %#v", driver.axFocusRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["focused"] != true {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopAXFocusRejectsNonEditableNode_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:      "ax:0/0",
				Role:    "AXButton",
				Title:   "发送",
				Enabled: &enabled,
			}},
		},
	}
	driver := &fakeDesktopDriver{axSnapshots: []desktop.AXSnapshotResult{snapshot}}
	outcome, err := NewDesktopAXFocus(driver).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXButton", "发送", "", "聚焦按钮"),
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_ax_focus_not_editable" || driver.axFocusRequest.NodeID != "" {
		t.Fatalf("error = %#v, focus request = %#v", toolErr, driver.axFocusRequest)
	}
}

func TestDesktopClickClicksEditableNodeWithoutConfirmation_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXTextArea",
				Title:       "输入",
				Description: "消息输入框",
				Enabled:     &enabled,
				Bounds:      desktop.Bounds{X: 10, Y: 20, Width: 300, Height: 80},
			}},
		},
	}
	driver := &fakeDesktopDriver{
		axSnapshots: []desktop.AXSnapshotResult{snapshot},
		click: desktop.ClickResult{
			PID:             123,
			NodeID:          "ax:0/0",
			Action:          "Click",
			Performed:       true,
			ActiveBefore:    true,
			ActiveAfter:     true,
			X:               160,
			Y:               60,
			CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		},
	}
	outcome, err := NewDesktopClick(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXTextArea", "输入", "消息输入框", "聚焦微信消息输入框"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.clickRequest.NodeID != "ax:0/0" || driver.clickRequest.ExpectedTitle != "输入" {
		t.Fatalf("click request = %#v", driver.clickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["risk"] != desktopRiskReversible {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopClickRequiresConfirmationForSendNode_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:          "ax:0/0",
				Role:        "AXButton",
				Title:       "发送",
				Description: "发送消息",
				Enabled:     &enabled,
				Bounds:      desktop.Bounds{X: 10, Y: 20, Width: 80, Height: 40},
			}},
		},
	}
	driver := &fakeDesktopDriver{
		axSnapshots: []desktop.AXSnapshotResult{snapshot},
		click: desktop.ClickResult{
			PID:             123,
			NodeID:          "ax:0/0",
			Action:          "Click",
			Performed:       true,
			ActiveBefore:    true,
			ActiveAfter:     true,
			X:               50,
			Y:               40,
			CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		},
	}
	store := NewConfirmationStore()
	tool := NewDesktopClick(driver, store)
	args := desktopAXPressArgs(123, "ax:0/0", "AXButton", "发送", "发送消息", "点击发送已确认消息")

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.clickRequest.NodeID != "" {
		t.Fatalf("outcome = %#v, click request = %#v", required, driver.clickRequest)
	}

	token, err := store.Issue(ActionApproval{
		Operation: desktopClickOperation,
		PID:       123,
		NodeID:    "ax:0/0",
		Reason:    "点击发送已确认消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopClickRefusesHighRiskNode_BitsUT(t *testing.T) {
	enabled := true
	snapshot := desktop.AXSnapshotResult{
		PID: 123,
		Root: desktop.AXNode{
			ID:   "ax:0",
			Role: "AXApplication",
			Children: []desktop.AXNode{{
				ID:      "ax:0/0",
				Role:    "AXButton",
				Title:   "删除好友",
				Enabled: &enabled,
				Bounds:  desktop.Bounds{X: 10, Y: 20, Width: 80, Height: 40},
			}},
		},
	}
	driver := &fakeDesktopDriver{axSnapshots: []desktop.AXSnapshotResult{snapshot}}
	outcome, err := NewDesktopClick(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: desktopAXPressArgs(123, "ax:0/0", "AXButton", "删除好友", "", "删除好友"),
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_action_high_risk_refused" || driver.clickRequest.NodeID != "" {
		t.Fatalf("error = %#v, click request = %#v", toolErr, driver.clickRequest)
	}
}

func TestDesktopVisualClickMapsScreenshotBBoxAndActivates_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "12",
		Width:                 200,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 100, Y: 200, Width: 400, Height: 200},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	driver := &fakeDesktopDriver{
		visualClick: desktop.VisualClickResult{
			PID:             123,
			Action:          "VisualClick",
			Performed:       true,
			ActiveBefore:    true,
			ActiveAfter:     true,
			X:               200,
			Y:               260,
			CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		},
	}
	outcome, err := NewDesktopVisualClick(driver, NewConfirmationStore(), workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":           123,
			"image_path":    imagePath,
			"manifest_path": manifestPath,
			"bbox":          []any{40, 20, 60, 40},
			"expected_text": "发消息...",
			"reason":        "聚焦消息输入框",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.activateRequest.PID != 123 {
		t.Fatalf("activate request = %#v", driver.activateRequest)
	}
	if driver.visualClickRequest.X != 200 || driver.visualClickRequest.Y != 260 || driver.visualClickRequest.CoordinateSpace != desktop.CoordinateSpaceScreenPhysical {
		t.Fatalf("visual click request = %#v", driver.visualClickRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if token, ok := data["visual_focus_token"].(string); !ok || token == "" {
		t.Fatalf("expected visual focus token for input bbox: %#v", data)
	}
}

func TestDesktopVisualClickSearchResultDoesNotIssueVisualFocusToken_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "12",
		Width:                 200,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 100, Y: 200, Width: 400, Height: 200},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	driver := &fakeDesktopDriver{
		visualClick: desktop.VisualClickResult{
			PID:             123,
			Action:          "VisualClick",
			Performed:       true,
			ActiveBefore:    true,
			ActiveAfter:     true,
			X:               200,
			Y:               260,
			CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		},
	}
	outcome, err := NewDesktopVisualClick(driver, NewConfirmationStore(), workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":           123,
			"image_path":    imagePath,
			"manifest_path": manifestPath,
			"bbox":          []any{40, 20, 60, 40},
			"expected_text": "梁亚琦",
			"reason":        "点击搜索结果中的「梁亚琦」打开对话窗口",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["visual_focus_token"]; exists {
		t.Fatalf("search result click must not issue visual focus token: %#v", data)
	}
}

func TestDesktopVisualClickRequiresConfirmationForSendText_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "12",
		Width:                 100,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 0, Y: 0, Width: 100, Height: 100},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	driver := &fakeDesktopDriver{
		visualClick: desktop.VisualClickResult{
			PID:             123,
			Action:          "VisualClick",
			Performed:       true,
			ActiveBefore:    true,
			ActiveAfter:     true,
			X:               20,
			Y:               20,
			CoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		},
	}
	store := NewConfirmationStore()
	tool := NewDesktopVisualClick(driver, store, workspace)
	args := map[string]any{
		"pid":           123,
		"image_path":    imagePath,
		"manifest_path": manifestPath,
		"bbox":          []any{10, 10, 30, 30},
		"expected_text": "发送",
		"reason":        "点击发送已确认消息",
	}
	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.visualClickRequest.PID != 0 {
		t.Fatalf("outcome = %#v, request = %#v", required, driver.visualClickRequest)
	}
	approval := required["approval_request"].(map[string]any)
	if approval["bbox"] != "10,10,30,30" || approval["image_path"] != imagePath {
		t.Fatalf("approval = %#v", approval)
	}
	token, err := store.Issue(ActionApproval{
		Operation: desktopVisualClickOperation,
		PID:       123,
		ImagePath: imagePath,
		BBox:      "10,10,30,30",
		Reason:    "点击发送已确认消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopVisualClickRejectsBBoxOutsideManifest_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	imagePath, manifestPath := writeDesktopVisualFixture(t, workspace, desktopScreenshotManifest{
		Version:               1,
		PID:                   123,
		WindowID:              "12",
		Width:                 100,
		Height:                100,
		WindowBounds:          desktop.Bounds{X: 0, Y: 0, Width: 100, Height: 100},
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
	})
	driver := &fakeDesktopDriver{}
	outcome, err := NewDesktopVisualClick(driver, NewConfirmationStore(), workspace).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":           123,
			"image_path":    imagePath,
			"manifest_path": manifestPath,
			"bbox":          []any{90, 90, 120, 120},
			"expected_text": "发消息...",
			"reason":        "聚焦输入框",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_visual_click_bbox_outside_image" || driver.visualClickRequest.PID != 0 {
		t.Fatalf("error = %#v, request = %#v", toolErr, driver.visualClickRequest)
	}
}

func TestDesktopTypeTextUsesVisualFocusTokenAfterAXFocusRejection_BitsUT(t *testing.T) {
	visualFocuses := NewVisualFocusStore()
	token, _, err := visualFocuses.Issue(VisualFocusGrant{
		PID:       123,
		ImagePath: "/tmp/visual.png",
		BBox:      "10,10,30,30",
		Reason:    "聚焦消息输入框",
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDesktopDriver{
		typeTextErrs: []error{
			&desktop.ToolError{
				Code:    "desktop_type_text_focus_not_editable",
				Message: "focused UI element is not editable: role='AXLink'",
				Hint:    "请先聚焦输入框。",
			},
			nil,
		},
		typeTexts: []desktop.TypeTextResult{
			{},
			{
				PID:               123,
				Action:            "TypeText",
				Performed:         true,
				ActiveBefore:      true,
				ActiveAfter:       true,
				TextLength:        2,
				LineCount:         1,
				FocusVerification: "visual_token",
			},
		},
	}
	outcome, err := NewDesktopTypeText(driver, visualFocuses).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":                123,
			"text":               "你好",
			"reason":             "在 WebView 输入框中起草消息",
			"visual_focus_token": token,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(driver.typeTextRequests) != 2 {
		t.Fatalf("type text requests = %#v", driver.typeTextRequests)
	}
	if driver.typeTextRequests[0].AllowVisualFocus {
		t.Fatalf("first request should require AX focus: %#v", driver.typeTextRequests[0])
	}
	if !driver.typeTextRequests[1].AllowVisualFocus {
		t.Fatalf("second request should use visual focus fallback: %#v", driver.typeTextRequests[1])
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["visual_focus_used"] != true || data["focus_verification"] != "visual_token" {
		t.Fatalf("outcome = %#v", data)
	}
	if _, ok := visualFocuses.Consume(token, 123); ok {
		t.Fatal("visual focus token was reusable")
	}
}

func TestDesktopTypeTextRejectsInvalidVisualFocusToken_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		typeTextErrs: []error{
			&desktop.ToolError{
				Code:    "desktop_type_text_focus_unavailable",
				Message: "Accessibility did not return a focused UI element",
				Hint:    "请先聚焦输入框。",
			},
		},
	}
	outcome, err := NewDesktopTypeText(driver, NewVisualFocusStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":                123,
			"text":               "你好",
			"reason":             "在 WebView 输入框中起草消息",
			"visual_focus_token": "invalid-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_type_text_visual_focus_token_invalid" {
		t.Fatalf("error = %#v", toolErr)
	}
	if len(driver.typeTextRequests) != 1 || driver.typeTextRequests[0].AllowVisualFocus {
		t.Fatalf("unexpected retry requests = %#v", driver.typeTextRequests)
	}
}

func TestConfirmationStoreConsumesOnlyExactActionOnce_BitsUT(t *testing.T) {
	store := NewConfirmationStore()
	approval := ActionApproval{Operation: desktopAXPressOperation, PID: 123, NodeID: "ax:0/0", Reason: "发送更新"}
	token, err := store.Issue(approval)
	if err != nil {
		t.Fatal(err)
	}
	if store.Consume(token, ActionApproval{Operation: desktopAXPressOperation, PID: 123, NodeID: "ax:0/1", Reason: "发送更新"}) {
		t.Fatal("mismatched approval unexpectedly consumed")
	}
	if store.Consume(token, approval) {
		t.Fatal("mismatched consume must invalidate token")
	}
	token, err = store.Issue(approval)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Consume(token, approval) {
		t.Fatal("matching approval did not consume")
	}
	if store.Consume(token, approval) {
		t.Fatal("token was reusable")
	}
}

func TestDesktopPressKeyRunsLowRiskKey_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		pressKey: desktop.PressKeyResult{
			PID:          123,
			Key:          "Escape",
			Action:       "PressKey",
			Performed:    true,
			ActiveBefore: true,
			ActiveAfter:  true,
		},
	}
	outcome, err := NewDesktopPressKey(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "key": "Esc", "reason": "关闭当前浮层"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.pressKeyRequest.Key != "Escape" {
		t.Fatalf("press key request = %#v", driver.pressKeyRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true || data["risk"] != desktopRiskReversible {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopPressKeyOpenSelectedResultEnterIsLowRisk_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		pressKey: desktop.PressKeyResult{
			PID:          123,
			Key:          "Enter",
			Action:       "PressKey",
			Performed:    true,
			ActiveBefore: true,
			ActiveAfter:  true,
		},
	}
	outcome, err := NewDesktopPressKey(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{
			"pid":    123,
			"key":    "Enter",
			"intent": "open_selected_result",
			"reason": "确认选择搜索结果李洋，打开聊天窗口",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.pressKeyRequest.Key != "Enter" {
		t.Fatalf("press key request = %#v", driver.pressKeyRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskReversible || data["intent"] != "open_selected_result" {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopPressKeyRequiresConfirmationForEnter_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		pressKey: desktop.PressKeyResult{
			PID:          123,
			Key:          "Cmd+Enter",
			Action:       "PressKey",
			Performed:    true,
			ActiveBefore: true,
			ActiveAfter:  true,
		},
	}
	store := NewConfirmationStore()
	tool := NewDesktopPressKey(driver, store)
	args := map[string]any{"pid": 123, "key": "Meta+Return", "reason": "发送已确认消息"}

	outcome, err := tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	required := outcome.Data.(map[string]any)
	if required["code"] != "desktop_action_confirmation_required" || driver.pressKeyRequest.Key != "" {
		t.Fatalf("outcome = %#v, press request = %#v", required, driver.pressKeyRequest)
	}

	token, err := store.Issue(ActionApproval{
		Operation: desktopPressKeyOperation,
		PID:       123,
		Key:       "Cmd+Enter",
		Reason:    "发送已确认消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	args["confirmation_token"] = token
	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["risk"] != desktopRiskExternal {
		t.Fatalf("outcome = %#v", data)
	}

	outcome, err = tool.Run(context.Background(), agent.ToolCallContext{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	reused := outcome.Data.(map[string]any)
	if reused["code"] != "desktop_action_confirmation_required" {
		t.Fatalf("reused token outcome = %#v", reused)
	}
}

func TestDesktopPressKeyRejectsUnsupportedKey_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{}
	outcome, err := NewDesktopPressKey(driver, NewConfirmationStore()).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "key": "Cmd+Q", "reason": "退出应用"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_press_key_unsupported" || driver.pressKeyRequest.Key != "" {
		t.Fatalf("error = %#v, press request = %#v", toolErr, driver.pressKeyRequest)
	}
}

func TestDesktopTypeTextDraftsWithoutEchoingContent_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{
		typeText: desktop.TypeTextResult{
			PID:              123,
			Action:           "TypeText",
			Performed:        true,
			ActiveBefore:     true,
			ActiveAfter:      true,
			TextLength:       5,
			LineCount:        1,
			FocusRole:        "AXTextArea",
			FocusTitle:       "输入",
			FocusDescription: "消息输入框",
		},
	}
	outcome, err := NewDesktopTypeText(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "text": "hello", "reason": "起草微信回复"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.typeTextRequest.PID != 123 || driver.typeTextRequest.Text != "hello" {
		t.Fatalf("type text request = %#v", driver.typeTextRequest)
	}
	data := outcome.Data.(map[string]any)
	if data["status"] != agent.ToolStatusSuccess || data["verified"] != true {
		t.Fatalf("outcome = %#v", data)
	}
	if _, exists := data["text"]; exists {
		t.Fatalf("tool result must not echo text: %#v", data)
	}
	if data["content_returned"] != false || data["text_length"] != 5 {
		t.Fatalf("outcome = %#v", data)
	}
}

func TestDesktopTypeTextRejectsEmptyAndTooLongText_BitsUT(t *testing.T) {
	driver := &fakeDesktopDriver{}
	outcome, err := NewDesktopTypeText(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "text": "", "reason": "空输入"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr := outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_type_text_bad_request" || driver.typeTextRequest.Text != "" {
		t.Fatalf("error = %#v, request = %#v", toolErr, driver.typeTextRequest)
	}

	longText := strings.Repeat("好", maxDesktopTypeTextRunes+1)
	outcome, err = NewDesktopTypeText(driver).Run(context.Background(), agent.ToolCallContext{
		Args: map[string]any{"pid": 123, "text": longText, "reason": "超长输入"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolErr = outcome.Data.(agent.ToolErrorData)
	if toolErr.Code != "desktop_type_text_too_long" || driver.typeTextRequest.Text != "" {
		t.Fatalf("error = %#v, request = %#v", toolErr, driver.typeTextRequest)
	}
}

func desktopAXPressArgs(pid int, nodeID string, role string, title string, description string, reason string) map[string]any {
	return map[string]any{
		"pid":                  pid,
		"node_id":              nodeID,
		"expected_role":        role,
		"expected_title":       title,
		"expected_description": description,
		"reason":               reason,
	}
}

func writeDesktopVisualFixture(t *testing.T, workspace string, manifest desktopScreenshotManifest) (string, string) {
	t.Helper()
	imagePath := filepath.Join(workspace, "visual.png")
	manifestPath := filepath.Join(workspace, "visual.json")
	if err := os.WriteFile(imagePath, []byte("fake desktop image"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest.ImagePath = imagePath
	if err := writeDesktopScreenshotManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	return imagePath, manifestPath
}
