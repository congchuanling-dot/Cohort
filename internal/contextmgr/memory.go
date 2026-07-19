package contextmgr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"cohert/internal/llm"
)

const SessionMemoryFileName = "memory.md"

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

// prependSessionMemory 把 session memory 放到请求最前面。
//
// memory 是稳定事实，应该先于最近对话进入模型视野。
// 调用方保证这只是 request messages 副本，不会污染完整历史。
func prependSessionMemory(messages []llm.Message, memory llm.Message, ok bool) []llm.Message {
	if !ok {
		return messages
	}
	result := make([]llm.Message, 0, len(messages)+1)
	result = append(result, memory)
	result = append(result, messages...)
	return result
}

// reserveBudgetForSessionMemory 给受保护的 memory 前缀预留预算。
//
// group trim 只应该裁剪普通历史，不应该把 memory.md 当作“最旧消息”裁掉。
// 所以进入 trimMessages 前，需要先从消息数和字符数预算里扣掉 memory 自身占用。
func reserveBudgetForSessionMemory(cfg Config, memory llm.Message, ok bool) Config {
	if !ok {
		return cfg
	}
	if cfg.MaxHistoryMessages > 1 {
		cfg.MaxHistoryMessages--
	}
	if cfg.MaxRequestChars > 0 {
		remaining := cfg.MaxRequestChars - messageChars(memory)
		if remaining < 1 {
			remaining = 1
		}
		cfg.MaxRequestChars = remaining
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
