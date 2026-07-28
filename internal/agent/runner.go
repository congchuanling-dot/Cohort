package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/contextmgr"
	"cohort/internal/evolution"
	"cohort/internal/llm"
	"cohort/internal/observability"
	"cohort/internal/session"
	"cohort/internal/skill"
)

const (
	// defaultSessionTitle 是用户还没输入明确任务时的兜底标题。
	// 正常情况下会用第一条用户输入生成标题，这个常量只处理空输入等边界情况。
	defaultSessionTitle = "new session"

	// maxSessionTitleLength 限制自动生成的 session 标题长度。
	// 标题只用于 session list 展示，过长会让列表很难读，所以这里做轻量截断。
	maxSessionTitleLength = 40

	// longTermMemoryTurnThreshold 表示连续执行到这个 turn 后，可以提示模型评估是否需要沉淀长期记忆。
	// 阈值保持较低，是为了覆盖包含多步排查和验证的任务；提示本身不强制写入。
	longTermMemoryTurnThreshold = 3
)

// ToolRunner 是 Runner 对工具注册表的最小依赖。
//
// Runner 只需要“给模型列出可用工具”和“按名称执行一次调用”两种能力，
// 因此测试可以替换成轻量 fake，而不必启动真实浏览器或桌面驱动。
type ToolRunner interface {
	Schemas() []llm.ToolSchema
	Run(ctx context.Context, call ToolCallContext) (Outcome, error)
}

// ToolCallContext 是一次工具调用的上下文，由 Runner 传给具体工具。
type ToolCallContext struct {
	// Name 是模型想调用的工具名，例如 file_read、code_run。
	Name string
	// Args 是模型传给工具的 JSON 参数，已经解析成 map。
	Args map[string]any
	// Response 是当前轮模型的完整响应，方便工具按需参考上下文。
	Response llm.Response
	// Turn 是当前第几轮 Agent 循环。
	Turn int
	// Index 是当前工具调用在本轮 tool_calls 里的下标。
	Index int
	// ToolCount 是当前轮模型一次性返回的工具调用总数。
	ToolCount int
	// SessionID 是当前本地 session 标识；未落盘时为空。
	SessionID string
	// SessionDir 是当前 session 的磁盘目录；未落盘时为空。
	SessionDir string
	// WorkingCheckpoint 是工具调用时的短期工作记忆快照。
	WorkingCheckpoint WorkingCheckpoint
	// History 是工具调用前的完整内存历史副本，用于审计和证据校验。
	History []llm.Message
	// Evidence 是当前任务已经收集的结构化证据快照。
	Evidence []evolution.Evidence
}

// WorkingCheckpoint 是当前任务的短期工作记忆。
// 它只存在于 Runner 内存中，不落盘；用于保存 SOP 关键约束、任务进度和下一步。
type WorkingCheckpoint struct {
	// KeyInfo 保存当前任务、关键约束、禁止事项、当前进度和下一步。
	KeyInfo string
	// RelatedSOP 记录本任务关联的 SOP 路径，便于后续不确定时重读。
	RelatedSOP string
	// RelatedSkill 记录本任务关联的 Skill ID，便于后续不确定时重读。
	RelatedSkill string
	// UpdatedAtTurn 记录 checkpoint 最后一次更新发生在哪个 Agent turn。
	UpdatedAtTurn int
}

// pendingSOPRead 防止模型读完 SOP 后忽略其中的约束。
// 它只在当前任务的相邻轮次中有效，checkpoint 更新后会被清空。
type pendingSOPRead struct {
	// Path 是最近一次通过 file_read 读取到的 SOP 路径。
	Path string
	// ReminderSet 表示是否已经为这次 SOP 读取注入过 checkpoint 提醒。
	ReminderSet bool
}

// pendingSkillRead 防止模型读完 Skill 后忽略其中的工作流约束。
// 它只在当前任务的相邻轮次中有效，checkpoint 更新后会被清空。
type pendingSkillRead struct {
	// ID 是最近一次通过 skill_read 读取到的 Skill ID 或 alias。
	ID string
	// ReminderSet 表示是否已经为这次 Skill 读取注入过 checkpoint 提醒。
	ReminderSet bool
}

// longTermMemorySignals 记录单次 Run 中产生的可复用经验信号。
//
// 它只用于决定是否给模型追加一次临时提示，不替代 evolution 包对写入证据的最终校验。
type longTermMemorySignals struct {
	// userRequested 表示用户明确要求保存可复用经验。
	userRequested bool
	// successfulCodeRun 表示本任务有成功执行的命令或测试可作为证据。
	successfulCodeRun bool
	// readReusableReference 表示已读取 SOP、记忆或其他可复用规则。
	readReusableReference bool
	// recoveredFromFailure 表示连续失败后出现了成功结果，通常值得总结修复路径。
	recoveredFromFailure bool
	// consecutiveFailures 用于识别“失败后恢复”这一序列，而非单次工具失败。
	consecutiveFailures int
	// prompted 防止同一任务重复插入长期记忆提示。
	prompted bool
	// finalReviewPrompted 防止模型最终回答前的复核提示造成循环。
	finalReviewPrompted bool
	// started 表示模型已经进入长期记忆工具流程，无需再提示。
	started bool
}

