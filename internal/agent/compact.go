package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cohert/internal/contextmgr"
	"cohert/internal/llm"
)

const (
	memoryGenerationSystemPrompt = `你是 Cohert 的 Session Memory 生成器。

你的任务是从会话历史中提取稳定事实，生成 memory.md。
只记录对后续继续这个 session 有长期价值的信息。
不要记录临时命令输出、无关寒暄、重复过程或模型中间废话。
必须使用用户提供的 markdown 结构；没有内容的栏目写 "- 暂无"。`

	maxMemorySourceChars = 120000
	memorySourceHead     = 60000
	memorySourceTail     = 60000
)

// CompactMemoryResult 描述 /compact 生成 session memory 的结果。
type CompactMemoryResult struct {
	SessionID string
	Path      string
	Chars     int
}

// CompactSessionMemory 调用模型把当前 Runner.history 压缩成 session memory，并写入 memory.md。
//
// 这不是普通 Agent 任务：它不会调用工具，也不会把生成的 memory 写入 history.jsonl。
// 生成后的 memory.md 会在后续请求前由 contextmgr 读取并注入 request messages。
func (r *Runner) CompactSessionMemory(ctx context.Context) (CompactMemoryResult, error) {
	if r.Client == nil {
		return CompactMemoryResult{}, errors.New("compact requires llm client")
	}
	if r.SessionStore == nil || r.sessionID == "" {
		return CompactMemoryResult{}, errors.New("compact requires an active session")
	}
	if len(r.history) == 0 {
		return CompactMemoryResult{}, errors.New("compact requires non-empty history")
	}

	sessionDir := r.sessionDir()
	if strings.TrimSpace(sessionDir) == "" {
		return CompactMemoryResult{}, errors.New("compact cannot resolve session directory")
	}

	prompt := buildMemoryGenerationPrompt(r.history)
	stream, err := r.Client.Chat(ctx, llm.ChatRequest{
		System: memoryGenerationSystemPrompt,
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: prompt,
		}},
	})
	if err != nil {
		return CompactMemoryResult{}, err
	}
	resp, err := consume(stream, silentSink{})
	if err != nil {
		return CompactMemoryResult{}, err
	}
	memory := strings.TrimSpace(resp.Content)
	if memory == "" {
		return CompactMemoryResult{}, errors.New("compact returned empty memory")
	}
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return CompactMemoryResult{}, err
	}
	path := filepath.Join(sessionDir, contextmgr.SessionMemoryFileName)
	if err := os.WriteFile(path, []byte(memory+"\n"), 0644); err != nil {
		return CompactMemoryResult{}, err
	}
	return CompactMemoryResult{
		SessionID: r.sessionID,
		Path:      path,
		Chars:     len([]rune(memory)),
	}, nil
}

func buildMemoryGenerationPrompt(history []llm.Message) string {
	rendered := limitMemorySource(renderMessagesForMemory(history))
	return `请从下面的 Cohert 会话历史中生成 session memory。

输出必须严格使用这个 markdown 结构：

# Session Memory

## 用户目标

- ...

## 用户偏好

- ...

## 项目约定

- ...

## 已完成事项

- ...

## 关键文件

- ...

## 错误和修复

- ...

## 当前状态

- ...

## 下一步

- ...

要求：
- 只提取稳定事实。
- 不记录临时命令输出。
- 不记录一次性中间过程。
- 不编造历史里没有的信息。
- 如果某一栏没有内容，写 "- 暂无"。

会话历史：

` + rendered
}

func renderMessagesForMemory(messages []llm.Message) string {
	var b strings.Builder
	for i, message := range messages {
		fmt.Fprintf(&b, "\n--- message %d role=%s ---\n", i+1, message.Role)
		if message.Name != "" {
			fmt.Fprintf(&b, "name: %s\n", message.Name)
		}
		if message.ToolCallID != "" {
			fmt.Fprintf(&b, "tool_call_id: %s\n", message.ToolCallID)
		}
		for _, call := range message.ToolCalls {
			fmt.Fprintf(&b, "tool_call: id=%s name=%s args=%s\n", call.ID, call.Function.Name, call.Function.Arguments)
		}
		if strings.TrimSpace(message.Content) == "" {
			b.WriteString("(empty)\n")
		} else {
			b.WriteString(message.Content)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func limitMemorySource(text string) string {
	runes := []rune(text)
	if len(runes) <= maxMemorySourceChars {
		return text
	}
	head := string(runes[:memorySourceHead])
	tail := string(runes[len(runes)-memorySourceTail:])
	return head + "\n\n[... earlier memory generation source omitted by Cohert ...]\n\n" + tail
}

type silentSink struct{}

func (silentSink) WriteText(string)               {}
func (silentSink) WriteToolCall(llm.ToolCall)     {}
func (silentSink) WriteToolResult(string, string) {}
func (silentSink) WriteError(error)               {}
