package contextmgr

import (
	"fmt"

	"cohert/internal/llm"
)

type messageGroup struct {
	Messages []llm.Message
}

// dropOrphanToolResults 清理协议非法的孤立 tool 消息。
//
// 它不是按预算裁剪历史，而是复用 groupMessages 的协议分组能力：
//   - 合法的普通 user/assistant 会进入 group。
//   - 合法的 assistant(tool_calls) + tool(result) 会进入同一个 group。
//   - 任何没有被 assistant(tool_calls) 接住的 tool 都会被 groupMessages 丢弃。
//
// 例子：
//
//	输入: [tool(call-1), user("继续")]
//	输出: [user("继续")]
//
// 这么做是为了保证即使低于 70% 压缩阈值，发给模型的 messages 也不会违反 tool calling 协议。
func dropOrphanToolResults(messages []llm.Message, stats *Stats) []llm.Message {
	groups := groupMessages(messages, stats)
	return flattenGroups(groups)
}

// trimMessages 是真正的历史裁剪入口。
//
// 进入这里说明：
//  1. 当前上下文已经超过 70% 压缩阈值；
//  2. 旧工具结果已经尝试过 Micro Compact；
//  3. 压缩后仍然超过可用输入预算或字符兜底预算。
//
// 裁剪单位不是单条 message，而是 messageGroup。这样可以避免把：
//
//	assistant(tool_calls)
//	tool(result)
//
// 拆开导致模型 API 报协议错误。
func trimMessages(messages []llm.Message, cfg Config, stats *Stats) []llm.Message {
	// 先把扁平 messages 转成按协议保护的 group。
	groups := groupMessages(messages, stats)

	// 从最新 group 往前保留。越新的上下文越接近当前任务，优先级最高。
	kept := keepGroupsFromTail(groups, cfg)
	trimmed := countGroupMessages(groups) - countGroupMessages(kept)
	if trimmed <= 0 {
		return flattenGroups(kept)
	}

	result := flattenGroups(kept)
	stats.TrimmedMessages += trimmed
	if len(result) == 0 {
		// 极端情况下所有历史都放不下，至少发一条 notice 给模型，说明旧上下文被省略。
		return []llm.Message{{Role: llm.RoleUser, Content: contextNotice}}
	}

	// contextNotice 本身也占一条 message 和若干字符。
	// 如果插入 notice 后又超预算，就继续从最旧 group 开始腾空间。
	result = makeRoomForNotice(result, cfg, stats)
	result = append([]llm.Message{{Role: llm.RoleUser, Content: contextNotice}}, result...)
	stats.InsertedNotice = true
	return result
}

// groupMessages 把线性的 messages 切成不能再拆的协议单元。
//
// 分组规则：
//   - 普通 user/assistant/system 等消息：单条一个 group。
//   - assistant 带 tool_calls：和后面连续、且 tool_call_id 匹配的 tool 消息合成一个 group。
//   - 单独出现的 tool：没有合法上游 assistant(tool_calls)，直接丢弃并记录 warning。
//
// 为什么要这样：
// OpenAI-compatible 协议要求 assistant(tool_calls) 后面必须跟对应 tool result。
// 裁剪时如果只保留 tool 或只保留 assistant(tool_calls)，请求都会变成非法形状。
func groupMessages(messages []llm.Message, stats *Stats) []messageGroup {
	groups := make([]messageGroup, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		if message.Role == llm.RoleTool {
			// 走到这里说明当前 tool 没有被前一个 assistant(tool_calls) 消费。
			// 它可能来自损坏的 history、旧版本写入、或未来手工编辑 session 文件。
			stats.Warnings = append(stats.Warnings, fmt.Sprintf("dropped orphan tool result %q", message.ToolCallID))
			continue
		}
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			groups = append(groups, messageGroup{Messages: []llm.Message{message}})
			continue
		}

		group := messageGroup{Messages: []llm.Message{message}}
		needed := toolCallIDSet(message.ToolCalls)

		// 只吸收紧跟在 assistant 后面的 tool 消息。
		// 一旦遇到非 tool，或 tool_call_id 不属于当前 assistant，就停止当前 group。
		for i+1 < len(messages) && messages[i+1].Role == llm.RoleTool {
			next := messages[i+1]
			if !needed[next.ToolCallID] {
				break
			}
			group.Messages = append(group.Messages, next)
			delete(needed, next.ToolCallID)
			i++
		}
		if len(needed) > 0 {
			// assistant 声明了 tool_calls，但历史里没有对应 tool result。
			// 这种 group 仍保留，因为 assistant 本身是历史的一部分；这里只记录 warning。
			stats.Warnings = append(stats.Warnings, "assistant tool_calls missing matching tool results")
		}
		groups = append(groups, group)
	}
	return groups
}