// Runner 表示一个 Agent 会话，负责串起模型、工具、历史消息和循环控制。
type Runner struct {
	// Client 负责和模型服务通信。
	Client llm.Client
	// Tools 负责提供工具 schema，并执行模型请求的工具。
	Tools ToolRunner
	// SystemPrompt 是每次请求模型时固定携带的系统提示词。
	SystemPrompt string
	// MaxTurns 限制最大循环轮数，避免模型不断调用工具导致死循环。
	MaxTurns int
	// LogDir 用来保存模型原始响应日志。
	LogDir string
	// ContextManager 负责在请求模型前构造可见上下文；完整历史仍保留在 history 和 history.jsonl。
	ContextManager *contextmgr.Manager
	// SessionStore 负责把对话消息追加写入 history.jsonl。
	// 为空时表示只保留内存 history，不做本地会话落盘。
	SessionStore *session.Store
	// SessionCWD 记录本次会话对应的工作目录，会写入 meta.json。
	SessionCWD string
	// SessionModel 记录本次会话使用的模型名，会写入 meta.json。
	SessionModel string
	// CloseFunc 由应用装配层注入，用于关闭 MCP 子进程等 Runner 持有的资源。
	// Runner 本身不依赖具体实现，因此测试可留空。
	CloseFunc func() error
	// SkillStore 保存启动时发现的 Skill 索引，并支持 REPL 中 reload。
	SkillStore *skill.Store
	// Observability 接收 Runner 生命周期事件；为空时 Runner 会写入本地 run.log.jsonl。
	Observability observability.Bus
	// ObservationSinks 是默认本地 JSONL 之外的观测输出，例如 Langfuse。
	ObservationSinks []observability.Sink
	// WorkingCheckpoint 保存当前任务的短期关键约束，避免读过 SOP 后在多轮执行中遗忘。
	WorkingCheckpoint WorkingCheckpoint

	// history 保存当前会话历史。小写字段表示只允许 agent 包内部直接修改。
	history []llm.Message
	// sessionID 是当前 Runner 对应的本地 session 目录名。
	// 它第一次收到用户输入时创建，之后同一个 REPL Runner 会持续复用。
	sessionID string
	// pendingHints 保存下一轮临时注入给模型的系统提醒，不写入持久 history。
	pendingHints []string
	// pendingSOPRead 记录最近一次读取的 SOP。若下一轮没有 checkpoint，会再提醒一次。
	pendingSOPRead pendingSOPRead
	// pendingSkillRead 记录最近一次读取的 Skill。若下一轮没有 checkpoint，会再提醒一次。
	pendingSkillRead pendingSkillRead
}

