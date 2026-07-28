#!/usr/bin/env python3
"""macOS desktop sensing helper for Cohert.

The script exposes a small JSON protocol for desktop sensing and narrowly
reviewed semantic input. Mouse click input is only exposed through current AX
nodes and never through free-form model-provided coordinates.
"""

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path


def emit_success(data):
    print(json.dumps({"status": "success", "data": data}, ensure_ascii=False))


def emit_error(code, message, hint):
    print(
        json.dumps(
            {
                "status": "error",
                "code": code,
                "message": message,
                "hint": hint,
            },
            ensure_ascii=False,
        )
    )


def require_macos_modules():
    try:
        import Quartz
        from AppKit import NSRunningApplication, NSWorkspace

        return Quartz, NSRunningApplication, NSWorkspace
    except ModuleNotFoundError as exc:
        raise DesktopError(
            "desktop_dependency_missing",
            f"Python desktop dependency is missing: {exc.name}",
            "请手动安装依赖：python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices。",
        ) from exc


class DesktopError(Exception):
    def __init__(self, code, message, hint):
        super().__init__(message)
        self.code = code
        self.message = message
        self.hint = hint


def parse_args():
    parser = argparse.ArgumentParser(description="Cohert macOS desktop sensing helper")
    parser.add_argument("--command", required=True)
    parser.add_argument("--json", required=True, dest="payload")
    return parser.parse_args()


def parse_payload(raw):
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise DesktopError(
            "desktop_bad_request",
            f"desktop helper received invalid JSON: {exc}",
            "请检查 desktop 工具参数。",
        ) from exc
    if not isinstance(payload, dict):
        raise DesktopError(
            "desktop_bad_request",
            "desktop helper request must be a JSON object",
            "请检查 desktop 工具参数。",
        )
    return payload


def display_scale(quartz):
    display_id = quartz.CGMainDisplayID()
    logical = quartz.CGDisplayBounds(display_id)
    mode = quartz.CGDisplayCopyDisplayMode(display_id)
    physical_width = int(quartz.CGDisplayModeGetPixelWidth(mode))
    if physical_width <= 0:
        return 1.0
    return float(logical.size.width) / physical_width


def logical_to_physical(value, scale):
    if not scale:
        return int(round(value))
    return int(round(float(value) / scale))


def physical_to_logical(value, scale):
    if not scale:
        return float(value)
    return float(value) * scale


def physical_bounds(raw, scale):
    return {
        "x": logical_to_physical(raw.get("X", 0), scale),
        "y": logical_to_physical(raw.get("Y", 0), scale),
        "width": logical_to_physical(raw.get("Width", 0), scale),
        "height": logical_to_physical(raw.get("Height", 0), scale),
    }


def ax_permission():
    try:
        from ApplicationServices import AXIsProcessTrusted

        return bool(AXIsProcessTrusted())
    except ModuleNotFoundError:
        return None
    except Exception:
        return None


def permissions(_payload):
    quartz, _, _ = require_macos_modules()
    accessibility = ax_permission()
    screen_recording = None
    if hasattr(quartz, "CGPreflightScreenCaptureAccess"):
        screen_recording = bool(quartz.CGPreflightScreenCaptureAccess())

    missing = []
    hints = []
    if accessibility is False:
        missing.append("accessibility")
        hints.append("系统设置 -> 隐私与安全性 -> 辅助功能：允许运行 Cohert 的终端或 IDE。")
    if screen_recording is False:
        missing.append("screen_recording")
        hints.append("系统设置 -> 隐私与安全性 -> 屏幕录制：允许运行 Cohert 的终端或 IDE。")
    return {
        "platform": "darwin",
        "accessibility": accessibility,
        "screen_recording": screen_recording,
        "input_monitoring": None,
        "missing": missing,
        "hints": hints,
    }


def active_pid(workspace):
    app = workspace.sharedWorkspace().frontmostApplication()
    if app is None:
        return 0
    return int(app.processIdentifier())


def collect_windows(payload):
    quartz, _, workspace = require_macos_modules()
    app_name = str(payload.get("app_name") or "").strip().lower()
    title = str(payload.get("title") or "").strip().lower()
    limit = int(payload.get("limit") or 50)
    scale = display_scale(quartz)
    front_pid = active_pid(workspace)
    options = quartz.kCGWindowListOptionOnScreenOnly | quartz.kCGWindowListExcludeDesktopElements
    records = quartz.CGWindowListCopyWindowInfo(options, quartz.kCGNullWindowID) or []
    windows = []

    for record in records:
        if int(record.get("kCGWindowLayer", 0)) != 0:
            continue
        bounds = record.get("kCGWindowBounds") or {}
        width = int(bounds.get("Width", 0))
        height = int(bounds.get("Height", 0))
        if width <= 0 or height <= 0:
            continue
        owner = str(record.get("kCGWindowOwnerName") or "")
        window_title = str(record.get("kCGWindowName") or "")
        if app_name and app_name not in owner.lower():
            continue
        if title and title not in window_title.lower():
            continue
        pid = int(record.get("kCGWindowOwnerPID", 0))
        windows.append(
            {
                "window_id": str(record.get("kCGWindowNumber", "")),
                "pid": pid,
                "app_name": owner,
                "title": window_title,
                "bounds": physical_bounds(bounds, scale),
                "is_visible": float(record.get("kCGWindowAlpha", 1)) > 0,
                "is_active": pid == front_pid,
            }
        )
        if len(windows) >= limit:
            break
    return windows


