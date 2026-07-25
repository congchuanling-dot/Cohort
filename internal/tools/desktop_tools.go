package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohert/internal/agent"
	"cohert/internal/desktop"
	"cohert/internal/llm"
	"cohert/internal/vision"
)

const (
	defaultDesktopWindowLimit      = 50
	maxDesktopWindowLimit          = 100
	defaultDesktopAXDepth          = 8
	maxDesktopAXDepth              = 12
	defaultDesktopAXNodes          = 300
	maxDesktopAXNodes              = 500
	defaultDesktopScreenshotDir    = ".cohert/desktop/screenshots"
	defaultDesktopOCRMinConfidence = 0.5
	defaultDesktopOCRMaxLines      = 80
	defaultDesktopOCRMaxChars      = 8000
	maxDesktopOCRLines             = 200
	maxDesktopOCRChars             = 12000
)

type desktopScreenshotManifest struct {
	Version               int            `json:"version"`
	ImagePath             string         `json:"image_path"`
	PID                   int            `json:"pid"`
	WindowID              string         `json:"window_id"`
	Width                 int            `json:"width"`
	Height                int            `json:"height"`
	WindowBounds          desktop.Bounds `json:"window_bounds"`
	CoordinateSpace       string         `json:"coordinate_space"`
	ScreenCoordinateSpace string         `json:"screen_coordinate_space"`
	CreatedAt             string         `json:"created_at"`
}

// DesktopPermissions 检查桌面感知所需的 macOS 权限。
type DesktopPermissions struct {
	driver desktop.Driver
}

func NewDesktopPermissions(driver desktop.Driver) *DesktopPermissions {
	return &DesktopPermissions{driver: driver}
}

func (t *DesktopPermissions) Name() string { return ToolNameDesktopPermissions }

func (t *DesktopPermissions) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Check macOS accessibility and screen-recording permissions required for read-only desktop sensing. Run this before desktop screenshots or AX snapshots when desktop capability is uncertain.",
		Parameters:  objectSchema(map[string]any{}),
	}}
}

func (t *DesktopPermissions) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	result, err := t.driver.Permissions(ctx)
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"platform":         result.Platform,
			"accessibility":    result.Accessibility,
			"screen_recording": result.ScreenRecording,
			"input_monitoring": result.InputMonitoring,
			"missing":          result.Missing,
			"hints":            result.Hints,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopWindows 枚举可见普通应用窗口，供模型定位目标 PID。
type DesktopWindows struct {
	driver desktop.Driver
}

func NewDesktopWindows(driver desktop.Driver) *DesktopWindows {
	return &DesktopWindows{driver: driver}
}

func (t *DesktopWindows) Name() string { return ToolNameDesktopWindows }

func (t *DesktopWindows) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "List visible desktop application windows. Use this before desktop activation or sensing to obtain the target PID and window ID.",
		Parameters: objectSchema(map[string]any{
			"app_name": stringProp("Optional case-insensitive application-name substring filter."),
			"title":    stringProp("Optional case-insensitive window-title substring filter."),
			"limit":    intProp("Maximum windows to return. Default 50, max 100.", defaultDesktopWindowLimit),
		}),
	}}
}

func (t *DesktopWindows) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	limit := clampDesktopLimit(asInt(call.Args["limit"], defaultDesktopWindowLimit), defaultDesktopWindowLimit, maxDesktopWindowLimit)
	result, err := t.driver.ListWindows(ctx, desktop.ListWindowsRequest{
		AppName: strings.TrimSpace(asString(call.Args["app_name"])),
		Title:   strings.TrimSpace(asString(call.Args["title"])),
		Limit:   limit,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"windows":          result.Windows,
			"count":            len(result.Windows),
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopActivate 将一个 PID 对应的应用带到前台，并验证结果。
type DesktopActivate struct {
	driver desktop.Driver
}

func NewDesktopActivate(driver desktop.Driver) *DesktopActivate {
	return &DesktopActivate{driver: driver}
}

func (t *DesktopActivate) Name() string { return ToolNameDesktopActivate }

func (t *DesktopActivate) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Activate a desktop application by PID and verify it became frontmost. Always run this before desktop screenshots or any future desktop input action.",
		Parameters: objectSchema(map[string]any{
			"pid":       intProp("Target application PID from desktop_windows.", 0),
			"window_id": stringProp("Optional window ID for result correlation; macOS activation is application-scoped."),
		}, "pid"),
	}}
}

