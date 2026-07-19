package contextmgr

import (
	"fmt"

	"cohert/internal/llm"
)

type messageGroup struct {
	Messages []llm.Message
}

func dropOrphanToolResults(messages []llm.Message, stats *Stats) []llm.Message {
	groups := groupMessages(messages, stats)
	return flattenGroups(groups)
}

func trimMessages(messages []llm.Message, cfg Config, stats *Stats) []llm.Message {
	groups := groupMessages(messages, stats)
	kept := keepGroupsFromTail(groups, cfg)
	trimmed := countGroupMessages(groups) - countGroupMessages(kept)
	if trimmed <= 0 {
		return flattenGroups(kept)
	}

	result := flattenGroups(kept)
	stats.TrimmedMessages += trimmed
	if len(result) == 0 {
		return []llm.Message{{Role: llm.RoleUser, Content: contextNotice}}
	}
	result = makeRoomForNotice(result, cfg, stats)
	result = append([]llm.Message{{Role: llm.RoleUser, Content: contextNotice}}, result...)
	stats.InsertedNotice = true
	return result
}

func groupMessages(messages []llm.Message, stats *Stats) []messageGroup {
	groups := make([]messageGroup, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		if message.Role == llm.RoleTool {
			stats.Warnings = append(stats.Warnings, fmt.Sprintf("dropped orphan tool result %q", message.ToolCallID))
			continue
		}
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			groups = append(groups, messageGroup{Messages: []llm.Message{message}})
			continue
		}

		group := messageGroup{Messages: []llm.Message{message}}
		needed := toolCallIDSet(message.ToolCalls)
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

	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
}

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

func makeRoomForNotice(messages []llm.Message, cfg Config, stats *Stats) []llm.Message {
	for cfg.MaxHistoryMessages > 0 && len(messages)+1 > cfg.MaxHistoryMessages && len(messages) > 0 {
		messages = dropOldestGroup(messages, stats)
	}
	for cfg.MaxRequestChars > 0 && messagesChars(messages)+len([]rune(contextNotice)) > cfg.MaxRequestChars && len(messages) > 0 {
		messages = dropOldestGroup(messages, stats)
	}
	return messages
}

func dropOldestGroup(messages []llm.Message, stats *Stats) []llm.Message {
	groups := groupMessages(messages, stats)
	if len(groups) == 0 {
		return nil
	}
	stats.TrimmedMessages += len(groups[0].Messages)
	return flattenGroups(groups[1:])
}