def list_windows(payload):
    return {"windows": collect_windows(payload)}


def activate(payload):
    _, running_application, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_activate requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    app = running_application.runningApplicationWithProcessIdentifier_(pid)
    if app is None:
        raise DesktopError(
            "desktop_target_not_found",
            f"no running application found for pid {pid}",
            "请重新调用 desktop_windows；目标应用可能已退出或 PID 已变化。",
        )
    app.activateWithOptions_(1 << 1)  # NSApplicationActivateAllWindows
    time.sleep(0.25)
    is_active = active_pid(workspace) == pid
    if not is_active:
        raise DesktopError(
            "desktop_activate_failed",
            f"application pid {pid} did not become frontmost",
            "请确认目标应用没有系统模态弹窗、全屏限制或权限限制，再重新枚举窗口。",
        )
    return {"pid": pid, "active": True, "verified": True}


def find_window(payload):
    pid = int(payload.get("pid") or 0)
    requested_id = str(payload.get("window_id") or "").strip()
    windows = collect_windows({"limit": 100})
    for window in windows:
        if requested_id and window["window_id"] != requested_id:
            continue
        if pid and window["pid"] != pid:
            continue
        return window
    raise DesktopError(
        "desktop_window_not_found",
        "target window is no longer visible",
        "请重新调用 desktop_windows 获取当前窗口和 PID。",
    )


def require_active_pid(pid):
    _, _, workspace = require_macos_modules()
    if active_pid(workspace) == pid:
        return
    raise DesktopError(
        "desktop_target_not_active",
        f"application pid {pid} is not frontmost",
        "请先调用 desktop_activate 并确认 verified=true，再读取 AX 控件树或截取窗口。",
    )


def ensure_active_pid(pid):
    _, _, workspace = require_macos_modules()
    if active_pid(workspace) == pid:
        return True
    activate({"pid": pid})
    return active_pid(workspace) == pid


def image_dimensions(path, fallback_bounds):
    completed = subprocess.run(
        ["/usr/bin/sips", "-g", "pixelWidth", "-g", "pixelHeight", str(path)],
        check=True,
        text=True,
        capture_output=True,
    )
    result = {}
    for line in completed.stdout.splitlines():
        line = line.strip()
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        key = key.strip()
        if key in {"pixelWidth", "pixelHeight"}:
            result[key] = int(value.strip())
    return (
        result.get("pixelWidth", fallback_bounds["width"]),
        result.get("pixelHeight", fallback_bounds["height"]),
    )


