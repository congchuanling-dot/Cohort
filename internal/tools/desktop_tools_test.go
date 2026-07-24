package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cohert/internal/agent"
	"cohert/internal/desktop"
	"cohert/internal/vision"
)

type fakeDesktopDriver struct {
	permissions desktop.PermissionsResult
	windows     desktop.ListWindowsResult
	activate    desktop.ActivateResult
	screenshot  desktop.ScreenshotResult
	axSnapshot  desktop.AXSnapshotResult
	axSnapshots []desktop.AXSnapshotResult
	axPress     desktop.AXPressResult
	err         error

	listRequest       desktop.ListWindowsRequest
	activateRequest   desktop.ActivateRequest
	screenshotRequest desktop.ScreenshotRequest
	axRequest         desktop.AXSnapshotRequest
	axPressRequest    desktop.AXPressRequest
	axSnapshotCalls   int
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
	if _, err := os.Stat(driver.screenshotRequest.OutputPath); err != nil {
		t.Fatalf("expected screenshot: %v", err)
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
