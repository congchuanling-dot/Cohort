package agent

import (
	"strings"

	"cohort/internal/llm"
)

const maxFinishGuardRetries = 1

type finishGuardDecision struct {
	Allow  bool
	Reason string
	Prompt string
}

// evaluateFinishGuard keeps the default Agent Loop semantics intact: no
// tool_calls means the model may finish. It only blocks strong abnormal exits
// where continuing once is safer and easier to debug than silently accepting
// the response.
func evaluateFinishGuard(input string, resp *llm.Response) finishGuardDecision {
	if resp == nil {
		return finishGuardDecision{
			Reason: "nil_response",
			Prompt: "上一轮模型返回了空响应对象。请重新评估用户任务；如果需要读取、写入、执行命令或验证状态，必须调用工具；否则给出明确最终结论。",
		}
	}
	content := strings.TrimSpace(resp.Content)
	switch {
	case content == "":
		return finishGuardDecision{
			Reason: "empty_response",
			Prompt: "上一轮模型没有返回任何文本或工具调用。请继续完成用户任务：需要外部信息或文件状态时调用工具；如果任务已经完成，请给出非空最终结论。",
		}
	case responseLooksTruncated(resp.Raw):
		return finishGuardDecision{
			Reason: "truncated_response",
			Prompt: "上一轮模型响应疑似因 max_tokens/length 被截断。请不要复述已截断内容；继续完成未完成部分，必要时调用工具验证后再给最终结论。",
		}
	case containsLargeCodeBlock(content) && taskLikelyRequiresFileWrite(input):
		return finishGuardDecision{
			Reason: "large_code_block_without_file_tool",
			Prompt: "上一轮输出了大段代码，但用户任务看起来需要修改项目文件。请使用 file_read/file_write/file_patch 落盘变更并验证；不要只在最终文本中贴代码。",
		}
	case claimsCompletionWithoutEvidence(input, content):
		return finishGuardDecision{
			Reason: "completion_without_evidence",
			Prompt: "上一轮声称任务已完成，但当前任务需要修改或验证。请先用工具检查真实状态、运行必要验证，确认后再给最终结论。",
		}
	default:
		return finishGuardDecision{Allow: true}
	}
}

func responseLooksTruncated(raw string) bool {
	raw = strings.ToLower(raw)
	if raw == "" {
		return false
	}
	return strings.Contains(raw, `"finish_reason":"length"`) ||
		strings.Contains(raw, `"finish_reason": "length"`) ||
		strings.Contains(raw, `"stop_reason":"max_tokens"`) ||
		strings.Contains(raw, `"stop_reason": "max_tokens"`)
}

func containsLargeCodeBlock(content string) bool {
	if strings.Count(content, "```") < 2 {
		return false
	}
	lines := strings.Count(content, "\n")
	return len([]rune(content)) > 2400 || lines > 80
}

func taskLikelyRequiresFileWrite(input string) bool {
	lower := strings.ToLower(input)
	keywords := []string{
		"实现", "修改", "修复", "新增", "补全", "落地", "开发", "写入", "创建", "更新",
		"implement", "modify", "fix", "add", "create", "update", "write",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func claimsCompletionWithoutEvidence(input string, content string) bool {
	if !taskLikelyRequiresFileWrite(input) {
		return false
	}
	lower := strings.ToLower(content)
	claims := []string{
		"已完成", "完成了", "已经实现", "已实现", "修改完成", "修复完成",
		"done", "completed", "implemented", "fixed",
	}
	for _, claim := range claims {
		if strings.Contains(lower, claim) {
			return true
		}
	}
	return false
}