def screenshot(payload):
    _, _, _ = require_macos_modules()
    output_path = str(payload.get("output_path") or "").strip()
    if not output_path:
        raise DesktopError(
            "desktop_bad_output_path",
            "desktop_screenshot requires an output_path",
            "请通过 Cohert 的 desktop_screenshot 工具调用，不能直接调用 helper。",
        )
    window = find_window(payload)
    require_active_pid(window["pid"])
    path = Path(output_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        subprocess.run(
            ["/usr/sbin/screencapture", "-x", "-o", "-l", window["window_id"], str(path)],
            check=True,
            capture_output=True,
        )
        if not path.is_file() or path.stat().st_size == 0:
            raise RuntimeError("screencapture did not create an image")
        width, height = image_dimensions(path, window["bounds"])
    except Exception as exc:
        raise DesktopError(
            "desktop_screenshot_failed",
            f"unable to capture target window: {exc}",
            "请确认已授权屏幕录制权限，并重新调用 desktop_permissions 检查。",
        ) from exc
    return {
        "width": width,
        "height": height,
        "window_id": window["window_id"],
        "pid": window["pid"],
        "bounds": window["bounds"],
    }


def load_ax():
    try:
        from ApplicationServices import (
            AXIsProcessTrusted,
            AXUIElementCopyActionNames,
            AXUIElementCopyAttributeValue,
            AXUIElementCreateApplication,
            AXUIElementPerformAction,
            AXUIElementSetAttributeValue,
            AXValueGetValue,
            kAXChildrenAttribute,
            kAXDescriptionAttribute,
            kAXEnabledAttribute,
            kAXFocusedAttribute,
            kAXFocusedUIElementAttribute,
            kAXPositionAttribute,
            kAXPressAction,
            kAXRoleAttribute,
            kAXSizeAttribute,
            kAXTitleAttribute,
            kAXValueCGPointType,
            kAXValueCGSizeType,
            kAXValueAttribute,
            kAXWindowsAttribute,
        )
    except ModuleNotFoundError as exc:
        raise DesktopError(
            "desktop_dependency_missing",
            f"Accessibility dependency is missing: {exc.name}",
            "请手动安装依赖：python3 -m pip install pyobjc-framework-ApplicationServices。",
        ) from exc
    return {
        "trusted": AXIsProcessTrusted,
        "application": AXUIElementCreateApplication,
        "attribute": AXUIElementCopyAttributeValue,
        "set_attribute": AXUIElementSetAttributeValue,
        "actions": AXUIElementCopyActionNames,
        "perform_action": AXUIElementPerformAction,
        "value": AXValueGetValue,
        "children": kAXChildrenAttribute,
        "windows": kAXWindowsAttribute,
        "focused": kAXFocusedUIElementAttribute,
        "role": kAXRoleAttribute,
        "title": kAXTitleAttribute,
        "description": kAXDescriptionAttribute,
        "value_attribute": kAXValueAttribute,
        "enabled": kAXEnabledAttribute,
        "focused_attr": kAXFocusedAttribute,
        "position": kAXPositionAttribute,
        "press_action": kAXPressAction,
        "size": kAXSizeAttribute,
        "point_type": kAXValueCGPointType,
        "size_type": kAXValueCGSizeType,
    }


def ax_attr(api, element, attribute):
    try:
        error, value = api["attribute"](element, attribute, None)
        return value if int(error) == 0 else None
    except Exception:
        return None


def ax_actions(api, element):
    try:
        error, actions = api["actions"](element, None)
        if int(error) != 0 or not actions:
            return []
        return [str(action) for action in actions]
    except Exception:
        return []


def ax_bounds(api, element, scale):
    x = y = width = height = 0.0
    position = ax_attr(api, element, api["position"])
    size = ax_attr(api, element, api["size"])
    if position is not None:
        try:
            ok, point = api["value"](position, api["point_type"], None)
            if ok:
                x, y = point.x, point.y
        except Exception:
            pass
    if size is not None:
        try:
            ok, rect_size = api["value"](size, api["size_type"], None)
            if ok:
                width, height = rect_size.width, rect_size.height
        except Exception:
            pass
    return {
        "x": logical_to_physical(x, scale),
        "y": logical_to_physical(y, scale),
        "width": logical_to_physical(width, scale),
        "height": logical_to_physical(height, scale),
    }


def resolve_ax_node(api, pid, node_id):
    parts = str(node_id or "").split("/")
    if not parts or parts[0] != "ax:0":
        raise DesktopError(
            "desktop_ax_node_invalid",
            f"invalid AX node_id: {node_id!r}",
            "请使用当前 desktop_ax_snapshot 返回的 ax:0/... 节点 ID。",
        )
    element = api["application"](pid)
    for depth, part in enumerate(parts[1:]):
        if not part.isdigit():
            raise DesktopError(
                "desktop_ax_node_invalid",
                f"invalid AX node path segment: {part!r}",
                "请使用当前 desktop_ax_snapshot 返回的 ax:0/... 节点 ID。",
            )
        children = ax_attr(api, element, api["children"]) or []
        if depth == 0 and not children:
            children = ax_attr(api, element, api["windows"]) or []
        index = int(part)
        if index >= len(children):
            raise DesktopError(
                "desktop_ax_node_stale",
                f"AX node {node_id!r} no longer exists",
                "界面可能已变化；请重新调用 desktop_ax_snapshot 后再决定是否操作。",
            )
        element = children[index]
    return element


def ax_node_metadata(api, element, scale):
    enabled = ax_attr(api, element, api["enabled"])
    return {
        "role": str(ax_attr(api, element, api["role"]) or ""),
        "title": str(ax_attr(api, element, api["title"]) or ""),
        "description": str(ax_attr(api, element, api["description"]) or ""),
        "enabled": bool(enabled) if isinstance(enabled, (bool, int)) else None,
        "bounds": ax_bounds(api, element, scale),
        "actions": ax_actions(api, element),
    }


def require_ax_target(payload, operation_name, require_action=None, require_bounds=False, require_editable=False):
    quartz, _, _ = require_macos_modules()
    api = load_ax()
    if not bool(api["trusted"]()):
        raise DesktopError(
            "desktop_permission_denied",
            "Accessibility permission is not granted",
            "系统设置 -> 隐私与安全性 -> 辅助功能：允许运行 Cohert 的终端或 IDE。",
        )
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            f"{operation_name} requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    node_id = str(payload.get("node_id") or "").strip()
    element = resolve_ax_node(api, pid, node_id)
    metadata = ax_node_metadata(api, element, display_scale(quartz))
    expected = {
        "role": str(payload.get("expected_role") or ""),
        "title": str(payload.get("expected_title") or ""),
        "description": str(payload.get("expected_description") or ""),
    }
    for field, expected_value in expected.items():
        if metadata[field] != expected_value:
            raise DesktopError(
                "desktop_ax_node_stale",
                f"AX node {node_id!r} no longer matches expected {field}",
                "界面可能已变化；请重新调用 desktop_ax_snapshot，确认节点语义后再操作。",
            )
    if metadata["enabled"] is False:
        raise DesktopError(
            "desktop_ax_node_disabled",
            f"AX node {node_id!r} is disabled",
            "请重新读取 AX 快照，选择 enabled=true 的可操作节点。",
        )
    if require_action and require_action not in metadata["actions"]:
        raise DesktopError(
            f"{operation_name}_unsupported",
            f"AX node {node_id!r} does not support {require_action}",
            "请重新读取 AX 快照，选择支持目标动作的节点。",
        )
    if require_bounds and (metadata["bounds"]["width"] <= 0 or metadata["bounds"]["height"] <= 0):
        raise DesktopError(
            "desktop_click_bad_bounds",
            f"AX node {node_id!r} has no clickable bounds",
            "请选择当前可见且带有有效 bounds 的 AX 节点。",
        )
    if require_editable and not editable_metadata(metadata):
        raise DesktopError(
            "desktop_ax_focus_not_editable",
            f"AX node {node_id!r} is not editable: role={metadata['role']!r}",
            "请选择 AXTextField、AXTextArea、AXSearchField 或其他可编辑文本节点。",
        )
    return quartz, api, pid, node_id, element, metadata


def ax_press(payload):
    _, api, pid, node_id, element, _metadata = require_ax_target(
        payload,
        "desktop_ax_press",
        require_action="AXPress",
    )
    error = api["perform_action"](element, api["press_action"])
    if int(error) != 0:
        raise DesktopError(
            "desktop_ax_press_failed",
            f"AXPress failed for node {node_id!r}: AXError={int(error)}",
            "请重新读取 AX 快照确认目标状态；不要盲目重复同一动作。",
        )
    time.sleep(0.25)
    return {"pid": pid, "node_id": node_id, "action": "AXPress", "performed": True}


def editable_metadata(metadata):
    role = str(metadata.get("role") or "")
    role_lower = role.lower()
    if role in {"AXTextField", "AXTextArea", "AXSearchField", "AXComboBox"}:
        return True
    return "text" in role_lower and "static" not in role_lower


def same_focus_target(expected, focused):
    if focused is None:
        return False
    for field in ("role", "title", "description", "bounds"):
        if focused.get(field) != expected.get(field):
            return False
    return True


def ax_focus(payload):
    _quartz, api, pid, node_id, element, metadata = require_ax_target(
        payload,
        "desktop_ax_focus",
        require_editable=True,
    )
    error = api["set_attribute"](element, api["focused_attr"], True)
    if int(error) != 0:
        raise DesktopError(
            "desktop_ax_focus_failed",
            f"AX focus failed for node {node_id!r}: AXError={int(error)}",
            "请重新读取 AX 快照，确认目标是可编辑输入节点；必要时改用受控 desktop_click 聚焦。",
        )
    time.sleep(0.15)
    app = api["application"](pid)
    focused = ax_attr(api, app, api["focused"])
    focused_metadata_value = None
    if focused is not None:
        focused_metadata_value = ax_node_metadata(api, focused, display_scale(require_macos_modules()[0]))
    focused_ok = same_focus_target(metadata, focused_metadata_value)
    return {
        "pid": pid,
        "node_id": node_id,
        "action": "AXFocus",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(require_macos_modules()[2]) == pid,
        "focused": focused_ok,
        "focus_role": (focused_metadata_value or {}).get("role", ""),
        "focus_title": (focused_metadata_value or {}).get("title", ""),
        "focus_description": (focused_metadata_value or {}).get("description", ""),
    }


def click(payload):
    quartz, _api, pid, node_id, _element, metadata = require_ax_target(
        payload,
        "desktop_click",
        require_bounds=True,
    )
    bounds = metadata["bounds"]
    x = bounds["x"] + int(round(bounds["width"] / 2))
    y = bounds["y"] + int(round(bounds["height"] / 2))
    logical_point = (
        physical_to_logical(x, display_scale(quartz)),
        physical_to_logical(y, display_scale(quartz)),
    )
    try:
        post_mouse_click(quartz, logical_point)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_click_failed",
            f"desktop click failed: {exc}",
            "请重新确认目标应用前台状态和节点 bounds，不要连续重试。",
        ) from exc
    return {
        "pid": pid,
        "node_id": node_id,
        "action": "Click",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(require_macos_modules()[2]) == pid,
        "x": x,
        "y": y,
        "coordinate_space": "screen-physical",
    }


