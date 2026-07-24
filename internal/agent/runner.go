package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohert/internal/contextmgr"
	"cohert/internal/evolution"
	"cohert/internal/llm"
	"cohert/internal/session"
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
	// UpdatedAtTurn 记录 checkpoint 最后一次更新发生在哪个 Agent turn。
	UpdatedAtTurn int
}

type pendingSOPRead struct {
	// Path 是最近一次通过 file_read 读取到的 SOP 路径。
	Path string
	// ReminderSet 表示是否已经为这次 SOP 读取注入过 checkpoint 提醒。
	ReminderSet bool
}

// longTermMemorySignals 记录单次 Run 中产生的可复用经验信号。
//
// 它只用于决定是否给模型追加一次临时提示，不替代 evolution 包对写入证据的最终校验。
type longTermMemorySignals struct {
	userRequested         bool
	successfulCodeRun     bool
	readReusableReference bool
	recoveredFromFailure  bool
	consecutiveFailures   int
	prompted              bool
	finalReviewPrompted   bool
	started               bool
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
}

// Run 执行一次用户任务。流程是：用户输入 -> 调模型 -> 执行工具 -> 工具结果回灌 -> 继续调模型。
// 当模型不再返回 tool_calls，而是直接回答时，本次任务结束。
func (r *Runner) Run(ctx context.Context, input string, sink OutputSink) (RunResult, error) {
	// 没配置最大轮数时给一个保守默认值，避免无限循环。
	if r.MaxTurns <= 0 {
		r.MaxTurns = 100
	}
	// 每次运行前确保日志目录存在，日志失败属于运行环境错误。
	if err := r.ensureLogDir(); err != nil {
		return RunResult{}, err
	}

	// 用户输入先进入 history，后续每一轮模型都能看到完整上下文。
	if err := r.appendMessage(llm.Message{Role: llm.RoleUser, Content: input}, input); err != nil {
		return RunResult{}, err
	}
	memorySignals := longTermMemorySignals{userRequested: requestsLongTermMemory(input)}
	evidenceLedger := []evolution.Evidence{{
		ID:       "user:input",
		Source:   "user",
		Verified: memorySignals.userRequested,
		Summary:  "user explicitly requested long-term memory",
	}}
	r.maybeAddLongTermMemoryHint(&memorySignals, 0)
	r.addSOPRouteHint(input)
	messages := r.buildRequestMessages()

	for turn := 1; turn <= r.MaxTurns; turn++ {
		sink.WriteText(fmt.Sprintf("\nLLM Running (Turn %d) ...\n\n", turn))
		// 把系统提示词、历史消息、工具 schema 一起发给模型。
		stream, err := r.Client.Chat(ctx, llm.ChatRequest{
			System:   r.SystemPrompt,
			Messages: messages,
			Tools:    r.Tools.Schemas(),
		})
		if err != nil {
			return RunResult{}, err
		}

		// consume 会消费流式响应：文本实时输出，最终返回完整 Response。
		resp, err := consume(stream, sink)
		if err != nil {
			return RunResult{}, err
		}
		// 记录模型原始响应用于排查问题，不影响主流程。
		r.logResponse(turn, resp)
		if r.pendingSOPRead.Path != "" && !containsToolCall(resp.ToolCalls, "update_working_checkpoint") && !r.pendingSOPRead.ReminderSet {
			r.addPendingHint(fmt.Sprintf("[SYSTEM HINT] 上一轮读取了 SOP：%s。如果决定按它执行，本轮应调用 update_working_checkpoint 保存 [任务]/[关键约束]/[禁止事项]/[当前进度]/[下一步]。", r.pendingSOPRead.Path))
			r.pendingSOPRead.ReminderSet = true
		}

		// 没有 tool_calls 表示模型已经给出最终回答，任务可以结束。
		if len(resp.ToolCalls) == 0 {
			if err := r.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: resp.Content}, ""); err != nil {
				return RunResult{}, err
			}
			if r.maybeForceLongTermMemoryReview(&memorySignals, turn) {
				messages = r.buildRequestMessages()
				continue
			}
			ensureTerminalLineBreak(sink, resp.Content)
			return RunResult{Status: RunStatusDone, Response: resp}, nil
		}

		// OpenAI-compatible 工具协议要求：
		// assistant 的 tool_calls 消息必须出现在对应 tool 结果消息之前。
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls}
		if err := r.appendMessage(assistantMsg, ""); err != nil {
			return RunResult{}, err
		}

		for i, call := range resp.ToolCalls {
			sink.WriteToolCall(call)

			// 模型返回的工具参数是 JSON 字符串，这里先解析成 map 给工具使用。
			args, err := parseToolArgs(call.Function.Arguments)
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
			r.recordLongTermMemorySignal(&memorySignals, call.Function.Name, args, outcome)
			evidenceLedger = append(evidenceLedger, newToolEvidence(call, turn, i, outcome))
			if outcome.ShouldExit {
				return RunResult{Status: RunStatusExited, Response: resp}, nil
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
				return RunResult{}, err
			}
			r.addPendingHint(outcome.NextPrompt)
		}
		if turn > 0 && turn%10 == 0 {
			r.addPendingHint("[SYSTEM HINT] 任务已运行多轮。如果已经多次失败、策略不清或涉及 SOP 约束，请重读 related_sop 并更新 update_working_checkpoint。")
		}
		r.maybeAddLongTermMemoryHint(&memorySignals, turn)
		// 工具结果已经进入完整 history；下一轮模型请求前重新构造可见上下文。
		// Context Manager 应根据预算决定是否压缩，而不是每轮固定裁剪。
		messages = r.buildRequestMessages()
	}
	// 达到最大轮数说明模型一直没有收敛，返回受控状态而不是无限运行。
	return RunResult{Status: RunStatusMaxTurnsExceeded}, nil
}

