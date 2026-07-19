package contextmgr

import "cohert/internal/llm"

func messageChars(message llm.Message) int {
	total := len([]rune(message.Role)) +
		len([]rune(message.Content)) +
		len([]rune(message.ToolCallID)) +
		len([]rune(message.Name))
	for _, call := range message.ToolCalls {
		total += len([]rune(call.ID))
		total += len([]rune(call.Type))
		total += len([]rune(call.Function.Name))
		total += len([]rune(call.Function.Arguments))
	}
	return total
}

func messagesChars(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += messageChars(message)
	}
	return total
}