// Run 执行一次用户任务。流程是：用户输入 -> 调模型 -> 执行工具 -> 工具结果回灌 -> 继续调模型。
// 当模型不再返回 tool_calls，而是直接回答时，本次任务结束。
func (r *Runner) Run(ctx context.Context, input string, sink OutputSink) (RunResult, error) {
	// 没配置最大轮数时给一个保守默认值，避免无限循环。
	if r.MaxTurns <= 0 {
		r.MaxTurns = 100
	}
	runID := observability.NewRunID()
	runStartedAt := time.Now()
	// 每次运行前确保日志目录存在，日志失败属于运行环境错误。
	if err := r.ensureLogDir(); err != nil {
		return RunResult{}, err
	}

	// 用户输入先进入 history，后续每一轮模型都能看到完整上下文。
	if err := r.appendMessage(llm.Message{Role: llm.RoleUser, Content: input}, input); err != nil {
		return RunResult{}, err
	}
	obs := r.observationBus()
	defer obs.Close(ctx)
	lastTurn := 0
	finishRun := func(result RunResult, err error) (RunResult, error) {
		data := map[string]any{
			"status":      result.Status,
			"duration_ms": time.Since(runStartedAt).Milliseconds(),
			"history_len": len(r.history),
		}
		severity := observability.SeverityInfo
		if err != nil {
			data["status"] = "error"
			data["error"] = err.Error()
			severity = observability.SeverityError
		}
		r.emitObservation(ctx, obs, runID, observability.EventRunFinished, lastTurn, severity, data)
		return result, err
	}
	r.emitObservation(ctx, obs, runID, observability.EventRunStarted, 0, observability.SeverityInfo, map[string]any{
		"max_turns":   r.MaxTurns,
		"history_len": len(r.history),
		"log_dir":     r.LogDir,
	})
	r.emitObservation(ctx, obs, runID, observability.EventUserPromptSubmitted, 0, observability.SeverityInfo, map[string]any{
		"prompt": promptSummary(input),
	})
	memorySignals := longTermMemorySignals{userRequested: requestsLongTermMemory(input)}
	evidenceLedger := []evolution.Evidence{{
		ID:       "user:input",
		Source:   "user",
		Verified: memorySignals.userRequested,
		Summary:  "user explicitly requested long-term memory",
	}}
	r.maybeAddLongTermMemoryHint(&memorySignals, 0)
	r.addSOPRouteHint(input)
	messages, stats := r.buildRequestMessagesWithStats()
	r.emitObservation(ctx, obs, runID, observability.EventContextBuilt, 0, observability.SeverityInfo, contextStatsData(stats))

	for turn := 1; turn <= r.MaxTurns; turn++ {
		lastTurn = turn
		sink.WriteText(fmt.Sprintf("\nLLM Running (Turn %d) ...\n\n", turn))
		r.emitObservation(ctx, obs, runID, observability.EventTurnStarted, turn, observability.SeverityInfo, map[string]any{
			"history_len":       len(r.history),
			"request_messages":  len(messages),
			"pending_hints":     len(r.pendingHints),
			"checkpoint_active": strings.TrimSpace(r.WorkingCheckpoint.KeyInfo) != "",
		})
		// 把系统提示词、历史消息、工具 schema 一起发给模型。
		tools := r.Tools.Schemas()
		r.emitObservation(ctx, obs, runID, observability.EventLLMRequestStarted, turn, observability.SeverityInfo, llmRequestData(messages, tools, r.SystemPrompt))
		llmStartedAt := time.Now()
		stream, err := r.Client.Chat(ctx, llm.ChatRequest{
			System:   r.SystemPrompt,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			r.emitObservation(ctx, obs, runID, observability.EventLLMResponseFinished, turn, observability.SeverityError, map[string]any{
				"status":      ToolStatusError,
				"duration_ms": time.Since(llmStartedAt).Milliseconds(),
				"error":       err.Error(),
			})
			return finishRun(RunResult{}, err)
		}

		// consume 会消费流式响应：文本实时输出，最终返回完整 Response。
		resp, err := consume(stream, sink)
		if err != nil {
			r.emitObservation(ctx, obs, runID, observability.EventLLMResponseFinished, turn, observability.SeverityError, map[string]any{
				"status":      ToolStatusError,
				"duration_ms": time.Since(llmStartedAt).Milliseconds(),
				"error":       err.Error(),
			})
			return finishRun(RunResult{}, err)
		}
		r.emitObservation(ctx, obs, runID, observability.EventLLMResponseFinished, turn, observability.SeverityInfo, llmResponseData(resp, time.Since(llmStartedAt), messages, tools, r.SystemPrompt))
		// 记录模型原始响应用于排查问题，不影响主流程。
		r.logResponse(turn, resp)
		if r.pendingSOPRead.Path != "" && !containsToolCall(resp.ToolCalls, "update_working_checkpoint") && !r.pendingSOPRead.ReminderSet {
			r.addPendingHint(fmt.Sprintf("[SYSTEM HINT] 上一轮读取了 SOP：%s。如果决定按它执行，本轮应调用 update_working_checkpoint 保存 [任务]/[关键约束]/[禁止事项]/[当前进度]/[下一步]。", r.pendingSOPRead.Path))
			r.pendingSOPRead.ReminderSet = true
		}
		if r.pendingSkillRead.ID != "" && !containsToolCall(resp.ToolCalls, "update_working_checkpoint") && !r.pendingSkillRead.ReminderSet {
			r.addPendingHint(fmt.Sprintf("[SYSTEM HINT] 上一轮读取了 Skill：%s。如果决定按它执行，本轮应调用 update_working_checkpoint 保存 [任务]/[关键约束]/[禁止事项]/[当前进度]/[下一步] 和 related_skill。", r.pendingSkillRead.ID))
			r.pendingSkillRead.ReminderSet = true
		}

		// 没有 tool_calls 表示模型已经给出最终回答，任务可以结束。
		if len(resp.ToolCalls) == 0 {
			if err := r.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: resp.Content}, ""); err != nil {
				return finishRun(RunResult{}, err)
			}
			if !awaitingUserInput(resp.Content) && r.maybeForceLongTermMemoryReview(&memorySignals, turn) {
				messages, stats = r.buildRequestMessagesWithStats()
				r.emitObservation(ctx, obs, runID, observability.EventContextBuilt, turn, observability.SeverityInfo, contextStatsData(stats))
				continue
			}
			ensureTerminalLineBreak(sink, resp.Content)
			return finishRun(RunResult{Status: RunStatusDone, Response: resp}, nil)
		}

		// OpenAI-compatible 工具协议要求：
		// assistant 的 tool_calls 消息必须出现在对应 tool 结果消息之前。
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls}
		if err := r.appendMessage(assistantMsg, ""); err != nil {
			return finishRun(RunResult{}, err)
		}

		for i, call := range resp.ToolCalls {
			sink.WriteToolCall(call)

			// 模型返回的工具参数是 JSON 字符串，这里先解析成 map 给工具使用。
			toolStartedAt := time.Now()
			args, err := parseToolArgs(call.Function.Arguments)
			r.emitObservation(ctx, obs, runID, observability.EventToolStarted, turn, observability.SeverityInfo, toolStartedData(call, turn, i, args, err))
			var outcome Outcome
			if err != nil {
				outcome = Outcome{
					Data: NewToolError(
						"bad_json",
						"tool arguments are not valid JSON: "+err.Error(),
						"请重新生成合法 JSON 参数。不要省略引号、逗号或右括号；必要时先读取文件确认参数内容。",
					),
					NextPrompt: "\n",
				}
			} else {
				// Registry 会根据工具名分发到具体工具，例如 file_read.Run。
				var runErr error
				outcome, runErr = r.Tools.Run(ctx, ToolCallContext{
					Name:              call.Function.Name,
					Args:              args,
					Response:          *resp,
					Turn:              turn,
					Index:             i,
					ToolCount:         len(resp.ToolCalls),
					SessionID:         r.sessionID,
					SessionDir:        r.sessionDir(),
					WorkingCheckpoint: r.WorkingCheckpoint,
					History:           append([]llm.Message(nil), r.history...),
					Evidence:          append([]evolution.Evidence(nil), evidenceLedger...),
				})
				if runErr != nil {
					// 工具失败时不直接中断 Agent，而是把错误作为工具结果交回模型。
					// 这样模型有机会修正参数后再次调用。
					outcome = Outcome{
						Data: NewToolError(
							"tool_run_failed",
							runErr.Error(),
							"请根据错误信息修正工具名或参数后重试；如果缺少文件内容，先调用 file_read。",
						),
						NextPrompt: "\n",
					}
				}
			}
			if err == nil && call.Function.Name == "update_working_checkpoint" {
				r.updateWorkingCheckpoint(args, turn)
			}
			if err == nil && call.Function.Name == "file_read" {
				r.rememberSOPRead(args)
			}
			if err == nil && call.Function.Name == "skill_read" {
				r.rememberSkillRead(args)
			}
			r.recordLongTermMemorySignal(&memorySignals, call.Function.Name, args, outcome)
			evidenceLedger = append(evidenceLedger, newToolEvidence(call, turn, i, outcome))
			r.logToolRun(call, args, turn, i, outcome, time.Since(toolStartedAt))
			toolSeverity := observability.SeverityInfo
			if !outcomeSucceeded(outcome) {
				toolSeverity = observability.SeverityWarn
			}
			r.emitObservation(ctx, obs, runID, observability.EventToolFinished, turn, toolSeverity, toolFinishedData(call, outcome, time.Since(toolStartedAt)))
			if outcome.ShouldExit {
				return finishRun(RunResult{Status: RunStatusExited, Response: resp}, nil)
			}
			// 工具输出会被转成 role=tool 消息，下一轮模型才能读到工具结果。
			resultText := stringify(outcome.Data)
			sink.WriteToolResult(call.Function.Name, resultText)
			if err := r.appendMessage(llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    resultText,
			}, ""); err != nil {
				return finishRun(RunResult{}, err)
			}
			r.addPendingHint(outcome.NextPrompt)
		}
		if turn > 0 && turn%10 == 0 {
			r.addPendingHint("[SYSTEM HINT] 任务已运行多轮。如果已经多次失败、策略不清或涉及 SOP 约束，请重读 related_sop 并更新 update_working_checkpoint。")
		}
		r.maybeAddLongTermMemoryHint(&memorySignals, turn)
		// 工具结果已经进入完整 history；下一轮模型请求前重新构造可见上下文。
		// Context Manager 应根据预算决定是否压缩，而不是每轮固定裁剪。
		messages, stats = r.buildRequestMessagesWithStats()
		r.emitObservation(ctx, obs, runID, observability.EventContextBuilt, turn, observability.SeverityInfo, contextStatsData(stats))
	}
	// 达到最大轮数说明模型一直没有收敛，返回受控状态而不是无限运行。
	return finishRun(RunResult{Status: RunStatusMaxTurnsExceeded}, nil)
}

