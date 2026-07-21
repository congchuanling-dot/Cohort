package contextmgr

import "cohert/internal/llm"

// Build 根据完整历史构造“本轮真正发给模型”的 messages。
//
// 这里有一个很重要的边界：
//   - input.Messages 来自 Runner.history，是完整历史。
//   - Build 内部只处理复制出来的请求副本。
//   - Runner.history 和 history.jsonl 永远不在这里裁剪或压缩。
//
// 流程顺序：
//  1. clone 完整历史，避免误改调用方。
//  2. 清理协议非法的孤立 tool 消息。
//  3. 如果 session 目录存在 memory.md 和 compact.md，把它们作为受保护前缀注入请求。
//  4. 估算 token，占用低于 70% 时直接返回。
//  5. 超过 70% 时先压缩旧 tool result。
//  6. 压缩后仍超预算时，才按 message group 裁旧历史。
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
	stats.OriginalTokens = estimateTokensFromChars(stats.OriginalChars)

	// 先做协议清理，不等同于“上下文压缩”。
	//
	// OpenAI-compatible tool calling 要求 tool 消息前面必须有对应的：
	//   assistant(tool_calls: [{id: "..."}])
	//
	// 这种孤立 tool 是非法请求形状：
	//   tool(tool_call_id: "call-1")
	//   user("继续")
	//
	// 即使当前上下文还没达到 70% 压缩阈值，也不能把这种非法 tool 消息发给模型。
	messages = dropOrphanToolResults(messages, &stats)

	// 第一层长期记忆索引只做“读取并注入指针”。
	// 详细项目/全局记忆不在这里全文注入，由模型按需读取。
	indexText, _, indexErr := loadLongTermMemoryIndex(m.MemoryRoot)
	if indexErr != nil {
		stats.Warnings = append(stats.Warnings, "failed to load long-term memory index: "+indexErr.Error())
	}
	indexMessage, _ := buildLongTermMemoryIndexMessage(indexText, cfg, &stats)

	// 第二层 Session Memory Compact 的第一版只做“读取并注入”。
	// 如果 temp/sessions/<session_id>/memory.md 存在，就把它转换成一条 assistant 消息。
	// 这条消息是受保护前缀：后续 group trim 只裁剪普通历史，不会把 memory 当作最旧消息裁掉。
	memoryText, _, memoryErr := loadSessionMemory(input.SessionDir)
	if memoryErr != nil {
		stats.Warnings = append(stats.Warnings, "failed to load session memory: "+memoryErr.Error())
	}
	memoryMessage, hasMemory := buildSessionMemoryMessage(memoryText, cfg, &stats)

	// 第三层 Full Compact 的第一版同样只在请求前读取 compact.md。
	// 生成由 /full-compact 手动触发；只要文件存在，后续每轮请求都会自动注入。
	// 注入顺序固定为 memory.md -> compact.md -> 最近历史。
	compactText, _, compactErr := loadCompactSummary(input.SessionDir)
	if compactErr != nil {
		stats.Warnings = append(stats.Warnings, "failed to load compact summary: "+compactErr.Error())
	}
	compactMessage, hasCompact := buildCompactSummaryMessage(compactText, cfg, &stats)
	messagesWithContext := prependProtectedContext(messages, indexMessage, memoryMessage, compactMessage)

	// newBudget 根据模型上下文窗口计算两个值：
	//   UsableInputTokens：本轮输入最多可占多少 token。
	//   CompactTriggerTokens：达到多少 token 后开始压缩，默认是可用输入预算的 70%。
	budget := newBudget(cfg)
	stats.UsableInputTokens = budget.UsableInputTokens
	stats.CompactTriggerTokens = budget.CompactTriggerTokens

	// 低于 70% 阈值时，本轮不做 Micro Compact，也不做 Group Trim。
	// 这里返回的是“协议清理后的副本”，不是原始 Runner.history。
	requestTokens := estimateTokens(messagesWithContext)
	if requestTokens < budget.CompactTriggerTokens {
		stats.SkippedCompact = true
		stats.TriggerReason = triggerReasonBelowThreshold
		stats.FinalMessages = len(messagesWithContext)
		stats.FinalChars = messagesChars(messagesWithContext)
		stats.FinalTokens = estimateTokensFromChars(stats.FinalChars)
		return BuildResult{Messages: messagesWithContext, Stats: stats}
	}
	stats.TriggerReason = triggerReasonOverThreshold

	if cfg.EnableMicroCompact {
		// 第一层是确定性的 Micro Compact：优先压缩旧工具结果内容。
		// 这里不删除 tool message，也不打散 assistant tool_calls 与 tool result 的协议结构。
		compactToolResults(messages, cfg, &stats)
	}

	// Micro Compact 后重新估算。
	// 如果压缩旧 tool result 后已经放得进模型窗口，就停止，不继续裁剪历史消息。
	// 这样可以尽量保留完整对话结构，只牺牲旧工具输出的中间部分。
	messagesWithContext = prependProtectedContext(messages, indexMessage, memoryMessage, compactMessage)
	compactedChars := messagesChars(messagesWithContext)
	compactedTokens := estimateTokensFromChars(compactedChars)
	if compactedTokens <= budget.UsableInputTokens && compactedChars <= cfg.MaxRequestChars {
		stats.FinalMessages = len(messagesWithContext)
		stats.FinalChars = compactedChars
		stats.FinalTokens = compactedTokens
		return BuildResult{Messages: messagesWithContext, Stats: stats}
	}
	stats.TriggerReason = triggerReasonOverBudget

	// 最后才按 group 裁剪旧历史。group 裁剪会保护 tool-call 协议完整性，
	// 必要时插入一条 context notice，告诉模型早期消息已从本轮请求中省略。
	var protected []llm.Message
	for _, message := range []llm.Message{indexMessage, memoryMessage, compactMessage} {
		if message.Role != "" || message.Content != "" {
			protected = append(protected, message)
		}
	}
	trimCfg := reserveBudgetForProtectedContext(cfg, protected...)
	messages = trimMessages(messages, trimCfg, &stats)
	messages = prependProtectedContext(messages, indexMessage, memoryMessage, compactMessage)
	stats.FinalMessages = len(messages)
	stats.FinalChars = messagesChars(messages)
	stats.FinalTokens = estimateTokensFromChars(stats.FinalChars)
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