def visual_click(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_visual_click requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    coordinate_space = str(payload.get("coordinate_space") or "").strip()
    if coordinate_space != "screen-physical":
        raise DesktopError(
            "desktop_visual_click_bad_coordinate_space",
            f"desktop_visual_click requires screen-physical coordinates, got {coordinate_space!r}",
            "请通过 Cohert desktop_visual_click 工具调用，不能直接传入截图坐标或任意坐标。",
        )
    x = int(payload.get("x") or 0)
    y = int(payload.get("y") or 0)
    if x < 0 or y < 0:
        raise DesktopError(
            "desktop_visual_click_bad_point",
            "desktop_visual_click requires a non-negative screen point",
            "请重新读取 screenshot manifest 和 OCR bbox 后再点击。",
        )
    active_ok = ensure_active_pid(pid)
    if not active_ok:
        raise DesktopError(
            "desktop_activate_failed",
            f"application pid {pid} did not become frontmost",
            "请重新调用 desktop_windows；目标应用可能已退出、被系统弹窗遮挡或 PID 已变化。",
        )
    try:
        logical_point = (
            physical_to_logical(x, display_scale(quartz)),
            physical_to_logical(y, display_scale(quartz)),
        )
        post_mouse_click(quartz, logical_point)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_visual_click_failed",
            f"desktop visual click failed: {exc}",
            "请重新确认目标截图和 bbox，不要连续重试同一视觉点。",
        ) from exc
    return {
        "pid": pid,
        "action": "VisualClick",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(workspace) == pid,
        "x": x,
        "y": y,
        "coordinate_space": "screen-physical",
    }