func (t *DesktopActivate) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	result, err := t.driver.Activate(ctx, desktop.ActivateRequest{
		PID:      pid,
		WindowID: strings.TrimSpace(asString(call.Args["window_id"])),
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":   agent.ToolStatusSuccess,
			"pid":      result.PID,
			"active":   result.Active,
			"verified": result.Verified,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopScreenshot 捕获指定 PID 的一个窗口，并由 Go 侧固定保存路径。
type DesktopScreenshot struct {
	driver desktop.Driver
	workspaceTool
}

func NewDesktopScreenshot(driver desktop.Driver, workspace string) *DesktopScreenshot {
	return &DesktopScreenshot{driver: driver, workspaceTool: newWorkspaceTool(workspace)}
}

func (t *DesktopScreenshot) Name() string { return ToolNameDesktopScreenshot }

func (t *DesktopScreenshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Capture a visible window for a target PID and save it under the workspace. Activate the PID first. The returned OCR coordinates are screenshot-local, not screen coordinates.",
		Parameters: objectSchema(map[string]any{
			"pid":       intProp("Target application PID from desktop_windows.", 0),
			"window_id": stringProp("Optional visible window ID. If empty, captures the first visible window for this PID."),
		}, "pid"),
	}}
}

func (t *DesktopScreenshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	dir := filepath.Join(t.workspace, defaultDesktopScreenshotDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_save_failed",
				fmt.Sprintf("create desktop screenshot directory: %v", err),
				"请检查 workspace 是否可写。",
			),
			NextPrompt: "\n",
		}, nil
	}
	outputPath := filepath.Join(dir, fmt.Sprintf("desktop_screenshot_%d.png", time.Now().UnixNano()))
	result, err := t.driver.Screenshot(ctx, desktop.ScreenshotRequest{
		PID:        pid,
		WindowID:   strings.TrimSpace(asString(call.Args["window_id"])),
		OutputPath: outputPath,
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_save_failed",
				fmt.Sprintf("desktop helper did not create the requested screenshot: %v", err),
				"请确认已授予屏幕录制权限，并重新调用 desktop_permissions 检查。",
			),
			NextPrompt: "\n",
		}, nil
	}
	manifestPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".json"
	manifest := desktopScreenshotManifest{
		Version:               1,
		ImagePath:             outputPath,
		PID:                   result.PID,
		WindowID:              result.WindowID,
		Width:                 result.Width,
		Height:                result.Height,
		WindowBounds:          result.Bounds,
		CoordinateSpace:       desktop.CoordinateSpaceScreenshotLocal,
		ScreenCoordinateSpace: desktop.CoordinateSpaceScreenPhysical,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeDesktopScreenshotManifest(manifestPath, manifest); err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_screenshot_manifest_failed",
				fmt.Sprintf("write desktop screenshot manifest: %v", err),
				"请检查 workspace 是否可写；没有 manifest 的截图不能用于 desktop_visual_click。",
			),
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":                  agent.ToolStatusSuccess,
			"image_path":              outputPath,
			"manifest_path":           manifestPath,
			"width":                   result.Width,
			"height":                  result.Height,
			"window_id":               result.WindowID,
			"pid":                     result.PID,
			"window_bounds":           result.Bounds,
			"coordinate_space":        desktop.CoordinateSpaceScreenshotLocal,
			"screen_coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"bytes":                   info.Size(),
		},
		NextPrompt: "\n",
	}, nil
}

func writeDesktopScreenshotManifest(path string, manifest desktopScreenshotManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// DesktopAXSnapshot 返回目标应用的受限 AX 控件树。
type DesktopAXSnapshot struct {
	driver desktop.Driver
}

func NewDesktopAXSnapshot(driver desktop.Driver) *DesktopAXSnapshot {
	return &DesktopAXSnapshot{driver: driver}
}

func (t *DesktopAXSnapshot) Name() string { return ToolNameDesktopAXSnapshot }

func (t *DesktopAXSnapshot) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Read a bounded macOS Accessibility control tree for a target PID. Prefer this semantic snapshot before screenshot/OCR; secure text values are never returned.",
		Parameters: objectSchema(map[string]any{
			"pid":               intProp("Target application PID from desktop_windows.", 0),
			"max_depth":         intProp("Maximum tree depth. Default 8, max 12.", defaultDesktopAXDepth),
			"max_nodes":         intProp("Maximum returned nodes. Default 300, max 500.", defaultDesktopAXNodes),
			"include_zero_size": boolProp("Include zero-size structural nodes. Default false.", false),
		}, "pid"),
	}}
}

