package cli

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cohort/internal/app"
	"cohort/internal/browser"
	"cohort/internal/desktop"
	"cohort/internal/vision"
)

type computerDoctorOptions struct {
	SmokeApp string
}

type computerDesktopDoctor interface {
	Permissions(ctx context.Context) (desktop.PermissionsResult, error)
	ListWindows(ctx context.Context, req desktop.ListWindowsRequest) (desktop.ListWindowsResult, error)
	Activate(ctx context.Context, req desktop.ActivateRequest) (desktop.ActivateResult, error)
	Screenshot(ctx context.Context, req desktop.ScreenshotRequest) (desktop.ScreenshotResult, error)
	AXSnapshot(ctx context.Context, req desktop.AXSnapshotRequest) (desktop.AXSnapshotResult, error)
}

type computerBrowserDoctor interface {
	Tabs(ctx context.Context) ([]browser.Tab, error)
}

type computerDoctorDeps struct {
	GOOS              string
	Workspace         string
	DesktopScriptPath string
	OCRScriptPath     string
	Desktop           computerDesktopDoctor
	OCR               vision.OCRRunner
	Browser           computerBrowserDoctor
	BrowserStartErr   error
	BrowserClose      func(context.Context) error
}

func runComputerDoctorCommand(ctx context.Context, args []string, cfg app.Config, loadErr error, out io.Writer) error {
	opts, err := parseComputerDoctorArgs(args)
	if err != nil {
		return err
	}
	deps := newComputerDoctorDeps(cfg)
	defer closeComputerDoctorDeps(context.Background(), deps)
	return runComputerDoctorCommandWithDeps(ctx, opts, cfg, loadErr, deps, out)
}

func parseComputerDoctorArgs(args []string) (computerDoctorOptions, error) {
	var opts computerDoctorOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			return opts, errors.New("usage: cohort doctor computer [--smoke-app <app_name>]")
		case "--smoke-app":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--smoke-app requires a non-empty app name")
			}
			opts.SmokeApp = strings.TrimSpace(args[i])
		default:
			if value, ok := strings.CutPrefix(arg, "--smoke-app="); ok {
				value = strings.TrimSpace(value)
				if value == "" {
					return opts, errors.New("--smoke-app requires a non-empty app name")
				}
				opts.SmokeApp = value
				continue
			}
			return opts, fmt.Errorf("unknown doctor computer option %q", arg)
		}
	}
	return opts, nil
}

func newComputerDoctorDeps(cfg app.Config) computerDoctorDeps {
	workspace := strings.TrimSpace(cfg.Workspace)
	if workspace == "" {
		workspace = "."
	}
	desktopScript := app.ResolveRuntimeScriptPath(workspace, app.DesktopDarwinHelperPath)
	ocrScript := app.ResolveRuntimeScriptPath(workspace, app.BrowserOCRHelperPath)

	bridge := browser.NewBridge(browser.DefaultListenAddr, browser.DefaultPath)
	startErr := bridge.Start()
	var browserClient computerBrowserDoctor = bridge
	var closeFunc func(context.Context) error = bridge.Close
	if startErr != nil {
		browserClient = nil
		closeFunc = nil
	}

	return computerDoctorDeps{
		GOOS:              runtime.GOOS,
		Workspace:         workspace,
		DesktopScriptPath: desktopScript,
		OCRScriptPath:     ocrScript,
		Desktop:           desktop.NewPythonDriver("python3", desktopScript, desktop.DefaultTimeout),
		OCR:               vision.NewPythonOCRRunner("python3", ocrScript, vision.DefaultOCRTimeout),
		Browser:           browserClient,
		BrowserStartErr:   startErr,
		BrowserClose:      closeFunc,
	}
}

func closeComputerDoctorDeps(ctx context.Context, deps computerDoctorDeps) {
	if deps.BrowserClose == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = deps.BrowserClose(closeCtx)
}

func runComputerDoctorCommandWithDeps(ctx context.Context, opts computerDoctorOptions, cfg app.Config, loadErr error, deps computerDoctorDeps, out io.Writer) error {
	summary := &doctorSummary{}
	fmt.Fprintln(out, "doctor computer:")

	if loadErr != nil {
		summary.fail(out, "config.parse", loadErr.Error())
	} else {
		checkWritableDir(out, summary, "workspace", cfg.Workspace)
	}
	checkWritableDir(out, summary, "computer.artifacts", filepath.Join(firstNonEmpty(deps.Workspace, cfg.Workspace, "."), ".cohort", "doctor"))

	checkComputerPlatform(out, summary, deps.GOOS)
	checkDesktopHelper(ctx, out, summary, deps)
	checkComputerSmokeApp(ctx, out, summary, deps, opts.SmokeApp)
	checkOCRHelper(ctx, out, summary, deps)
	checkBrowserBridge(ctx, out, summary, deps)

	if summary.failures > 0 {
		return fmt.Errorf("doctor computer found %d failure(s)", summary.failures)
	}
	fmt.Fprintf(out, "doctor computer: ok (%d warning(s))\n", summary.warnings)
	return nil
}

