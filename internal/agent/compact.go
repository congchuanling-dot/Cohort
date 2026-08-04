package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cohort/internal/contextmgr"
	"cohort/internal/llm"
	"cohort/internal/observability"
)

const (
	memoryGenerationSystemPrompt = `你是 Cohort 的 Session Memory 生成器。

你的任务是从会话历史中提取稳定事实，生成 memory.md。
只记录对后续继续这个 session 有长期价值的信息。
不要记录临时命令输出、无关寒暄、重复过程或模型中间废话。
必须使用用户提供的 markdown 结构；没有内容的栏目写 "- 暂无"。`

	fullCompactGenerationSystemPrompt = `你是 Cohort 的 Full Compact 生成器。

你的任务是从完整会话历史中生成 compact.md，用于恢复长会话的关键上下文。
compact.md 应保留任务目标、技术背景、关键文件、错误修复、当前进度和下一步。
不要保存 <analysis> 内容；最终答案必须包含 <summary>...</summary>。`

	maxMemorySourceChars  = 120000
	memorySourceHead      = 60000
	memorySourceTail      = 60000
	maxCompactSourceChars = 240000
	compactSourceHead     = 120000
	compactSourceTail     = 120000
)

var ErrNoActiveSession = errors.New("no active session")

// CompactMemoryResult 描述 /compact 生成 session memory 的结果。
type CompactMemoryResult struct {
	// SessionID 是本次 compact 所属的会话标识。
	SessionID string
	// Path 是生成后的 memory.md 文件路径。
	Path string
	// BackupPath 是旧 memory.md 的备份路径；没有备份时为空。
	BackupPath string
	// BackedUp 表示本次写入前是否成功备份了旧 memory.md。
	BackedUp bool
	// Chars 是生成的 session memory 字符数。
	Chars int
}

// SessionMemorySnapshot 是当前 session memory.md 的只读快照，用于 /memory 展示。
type SessionMemorySnapshot struct {
	// SessionID 是快照所属的会话标识。
	SessionID string
	// Path 是 memory.md 的预期文件路径。
	Path string
	// Content 是当前 memory.md 的完整内容；文件不存在时为空。
	Content string
	// Chars 是 Content 的字符数。
	Chars int
	// Exists 表示 memory.md 当前是否存在。
	Exists bool
}

// FullCompactResult 描述 /full-compact 生成 compact.md 的结果。
type FullCompactResult struct {
	// SessionID 是本次 full compact 所属的会话标识。
	SessionID string
	// Path 是生成后的 compact.md 文件路径。
	Path string
	// BackupPath 是旧 compact.md 的备份路径；没有备份时为空。
	BackupPath string
	// BackedUp 表示本次写入前是否成功备份了旧 compact.md。
	BackedUp bool
	// Chars 是生成的 compact summary 字符数。
	Chars int
}

// CompactSummarySnapshot 是当前 session compact.md 的只读快照，用于后续查看命令扩展。
type CompactSummarySnapshot struct {
	// SessionID 是快照所属的会话标识。
	SessionID string
	// Path 是 compact.md 的预期文件路径。
	Path string
	// Content 是当前 compact.md 的完整内容；文件不存在时为空。
	Content string
	// Chars 是 Content 的字符数。
	Chars int
	// Exists 表示 compact.md 当前是否存在。
	Exists bool
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
	obs := r.observationBus()
	defer obs.Close(ctx)
	runID := observability.NewRunID()
	r.emitObservation(ctx, obs, runID, observability.EventCompactStarted, 0, observability.SeverityInfo, map[string]any{
		"kind":        "session_memory",
		"history_len": len(r.history),
	})

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
	if mkdirErr := os.MkdirAll(sessionDir, 0755); mkdirErr != nil {
		return CompactMemoryResult{}, mkdirErr
	}
	path := filepath.Join(sessionDir, contextmgr.SessionMemoryFileName)
	backupPath, backedUp, err := backupSessionMemory(path)
	if err != nil {
		return CompactMemoryResult{}, err
	}
	if writeErr := os.WriteFile(path, []byte(memory+"\n"), 0644); writeErr != nil {
		return CompactMemoryResult{}, writeErr
	}
	r.emitObservation(ctx, obs, runID, observability.EventCompactFinished, 0, observability.SeverityInfo, map[string]any{
		"kind":      "session_memory",
		"path":      path,
		"chars":     len([]rune(memory)),
		"backed_up": backedUp,
	})
	return CompactMemoryResult{
		SessionID:  r.sessionID,
		Path:       path,
		BackupPath: backupPath,
		BackedUp:   backedUp,
		Chars:      len([]rune(memory)),
	}, nil
}

