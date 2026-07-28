package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"cohort/internal/agent"
	"cohort/internal/llm"
)

const (
	// ToolNameFileRead 读取文本文件。
	ToolNameFileRead = "file_read"
	// ToolNameFileWrite 创建或修改文本文件。
	ToolNameFileWrite = "file_write"
	// ToolNameFilePatch 替换文件中的唯一文本块。
	ToolNameFilePatch = "file_patch"
	// ToolNameCodeRun 在工作区执行 shell 命令。
	ToolNameCodeRun = "code_run"
	// ToolNameAskUser 在命令行向用户提问。
	ToolNameAskUser = "ask_user"
	// ToolNameSkillRead 读取已发现 Skill 的 SKILL.md 正文。
	ToolNameSkillRead = "skill_read"
	// ToolNameUpdateWorkingCheckpoint 更新短期工作记忆。
	ToolNameUpdateWorkingCheckpoint = "update_working_checkpoint"
	// ToolNameStartLongTermUpdate 启动受控长期记忆沉淀流程。
	ToolNameStartLongTermUpdate = "start_long_term_update"
	// ToolNameMemoryProposeUpdate 提交长期记忆候选，不直接写入。
	ToolNameMemoryProposeUpdate = "memory_propose_update"
	// ToolNameMemoryApplyUpdate 应用已验证的长期记忆 append 更新。
	ToolNameMemoryApplyUpdate = "memory_apply_update"
	// ToolNameBrowserTabs 列出浏览器标签页。
	ToolNameBrowserTabs = "browser_tabs"
	// ToolNameBrowserOpen 打开或导航浏览器页面。
	ToolNameBrowserOpen = "browser_open"
	// ToolNameBrowserScan 读取浏览器页面正文。
	ToolNameBrowserScan = "browser_scan"
	// ToolNameBrowserDOMSummary 返回低噪声 DOM/表单/iframe/shadowRoot 摘要。
	ToolNameBrowserDOMSummary = "browser_dom_summary"
	// ToolNameBrowserExecuteJS 在浏览器页面中执行 JavaScript。
	ToolNameBrowserExecuteJS = "browser_execute_js"
	// ToolNameBrowserCDP 向浏览器发送原始 CDP 命令。
	// 这是内部调试工具，默认不注册给模型；普通动作通过 browser_execute_js JSON 路由或高层工具完成。
	ToolNameBrowserCDP = "browser_cdp"
	// ToolNameBrowserClick 使用 CDP 鼠标事件点击 viewport 坐标。
	ToolNameBrowserClick = "browser_click"
	// ToolNameBrowserClickElement 按 CSS selector 定位并点击元素。
	ToolNameBrowserClickElement = "browser_click_element"
	// ToolNameBrowserType 使用 CDP 键盘输入向当前焦点输入文本。
	ToolNameBrowserType = "browser_type"
	// ToolNameBrowserTypeElement 按 CSS selector 聚焦元素并输入文本。
	ToolNameBrowserTypeElement = "browser_type_element"
	// ToolNameBrowserPressKey 发送真实键盘按键或组合键。
	ToolNameBrowserPressKey = "browser_press_key"
	// ToolNameBrowserSnapshot 返回页面可交互元素摘要。
	ToolNameBrowserSnapshot = "browser_snapshot"
	// ToolNameBrowserWaitForLoad 等待页面基础加载完成。
	ToolNameBrowserWaitForLoad = "browser_wait_for_load"
	// ToolNameBrowserWaitForSelector 等待元素达到指定状态。
	ToolNameBrowserWaitForSelector = "browser_wait_for_selector"
	// ToolNameBrowserWaitForText 等待页面出现指定文本。
	ToolNameBrowserWaitForText = "browser_wait_for_text"
	// ToolNameBrowserWaitForURL 等待页面 URL 达到指定条件。
	ToolNameBrowserWaitForURL = "browser_wait_for_url"
	// ToolNameBrowserWaitForStable 等待页面轻量状态稳定。
	ToolNameBrowserWaitForStable = "browser_wait_for_stable"
	// ToolNameBrowserScreenshot 截取浏览器页面并保存图片。
	ToolNameBrowserScreenshot = "browser_screenshot"
	// ToolNameBrowserOCR 对浏览器截图或 workspace 内图片执行只读 OCR。
	ToolNameBrowserOCR = "browser_ocr"
	// ToolNameDesktopPermissions 检查桌面感知所需系统权限。
	ToolNameDesktopPermissions = "desktop_permissions"
	// ToolNameDesktopWindows 枚举可见桌面窗口。
	ToolNameDesktopWindows = "desktop_windows"
	// ToolNameDesktopActivate 激活目标 PID 对应的桌面应用。
	ToolNameDesktopActivate = "desktop_activate"
	// ToolNameDesktopScreenshot 截取目标桌面窗口。
	ToolNameDesktopScreenshot = "desktop_screenshot"
	// ToolNameDesktopAXSnapshot 返回目标应用的 AX 控件树。
	ToolNameDesktopAXSnapshot = "desktop_ax_snapshot"
	// ToolNameDesktopOCR 对桌面截图执行只读 OCR。
	ToolNameDesktopOCR = "desktop_ocr"
	// ToolNameDesktopAXPress 对已验证的 AX 节点执行受控语义点击。
	ToolNameDesktopAXPress = "desktop_ax_press"
	// ToolNameDesktopAXFocus 对已验证的可编辑 AX 节点设置输入焦点。
	ToolNameDesktopAXFocus = "desktop_ax_focus"
	// ToolNameDesktopClick 对已验证 AX 节点的中心点执行受控物理点击。
	ToolNameDesktopClick = "desktop_click"
	// ToolNameDesktopVisualClick 对截图/OCR bbox 执行受控视觉物理点击。
	ToolNameDesktopVisualClick = "desktop_visual_click"
	// ToolNameDesktopPressKey 对前台目标 PID 执行受限桌面按键。
	ToolNameDesktopPressKey = "desktop_press_key"
	// ToolNameDesktopTypeText 对前台目标 PID 的当前输入焦点起草文本。
	ToolNameDesktopTypeText = "desktop_type_text"
	// ToolNameComputerSee 观察当前电脑状态并缓存候选目标。
	ToolNameComputerSee = "computer_see"
	// ToolNameComputerFind 从最近一次 computer_see 中查找可操作目标。
	ToolNameComputerFind = "computer_find"
	// ToolNameComputerClick 点击缓存的 computer target。
	ToolNameComputerClick = "computer_click"
	// ToolNameComputerDoubleClick 双击缓存的 computer target。
	ToolNameComputerDoubleClick = "computer_double_click"
	// ToolNameComputerRightClick 右键点击缓存的 computer target。
	ToolNameComputerRightClick = "computer_right_click"
	// ToolNameComputerType 向缓存输入目标起草文本。
	ToolNameComputerType = "computer_type"
	// ToolNameComputerPress 对最近 computer_see 的目标窗口发送受限按键。
	ToolNameComputerPress = "computer_press"
	// ToolNameComputerWait 等待最近 computer_see 的目标窗口出现指定状态。
	ToolNameComputerWait = "computer_wait"
	// ToolNameComputerCheck 验证当前 GUI 状态。
	ToolNameComputerCheck = "computer_check"
	// ToolNameComputerScroll 在最近 computer_see 的目标窗口中滚动。
	ToolNameComputerScroll = "computer_scroll"
	// ToolNameComputerDrag 从缓存 target 拖拽到另一个 target 或相对偏移。
	ToolNameComputerDrag = "computer_drag"
	// ToolNameComputerDrop 从缓存 source target 拖放到缓存 destination target。
	ToolNameComputerDrop = "computer_drop"
	// ToolNameComputerClipboardWrite 写入系统剪贴板但不读取原内容。
	ToolNameComputerClipboardWrite = "computer_clipboard_write"
	// ToolNameComputerPaste 将可选文本写入剪贴板后粘贴到目标窗口。
	ToolNameComputerPaste = "computer_paste"
	// ToolNameComputerWindowSwitch 切换到匹配的桌面窗口。
	ToolNameComputerWindowSwitch = "computer_window_switch"
	// ToolNameComputerMenu 选择目标应用菜单项。
	ToolNameComputerMenu = "computer_menu"
	// ToolNameComputerFileDialog 在文件对话框中跳转路径并可选确认。
	ToolNameComputerFileDialog = "computer_file_dialog"
	// ToolNameComputerWindowMove 移动目标窗口。
	ToolNameComputerWindowMove = "computer_window_move"
	// ToolNameComputerWindowResize 调整目标窗口尺寸。
	ToolNameComputerWindowResize = "computer_window_resize"
	// ToolNameComputerVisualSnapshot 返回最近 computer_see 的视觉候选快照。
	ToolNameComputerVisualSnapshot = "computer_visual_snapshot"
	// ToolNameComputerExecuteStep 执行单步 Observe-Act-Verify GUI 动作。
	ToolNameComputerExecuteStep = "computer_execute_step"
	// ToolNameComputerExecutePlan 执行多步 Observe-Act-Verify GUI 计划。
	ToolNameComputerExecutePlan = "computer_execute_plan"
)

