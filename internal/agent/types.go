package agent

import (
	"fmt"
	"io"

	"cohert/internal/llm"
)

// Outcome 是工具执行后的统一结果。
// Data 会回灌给模型，ShouldExit 用于工具主动终止当前 Agent。
type Outcome struct {
	Data       any
	NextPrompt string
	ShouldExit bool
}

// RunResult 表示一次 Runner.Run 的最终状态。
type RunResult struct {
	Status   string
	Response *llm.Response
}

// OutputSink 是 Agent 输出接口。命令行、测试、未来 UI 都可以实现它。
type OutputSink interface {
	WriteText(text string)
	WriteToolCall(call llm.ToolCall)
	WriteToolResult(name string, result string)
	WriteError(err error)
}

// ConsoleSink 把 Agent 输出写到终端。
type ConsoleSink struct {
	out io.Writer
}

// NewConsoleSink 创建终端输出器，通常传 os.Stdout。
func NewConsoleSink(out io.Writer) *ConsoleSink {
	return &ConsoleSink{out: out}
}

// WriteText 写入模型流式文本。
func (s *ConsoleSink) WriteText(text string) {
	fmt.Fprint(s.out, text)
}

// WriteToolCall 打印模型发起的工具调用和参数。
func (s *ConsoleSink) WriteToolCall(call llm.ToolCall) {
	fmt.Fprintf(s.out, "\n\nTool: %s\nArgs: %s\n", call.Function.Name, call.Function.Arguments)
}

// WriteToolResult 打印工具结果。过长结果会截断，避免终端输出爆炸。
func (s *ConsoleSink) WriteToolResult(name string, result string) {
	if len(result) > 800 {
		result = result[:400] + "\n...[omitted]...\n" + result[len(result)-300:]
	}
	fmt.Fprintf(s.out, "Result(%s): %s\n", name, result)
}

// WriteError 打印模型或工具执行错误。
func (s *ConsoleSink) WriteError(err error) {
	fmt.Fprintf(s.out, "\n[error] %v\n", err)
}
