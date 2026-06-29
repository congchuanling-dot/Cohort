package builtin

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"cohort/internal/action"
	"cohort/internal/foundation"
	"cohort/internal/llm"
)

// WriteCode 根据技术设计生成代码。
type WriteCode struct {
	*action.BaseAction
	OutputRoot string // 输出根目录，所有生成的文件都写在此目录下（空 = 当前工作目录）
}

const codeSystemPrompt = `You are a senior Software Engineer with 15 years of Go experience.

Your task is to write production-quality Go code based on the technical design document.

Requirements:
- Write clean, idiomatic Go code with proper error handling
- Use only the Go standard library (no third-party dependencies)
- Include package declarations, imports, and complete function bodies
- Add meaningful comments for exported types and functions
- Output the complete, runnable code file(s) in markdown code blocks with file paths

Output format:

` + "```" + `go filename: main.go
// complete code
` + "```" + `

` + "```" + `go filename: internal/xxx/xxx.go
// complete code
` + "```" + `

Explain briefly how to run the code at the end.`

// NewWriteCode 创建一个 WriteCode Action。
func NewWriteCode(client llm.Client) *WriteCode {
	return &WriteCode{
		BaseAction: action.NewBaseAction("WriteCode", codeSystemPrompt, client),
	}
}

// SetOutputRoot 设置输出根目录。生成的所有文件都会写在这个目录下。
// 调用方（如 demo）在 Hire 之前设置，防止生成的文件污染项目根目录。
func (a *WriteCode) SetOutputRoot(dir string) {
	a.OutputRoot = dir
}

// Run 执行代码生成。
func (a *WriteCode) Run(ctx context.Context, history []*foundation.Message) (*action.ActionOutput, error) {
	design := findLatestByAction(history, "WriteDesign")
	if design == "" {
		design = "请根据上下文中的技术设计写代码"
	}

	prompt := fmt.Sprintf(
		"Please write the complete Go code based on the following technical design:\n\n%s\n\n"+
			"Write complete, runnable code. Every file should be in a separate code block with the file path. "+
			"Use only Go standard library.",
		design,
	)

	content, err := a.AskLLM(ctx, prompt, history)
	if err != nil {
		return nil, err
	}

	// ★ 有 Tool 时：自动提取代码块、写入磁盘、编译
	toolResults := ""
	if a.Tools().Count() > 0 {
		toolResults = a.saveAndBuild(ctx, content)
	}

	return &action.ActionOutput{Content: content + toolResults}, nil
}

// saveAndBuild 从 LLM 输出中提取代码块，写入磁盘，尝试编译。
func (a *WriteCode) saveAndBuild(ctx context.Context, llmOutput string) string {
	var results []string

	// 提取所有 ```go filename: xxx 代码块
	codeBlockPattern := "(?s)" +
		"\\`\\`\\`" + "go" + "\\s+filename:\\s*(\\S+)\\s*\\n" +
		"(.+?)" +
		"\\`\\`\\`"
	re := regexp.MustCompile(codeBlockPattern)
	matches := re.FindAllStringSubmatch(llmOutput, -1)

	if len(matches) == 0 {
		return "\n\n警告: 未检测到代码块，跳过文件写入。"
	}

	results = append(results, fmt.Sprintf("\n\n## Tool 执行结果\n"))

	for _, match := range matches {
		filename := strings.TrimSpace(match[1])
		code := match[2]

		// ★ 拼到输出根目录下，防止污染项目根目录
		writePath := filename
		if a.OutputRoot != "" {
			writePath = filepath.Join(a.OutputRoot, filename)
		}
		// 安全检查：拒绝 .. 路径逃逸
		if strings.Contains(writePath, "..") {
			results = append(results, fmt.Sprintf("⚠️ 跳过不安全路径: %s", writePath))
			continue
		}

		// 用 WriteFile Tool 写入磁盘
		writeResult, err := a.CallTool(ctx, "WriteFile", map[string]any{
			"path":    writePath,
			"content": code,
		})
		if err != nil {
			results = append(results, fmt.Sprintf("❌ 写入 %s 失败: %v", filename, err))
		} else {
			results = append(results, writeResult)
		}
	}

	// 用 RunCommand Tool 尝试编译（在输出目录中执行）
	buildArgs := map[string]any{"command": "go build ./..."}
	if a.OutputRoot != "" {
		buildArgs["workdir"] = a.OutputRoot
	}
	buildResult, err := a.CallTool(ctx, "RunCommand", buildArgs)
	if err != nil {
		results = append(results, fmt.Sprintf("❌ 编译失败: %v", err))
	} else {
		results = append(results, buildResult)
	}

	return "\n" + strings.Join(results, "\n")
}
