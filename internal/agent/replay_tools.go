package agent

import (
	"context"
	"fmt"

	"cohort/internal/llm"
	"cohort/internal/replay"
)

// ReplayToolRunner 在分叉点之前返回已录制的 Observation，之后委托真实工具。
// 这样前缀重放不会重复文件写入、浏览器点击或外部 MCP 副作用。
type ReplayToolRunner struct {
	Base                 ToolRunner
	Plan                 replay.ForkPlan
	ObservationOverrides map[string]string
}

func (r ReplayToolRunner) Schemas() []llm.ToolSchema {
	if r.Base == nil {
		return nil
	}
	return r.Base.Schemas()
}

func (r ReplayToolRunner) Run(ctx context.Context, call ToolCallContext) (Outcome, error) {
	if call.Turn >= r.Plan.ForkTurn {
		if r.Base == nil {
			return Outcome{}, fmt.Errorf("fork replay reached live suffix without a tool runner")
		}
		return r.Base.Run(ctx, call)
	}
	key := replay.ToolFrameKey(call.Turn, call.Index, recordedCallID(call))
	recorded, ok := r.Plan.Tools[key]
	if !ok {
		return Outcome{}, fmt.Errorf(
			"recorded tool result missing at turn %d index %d for %s",
			call.Turn,
			call.Index,
			call.Name,
		)
	}
	if recorded.Call.Function.Name != call.Name ||
		replay.StableHash(recorded.Arguments) != replay.StableHash(call.Args) {
		return Outcome{}, fmt.Errorf(
			"tool prefix diverged at turn %d index %d: expected %s, got %s",
			call.Turn,
			call.Index,
			recorded.Call.Function.Name,
			call.Name,
		)
	}
	result := recorded.Result
	if override, exists := r.ObservationOverrides[key]; exists {
		result = override
	}
	return Outcome{
		Data:       result,
		NextPrompt: recorded.NextPrompt,
		ShouldExit: recorded.ShouldExit,
		Audit:      recorded.Audit,
	}, nil
}

func recordedCallID(call ToolCallContext) string {
	if call.Index < 0 || call.Index >= len(call.Response.ToolCalls) {
		return ""
	}
	return call.Response.ToolCalls[call.Index].ID
}