func (r *Runner) buildRequestMessages() []llm.Message {
	messages, _ := r.buildRequestMessagesWithStats()
	return messages
}

func (r *Runner) buildRequestMessagesWithStats() ([]llm.Message, contextmgr.Stats) {
	messages := append([]llm.Message(nil), r.history...)
	if r.ContextManager == nil {
		result := r.appendEphemeralGuidance(messages)
		return result, contextmgr.Stats{
			OriginalMessages: len(messages),
			FinalMessages:    len(result),
			OriginalChars:    messagesChars(messages),
			FinalChars:       messagesChars(result),
		}
	}
	result := r.ContextManager.Build(contextmgr.BuildInput{
		Messages:   messages,
		SessionID:  r.sessionID,
		SessionDir: r.sessionDir(),
	})
	r.logContextStats(result.Stats)
	finalMessages := r.appendEphemeralGuidance(result.Messages)
	stats := result.Stats
	stats.FinalMessages = len(finalMessages)
	stats.FinalChars = messagesChars(finalMessages)
	return finalMessages, stats
}

// updateWorkingCheckpoint 将模型提交的短期工作记忆写回 Runner。
//
// 使用 fmt.Sprint 读取 map 参数是为了兼容工具调用 JSON 解码后的动态类型；
// "<nil>" 需要显式排除，避免把缺失字段误写成字符串。
func (r *Runner) updateWorkingCheckpoint(args map[string]any, turn int) {
	if value := strings.TrimSpace(fmt.Sprint(args["key_info"])); value != "" && value != "<nil>" {
		r.WorkingCheckpoint.KeyInfo = value
	}
	if value := strings.TrimSpace(fmt.Sprint(args["related_sop"])); value != "" && value != "<nil>" {
		r.WorkingCheckpoint.RelatedSOP = value
	}
	if value := strings.TrimSpace(fmt.Sprint(args["related_skill"])); value != "" && value != "<nil>" {
		r.WorkingCheckpoint.RelatedSkill = value
	}
	r.WorkingCheckpoint.UpdatedAtTurn = turn
	r.pendingSOPRead = pendingSOPRead{}
	r.pendingSkillRead = pendingSkillRead{}
}