func (t *DesktopAXSnapshot) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	pid := asInt(call.Args["pid"], 0)
	if pid <= 0 {
		return desktopBadPIDOutcome(t.Name()), nil
	}
	result, err := t.driver.AXSnapshot(ctx, desktop.AXSnapshotRequest{
		PID:             pid,
		MaxDepth:        clampDesktopLimit(asInt(call.Args["max_depth"], defaultDesktopAXDepth), defaultDesktopAXDepth, maxDesktopAXDepth),
		MaxNodes:        clampDesktopLimit(asInt(call.Args["max_nodes"], defaultDesktopAXNodes), defaultDesktopAXNodes, maxDesktopAXNodes),
		IncludeZeroSize: asBool(call.Args["include_zero_size"], false),
	})
	if err != nil {
		return desktopToolError(err), nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"pid":              result.PID,
			"coordinate_space": desktop.CoordinateSpaceScreenPhysical,
			"root":             result.Root,
			"node_count":       result.NodeCount,
			"truncated":        result.Truncated,
		},
		NextPrompt: "\n",
	}, nil
}

// DesktopOCR 对已经保存到 workspace 的桌面截图执行只读 OCR。
type DesktopOCR struct {
	workspaceTool
	runner vision.OCRRunner
}

func NewDesktopOCR(workspace string) *DesktopOCR {
	workspaceRoot := newWorkspaceTool(workspace).workspace
	scriptPath := filepath.Join("scripts", "browser_ocr.py")
	if root := findGitRoot(workspaceRoot); root != "" {
		scriptPath = filepath.Join(root, "scripts", "browser_ocr.py")
	} else if absolutePath, err := filepath.Abs(scriptPath); err == nil {
		scriptPath = absolutePath
	}
	return NewDesktopOCRWithRunner(
		workspace,
		vision.NewPythonOCRRunner("python3", scriptPath, vision.DefaultOCRTimeout),
	)
}

func NewDesktopOCRWithRunner(workspace string, runner vision.OCRRunner) *DesktopOCR {
	return &DesktopOCR{workspaceTool: newWorkspaceTool(workspace), runner: runner}
}

func (t *DesktopOCR) Name() string { return ToolNameDesktopOCR }

func (t *DesktopOCR) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Run read-only OCR on a desktop screenshot already saved under the workspace. Bounding boxes are screenshot-local and must be converted by a later controlled tool before any input action.",
		Parameters: objectSchema(map[string]any{
			"image_path":     stringProp("Required screenshot path under the workspace, usually returned by desktop_screenshot."),
			"min_confidence": floatProp("Minimum OCR confidence from 0 to 1. Default 0.5.", defaultDesktopOCRMinConfidence),
			"max_lines":      intProp("Maximum OCR lines to return. Default 80, max 200.", defaultDesktopOCRMaxLines),
			"max_chars":      intProp("Maximum OCR text characters to return. Default 8000, max 12000.", defaultDesktopOCRMaxChars),
			"enhance":        boolProp("Apply contrast and scale preprocessing. Default false.", false),
		}, "image_path"),
	}}
}