func toolCallIDSet(calls []llm.ToolCall) map[string]bool {
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID != "" {
			ids[call.ID] = true
		}
	}
	return ids
}

// keepGroupsFromTail 从最新消息开始向前保留 group。
//
// 例如原始 groups 是：
//
//	[old user] [old assistant] [tool-call group] [latest user]
//
// 保留顺序是从 latest user 开始往前拿，直到再拿一个 group 会超过：
//   - MaxHistoryMessages
//   - MaxRequestChars
//
// 注意 totalMessages > 0 / totalChars > 0 的判断：
// 即使单个最新 group 已经超过预算，也至少保留它，否则模型会完全看不到当前问题。
func keepGroupsFromTail(groups []messageGroup, cfg Config) []messageGroup {
	if len(groups) == 0 {
		return nil
	}

	var kept []messageGroup
	totalMessages := 0
	totalChars := 0
	for i := len(groups) - 1; i >= 0; i-- {
		groupMessages := len(groups[i].Messages)
		groupChars := messagesChars(groups[i].Messages)
		wouldExceedMessages := cfg.MaxHistoryMessages > 0 && totalMessages > 0 && totalMessages+groupMessages > cfg.MaxHistoryMessages
		wouldExceedChars := cfg.MaxRequestChars > 0 && totalChars > 0 && totalChars+groupChars > cfg.MaxRequestChars
		if wouldExceedMessages || wouldExceedChars {
			break
		}
		kept = append(kept, groups[i])
		totalMessages += groupMessages
		totalChars += groupChars
	}

	// 上面是从新到旧 append，顺序是反的。
	// 发给模型时还要恢复成正常时间顺序：旧 -> 新。
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
}

// flattenGroups 把 group 重新摊平成 llm.Message 切片，作为最终 request messages。
func flattenGroups(groups []messageGroup) []llm.Message {
	total := countGroupMessages(groups)
	result := make([]llm.Message, 0, total)
	for _, group := range groups {
		result = append(result, group.Messages...)
	}
	return result
}

func countGroupMessages(groups []messageGroup) int {
	total := 0
	for _, group := range groups {
		total += len(group.Messages)
	}
	return total
}

// makeRoomForNotice 确保插入 contextNotice 后仍满足消息数和字符数限制。
//
// notice 是发给模型的提示，告诉它早期历史已从本轮请求省略。
// 但 notice 本身也会占预算，所以必要时还要继续丢最旧 group。
func makeRoomForNotice(messages []llm.Message, cfg Config, stats *Stats) []llm.Message {
	for cfg.MaxHistoryMessages > 0 && len(messages)+1 > cfg.MaxHistoryMessages && len(messages) > 0 {
		messages = dropOldestGroup(messages, stats)
	}
	for cfg.MaxRequestChars > 0 && messagesChars(messages)+len([]rune(contextNotice)) > cfg.MaxRequestChars && len(messages) > 0 {
		messages = dropOldestGroup(messages, stats)
	}
	return messages
}

// dropOldestGroup 删除当前 messages 里的第一个 group。
//
// 这里不能简单 messages[1:]，因为第一个 group 可能是：
//
//	assistant(tool_calls) + tool(result)
//
// 必须整体删除，不能只删 assistant 或只删 tool。
func dropOldestGroup(messages []llm.Message, stats *Stats) []llm.Message {
	groups := groupMessages(messages, stats)
	if len(groups) == 0 {
		return nil
	}
	stats.TrimmedMessages += len(groups[0].Messages)
	return flattenGroups(groups[1:])
}
