package contextmgr

import "cohert/internal/llm"

// Build 返回本轮模型可见的 messages。输入消息会被完整复制，调用方的历史不会被修改。
func (m Manager) Build(input BuildInput) BuildResult {
	// Normalize 兜底配置里的零值或非法值，避免配置缺失时把请求上下文裁剪到不可用。
	cfg := m.Config.Normalize()

	// Context Manager 只处理“本轮请求副本”。
	// Runner.history 和 history.jsonl 必须保留完整原始消息，后续 resume、审计和重新压缩都依赖它们。
	messages := cloneMessages(input.Messages)
	stats := Stats{
		OriginalMessages: len(input.Messages),
		OriginalChars:    messagesChars(input.Messages),
	}

	if cfg.EnableMicroCompact {
		// 第一层是确定性的 Micro Compact：优先压缩旧工具结果内容。
		// 这里不删除 tool message，也不打散 assistant tool_calls 与 tool result 的协议结构。
		compactToolResults(messages, cfg, &stats)
	}

	// 第二层才按 group 裁剪旧历史。group 裁剪会保护 tool-call 协议完整性，
	// 必要时插入一条 context notice，告诉模型早期消息已从本轮请求中省略。
	messages = trimMessages(messages, cfg, &stats)
	stats.FinalMessages = len(messages)
	stats.FinalChars = messagesChars(messages)
	return BuildResult{Messages: messages, Stats: stats}
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		if len(message.ToolCalls) > 0 {
			// ToolCalls 是 slice，需要单独复制底层数组，避免后续压缩或裁剪流程误改调用方历史。
			cloned[i].ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
		}
	}
	return cloned
}