// rememberSOPRead 仅记录真正位于 SOP 目录或名称包含 sop 的读取操作。
// 普通文件读取不会触发 checkpoint 提醒，避免给每轮对话增加无关提示。
func (r *Runner) rememberSOPRead(args map[string]any) {
	path := strings.TrimSpace(fmt.Sprint(args["path"]))
	if !isSOPPath(path) {
		return
	}
	r.pendingSOPRead = pendingSOPRead{Path: path}
}

// rememberSkillRead 记录最近一次 Skill 读取，用于下一轮提醒模型落 checkpoint。
func (r *Runner) rememberSkillRead(args map[string]any) {
	id := strings.TrimSpace(fmt.Sprint(args["skill_id"]))
	if id == "" || id == "<nil>" {
		return
	}
	r.pendingSkillRead = pendingSkillRead{ID: id}
}

// containsToolCall 判断本轮模型是否已经调用指定工具。
// 它用于避免在模型已更新 checkpoint 时追加重复提醒。
func containsToolCall(calls []llm.ToolCall, name string) bool {
	for _, call := range calls {
		if call.Function.Name == name {
			return true
		}
	}
	return false
}

// addPendingHint 暂存只对下一次模型请求可见的系统提示。
// 这些提示不进入 history.jsonl，避免把运行控制信息污染可恢复的用户会话。
func (r *Runner) addPendingHint(hint string) {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return
	}
	r.pendingHints = append(r.pendingHints, hint)
}

// recordLongTermMemorySignal 根据已执行工具的实际结果记录长期记忆触发信号。
//
// 这里不直接写记忆，也不把结果当作最终证据；最终写入仍必须经过 evolution 的 evidence 校验。
func (r *Runner) recordLongTermMemorySignal(signals *longTermMemorySignals, name string, args map[string]any, outcome Outcome) {
	if signals == nil {
		return
	}
	if name == "start_long_term_update" && outcomeSucceeded(outcome) {
		signals.started = true
		return
	}
	if name == "memory_propose_update" || name == "memory_apply_update" {
		return
	}

	if !outcomeSucceeded(outcome) {
		signals.consecutiveFailures++
		return
	}
	if signals.consecutiveFailures >= 2 {
		signals.recoveredFromFailure = true
	}
	signals.consecutiveFailures = 0

	switch {
	case name == "code_run":
		signals.successfulCodeRun = true
	case strings.HasPrefix(name, "browser_"):
		// 浏览器工具返回的成功状态可以作为页面状态已验证的候选信号。
		signals.readReusableReference = true
	case name == "skill_read":
		signals.readReusableReference = true
	case name == "file_read" && isReusableReferencePath(fmt.Sprint(args["path"])):
		signals.readReusableReference = true
	}
}

// maybeAddLongTermMemoryHint 在下一轮模型请求前仅注入一次长期记忆提示。
//
// 提示不强制写入。模型仍需先调用 start_long_term_update，并在没有可复用、已验证经验时使用 skip。
func (r *Runner) maybeAddLongTermMemoryHint(signals *longTermMemorySignals, turn int) {
	if signals == nil || signals.prompted || signals.started {
		return
	}
	reasons := longTermMemoryReasons(signals, turn)
	if len(reasons) == 0 {
		return
	}
	signals.prompted = true
	r.addPendingHint("[LONG-TERM MEMORY HINT] 本轮可能产生可复用经验（" + strings.Join(reasons, "；") + "）。在最终答复前，请判断是否调用 start_long_term_update。只有工具验证、已读文件、浏览器确认、用户明确稳定偏好或已有记忆支持的事实才能沉淀；无值得保留内容时请不要调用，或在后续 memory_propose_update 中使用 skip=true。")
}

func (r *Runner) maybeForceLongTermMemoryReview(signals *longTermMemorySignals, turn int) bool {
	if signals == nil || signals.started || signals.finalReviewPrompted {
		return false
	}
	if r.MaxTurns > 0 && turn >= r.MaxTurns {
		return false
	}
	reasons := longTermMemoryReasons(signals, turn)
	if len(reasons) == 0 {
		return false
	}
	signals.finalReviewPrompted = true
	r.addPendingHint("[LONG-TERM MEMORY FINAL REVIEW] 模型准备结束任务，但本轮存在长期记忆信号（" + strings.Join(reasons, "；") + "），且尚未启动经验沉淀。不要直接重复最终答复。请先调用 start_long_term_update 判断是否有可复用经验；如果没有值得保留的内容，在随后 memory_propose_update 使用 skip=true。只允许沉淀工具验证、已读文件、浏览器确认、用户稳定偏好或已有记忆支持的经验；不要沉淀一次性任务事实、联系人、消息正文、临时页面内容或敏感信息。")
	return true
}

func awaitingUserInput(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return false
	}
	prompts := []string{
		"你想", "你要", "是否", "哪几个", "哪一个", "哪个", "请选择", "请确认", "请提供", "请告诉", "请回答", "请决定",
		"which", "what would you like", "do you want", "would you like", "please choose", "please confirm", "please provide", "please tell me",
	}
	for _, prompt := range prompts {
		if strings.Contains(lower, prompt) {
			return true
		}
	}
	return strings.Contains(lower, "?") || strings.Contains(lower, "？")
}

