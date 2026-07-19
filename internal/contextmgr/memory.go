package contextmgr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"cohert/internal/llm"
)

const (
	SessionMemoryFileName        = "memory.md"
	SessionMemoryBackupFileName  = "memory.bak.md"
	CompactSummaryFileName       = "compact.md"
	CompactSummaryBackupFileName = "compact.bak.md"
)

// loadSessionMemory 读取当前 session 目录下的 memory.md。
//
// 第一版只负责读取和注入，不负责生成或更新 memory.md。
// 如果文件不存在或内容为空，返回 ok=false；这不是错误，正常跳过注入即可。
func loadSessionMemory(sessionDir string) (text string, ok bool, err error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, SessionMemoryFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	text = strings.TrimSpace(string(data))
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

// buildSessionMemoryMessage 把 memory.md 转成一条只用于本轮请求的 assistant 消息。
//
// 这条消息不会写入 Runner.history，也不会写回 history.jsonl。
// 它的作用是给模型提供稳定背景，例如用户目标、项目约定、当前状态和下一步。
func buildSessionMemoryMessage(memoryText string, cfg Config, stats *Stats) (llm.Message, bool) {
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return llm.Message{}, false
	}

	limited, truncated := limitRunes(memoryText, cfg.MaxSessionMemoryChars)
	content := sessionMemoryNotice + "\n\n" + limited
	if truncated {
		content += "\n\n[Cohert session memory truncated]"
		stats.SessionMemoryTruncated = true
	}

	stats.InjectedSessionMemory = true
	stats.SessionMemoryChars = len([]rune(limited))
	return llm.Message{Role: llm.RoleAssistant, Content: content}, true
}

// loadCompactSummary 读取当前 session 目录下的 compact.md。
//
// compact.md 是 Full Compact 的产物，用来承载较长历史的结构化摘要。
// 文件不存在或内容为空时正常跳过注入，不影响普通会话继续运行。
func loadCompactSummary(sessionDir string) (text string, ok bool, err error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, CompactSummaryFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	text = strings.TrimSpace(string(data))
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

// buildCompactSummaryMessage 把 compact.md 转成一条只用于本轮请求的 assistant 消息。
//
// 它和 memory.md 一样不会写入 Runner.history 或 history.jsonl。
// 注入顺序由调用方保证为：memory.md -> compact.md -> 最近对话历史。
func buildCompactSummaryMessage(summaryText string, cfg Config, stats *Stats) (llm.Message, bool) {
	summaryText = strings.TrimSpace(summaryText)
	if summaryText == "" {
		return llm.Message{}, false
	}

	limited, truncated := limitRunes(summaryText, cfg.MaxCompactSummaryChars)
	content := compactSummaryNotice + "\n\n" + limited
	if truncated {
		content += "\n\n[Cohert compact summary truncated]"
		stats.CompactSummaryTruncated = true
	}

	stats.InjectedCompactSummary = true
	stats.CompactSummaryChars = len([]rune(limited))
	return llm.Message{Role: llm.RoleAssistant, Content: content}, true
}

// prependProtectedContext 按固定顺序注入受保护上下文前缀。
//
// 顺序必须保持为 memory.md -> compact.md -> 最近对话：
//   - memory.md 存稳定事实和用户偏好；
//   - compact.md 存旧历史摘要；
//   - 最近对话仍保留原始消息形状。
func prependProtectedContext(messages []llm.Message, memory llm.Message, hasMemory bool, summary llm.Message, hasSummary bool) []llm.Message {
	if !hasMemory && !hasSummary {
		return messages
	}
	result := make([]llm.Message, 0, len(messages)+2)
	if hasMemory {
		result = append(result, memory)
	}
	if hasSummary {
		result = append(result, summary)
	}
	result = append(result, messages...)
	return result
}

// reserveBudgetForProtectedContext 给 memory.md 和 compact.md 这两个受保护前缀预留预算。
//
// trimMessages 只接收普通历史消息，因此需要先从预算里扣掉前缀占用。
// 这样 group trim 不会为了满足 MaxHistoryMessages 或 MaxRequestChars 把摘要消息裁掉。
func reserveBudgetForProtectedContext(cfg Config, protected ...llm.Message) Config {
	for _, message := range protected {
		if message.Role == "" && message.Content == "" {
			continue
		}
		if cfg.MaxHistoryMessages > 1 {
			cfg.MaxHistoryMessages--
		}
		if cfg.MaxRequestChars > 0 {
			remaining := cfg.MaxRequestChars - messageChars(message)
			if remaining < 1 {
				remaining = 1
			}
			cfg.MaxRequestChars = remaining
		}
	}
	return cfg
}

func limitRunes(text string, max int) (string, bool) {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text, false
	}
	return string(runes[:max]), true
}