func (r *Runner) buildRequestMessages() []llm.Message {
	messages := append([]llm.Message(nil), r.history...)
	if r.ContextManager == nil {
		return r.appendEphemeralGuidance(messages)
	}
	result := r.ContextManager.Build(contextmgr.BuildInput{
		Messages:   messages,
		SessionID:  r.sessionID,
		SessionDir: r.sessionDir(),
	})
	r.logContextStats(result.Stats)
	return r.appendEphemeralGuidance(result.Messages)
}

func (r *Runner) updateWorkingCheckpoint(args map[string]any, turn int) {
	if value := strings.TrimSpace(fmt.Sprint(args["key_info"])); value != "" && value != "<nil>" {
		r.WorkingCheckpoint.KeyInfo = value
	}
	if value := strings.TrimSpace(fmt.Sprint(args["related_sop"])); value != "" && value != "<nil>" {
		r.WorkingCheckpoint.RelatedSOP = value
	}
	r.WorkingCheckpoint.UpdatedAtTurn = turn
	r.pendingSOPRead = pendingSOPRead{}
}

func (r *Runner) rememberSOPRead(args map[string]any) {
	path := strings.TrimSpace(fmt.Sprint(args["path"]))
	if !isSOPPath(path) {
		return
	}
	r.pendingSOPRead = pendingSOPRead{Path: path}
}

func containsToolCall(calls []llm.ToolCall, name string) bool {
	for _, call := range calls {
		if call.Function.Name == name {
			return true
		}
	}
	return false
}

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

// newToolEvidence creates a metadata-only ledger entry for one completed tool call.
// It intentionally excludes raw tool output, which can be large, volatile, or sensitive.
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

func toolEvidenceSummary(name string, verified bool) string {
	if !verified {
		return name + " did not produce verified evidence"
	}
	if name == "code_run" {
		return "code_run completed with exit_code=0"
	}
	return name + " completed successfully"
}

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

func (r *Runner) addSOPRouteHint(input string) {
	matches := routeSOPs(input)
	if len(matches) == 0 {
		return
	}
	r.addPendingHint("[SOP HINT] 这个任务可能相关：" + strings.Join(matches, "、") + "。请先 file_read 相关 SOP；如果采用其规则，请调用 update_working_checkpoint 保存关键约束和 related_sop。")
}

func (r *Runner) workingCheckpointPrompt() string {
	if strings.TrimSpace(r.WorkingCheckpoint.KeyInfo) == "" && strings.TrimSpace(r.WorkingCheckpoint.RelatedSOP) == "" {
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
	return strings.TrimSpace(b.String())
}

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

func isSOPPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	base := filepath.Base(path)
	return strings.Contains(path, "/sops/") || strings.HasPrefix(path, "sops/") || strings.Contains(base, "sop")
}

func (r *Runner) ToolSchemas() []llm.ToolSchema {
	return r.Tools.Schemas()
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
