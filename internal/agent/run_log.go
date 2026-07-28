package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/llm"
)

const runLogFileName = "run.log"

// runLogEntry 是一次工具调用完成后的最小审计记录。
//
// run.log 的目标是回答“执行了什么、是否经过授权、结果如何”，而不是复制用户
// 输入、密钥或远端正文。它统一记录所有工具，MCP 工具会额外填充外部来源字段。
type runLogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	SessionID  string    `json:"session_id,omitempty"`
	Turn       int       `json:"turn"`
	Index      int       `json:"index"`
	Event      string    `json:"event"`
	Tool       string    `json:"tool"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`

	ArgsHash    string `json:"args_hash,omitempty"`
	ArgsSummary string `json:"args_summary,omitempty"`
	ResultChars int    `json:"result_chars"`
	Truncated   bool   `json:"truncated,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`

	External           bool   `json:"external,omitempty"`
	Server             string `json:"server,omitempty"`
	MCPTool            string `json:"mcp_tool,omitempty"`
	Risk               string `json:"risk,omitempty"`
	PermissionDecision string `json:"permission_decision,omitempty"`
}

// logToolRun 以 JSONL 追加一条工具结果审计。日志写入失败不影响主任务，
// 因为审计能力不应让用户已经确认的外部操作在执行后被误报为失败。
func (r *Runner) logToolRun(
	call llm.ToolCall,
	args map[string]any,
	turn, index int,
	outcome Outcome,
	duration time.Duration,
) {
	path := r.runLogPath()
	if path == "" {
		return
	}
	entry := runLogEntry{
		Timestamp:   time.Now().UTC(),
		SessionID:   r.sessionID,
		Turn:        turn,
		Index:       index,
		Event:       "tool_completed",
		Tool:        call.Function.Name,
		Status:      outcomeStatus(outcome),
		DurationMS:  duration.Milliseconds(),
		ArgsHash:    stableArgsHash(args),
		ArgsSummary: redactArgsSummary(args),
	}
	entry.ResultChars, entry.Truncated, entry.ErrorCode = outcomeAuditShape(outcome.Data)
	applyOutcomeAudit(&entry, outcome.Audit)

	content, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if openErr != nil {
		return
	}
	_, _ = file.Write(append(content, '\n'))
	_ = file.Close()
}

// runLogPath 优先把审计文件放到独立 session 目录，便于同一会话的 history、
// context.log 和 run.log 一起排查。未启用 SessionStore 的测试或嵌入场景才
// 回退到 LogDir。
func (r *Runner) runLogPath() string {
	if sessionDir := r.sessionDir(); sessionDir != "" {
		return filepath.Join(sessionDir, runLogFileName)
	}
	if strings.TrimSpace(r.LogDir) == "" {
		return ""
	}
	return filepath.Join(r.LogDir, runLogFileName)
}

func outcomeStatus(outcome Outcome) string {
	if !outcomeSucceeded(outcome) {
		return ToolStatusError
	}
	return ToolStatusSuccess
}

// outcomeAuditShape 只读取安全的大小、状态和错误码，不把结果正文写到 run.log。
func outcomeAuditShape(data any) (resultChars int, truncated bool, errorCode string) {
	switch value := data.(type) {
	case ToolErrorData:
		return 0, false, value.Code
	case map[string]any:
		if content, ok := value["content"].(string); ok {
			resultChars = len(content)
		}
		if value["truncated"] == true {
			truncated = true
		}
		if code, ok := value["code"].(string); ok {
			errorCode = code
		}
		return resultChars, truncated, errorCode
	case string:
		return len(value), false, ""
	default:
		content, err := json.Marshal(value)
		if err != nil {
			return 0, false, ""
		}
		return len(content), false, ""
	}
}

// applyOutcomeAudit 仅接受 MCPTool 构造的白名单字段，阻止未来工具把任意正文
// 经 Audit map 漏写入日志。
func applyOutcomeAudit(entry *runLogEntry, audit map[string]any) {
	if entry == nil || audit == nil {
		return
	}
	entry.External, _ = audit["external"].(bool)
	entry.Server, _ = audit["server"].(string)
	entry.MCPTool, _ = audit["mcp_tool"].(string)
	entry.Risk, _ = audit["risk"].(string)
	entry.PermissionDecision, _ = audit["permission_decision"].(string)
	if hash, ok := audit["args_hash"].(string); ok && hash != "" {
		entry.ArgsHash = hash
	}
	if status, ok := audit["status"].(string); ok && status != "" {
		entry.Status = status
	}
}

// stableArgsHash 为任意工具输入生成可关联但不可反推正文的摘要。
func stableArgsHash(args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	content, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// redactArgsSummary 保留参数形状，递归隐藏常见密钥字段和值较长的正文。
// 这让审计日志仍能定位“调用了哪个字段”，但不会成为消息或 token 的副本。
func redactArgsSummary(args map[string]any) string {
	if args == nil {
		return "{}"
	}
	content, err := json.Marshal(redactValue(args, ""))
	if err != nil {
		return "{}"
	}
	const maxChars = 400
	if len(content) > maxChars {
		return string(content[:maxChars]) + "...[truncated]"
	}
	return string(content)
}

func redactValue(value any, key string) any {
	lowerKey := strings.ToLower(key)
	for _, sensitive := range []string{
		"authorization", "cookie", "credential", "secret", "token", "password",
		"api_key", "access_key", "private_key",
	} {
		if strings.Contains(lowerKey, sensitive) {
			return "[redacted]"
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = redactValue(childValue, childKey)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactValue(child, key)
		}
		return result
	case string:
		if len(typed) > 160 {
			return typed[:160] + "...[truncated]"
		}
		return typed
	default:
		return value
	}
}