// longTermMemoryReasons 将内部布尔状态转换成人和模型都可理解的触发原因。
// 返回空切片表示当前没有足够理由要求模型评估长期记忆。
func longTermMemoryReasons(signals *longTermMemorySignals, turn int) []string {
	if signals == nil {
		return nil
	}
	var reasons []string
	if signals.userRequested {
		reasons = append(reasons, "用户明确要求保留经验")
	}
	if signals.successfulCodeRun {
		reasons = append(reasons, "已获得成功的命令/测试验证")
	}
	if signals.readReusableReference {
		reasons = append(reasons, "已读取可复用规则或确认页面状态")
	}
	if signals.recoveredFromFailure {
		reasons = append(reasons, "经历重复失败后已恢复")
	}
	if turn >= longTermMemoryTurnThreshold {
		reasons = append(reasons, "任务已运行多轮")
	}
	return reasons
}

// outcomeSucceeded 从工具可返回的几种数据形状推断是否成功。
//
// 工具错误通常作为正常的 Outcome.Data 返回，而非 Go error；
// 这里集中处理该约定，避免长期记忆证据把失败结果误标为已验证。
func outcomeSucceeded(outcome Outcome) bool {
	switch data := outcome.Data.(type) {
	case ToolErrorData:
		return false
	case map[string]any:
		status, _ := data["status"].(string)
		return status == "" || status == ToolStatusSuccess
	case string:
		lower := strings.ToLower(strings.TrimSpace(data))
		return !strings.HasPrefix(lower, "error:") && !strings.Contains(lower, `"status":"error"`)
	default:
		return true
	}
}

// newToolEvidence 为一次完成的工具调用建立仅含元数据的证据账本条目。
//
// 它刻意不保存原始工具输出：原始输出可能很大、易变化，或包含敏感内容；
// 记忆系统只需要知道“哪次调用、是否验证成功、为什么可用”。
func newToolEvidence(call llm.ToolCall, turn, index int, outcome Outcome) evolution.Evidence {
	name := call.Function.Name
	verified := toolOutcomeVerified(name, outcome)
	return evolution.Evidence{
		ID:       fmt.Sprintf("tool:%d:%d", turn, index),
		Source:   "tool",
		ToolName: name,
		Turn:     turn,
		CallID:   call.ID,
		Verified: verified,
		Summary:  toolEvidenceSummary(name, verified),
	}
}

// toolOutcomeVerified 对需要更严格判定的工具补充验证条件。
// 例如 code_run 只有退出码为 0 才可证明命令或测试成功。
func toolOutcomeVerified(name string, outcome Outcome) bool {
	if !outcomeSucceeded(outcome) {
		return false
	}
	if name != "code_run" {
		return true
	}
	data, ok := outcome.Data.(map[string]any)
	if !ok {
		return false
	}
	exitCode, ok := integerValue(data["exit_code"])
	return ok && exitCode == 0
}

// toolEvidenceSummary 生成适合写入长期记忆审计记录的简短结果说明。
func toolEvidenceSummary(name string, verified bool) string {
	if !verified {
		return name + " did not produce verified evidence"
	}
	if name == "code_run" {
		return "code_run completed with exit_code=0"
	}
	return name + " completed successfully"
}

// integerValue 兼容 JSON 数字解码成 float64 与测试直接传入 int 的两种形态。
func integerValue(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case float64:
		return int(number), true
	default:
		return 0, false
	}
}

// requestsLongTermMemory 用中英文关键词识别用户是否明确要求沉淀经验。
// 这是提示条件而不是写入授权，实际内容仍由 evolution 包进行校验。
func requestsLongTermMemory(input string) bool {
	lower := strings.ToLower(input)
	keywords := []string{
		"记住", "记下来", "沉淀", "长期记忆", "以后沿用",
		"remember", "long-term memory", "save this preference",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// isReusableReferencePath 识别通常包含项目约定的文件，而不是任意业务数据文件。
// 命中后只会触发“考虑记忆”的提示，不会自动读取或保存文件内容。
func isReusableReferencePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))))
	if path == "." || path == "" {
		return false
	}
	if strings.HasPrefix(path, "sops/") || strings.Contains(path, "/sops/") {
		return true
	}
	if strings.HasPrefix(path, "memory/") || strings.Contains(path, "/memory/") {
		return true
	}
	base := filepath.Base(path)
	return base == "go.mod" || base == "go.sum" || base == "readme.md" ||
		strings.HasPrefix(path, "configs/") || strings.Contains(path, "/configs/")
}

// appendEphemeralGuidance 把 checkpoint 和一次性提示附加到请求副本末尾。
//
// 采用 user 消息是为了让模型优先看到当前执行约束；清空 pendingHints
// 确保它们只影响一轮，不能在长对话里反复累积。
func (r *Runner) appendEphemeralGuidance(messages []llm.Message) []llm.Message {
	content := r.workingCheckpointPrompt()
	if len(r.pendingHints) > 0 {
		if content != "" {
			content += "\n\n"
		}
		content += strings.Join(r.pendingHints, "\n")
		r.pendingHints = nil
	}
	if strings.TrimSpace(content) == "" {
		return messages
	}
	return append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: content,
	})
}