// FullCompactSession 调用模型把当前 Runner.history 压缩成长历史摘要，并写入 compact.md。
//
// 这是 P1 第一版 Full Compact：只支持手动 /full-compact 触发生成。
// 生成后的 compact.md 会在后续请求前由 contextmgr 自动读取，并按 memory.md -> compact.md -> 最近历史的顺序注入。
func (r *Runner) FullCompactSession(ctx context.Context) (FullCompactResult, error) {
	if r.Client == nil {
		return FullCompactResult{}, errors.New("full compact requires llm client")
	}
	if r.SessionStore == nil || r.sessionID == "" {
		return FullCompactResult{}, errors.New("full compact requires an active session")
	}
	if len(r.history) == 0 {
		return FullCompactResult{}, errors.New("full compact requires non-empty history")
	}

	sessionDir := r.sessionDir()
	if strings.TrimSpace(sessionDir) == "" {
		return FullCompactResult{}, errors.New("full compact cannot resolve session directory")
	}
	obs := r.observationBus()
	defer obs.Close(ctx)
	runID := observability.NewRunID()
	r.emitObservation(ctx, obs, runID, observability.EventCompactStarted, 0, observability.SeverityInfo, map[string]any{
		"kind":        "full_compact",
		"history_len": len(r.history),
	})

	prompt := buildFullCompactPrompt(r.history)
	stream, err := r.Client.Chat(ctx, llm.ChatRequest{
		System: fullCompactGenerationSystemPrompt,
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: prompt,
		}},
	})
	if err != nil {
		return FullCompactResult{}, err
	}
	resp, err := consume(stream, silentSink{})
	if err != nil {
		return FullCompactResult{}, err
	}
	summary := extractSummaryContent(resp.Content)
	if summary == "" {
		return FullCompactResult{}, errors.New("full compact returned empty summary")
	}
	if mkdirErr := os.MkdirAll(sessionDir, 0755); mkdirErr != nil {
		return FullCompactResult{}, mkdirErr
	}
	path := filepath.Join(sessionDir, contextmgr.CompactSummaryFileName)
	backupPath, backedUp, err := backupCompactSummary(path)
	if err != nil {
		return FullCompactResult{}, err
	}
	if writeErr := os.WriteFile(path, []byte(summary+"\n"), 0644); writeErr != nil {
		return FullCompactResult{}, writeErr
	}
	r.emitObservation(ctx, obs, runID, observability.EventCompactFinished, 0, observability.SeverityInfo, map[string]any{
		"kind":      "full_compact",
		"path":      path,
		"chars":     len([]rune(summary)),
		"backed_up": backedUp,
	})
	return FullCompactResult{
		SessionID:  r.sessionID,
		Path:       path,
		BackupPath: backupPath,
		BackedUp:   backedUp,
		Chars:      len([]rune(summary)),
	}, nil
}

// LoadSessionMemory 读取当前 session 的 memory.md，供 /memory 和 /session memory 展示。
// 没有 active session 会返回错误；memory.md 不存在时返回 Exists=false。
func (r *Runner) LoadSessionMemory() (SessionMemorySnapshot, error) {
	if r.SessionStore == nil || r.sessionID == "" {
		return SessionMemorySnapshot{}, ErrNoActiveSession
	}
	sessionDir := r.sessionDir()
	if strings.TrimSpace(sessionDir) == "" {
		return SessionMemorySnapshot{}, errors.New("cannot resolve session directory")
	}
	path := filepath.Join(sessionDir, contextmgr.SessionMemoryFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionMemorySnapshot{
				SessionID: r.sessionID,
				Path:      path,
				Exists:    false,
			}, nil
		}
		return SessionMemorySnapshot{}, err
	}
	content := strings.TrimRight(string(data), "\n")
	return SessionMemorySnapshot{
		SessionID: r.sessionID,
		Path:      path,
		Content:   content,
		Chars:     len([]rune(content)),
		Exists:    strings.TrimSpace(content) != "",
	}, nil
}