def post_mouse_click(quartz, point):
    down = quartz.CGEventCreateMouseEvent(
        None,
        quartz.kCGEventLeftMouseDown,
        point,
        quartz.kCGMouseButtonLeft,
    )
    up = quartz.CGEventCreateMouseEvent(
        None,
        quartz.kCGEventLeftMouseUp,
        point,
        quartz.kCGMouseButtonLeft,
    )
    if down is None or up is None:
        raise DesktopError(
            "desktop_click_failed",
            "unable to create Quartz mouse click event",
            "请确认 Cohert 运行进程具备必要的系统权限。",
        )
    quartz.CGEventPost(quartz.kCGHIDEventTap, down)
    time.sleep(0.03)
    quartz.CGEventPost(quartz.kCGHIDEventTap, up)


def post_mouse_drag(quartz, start_point, end_point):
    down = quartz.CGEventCreateMouseEvent(
        None,
        quartz.kCGEventLeftMouseDown,
        start_point,
        quartz.kCGMouseButtonLeft,
    )
    if down is None:
        raise DesktopError(
            "desktop_drag_failed",
            "unable to create Quartz mouse down event",
            "请确认 Cohert 运行进程具备必要的系统权限。",
        )
    quartz.CGEventPost(quartz.kCGHIDEventTap, down)
    time.sleep(0.08)
    steps = 8
    for index in range(1, steps + 1):
        ratio = index / steps
        point = (
            start_point[0] + (end_point[0] - start_point[0]) * ratio,
            start_point[1] + (end_point[1] - start_point[1]) * ratio,
        )
        moved = quartz.CGEventCreateMouseEvent(
            None,
            quartz.kCGEventLeftMouseDragged,
            point,
            quartz.kCGMouseButtonLeft,
        )
        if moved is None:
            raise DesktopError(
                "desktop_drag_failed",
                "unable to create Quartz mouse drag event",
                "请确认 Cohert 运行进程具备必要的系统权限。",
            )
        quartz.CGEventPost(quartz.kCGHIDEventTap, moved)
        time.sleep(0.025)
    up = quartz.CGEventCreateMouseEvent(
        None,
        quartz.kCGEventLeftMouseUp,
        end_point,
        quartz.kCGMouseButtonLeft,
    )
    if up is None:
        raise DesktopError(
            "desktop_drag_failed",
            "unable to create Quartz mouse up event",
            "请确认 Cohert 运行进程具备必要的系统权限。",
        )
    time.sleep(0.05)
    quartz.CGEventPost(quartz.kCGHIDEventTap, up)


def scroll(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_scroll requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    delta_x = int(payload.get("delta_x") or 0)
    delta_y = int(payload.get("delta_y") or 0)
    if delta_x == 0 and delta_y == 0:
        raise DesktopError(
            "desktop_scroll_bad_delta",
            "desktop_scroll requires a non-zero delta",
            "请提供明确的滚动方向和步数。",
        )
    delta_x = max(-2400, min(delta_x, 2400))
    delta_y = max(-2400, min(delta_y, 2400))
    try:
        event = quartz.CGEventCreateScrollWheelEvent(
            None,
            quartz.kCGScrollEventUnitPixel,
            2,
            delta_y,
            delta_x,
        )
        if event is None:
            raise DesktopError(
                "desktop_scroll_failed",
                "unable to create Quartz scroll event",
                "请确认 Cohert 运行进程具备必要的系统权限。",
            )
        quartz.CGEventPost(quartz.kCGHIDEventTap, event)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_scroll_failed",
            f"desktop scroll failed: {exc}",
            "请重新确认目标应用前台状态，不要连续重试。",
        ) from exc
    return {
        "pid": pid,
        "action": "Scroll",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(workspace) == pid,
        "delta_x": delta_x,
        "delta_y": delta_y,
    }