// addSOPRouteHint 根据用户任务的关键词提示可能相关的操作规程。
// 这只是导航，模型仍要先用 file_read 读取 SOP 原文再按其中规则执行。
func (r *Runner) addSOPRouteHint(input string) {
	matches := routeSOPs(input)
	if len(matches) == 0 {
		return
	}
	r.addPendingHint("[SOP HINT] 这个任务可能相关：" + strings.Join(matches, "、") + "。请先 file_read 相关 SOP；如果采用其规则，请调用 update_working_checkpoint 保存关键约束和 related_sop。")
}

// workingCheckpointPrompt 将内存中的短期状态编码为下一轮可见的提示文本。
// 空 checkpoint 不生成消息，以免为每轮请求增加无意义 token。
func (r *Runner) workingCheckpointPrompt() string {
	if strings.TrimSpace(r.WorkingCheckpoint.KeyInfo) == "" &&
		strings.TrimSpace(r.WorkingCheckpoint.RelatedSOP) == "" &&
		strings.TrimSpace(r.WorkingCheckpoint.RelatedSkill) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("[WORKING CHECKPOINT]\n")
	if r.WorkingCheckpoint.KeyInfo != "" {
		b.WriteString("key_info: ")
		b.WriteString(r.WorkingCheckpoint.KeyInfo)
		b.WriteByte('\n')
	}
	if r.WorkingCheckpoint.RelatedSOP != "" {
		b.WriteString("related_sop: ")
		b.WriteString(r.WorkingCheckpoint.RelatedSOP)
		b.WriteString("\nIf unsure, re-read related_sop before continuing.\n")
	}
	if r.WorkingCheckpoint.RelatedSkill != "" {
		b.WriteString("related_skill: ")
		b.WriteString(r.WorkingCheckpoint.RelatedSkill)
		b.WriteString("\nIf unsure, call skill_read for related_skill before continuing.\n")
	}
	return strings.TrimSpace(b.String())
}

// routeSOPs 通过保守关键词路由返回至多三个候选 SOP。
// 关键词匹配并不代表 SOP 一定适用，因此结果只用于建议读取，不直接改变工具权限。
func routeSOPs(input string) []string {
	lower := strings.ToLower(input)
	routes := []struct {
		// path 是命中关键词后建议模型读取的 SOP 文件路径。
		path string
		// keywords 是用于粗略识别任务场景的关键词集合。
		keywords []string
	}{
		{path: "sops/browser_sop.md", keywords: []string{"浏览器", "网页", "页面", "点击", "输入", "selector", "cdp", "ocr", "tab", "iframe", "browser", "chrome"}},
		{path: "sops/desktop_sop.md", keywords: []string{"桌面", "真实电脑", "电脑操作", "窗口", "原生应用", "系统弹窗", "辅助功能", "accessibility", "ax", "desktop", "macos"}},
		{path: "sops/code_run_sop.md", keywords: []string{"code_run", "命令", "脚本", "后台", "服务", "端口", "进程", "timeout", "server", "python", "shell"}},
		{path: "sops/file_edit_sop.md", keywords: []string{"修改", "编辑", "写文件", "补丁", "patch", "实现", "开发", "代码", "文档", "删除文件"}},
		{path: "sops/context_sop.md", keywords: []string{"context", "上下文", "compact", "token", "tool_calls", "tool result", "历史", "压缩"}},
		{path: "sops/memory_sop.md", keywords: []string{"memory", "记忆", "长期记忆", "项目记忆", "经验", "沉淀", "sop candidate", "skill", "技能", "能力等级", "start_long_term_update", "memory_propose_update", "memory_apply_update"}},
		{path: "sops/testing_sop.md", keywords: []string{"测试", "验证", "go test", "node --check", "检查", "验收"}},
	}
	var matches []string
	seen := map[string]bool{}
	for _, route := range routes {
		for _, keyword := range route.keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				if !seen[route.path] {
					matches = append(matches, route.path)
					seen[route.path] = true
				}
				break
			}
		}
	}
	if len(matches) > 3 {
		matches = matches[:3]
	}
	return matches
}

// isSOPPath 兼容相对路径、绝对路径和 Windows 风格分隔符，判断文件是否像 SOP。
func isSOPPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	base := filepath.Base(path)
	return strings.Contains(path, "/sops/") || strings.HasPrefix(path, "sops/") || strings.Contains(base, "sop")
}

// ToolSchemas 返回当前注册器暴露给模型的工具定义。
func (r *Runner) ToolSchemas() []llm.ToolSchema {
	return r.Tools.Schemas()
}

// Close 释放本 Runner 持有的外部资源。它是幂等的，便于 ask、REPL 和错误清理
// 路径统一调用；未配置 CloseFunc 的轻量测试 Runner 不做任何事。
func (r *Runner) Close() error {
	if r == nil || r.CloseFunc == nil {
		return nil
	}
	closeFunc := r.CloseFunc
	r.CloseFunc = nil
	return closeFunc()
}

// SessionID 返回当前 Runner 绑定的本地 session ID。
//
// 如果用户刚启动 REPL、还没有输入任何普通任务，session 还不会创建，
// 此时返回空字符串。这样欢迎页可以展示 "new session"，而不是强行创建空会话。
func (r *Runner) SessionID() string {
	return r.sessionID
}

// HistoryLen 返回当前内存历史消息数量。
// 这个数量只用于 REPL 展示和 slash 命令，不参与模型请求逻辑。
func (r *Runner) HistoryLen() int {
	return len(r.history)
}

