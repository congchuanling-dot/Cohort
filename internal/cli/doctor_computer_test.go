package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cohort/internal/app"
	"cohort/internal/browser"
	"cohort/internal/desktop"
	"cohort/internal/vision"
)

func TestRunComputerDoctorCommandPassesWithFakes_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	deps := fakeComputerDoctorDeps(t, workspace)
	trueValue := true
	falseValue := false
	deps.Desktop = &fakeComputerDesktopDoctor{
		permissions: desktop.PermissionsResult{
			Platform:        "darwin",
			Accessibility:   &trueValue,
			ScreenRecording: &trueValue,
			InputMonitoring: &falseValue,
		},
		windows: desktop.ListWindowsResult{Windows: []desktop.Window{{PID: 1, AppName: "Finder"}}},
	}
	deps.OCR = &fakeComputerOCRRunner{result: vision.OCRResult{Status: "success", Width: 64, Height: 32}}
	deps.Browser = &fakeComputerBrowserDoctor{tabs: []browser.Tab{{ID: "1", Title: "Example"}}}

	var out bytes.Buffer
	err := runComputerDoctorCommandWithDeps(context.Background(), computerDoctorOptions{}, app.Config{Workspace: workspace}, nil, deps, &out)
	if err != nil {
		t.Fatalf("runComputerDoctorCommandWithDeps error = %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"doctor computer:",
		"[pass] computer.platform",
		"[pass] desktop.permissions.accessibility",
		"[pass] ocr.dependencies",
		"[pass] browser.bridge.connection",
		"doctor computer: ok",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestRunComputerDoctorCommandFailsMissingRequiredPermission_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	deps := fakeComputerDoctorDeps(t, workspace)
	falseValue := false
	deps.Desktop = &fakeComputerDesktopDoctor{
		permissions: desktop.PermissionsResult{
			Platform:        "darwin",
			Accessibility:   &falseValue,
			ScreenRecording: &falseValue,
		},
	}
	deps.OCR = &fakeComputerOCRRunner{result: vision.OCRResult{Status: "success", Width: 64, Height: 32}}
	deps.Browser = &fakeComputerBrowserDoctor{err: browser.ErrNotConnected}

	var out bytes.Buffer
	err := runComputerDoctorCommandWithDeps(context.Background(), computerDoctorOptions{}, app.Config{Workspace: workspace}, nil, deps, &out)
	if err == nil {
		t.Fatalf("runComputerDoctorCommandWithDeps error = nil, want permission failure\n%s", out.String())
	}
	for _, want := range []string{
		"[fail] desktop.permissions.accessibility",
		"[fail] desktop.permissions.screen_recording",
		"[warn] browser.bridge.connection",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestRunComputerDoctorCommandFailsMissingHelper_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	deps := fakeComputerDoctorDeps(t, workspace)
	deps.DesktopScriptPath = filepath.Join(workspace, "missing-desktop.py")
	deps.OCRScriptPath = filepath.Join(workspace, "missing-ocr.py")
	deps.Desktop = &fakeComputerDesktopDoctor{}
	deps.OCR = &fakeComputerOCRRunner{}
	deps.Browser = &fakeComputerBrowserDoctor{err: browser.ErrNotConnected}

	var out bytes.Buffer
	err := runComputerDoctorCommandWithDeps(context.Background(), computerDoctorOptions{}, app.Config{Workspace: workspace}, nil, deps, &out)
	if err == nil {
		t.Fatalf("runComputerDoctorCommandWithDeps error = nil, want helper failure\n%s", out.String())
	}
	for _, want := range []string{"[fail] desktop.helper", "[fail] ocr.helper"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestRunComputerDoctorCommandRunsSmokeApp_BitsUT(t *testing.T) {
	workspace := t.TempDir()
	deps := fakeComputerDoctorDeps(t, workspace)
	trueValue := true
	deps.Desktop = &fakeComputerDesktopDoctor{
		permissions: desktop.PermissionsResult{
			Platform:        "darwin",
			Accessibility:   &trueValue,
			ScreenRecording: &trueValue,
		},
		windows: desktop.ListWindowsResult{Windows: []desktop.Window{{
			WindowID:  "w1",
			PID:       42,
			AppName:   "TextEdit",
			Title:     "Untitled",
			Bounds:    desktop.Bounds{X: 0, Y: 0, Width: 640, Height: 480},
			IsVisible: true,
			IsActive:  true,
		}}},
		activate:   desktop.ActivateResult{PID: 42, Active: true, Verified: true},
		screenshot: desktop.ScreenshotResult{PID: 42, WindowID: "w1", Width: 640, Height: 480},
		axSnapshot: desktop.AXSnapshotResult{PID: 42, NodeCount: 7, Root: desktop.AXNode{ID: "ax:0", Role: "AXApplication"}},
	}
	deps.OCR = &fakeComputerOCRRunner{result: vision.OCRResult{Status: "success", Width: 640, Height: 480, Lines: []vision.OCRLine{{Text: "Untitled"}}}}
	deps.Browser = &fakeComputerBrowserDoctor{tabs: []browser.Tab{{ID: "1", Title: "Example"}}}

	var out bytes.Buffer
	err := runComputerDoctorCommandWithDeps(context.Background(), computerDoctorOptions{SmokeApp: "TextEdit"}, app.Config{Workspace: workspace}, nil, deps, &out)
	if err != nil {
		t.Fatalf("runComputerDoctorCommandWithDeps error = %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"[pass] computer.smoke_app.window",
		"[pass] computer.smoke_app.activate",
		"[pass] computer.smoke_app.screenshot",
		"[pass] computer.smoke_app.ax_snapshot",
		"[pass] computer.smoke_app.ocr",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestParseComputerDoctorArgsAcceptsSmokeApp_BitsUT(t *testing.T) {
	opts, err := parseComputerDoctorArgs([]string{"--smoke-app", "TextEdit"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.SmokeApp != "TextEdit" {
		t.Fatalf("SmokeApp = %q", opts.SmokeApp)
	}

	opts, err = parseComputerDoctorArgs([]string{"--smoke-app=WeChat"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.SmokeApp != "WeChat" {
		t.Fatalf("SmokeApp = %q", opts.SmokeApp)
	}
}

func TestParseComputerDoctorArgsRejectsUnknown_BitsUT(t *testing.T) {
	_, err := parseComputerDoctorArgs([]string{"--smoke"})
	if err == nil {
		t.Fatal("parseComputerDoctorArgs error = nil, want unknown option")
	}
}

func fakeComputerDoctorDeps(t *testing.T, workspace string) computerDoctorDeps {
	t.Helper()
	scriptsDir := filepath.Join(workspace, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	desktopScript := filepath.Join(scriptsDir, "desktop_darwin.py")
	ocrScript := filepath.Join(scriptsDir, "browser_ocr.py")
	for _, path := range []string{desktopScript, ocrScript} {
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return computerDoctorDeps{
		GOOS:              "darwin",
		Workspace:         workspace,
		DesktopScriptPath: desktopScript,
		OCRScriptPath:     ocrScript,
	}
}

type fakeComputerDesktopDoctor struct {
	permissions desktop.PermissionsResult
	windows     desktop.ListWindowsResult
	activate    desktop.ActivateResult
	screenshot  desktop.ScreenshotResult
	axSnapshot  desktop.AXSnapshotResult
	err         error
	windowsErr  error
}

func (f *fakeComputerDesktopDoctor) Permissions(ctx context.Context) (desktop.PermissionsResult, error) {
	if f.err != nil {
		return desktop.PermissionsResult{}, f.err
	}
	return f.permissions, nil
}

func (f *fakeComputerDesktopDoctor) ListWindows(ctx context.Context, req desktop.ListWindowsRequest) (desktop.ListWindowsResult, error) {
	if f.windowsErr != nil {
		return desktop.ListWindowsResult{}, f.windowsErr
	}
	return f.windows, nil
}

func (f *fakeComputerDesktopDoctor) Activate(ctx context.Context, req desktop.ActivateRequest) (desktop.ActivateResult, error) {
	if f.err != nil {
		return desktop.ActivateResult{}, f.err
	}
	if f.activate.PID == 0 {
		f.activate.PID = req.PID
	}
	return f.activate, nil
}

func (f *fakeComputerDesktopDoctor) Screenshot(ctx context.Context, req desktop.ScreenshotRequest) (desktop.ScreenshotResult, error) {
	if f.err != nil {
		return desktop.ScreenshotResult{}, f.err
	}
	if err := os.WriteFile(req.OutputPath, []byte("fake screenshot"), 0644); err != nil {
		return desktop.ScreenshotResult{}, err
	}
	if f.screenshot.PID == 0 {
		f.screenshot.PID = req.PID
	}
	if f.screenshot.WindowID == "" {
		f.screenshot.WindowID = req.WindowID
	}
	return f.screenshot, nil
}

func (f *fakeComputerDesktopDoctor) AXSnapshot(ctx context.Context, req desktop.AXSnapshotRequest) (desktop.AXSnapshotResult, error) {
	if f.err != nil {
		return desktop.AXSnapshotResult{}, f.err
	}
	if f.axSnapshot.PID == 0 {
		f.axSnapshot.PID = req.PID
	}
	return f.axSnapshot, nil
}

type fakeComputerOCRRunner struct {
	result vision.OCRResult
	err    error
}

func (f *fakeComputerOCRRunner) Run(ctx context.Context, request vision.OCRRequest) (vision.OCRResult, error) {
	if f.err != nil {
		return vision.OCRResult{}, f.err
	}
	if strings.TrimSpace(request.ImagePath) == "" {
		return vision.OCRResult{}, errors.New("missing image")
	}
	return f.result, nil
}

type fakeComputerBrowserDoctor struct {
	tabs []browser.Tab
	err  error
}

func (f *fakeComputerBrowserDoctor) Tabs(ctx context.Context) ([]browser.Tab, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tabs, nil
}