def drag(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_drag requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    coordinate_space = str(payload.get("coordinate_space") or "").strip()
    if coordinate_space != "screen-physical":
        raise DesktopError(
            "desktop_drag_bad_coordinate_space",
            f"desktop_drag requires screen-physical coordinates, got {coordinate_space!r}",
            "请通过 Cohert computer_drag 工具调用，不能直接传入截图坐标或任意坐标。",
        )
    start_x = int(payload.get("start_x") or 0)
    start_y = int(payload.get("start_y") or 0)
    end_x = int(payload.get("end_x") or 0)
    end_y = int(payload.get("end_y") or 0)
    if min(start_x, start_y, end_x, end_y) < 0:
        raise DesktopError(
            "desktop_drag_bad_point",
            "desktop_drag requires non-negative screen points",
            "请重新通过 computer_see/computer_find 获取当前 target_id。",
        )
    try:
        start_point = (
            physical_to_logical(start_x, display_scale(quartz)),
            physical_to_logical(start_y, display_scale(quartz)),
        )
        end_point = (
            physical_to_logical(end_x, display_scale(quartz)),
            physical_to_logical(end_y, display_scale(quartz)),
        )
        post_mouse_drag(quartz, start_point, end_point)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_drag_failed",
            f"desktop drag failed: {exc}",
            "请重新确认目标应用前台状态和目标位置，不要连续重试。",
        ) from exc
    return {
        "pid": pid,
        "action": "Drag",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(workspace) == pid,
        "start_x": start_x,
        "start_y": start_y,
        "end_x": end_x,
        "end_y": end_y,
        "coordinate_space": "screen-physical",
    }


def parse_key(quartz, raw_key):
    key = str(raw_key or "").strip()
    keycodes = {
        "Escape": 53,
        "Tab": 48,
        "Enter": 36,
        "Delete": 117,
        "Backspace": 51,
        "ArrowLeft": 123,
        "ArrowRight": 124,
        "ArrowDown": 125,
        "ArrowUp": 126,
        "PageUp": 116,
        "PageDown": 121,
        "Home": 115,
        "End": 119,
    }
    modifiers = 0
    if "+" in key:
        modifier, base = key.split("+", 1)
        if modifier == "Shift":
            modifiers |= quartz.kCGEventFlagMaskShift
        elif modifier == "Cmd":
            modifiers |= quartz.kCGEventFlagMaskCommand
        elif modifier == "Ctrl":
            modifiers |= quartz.kCGEventFlagMaskControl
        else:
            raise DesktopError(
                "desktop_press_key_unsupported",
                f"unsupported desktop key modifier: {modifier}",
                "请使用 Cohert desktop_press_key 工具提供的受限按键集合。",
            )
        key = base
    keycode = keycodes.get(key)
    if keycode is None:
        raise DesktopError(
            "desktop_press_key_unsupported",
            f"unsupported desktop key: {raw_key!r}",
            "请使用 Cohert desktop_press_key 工具提供的受限按键集合。",
        )
    return keycode, modifiers


def post_key_event(quartz, keycode, down, flags):
    event = quartz.CGEventCreateKeyboardEvent(None, keycode, down)
    if event is None:
        raise DesktopError(
            "desktop_press_key_failed",
            "unable to create Quartz keyboard event",
            "请确认 Cohert 运行进程具备必要的系统权限。",
        )
    quartz.CGEventSetFlags(event, flags)
    quartz.CGEventPost(quartz.kCGHIDEventTap, event)


def press_key(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_press_key requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    key = str(payload.get("key") or "").strip()
    keycode, modifiers = parse_key(quartz, key)
    try:
        post_key_event(quartz, keycode, True, modifiers)
        time.sleep(0.03)
        post_key_event(quartz, keycode, False, modifiers)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_press_key_failed",
            f"desktop key event failed: {exc}",
            "请重新确认目标应用前台状态，不要连续重试。",
        ) from exc
    active_after = active_pid(workspace) == pid
    return {
        "pid": pid,
        "key": key,
        "action": "PressKey",
        "performed": True,
        "active_before": True,
        "active_after": active_after,
    }


def post_cmd_v(quartz):
    keycode_v = 9
    flags = quartz.kCGEventFlagMaskCommand
    post_key_event(quartz, keycode_v, True, flags)
    time.sleep(0.03)
    post_key_event(quartz, keycode_v, False, flags)