// Reset 清空内存会话绑定，不删除已经写入磁盘的历史目录。
// 它服务于 REPL 的 /clear，让用户开始新会话时保留旧会话可恢复。
func (r *Runner) Reset() {
	r.history = nil
	r.sessionID = ""
}

// ResumeSession 把已有 session 的历史装回 Runner。
//
// sessionID 会继续作为后续 history.jsonl 的追加目标；
// history 会复制一份保存到 Runner 内部，避免调用方后续修改切片影响正在运行的会话。
func (r *Runner) ResumeSession(sessionID string, history []llm.Message) {
	r.sessionID = sessionID
	r.history = append([]llm.Message(nil), history...)
}

// appendMessage 同时维护内存 history 和本地 history.jsonl。
//
// Runner 的主流程只调用这个方法追加消息，避免某些分支只写内存、不写文件。
// titleSeed 只在第一次创建 session 时使用，通常传第一条用户输入。
func (r *Runner) appendMessage(message llm.Message, titleSeed string) error {
	if err := r.ensureSession(titleSeed); err != nil {
		return err
	}
	if r.SessionStore != nil && r.sessionID != "" {
		if err := r.SessionStore.AppendHistory(r.sessionID, message); err != nil {
			return err
		}
	}
	r.history = append(r.history, message)
	return nil
}

// ensureSession 确保当前 Runner 已经有对应的本地 session。
//
// 没有配置 SessionStore 时表示关闭会话落盘，Runner 仍然只用内存 history 正常运行。
// 配置了 SessionStore 时，第一次用户输入会触发创建 meta.json，后续消息追加到同一个 history.jsonl。
func (r *Runner) ensureSession(titleSeed string) error {
	if r.SessionStore == nil || r.sessionID != "" {
		return nil
	}
	sess, err := r.SessionStore.Create(makeSessionTitle(titleSeed), r.SessionCWD, r.SessionModel)
	if err != nil {
		return err
	}
	r.sessionID = sess.ID
	return nil
}

// sessionDir 返回当前会话目录；尚未创建会话或关闭落盘时返回空字符串。
func (r *Runner) sessionDir() string {
	if r.SessionStore == nil || r.sessionID == "" {
		return ""
	}
	return r.SessionStore.SessionDir(r.sessionID)
}

// makeSessionTitle 根据用户第一条输入生成会话标题。
//
// 标题只是给人看的，不参与模型请求；这里保持简单截断，不引入额外摘要模型调用。
func makeSessionTitle(input string) string {
	title := strings.TrimSpace(input)
	if title == "" {
		return defaultSessionTitle
	}
	if len([]rune(title)) <= maxSessionTitleLength {
		return title
	}
	return string([]rune(title)[:maxSessionTitleLength]) + "..."
}

// consume 消费模型流式事件：文本事件直接输出，完成事件返回完整响应，错误事件返回 error。
func consume(stream <-chan llm.Event, sink OutputSink) (*llm.Response, error) {
	var written strings.Builder
	for event := range stream {
		switch event.Type {
		case llm.EventText:
			sink.WriteText(event.Text)
			written.WriteString(event.Text)
		case llm.EventDone:
			if event.Response == nil {
				return &llm.Response{}, nil
			}
			writeMissingFinalText(sink, written.String(), event.Response.Content)
			return event.Response, nil
		case llm.EventError:
			if event.Err != nil {
				sink.WriteError(event.Err)
				return nil, event.Err
			}
		}
	}
	return nil, fmt.Errorf("llm stream closed without done event")
}

// writeMissingFinalText 补齐部分供应商没有以 SSE delta 发出的最终文本尾部。
// 若完整响应与已输出前缀不一致，宁可不重复输出，避免终端出现错乱内容。
func writeMissingFinalText(sink OutputSink, written string, final string) {
	if final == "" {
		return
	}
	if written == "" {
		sink.WriteText(final)
		return
	}
	if strings.HasPrefix(final, written) && len(final) > len(written) {
		sink.WriteText(final[len(written):])
	}
}

// ensureTerminalLineBreak 让直接结束的模型输出不会与下一段终端提示连在同一行。
func ensureTerminalLineBreak(sink OutputSink, content string) {
	if content == "" || strings.HasSuffix(content, "\n") {
		return
	}
	sink.WriteText("\n")
}

// parseToolArgs 把模型返回的 JSON 参数字符串解析成工具可用的 map。
func parseToolArgs(raw string) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	return args, nil
}

// stringify 把工具结果转成字符串，方便写入 role=tool 的消息 Content。
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		data, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(data)
	}
}

// ensureLogDir 确保日志目录存在。LogDir 为空时表示不写日志。
func (r *Runner) ensureLogDir() error {
	if r.LogDir == "" {
		return nil
	}
	return os.MkdirAll(r.LogDir, 0755)
}

// logResponse 记录每轮模型原始响应，方便后续排查 tool_calls 或流式解析问题。
func (r *Runner) logResponse(turn int, resp *llm.Response) {
	if r.LogDir == "" || resp == nil {
		return
	}
	path := filepath.Join(r.LogDir, time.Now().Format("20060102")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "=== turn %d %s ===\n%s\n\n", turn, time.Now().Format(time.RFC3339), resp.Raw)
}