// LoadCompactSummary 读取当前 session 的 compact.md。
// 第一版暂未挂查看命令，但保留这个 API，便于后续补 /summary 或 /session compact。
func (r *Runner) LoadCompactSummary() (CompactSummarySnapshot, error) {
	if r.SessionStore == nil || r.sessionID == "" {
		return CompactSummarySnapshot{}, ErrNoActiveSession
	}
	sessionDir := r.sessionDir()
	if strings.TrimSpace(sessionDir) == "" {
		return CompactSummarySnapshot{}, errors.New("cannot resolve session directory")
	}
	path := filepath.Join(sessionDir, contextmgr.CompactSummaryFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CompactSummarySnapshot{
				SessionID: r.sessionID,
				Path:      path,
				Exists:    false,
			}, nil
		}
		return CompactSummarySnapshot{}, err
	}
	content := strings.TrimRight(string(data), "\n")
	return CompactSummarySnapshot{
		SessionID: r.sessionID,
		Path:      path,
		Content:   content,
		Chars:     len([]rune(content)),
		Exists:    strings.TrimSpace(content) != "",
	}, nil
}

func backupSessionMemory(memoryPath string) (backupPath string, backedUp bool, err error) {
	return backupNonEmptyFile(memoryPath, contextmgr.SessionMemoryBackupFileName)
}

func backupCompactSummary(summaryPath string) (backupPath string, backedUp bool, err error) {
	return backupNonEmptyFile(summaryPath, contextmgr.CompactSummaryBackupFileName)
}

func backupNonEmptyFile(path string, backupFileName string) (backupPath string, backedUp bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", false, nil
	}
	backupPath = filepath.Join(filepath.Dir(path), backupFileName)
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

func buildMemoryGenerationPrompt(history []llm.Message) string {
	rendered := limitMemorySource(renderMessagesForMemory(history))
	return `请从下面的 Cohort 会话历史中生成 session memory。

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

func buildFullCompactPrompt(history []llm.Message) string {
	rendered := limitCompactSource(renderMessagesForMemory(history))
	return `请从下面的 Cohort 会话历史中生成 full compact 摘要。

输出必须包含 <summary>...</summary>，summary 内部必须严格使用这个结构：

1. Primary Request and Intent:

2. Key Technical Concepts:

3. Files and Code Sections:

4. Errors and Fixes:

5. Problem Solving:

6. User Messages:

7. Pending Tasks:

8. Current Work:

9. Next Step:

要求：
- 可以在 <analysis> 中先分析，但保存时只会保留 <summary> 内部内容。
- 保留对后续继续任务有帮助的关键事实。
- 记录关键文件路径、函数名、重要约束和未完成任务。
- 不记录大段命令输出、无关寒暄或重复过程。
- 不编造历史里没有的信息。
- 如果某一栏没有内容，写 "None"。

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
	return head + "\n\n[... earlier memory generation source omitted by Cohort ...]\n\n" + tail
}

func limitCompactSource(text string) string {
	runes := []rune(text)
	if len(runes) <= maxCompactSourceChars {
		return text
	}
	head := string(runes[:compactSourceHead])
	tail := string(runes[len(runes)-compactSourceTail:])
	return head + "\n\n[... earlier full compact source omitted by Cohort ...]\n\n" + tail
}

func extractSummaryContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lower := strings.ToLower(content)
	start := strings.Index(lower, "<summary>")
	end := strings.LastIndex(lower, "</summary>")
	if start >= 0 && end > start {
		start += len("<summary>")
		return strings.TrimSpace(content[start:end])
	}
	return content
}

type silentSink struct{}

func (silentSink) WriteText(string)               {}
func (silentSink) WriteToolCall(llm.ToolCall)     {}
func (silentSink) WriteToolResult(string, string) {}
func (silentSink) WriteError(error)               {}