def clipboard_write(payload):
    text = str(payload.get("text") or "")
    if not text:
        raise DesktopError(
            "desktop_clipboard_write_bad_request",
            "clipboard_write requires non-empty text",
            "请提供要写入剪贴板的文本；工具不会读取或返回原剪贴板内容。",
        )
    try:
        from AppKit import NSPasteboard

        pasteboard = NSPasteboard.generalPasteboard()
        pasteboard.clearContents()
        ok = pasteboard.setString_forType_(text, "public.utf8-plain-text")
        if not ok:
            ok = pasteboard.setString_forType_(text, "NSStringPboardType")
        if not ok:
            raise RuntimeError("NSPasteboard rejected text")
    except Exception as exc:
        raise DesktopError(
            "desktop_clipboard_write_failed",
            f"unable to write clipboard: {exc}",
            "请确认当前会话允许访问系统剪贴板；不要自动读取或回显剪贴板内容。",
        ) from exc
    return {
        "action": "ClipboardWrite",
        "performed": True,
        "text_length": len(text),
        "line_count": text.count("\n") + 1,
        "text_returned": False,
    }


def clipboard_paste(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "clipboard_paste requires a positive pid",
            "请先通过 computer_see 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    try:
        post_cmd_v(quartz)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_clipboard_paste_failed",
            f"unable to paste clipboard: {exc}",
            "请重新确认目标应用前台状态和输入焦点。",
        ) from exc
    return {
        "pid": pid,
        "action": "ClipboardPaste",
        "performed": True,
        "active_before": True,
        "active_after": active_pid(workspace) == pid,
    }


def focused_metadata(pid):
    quartz, _, _ = require_macos_modules()
    api = load_ax()
    if not bool(api["trusted"]()):
        raise DesktopError(
            "desktop_permission_denied",
            "Accessibility permission is not granted",
            "系统设置 -> 隐私与安全性 -> 辅助功能：允许运行 Cohert 的终端或 IDE。",
        )
    app = api["application"](pid)
    element = ax_attr(api, app, api["focused"])
    if element is None:
        raise DesktopError(
            "desktop_type_text_focus_unavailable",
            "Accessibility did not return a focused UI element for the target app",
            "请先用 desktop_ax_press 或手动点击把光标放到目标输入框，再重试 desktop_type_text。",
        )
    return ax_node_metadata(api, element, display_scale(quartz))


def require_editable_focus(metadata):
    role = str(metadata.get("role") or "")
    role_lower = role.lower()
    if role in {"AXTextField", "AXTextArea", "AXSearchField", "AXComboBox"}:
        return
    if "text" in role_lower and "static" not in role_lower:
        return
    raise DesktopError(
        "desktop_type_text_focus_not_editable",
        f"focused UI element is not editable: role={role!r}",
        "请先聚焦输入框；不要在未知焦点下发送文本。",
    )


def post_unicode_text(quartz, text):
    # Split to keep each event small enough for Quartz and avoid partial
    # failures on long drafts. The Go tool bounds total length before this.
    chunk_size = 64
    for start in range(0, len(text), chunk_size):
        chunk = text[start : start + chunk_size]
        event = quartz.CGEventCreateKeyboardEvent(None, 0, True)
        if event is None:
            raise DesktopError(
                "desktop_type_text_failed",
                "unable to create Quartz unicode keyboard event",
                "请确认 Cohert 运行进程具备必要的系统权限。",
            )
        quartz.CGEventKeyboardSetUnicodeString(event, len(chunk), chunk)
        quartz.CGEventPost(quartz.kCGHIDEventTap, event)
        time.sleep(0.01)


def type_text(payload):
    quartz, _, workspace = require_macos_modules()
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_type_text requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    text = str(payload.get("text") or "")
    allow_visual_focus = bool(payload.get("allow_visual_focus", False))
    if not text:
        raise DesktopError(
            "desktop_type_text_bad_request",
            "desktop_type_text requires non-empty text",
            "请提供要输入的文本；发送动作仍需单独使用 desktop_press_key 并确认。",
        )
    focus_verification = "ax_editable"
    try:
        metadata = focused_metadata(pid)
        try:
            require_editable_focus(metadata)
        except DesktopError:
            if not allow_visual_focus:
                raise
            focus_verification = "visual_token"
    except DesktopError:
        if not allow_visual_focus:
            raise
        metadata = {"role": "", "title": "", "description": ""}
        focus_verification = "visual_token"
    try:
        post_unicode_text(quartz, text)
        time.sleep(0.15)
    except DesktopError:
        raise
    except Exception as exc:
        raise DesktopError(
            "desktop_type_text_failed",
            f"desktop text input failed: {exc}",
            "请重新确认目标应用前台状态和输入框焦点，不要连续重试。",
        ) from exc
    active_after = active_pid(workspace) == pid
    return {
        "pid": pid,
        "action": "TypeText",
        "performed": True,
        "active_before": True,
        "active_after": active_after,
        "text_length": len(text),
        "line_count": text.count("\n") + 1,
        "focus_role": metadata["role"],
        "focus_title": metadata["title"],
        "focus_description": metadata["description"],
        "focus_verification": focus_verification,
    }


