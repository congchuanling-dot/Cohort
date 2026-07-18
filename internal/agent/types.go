package agent

import (
	"fmt"
	"io"

	"cohert/internal/llm"
)

type Outcome struct {
	Data       any
	NextPrompt string
	ShouldExit bool
}

type RunResult struct {
	Status   string
	Response *llm.Response
}

type OutputSink interface {
	WriteText(text string)
	WriteToolCall(call llm.ToolCall)
	WriteToolResult(name string, result string)
	WriteError(err error)
}

type ConsoleSink struct {
	out io.Writer
}

func NewConsoleSink(out io.Writer) *ConsoleSink {
	return &ConsoleSink{out: out}
}

func (s *ConsoleSink) WriteText(text string) {
	fmt.Fprint(s.out, text)
}

func (s *ConsoleSink) WriteToolCall(call llm.ToolCall) {
	fmt.Fprintf(s.out, "\n\nTool: %s\nArgs: %s\n", call.Function.Name, call.Function.Arguments)
}

func (s *ConsoleSink) WriteToolResult(name string, result string) {
	if len(result) > 800 {
		result = result[:400] + "\n...[omitted]...\n" + result[len(result)-300:]
	}
	fmt.Fprintf(s.out, "Result(%s): %s\n", name, result)
}

func (s *ConsoleSink) WriteError(err error) {
	fmt.Fprintf(s.out, "\n[error] %v\n", err)
}