// Tool 是所有本地工具必须实现的接口。
// Runner 通过 Registry 调用工具，不直接依赖具体工具类型。
type Tool interface {
	Name() string
	Schema() llm.ToolSchema
	Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error)
}

// Registry 保存工具名到工具实例的映射，负责 schema 输出和工具分发。
type Registry struct {
	// tools 保存工具名到工具实例的映射。
	tools map[string]Tool
}

// NewRegistry 创建空工具注册表。
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register 把一个工具注册到模型可调用列表中。
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Schemas 返回给模型看的工具定义。
// 固定顺序可以让输出更稳定，方便调试和测试。
func (r *Registry) Schemas() []llm.ToolSchema {
	schemas := make([]llm.ToolSchema, 0, len(r.tools))
	order := []string{
		ToolNameFileRead,
		ToolNameFileWrite,
		ToolNameFilePatch,
		ToolNameCodeRun,
		ToolNameBrowserTabs,
		ToolNameBrowserOpen,
		ToolNameBrowserScan,
		ToolNameBrowserDOMSummary,
		ToolNameBrowserExecuteJS,
		ToolNameBrowserClick,
		ToolNameBrowserClickElement,
		ToolNameBrowserType,
		ToolNameBrowserTypeElement,
		ToolNameBrowserPressKey,
		ToolNameBrowserSnapshot,
		ToolNameBrowserWaitForLoad,
		ToolNameBrowserWaitForSelector,
		ToolNameBrowserWaitForText,
		ToolNameBrowserWaitForURL,
		ToolNameBrowserWaitForStable,
		ToolNameBrowserScreenshot,
		ToolNameBrowserOCR,
		ToolNameDesktopPermissions,
		ToolNameDesktopWindows,
		ToolNameDesktopActivate,
		ToolNameDesktopScreenshot,
		ToolNameDesktopAXSnapshot,
		ToolNameDesktopOCR,
		ToolNameDesktopAXPress,
		ToolNameDesktopAXFocus,
		ToolNameDesktopClick,
		ToolNameDesktopVisualClick,
		ToolNameDesktopPressKey,
		ToolNameDesktopTypeText,
		ToolNameComputerSee,
		ToolNameComputerFind,
		ToolNameComputerClick,
		ToolNameComputerDoubleClick,
		ToolNameComputerRightClick,
		ToolNameComputerType,
		ToolNameComputerPress,
		ToolNameComputerWait,
		ToolNameComputerCheck,
		ToolNameComputerScroll,
		ToolNameComputerDrag,
		ToolNameComputerDrop,
		ToolNameComputerClipboardWrite,
		ToolNameComputerPaste,
		ToolNameComputerWindowSwitch,
		ToolNameComputerMenu,
		ToolNameComputerFileDialog,
		ToolNameComputerWindowMove,
		ToolNameComputerWindowResize,
		ToolNameComputerVisualSnapshot,
		ToolNameComputerExecuteStep,
		ToolNameComputerExecutePlan,
		ToolNameSkillRead,
		ToolNameUpdateWorkingCheckpoint,
		ToolNameStartLongTermUpdate,
		ToolNameMemoryProposeUpdate,
		ToolNameMemoryApplyUpdate,
		ToolNameAskUser,
	}
	seen := map[string]bool{}
	for _, name := range order {
		if tool, ok := r.tools[name]; ok {
			schemas = append(schemas, tool.Schema())
			seen[name] = true
		}
	}
	for _, name := range r.toolNames() {
		if !seen[name] {
			schemas = append(schemas, r.tools[name].Schema())
		}
	}
	return schemas
}

// Run 根据模型返回的工具名找到具体工具并执行。
func (r *Registry) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	tool, ok := r.tools[call.Name]
	if !ok {
		names := r.toolNames()
		return agent.Outcome{
			Data: agent.NewToolError(
				"unknown_tool",
				"unknown tool: "+call.Name,
				"请改用当前可用工具之一："+strings.Join(names, "、"),
			),
			NextPrompt: "未知工具 " + call.Name,
		}, fmt.Errorf("unknown tool %q", call.Name)
	}
	return tool.Run(ctx, call)
}

func (r *Registry) toolNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// objectSchema 生成工具参数的 JSON Schema object。
func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// stringProp/intProp/floatProp/boolProp 是工具 schema 的小工具函数，减少重复 map 写法。
func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "default": def}
}

func floatProp(desc string, def float64) map[string]any {
	return map[string]any{"type": "number", "description": desc, "default": def}
}

func numberProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

func boolProp(desc string, def bool) map[string]any {
	return map[string]any{"type": "boolean", "description": desc, "default": def}
}

func objectProp(desc string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          desc,
		"additionalProperties": true,
	}
}
