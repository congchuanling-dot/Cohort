package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"cohert/internal/agent"
	"cohert/internal/llm"
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
	// ToolNameBrowserTabs 列出浏览器标签页。
	ToolNameBrowserTabs = "browser_tabs"
	// ToolNameBrowserOpen 打开或导航浏览器页面。
	ToolNameBrowserOpen = "browser_open"
	// ToolNameBrowserScan 读取浏览器页面正文。
	ToolNameBrowserScan = "browser_scan"
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
	// ToolNameBrowserWaitForLoad 等待页面基础加载完成。
	ToolNameBrowserWaitForLoad = "browser_wait_for_load"
	// ToolNameBrowserWaitForSelector 等待元素达到指定状态。
	ToolNameBrowserWaitForSelector = "browser_wait_for_selector"
	// ToolNameBrowserWaitForText 等待页面出现指定文本。
	ToolNameBrowserWaitForText = "browser_wait_for_text"
	// ToolNameBrowserWaitForStable 等待页面轻量状态稳定。
	ToolNameBrowserWaitForStable = "browser_wait_for_stable"
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
		ToolNameBrowserExecuteJS,
		ToolNameBrowserClick,
		ToolNameBrowserClickElement,
		ToolNameBrowserType,
		ToolNameBrowserTypeElement,
		ToolNameBrowserWaitForLoad,
		ToolNameBrowserWaitForSelector,
		ToolNameBrowserWaitForText,
		ToolNameBrowserWaitForStable,
		ToolNameAskUser,
	}
	seen := map[string]bool{}
	for _, name := range order {
		if tool, ok := r.tools[name]; ok {
			schemas = append(schemas, tool.Schema())
			seen[name] = true
		}
	}
	for name, tool := range r.tools {
		if !seen[name] {
			schemas = append(schemas, tool.Schema())
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

// stringProp/intProp/boolProp 是工具 schema 的小工具函数，减少重复 map 写法。
func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "default": def}
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
