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
)

type ToolRunner interface {
	Schemas() []llm.ToolSchema
	Run(ctx context.Context, call ToolCallContext) (Outcome, error)
}

// ToolCallContext 是一次工具调用的上下文，由 Runner 传给具体工具。
type ToolCallContext struct {
	// Name 是模型想调用的工具名，例如 file_read、code_run。
	Name      string
	Args      map[string]any // Args 是模型传给工具的 JSON 参数，已经解析成 map。
	Response  llm.Response   // Response 是当前轮模型的完整响应，方便工具按需参考上下文。
	Turn      int            // Turn 是当前第几轮 Agent 循环。
	Index     int            // Index 是当前工具调用在本轮 tool_calls 里的下标。
	ToolCount int
}

// Runner 表示一个 Agent 会话，负责串起模型、工具、历史消息和循环控制。
type Runner struct {
	Client       llm.Client // Client 负责和模型服务通信。
	Tools        ToolRunner // Tools 负责提供工具 schema，并执行模型请求的工具。
	SystemPrompt string     // SystemPrompt 是每次请求模型时固定携带的系统提示词。
	MaxTurns     int        // MaxTurns 限制最大循环轮数，避免模型不断调用工具导致死循环。
	LogDir       string     // LogDir 用来保存模型原始响应日志。
	// ContextManager 负责在请求模型前构造可见上下文；完整历史仍保留在 history 和 history.jsonl。
	ContextManager *contextmgr.Manager
	// SessionStore 负责把对话消息追加写入 history.jsonl。
	// 为空时表示只保留内存 history，不做本地会话落盘。
	SessionStore *session.Store
	// SessionCWD 记录本次会话对应的工作目录，会写入 meta.json。
	SessionCWD string
	// SessionModel 记录本次会话使用的模型名，会写入 meta.json。
	SessionModel string

	// history 保存当前会话历史。小写字段表示只允许 agent 包内部直接修改。
	history []llm.Message
	// sessionID 是当前 Runner 对应的本地 session 目录名。
	// 它第一次收到用户输入时创建，之后同一个 REPL Runner 会持续复用。
	sessionID string
}

// Run 执行一次用户任务。流程是：用户输入 -> 调模型 -> 执行工具 -> 工具结果回灌 -> 继续调模型。
// 当模型不再返回 tool_calls，而是直接回答时，本次任务结束。
func (r *Runner) Run(ctx context.Context, input string, sink OutputSink) (RunResult, error) {
	// 没配置最大轮数时给一个保守默认值，避免无限循环。
	if r.MaxTurns <= 0 {
		r.MaxTurns = 40
	}
	// 每次运行前确保日志目录存在，日志失败属于运行环境错误。
	if err := r.ensureLogDir(); err != nil {
		return RunResult{}, err
	}

	// 用户输入先进入 history，后续每一轮模型都能看到完整上下文。
	if err := r.appendMessage(llm.Message{Role: llm.RoleUser, Content: input}, input); err != nil {
		return RunResult{}, err
	}
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

		// 没有 tool_calls 表示模型已经给出最终回答，任务可以结束。
		if len(resp.ToolCalls) == 0 {
			if err := r.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: resp.Content}, ""); err != nil {
				return RunResult{}, err
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
					Name:      call.Function.Name,
					Args:      args,
					Response:  *resp,
					Turn:      turn,
					Index:     i,
					ToolCount: len(resp.ToolCalls),
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
		}
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
		return messages
	}
	result := r.ContextManager.Build(contextmgr.BuildInput{
		Messages:   messages,
		SessionID:  r.sessionID,
		SessionDir: r.sessionDir(),
	})
	r.logContextStats(result.Stats)
	return result.Messages
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
