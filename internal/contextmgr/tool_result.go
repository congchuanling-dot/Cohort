package contextmgr

import (
	"fmt"
	"strings"

	"cohort/internal/llm"
)

// compactToolResults 执行第一层 Micro Compact，只压缩旧工具结果的 Content。
//
// 这里不会删除 tool 消息，也不会修改 assistant tool_calls 结构；这样后续发给模型时，
// OpenAI-compatible 的 tool calling 协议仍然完整。
func compactToolResults(messages []llm.Message, cfg Config, stats *Stats) {
	remainingFullToolResults := cfg.KeepRecentToolResults
	// 从后往前扫描，是为了让“最近 N 条工具结果”保持完整。
	// 最近工具结果通常和当前任务最相关，过早压缩会降低模型继续推理的质量。
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleTool {
			continue
		}
		if remainingFullToolResults > 0 {
			remainingFullToolResults--
			continue
		}
		contentChars := len([]rune(messages[i].Content))
		if contentChars <= cfg.MaxToolResultChars {
			continue
		}
		compacted := compactText(messages[i].Content, cfg)
		// Stats 只描述本轮请求副本发生了什么，不代表磁盘历史或 Runner.history 被修改。
		stats.CompactedToolResults++
		stats.OmittedToolResultChars += contentChars - len([]rune(compacted))
		messages[i].Content = compacted
	}
}

// compactText 把超长文本压缩为“元信息 + 头部 + 尾部”的稳定格式。
//
// 头部通常保留命令起始输出、文件开头或错误上下文；尾部通常保留最终错误、
// 总结信息或命令退出前的关键日志。中间内容丢弃，但完整原文仍保存在 history.jsonl。
func compactText(text string, cfg Config) string {
	runes := []rune(text)
	if len(runes) <= cfg.MaxToolResultChars {
		return text
	}
	headChars := minInt(cfg.CompactedToolHeadChars, len(runes))
	tailChars := minInt(cfg.CompactedToolTailChars, len(runes)-headChars)

	head := string(runes[:headChars])
	tail := string(runes[len(runes)-tailChars:])
	return fmt.Sprintf(`[tool result compacted]
original_chars: %d
kept_head_chars: %d
kept_tail_chars: %d

--- head ---
%s

--- tail ---
%s`, len(runes), headChars, tailChars, strings.TrimRight(head, "\n"), strings.TrimLeft(tail, "\n"))
}

// minInt 避免 head/tail 配置大于原文长度时发生越界。
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