func (t *DesktopOCR) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	if t.runner == nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_unavailable",
				"OCR runner is not configured",
				"请确认 Cohert 随附的 scripts/browser_ocr.py 可用，并已配置 Python OCR 环境。",
			),
			NextPrompt: "\n",
		}, nil
	}
	imagePath, pathErr := t.resolveImagePath(strings.TrimSpace(asString(call.Args["image_path"])))
	if pathErr != nil {
		return agent.Outcome{Data: *pathErr, NextPrompt: "\n"}, nil
	}
	minConfidence := asFloat(call.Args["min_confidence"], defaultDesktopOCRMinConfidence)
	if math.IsNaN(minConfidence) || minConfidence < 0 || minConfidence > 1 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_bad_min_confidence",
				"min_confidence must be between 0 and 1",
				"请传入 0 到 1 之间的数值，默认 0.5。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.runner.Run(ctx, vision.OCRRequest{
		ImagePath:     imagePath,
		MinConfidence: minConfidence,
		Enhance:       asBool(call.Args["enhance"], false),
	})
	if err != nil {
		var ocrErr *vision.ToolError
		if errors.As(err, &ocrErr) {
			return agent.Outcome{
				Data: agent.NewToolError(
					strings.Replace(ocrErr.Code, "browser_ocr_", "desktop_ocr_", 1),
					ocrErr.Message,
					"请检查桌面截图、Python OCR 依赖和 helper 状态；不要在工具执行中自动安装依赖。",
				),
				NextPrompt: "\n",
			}, nil
		}
		return agent.Outcome{
			Data: agent.NewToolError(
				"desktop_ocr_failed",
				err.Error(),
				"请检查图片是否可读、Python OCR helper 是否可用，并根据错误决定是否重试。",
			),
			NextPrompt: "\n",
		}, nil
	}
	maxLines := clampDesktopLimit(asInt(call.Args["max_lines"], defaultDesktopOCRMaxLines), defaultDesktopOCRMaxLines, maxDesktopOCRLines)
	maxChars := clampDesktopLimit(asInt(call.Args["max_chars"], defaultDesktopOCRMaxChars), defaultDesktopOCRMaxChars, maxDesktopOCRChars)
	lines := result.Lines
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	text, textTruncated := truncateBrowserOCRText(joinOCRLines(lines), maxChars)
	return agent.Outcome{
		Data: map[string]any{
			"status":           agent.ToolStatusSuccess,
			"image_path":       imagePath,
			"coordinate_space": desktop.CoordinateSpaceScreenshotLocal,
			"width":            result.Width,
			"height":           result.Height,
			"text":             text,
			"lines":            lines,
			"line_count":       len(lines),
			"total_lines":      len(result.Lines),
			"truncated":        truncated || textTruncated,
		},
		NextPrompt: "\n",
	}, nil
}

func (t *DesktopOCR) resolveImagePath(rawPath string) (string, *agent.ToolErrorData) {
	if rawPath == "" {
		err := agent.NewToolError(
			"desktop_ocr_image_required",
			"desktop_ocr requires an image_path returned by desktop_screenshot",
			"请先调用 desktop_screenshot，再将其 image_path 传给 desktop_ocr。",
		)
		return "", &err
	}
	path := t.resolve(rawPath)
	rel, err := filepath.Rel(t.workspace, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		toolErr := agent.NewToolError(
			"desktop_ocr_path_outside_workspace",
			"desktop OCR image_path must stay inside the configured workspace",
			"请提供 desktop_screenshot 返回的 workspace 内路径。",
		)
		return "", &toolErr
	}
	if _, err := os.Stat(path); err != nil {
		toolErr := agent.NewToolError(
			"desktop_ocr_image_not_found",
			fmt.Sprintf("desktop OCR image is unavailable: %v", err),
			"请确认截图仍存在，或重新调用 desktop_screenshot。",
		)
		return "", &toolErr
	}
	realWorkspace, workspaceErr := filepath.EvalSymlinks(t.workspace)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if workspaceErr == nil && pathErr == nil {
		realRel, err := filepath.Rel(realWorkspace, realPath)
		if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			toolErr := agent.NewToolError(
				"desktop_ocr_path_outside_workspace",
				"desktop OCR image resolves outside the configured workspace",
				"图片路径不能通过符号链接逃出 workspace；请复制图片到 workspace 后重试。",
			)
			return "", &toolErr
		}
	}
	return path, nil
}

func desktopBadPIDOutcome(toolName string) agent.Outcome {
	return agent.Outcome{
		Data: agent.NewToolError(
			"desktop_bad_pid",
			toolName+" requires a positive pid",
			"请先调用 desktop_windows，选择目标窗口返回的 pid。",
		),
		NextPrompt: "\n",
	}
}

func desktopToolError(err error) agent.Outcome {
	var toolErr *desktop.ToolError
	if errors.As(err, &toolErr) {
		return agent.Outcome{
			Data:       agent.NewToolError(toolErr.Code, toolErr.Message, toolErr.Hint),
			NextPrompt: "\n",
		}
	}
	return agent.Outcome{
		Data: agent.NewToolError(
			"desktop_failed",
			err.Error(),
			"请检查 desktop 权限、目标 PID 和 macOS helper 状态，再决定是否重试。",
		),
		NextPrompt: "\n",
	}
}

func clampDesktopLimit(value int, fallback int, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