def ax_snapshot(payload):
    quartz, _, _ = require_macos_modules()
    api = load_ax()
    if not bool(api["trusted"]()):
        raise DesktopError(
            "desktop_permission_denied",
            "Accessibility permission is not granted",
            "系统设置 -> 隐私与安全性 -> 辅助功能：允许运行 Cohert 的终端或 IDE。",
        )
    pid = int(payload.get("pid") or 0)
    if pid <= 0:
        raise DesktopError(
            "desktop_bad_pid",
            "desktop_ax_snapshot requires a positive pid",
            "请先通过 desktop_windows 获取目标窗口对应的 pid。",
        )
    require_active_pid(pid)
    max_depth = max(1, min(int(payload.get("max_depth") or 8), 12))
    max_nodes = max(1, min(int(payload.get("max_nodes") or 300), 500))
    include_zero_size = bool(payload.get("include_zero_size", False))
    scale = display_scale(quartz)
    state = {"count": 0, "truncated": False}

    def walk(element, node_id, depth, force_include=False):
        if depth > max_depth:
            state["truncated"] = True
            return []
        if state["count"] >= max_nodes:
            state["truncated"] = True
            return []
        role = str(ax_attr(api, element, api["role"]) or "")
        title = str(ax_attr(api, element, api["title"]) or "")
        description = str(ax_attr(api, element, api["description"]) or "")
        value = ax_attr(api, element, api["value_attribute"])
        bounds = ax_bounds(api, element, scale)
        children = ax_attr(api, element, api["children"]) or []
        if force_include and not children:
            children = ax_attr(api, element, api["windows"]) or []

        visible = bounds["width"] > 0 and bounds["height"] > 0
        include = force_include or include_zero_size or visible
        if not include:
            child_nodes = []
            for index, child in enumerate(children):
                child_nodes.extend(walk(child, f"{node_id}/{index}", depth + 1))
            return child_nodes

        # Reserve this node before descending so max_nodes bounds the entire
        # returned tree, not just sibling subtrees completed earlier.
        state["count"] += 1
        child_nodes = []
        for index, child in enumerate(children):
            child_nodes.extend(walk(child, f"{node_id}/{index}", depth + 1))

        safe_value = ""
        if "secure" not in role.lower() and isinstance(value, (str, int, float, bool)):
            safe_value = str(value)
        enabled = ax_attr(api, element, api["enabled"])
        enabled_value = bool(enabled) if isinstance(enabled, (bool, int)) else None
        return [
            {
                "id": node_id,
                "role": role,
                "title": title,
                "value": safe_value,
                "description": description,
                "enabled": enabled_value,
                "bounds": bounds,
                "actions": ax_actions(api, element),
                "children": child_nodes,
            }
        ]

    roots = walk(api["application"](pid), "ax:0", 0, force_include=True)
    if not roots:
        raise DesktopError(
            "desktop_ax_unavailable",
            f"Accessibility did not return an application tree for pid {pid}",
            "请确认目标应用仍在运行，并重新调用 desktop_windows。",
        )
    return {
        "pid": pid,
        "root": roots[0],
        "node_count": state["count"],
        "truncated": state["truncated"],
    }


def dispatch(command, payload):
    commands = {
        "permissions": permissions,
        "list_windows": list_windows,
        "activate": activate,
        "screenshot": screenshot,
        "ax_snapshot": ax_snapshot,
        "ax_press": ax_press,
        "ax_focus": ax_focus,
        "click": click,
        "visual_click": visual_click,
        "press_key": press_key,
        "type_text": type_text,
        "scroll": scroll,
        "drag": drag,
        "clipboard_write": clipboard_write,
        "clipboard_paste": clipboard_paste,
    }
    handler = commands.get(command)
    if handler is None:
        raise DesktopError(
            "desktop_unknown_command",
            f"unsupported desktop helper command: {command}",
            "请使用 Cohert 已注册的 desktop 工具，不要直接调用未实现的 helper command。",
        )
    return handler(payload)


def main():
    args = parse_args()
    payload = parse_payload(args.payload)
    emit_success(dispatch(args.command, payload))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except DesktopError as exc:
        emit_error(exc.code, exc.message, exc.hint)
        sys.exit(0)
    except Exception as exc:
        emit_error(
            "desktop_helper_failed",
            f"desktop helper failed: {exc}",
            "请检查 macOS 权限、PyObjC 依赖和目标应用状态。",
        )
        sys.exit(0)