func checkComputerPlatform(out io.Writer, summary *doctorSummary, goos string) {
	if goos == "darwin" {
		summary.pass(out, "computer.platform", "macOS")
		return
	}
	summary.fail(out, "computer.platform", fmt.Sprintf("%s unsupported; computer control currently supports macOS first", goos))
}

func checkDesktopHelper(ctx context.Context, out io.Writer, summary *doctorSummary, deps computerDoctorDeps) {
	if !fileExists(deps.DesktopScriptPath) {
		summary.fail(out, "desktop.helper", fmt.Sprintf("%s not found; reinstall Cohort or place helper under ~/.cohort/scripts", filepath.Clean(deps.DesktopScriptPath)))
		return
	}
	summary.pass(out, "desktop.helper", filepath.Clean(deps.DesktopScriptPath))
	if deps.Desktop == nil {
		summary.fail(out, "desktop.driver", "not configured")
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	permissions, err := deps.Desktop.Permissions(reqCtx)
	if err != nil {
		summary.fail(out, "desktop.permissions", formatDoctorError(err))
		return
	}
	if strings.TrimSpace(permissions.Platform) != "" {
		summary.pass(out, "desktop.platform", permissions.Platform)
	}
	checkDesktopPermissionValue(out, summary, "desktop.permissions.accessibility", permissions.Accessibility, true)
	checkDesktopPermissionValue(out, summary, "desktop.permissions.screen_recording", permissions.ScreenRecording, true)
	checkDesktopPermissionValue(out, summary, "desktop.permissions.input_monitoring", permissions.InputMonitoring, false)
	for _, hint := range permissions.Hints {
		hint = strings.TrimSpace(hint)
		if hint != "" {
			summary.warn(out, "desktop.permission_hint", hint)
		}
	}

	reqCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	windows, err := deps.Desktop.ListWindows(reqCtx, desktop.ListWindowsRequest{Limit: 5})
	if err != nil {
		summary.warn(out, "desktop.windows", formatDoctorError(err))
		return
	}
	summary.pass(out, "desktop.windows", fmt.Sprintf("%d visible window(s)", len(windows.Windows)))
}

func checkComputerSmokeApp(ctx context.Context, out io.Writer, summary *doctorSummary, deps computerDoctorDeps, appName string) {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return
	}
	if deps.Desktop == nil {
		summary.fail(out, "computer.smoke_app", "desktop driver is not configured")
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	windows, err := deps.Desktop.ListWindows(reqCtx, desktop.ListWindowsRequest{AppName: appName, Limit: 10})
	if err != nil {
		summary.fail(out, "computer.smoke_app.windows", formatDoctorError(err))
		return
	}
	window, ok := selectComputerDoctorWindow(windows.Windows)
	if !ok {
		summary.fail(out, "computer.smoke_app.windows", fmt.Sprintf("no visible window matched app %q", appName))
		return
	}
	summary.pass(out, "computer.smoke_app.window", fmt.Sprintf("%s pid=%d title=%q", window.AppName, window.PID, window.Title))

	activate, err := deps.Desktop.Activate(reqCtx, desktop.ActivateRequest{PID: window.PID, WindowID: window.WindowID})
	if err != nil {
		summary.fail(out, "computer.smoke_app.activate", formatDoctorError(err))
		return
	}
	if activate.Verified && !activate.Active {
		summary.fail(out, "computer.smoke_app.activate", "helper verified that target app did not become active")
		return
	}
	summary.pass(out, "computer.smoke_app.activate", fmt.Sprintf("pid=%d verified=%t", activate.PID, activate.Verified))

	screenshotPath := filepath.Join(firstNonEmpty(deps.Workspace, "."), ".cohort", "doctor", fmt.Sprintf("computer-smoke-%d.png", time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(screenshotPath), 0755); err != nil {
		summary.fail(out, "computer.smoke_app.screenshot_dir", err.Error())
		return
	}
	screenshot, err := deps.Desktop.Screenshot(reqCtx, desktop.ScreenshotRequest{PID: window.PID, WindowID: window.WindowID, OutputPath: screenshotPath})
	if err != nil {
		summary.fail(out, "computer.smoke_app.screenshot", formatDoctorError(err))
		return
	}
	info, statErr := os.Stat(screenshotPath)
	if statErr != nil || info.IsDir() || info.Size() == 0 {
		summary.fail(out, "computer.smoke_app.screenshot", "helper did not create a non-empty screenshot artifact")
		return
	}
	summary.pass(out, "computer.smoke_app.screenshot", fmt.Sprintf("%dx%d %s", screenshot.Width, screenshot.Height, filepath.Clean(screenshotPath)))

	ax, err := deps.Desktop.AXSnapshot(reqCtx, desktop.AXSnapshotRequest{
		PID:             window.PID,
		MaxDepth:        6,
		MaxNodes:        400,
		IncludeZeroSize: false,
	})
	if err != nil {
		summary.fail(out, "computer.smoke_app.ax_snapshot", formatDoctorError(err))
		return
	}
	summary.pass(out, "computer.smoke_app.ax_snapshot", fmt.Sprintf("%d node(s), truncated=%t", ax.NodeCount, ax.Truncated))

	if deps.OCR == nil {
		summary.warn(out, "computer.smoke_app.ocr", "ocr runner is not configured")
		return
	}
	ocr, err := deps.OCR.Run(reqCtx, vision.OCRRequest{ImagePath: screenshotPath, MinConfidence: 0.1})
	if err != nil {
		summary.warn(out, "computer.smoke_app.ocr", formatDoctorError(err))
		return
	}
	summary.pass(out, "computer.smoke_app.ocr", fmt.Sprintf("%d line(s)", len(ocr.Lines)))
}

func selectComputerDoctorWindow(windows []desktop.Window) (desktop.Window, bool) {
	for _, window := range windows {
		if window.IsVisible && window.IsActive {
			return window, true
		}
	}
	for _, window := range windows {
		if window.IsVisible {
			return window, true
		}
	}
	if len(windows) == 0 {
		return desktop.Window{}, false
	}
	return windows[0], true
}

func checkDesktopPermissionValue(out io.Writer, summary *doctorSummary, name string, value *bool, required bool) {
	if value == nil {
		summary.warn(out, name, "unknown; helper could not determine this permission")
		return
	}
	if *value {
		summary.pass(out, name, "granted")
		return
	}
	if required {
		summary.fail(out, name, "missing; enable it in System Settings > Privacy & Security")
		return
	}
	summary.warn(out, name, "missing; keyboard injection may be limited")
}

func checkOCRHelper(ctx context.Context, out io.Writer, summary *doctorSummary, deps computerDoctorDeps) {
	if !fileExists(deps.OCRScriptPath) {
		summary.fail(out, "ocr.helper", fmt.Sprintf("%s not found; reinstall Cohort or place helper under ~/.cohort/scripts", filepath.Clean(deps.OCRScriptPath)))
		return
	}
	summary.pass(out, "ocr.helper", filepath.Clean(deps.OCRScriptPath))
	if deps.OCR == nil {
		summary.fail(out, "ocr.runner", "not configured")
		return
	}
	imagePath, cleanup, err := writeDoctorOCRProbeImage(firstNonEmpty(deps.Workspace, "."))
	if err != nil {
		summary.fail(out, "ocr.probe_image", err.Error())
		return
	}
	defer cleanup()

	reqCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	result, err := deps.OCR.Run(reqCtx, vision.OCRRequest{ImagePath: imagePath, MinConfidence: 0.1})
	if err != nil {
		summary.fail(out, "ocr.dependencies", formatDoctorError(err))
		return
	}
	summary.pass(out, "ocr.dependencies", fmt.Sprintf("ok (%dx%d probe, %d line(s))", result.Width, result.Height, len(result.Lines)))
}

func writeDoctorOCRProbeImage(workspace string) (string, func(), error) {
	dir := filepath.Join(workspace, ".cohort", "doctor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", func() {}, err
	}
	temp, err := os.CreateTemp(dir, "ocr-probe-*.png")
	if err != nil {
		return "", func() {}, err
	}
	path := temp.Name()
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.White)
		}
	}
	err = png.Encode(temp, img)
	closeErr := temp.Close()
	if err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", func() {}, closeErr
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func checkBrowserBridge(ctx context.Context, out io.Writer, summary *doctorSummary, deps computerDoctorDeps) {
	if deps.BrowserStartErr != nil {
		summary.warn(out, "browser.bridge.server", fmt.Sprintf("unavailable: %v", deps.BrowserStartErr))
		return
	}
	summary.pass(out, "browser.bridge.server", browser.DefaultListenAddr+browser.DefaultPath)
	if deps.Browser == nil {
		summary.warn(out, "browser.bridge.connection", "not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tabs, err := deps.Browser.Tabs(reqCtx)
	if err != nil {
		if errors.Is(err, browser.ErrNotConnected) || strings.Contains(err.Error(), browser.ErrNotConnected.Error()) {
			summary.warn(out, "browser.bridge.connection", "Chrome bridge is not connected; run `cohort extension open`, load the unpacked extension, then open any http/https page")
			return
		}
		summary.warn(out, "browser.bridge.connection", err.Error())
		return
	}
	summary.pass(out, "browser.bridge.connection", fmt.Sprintf("connected (%d tab(s))", len(tabs)))
}

func formatDoctorError(err error) string {
	if err == nil {
		return ""
	}
	var desktopErr *desktop.ToolError
	if errors.As(err, &desktopErr) {
		return joinDoctorDetail(desktopErr.Code, desktopErr.Message, desktopErr.Hint)
	}
	var ocrErr *vision.ToolError
	if errors.As(err, &ocrErr) {
		return joinDoctorDetail(ocrErr.Code, ocrErr.Message, ocrErr.Hint)
	}
	return err.Error()
}

func joinDoctorDetail(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ": ")
}
