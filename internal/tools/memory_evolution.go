package tools

import (
	"context"
	"fmt"
	"strings"

	"cohort/internal/agent"
	"cohort/internal/evolution"
	"cohort/internal/llm"
)

// StartLongTermUpdate 初始化受控长期记忆沉淀流程，但不直接写入经验内容。
type StartLongTermUpdate struct {
	manager evolution.Manager
}

func NewStartLongTermUpdate(workspace string) *StartLongTermUpdate {
	return &StartLongTermUpdate{manager: evolution.NewManager(workspace)}
}

func (t *StartLongTermUpdate) Name() string { return ToolNameStartLongTermUpdate }

func (t *StartLongTermUpdate) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Start controlled long-term memory distillation after a meaningful task. This only initializes memory files and returns the required policy; it does not write learned facts.",
		Parameters: objectSchema(map[string]any{
			"reason": stringProp("Why this task may contain reusable long-term knowledge. Use skip-worthy judgement; routine tasks should not write memory."),
		}, "reason"),
	}}
}

func (t *StartLongTermUpdate) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	_ = ctx
	created, err := t.manager.EnsureStructure()
	if err != nil {
		return agent.Outcome{}, err
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":             agent.ToolStatusSuccess,
			"created_files":      created,
			"memory_root":        t.manager.MemoryRoot(),
			"session_id":         call.SessionID,
			"checkpoint":         call.WorkingCheckpoint.KeyInfo,
			"related_sop":        call.WorkingCheckpoint.RelatedSOP,
			"available_evidence": call.Evidence,
			"policy":             "No Execution, No Memory. Propose only verified, reusable facts. Use structured fields: scene, trigger_keywords, lesson, recommended_steps, evidence_ids. Do not store secrets, volatile state, guesses, failed hypotheses, raw logs, or one-off task progress.",
			"project_id":         t.manager.ProjectID,
			"allowed_targets":    []string{evolution.GlobalMemoryPath, t.manager.ProjectMemoryPath(), evolution.SOPCandidateMemoryPath},
			"next_step":          "Call memory_propose_update with skip=true if nothing is worth keeping, otherwise provide structured candidates with type, target, scene, trigger_keywords, lesson, recommended_steps, evidence_ids, risk, and action=append. Use promote_to_sop=true only for repeated stable workflows worth a reviewed SOP candidate. Each evidence_id must be listed in available_evidence and verified=true.",
		},
		NextPrompt: "\n[SYSTEM HINT] 长期记忆更新已启动。只有工具验证、已读文件、成功测试、浏览器确认、用户明确稳定偏好或已存在记忆支持的信息才能进入 memory_propose_update；普通过程日志请 skip。\n",
	}, nil
}

// MemoryProposeUpdate 校验候选记忆更新，但不写入任何文件。
type MemoryProposeUpdate struct {
	manager evolution.Manager
}

func NewMemoryProposeUpdate(workspace string) *MemoryProposeUpdate {
	return &MemoryProposeUpdate{manager: evolution.NewManager(workspace)}
}

func (t *MemoryProposeUpdate) Name() string { return ToolNameMemoryProposeUpdate }

func (t *MemoryProposeUpdate) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Validate proposed long-term memory candidates. This never writes files. Use skip=true when no reusable verified memory should be kept.",
		Parameters: objectSchema(map[string]any{
			"skip":   boolProp("Set true when there is no worthwhile long-term memory to persist.", false),
			"reason": stringProp("Reason for skipping or proposing memory."),
			"candidates": map[string]any{
				"type":        "array",
				"description": "Candidate memory updates. Prefer structured fields: scene, trigger_keywords, lesson, recommended_steps, evidence_ids, risk, and action=append.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":             stringProp("Memory type, for example project_lesson, user_preference, or sop_candidate."),
						"target":           stringProp("Allowed target: memory/global.md, the project memory path returned by start_long_term_update, or memory/reflection/sop_candidates.md."),
						"scene":            stringProp("Short reusable scene, for example Lark browser automation."),
						"trigger_keywords": stringArrayProp("Keywords that should retrieve this memory later."),
						"lesson":           stringProp("The durable lesson to remember. Prefer this over free-form content."),
						"recommended_steps": map[string]any{
							"type":        "array",
							"description": "Concrete repeatable steps for this lesson.",
							"items":       map[string]any{"type": "string"},
						},
						"content": stringProp("Optional additional notes for compatibility."),
						"evidence_ids": map[string]any{
							"type":        "array",
							"description": "Verified EvidenceLedger IDs from start_long_term_update, for example [\"tool:1:0\"]. Free-form evidence claims are not accepted.",
							"items":       map[string]any{"type": "string"},
						},
						"risk":                       stringProp("Risk level: low, medium, or high."),
						"action":                     stringProp("Only append is allowed in P0."),
						"promote_to_sop":             boolProp("True when this repeated stable workflow should be recorded as a reviewed SOP candidate.", false),
						"sop_title":                  stringProp("Optional SOP candidate title when promote_to_sop is true."),
						"sop_path":                   stringProp("Optional proposed SOP path under sops/, for example sops/lark_browser_automation.md."),
						"requires_user_confirmation": boolProp("True if this should not be applied automatically.", false),
					},
				},
			},
		}),
	}}
}

func (t *MemoryProposeUpdate) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	_ = ctx
	if asBool(call.Args["skip"], false) {
		return agent.Outcome{
			Data: map[string]any{
				"status":  agent.ToolStatusSuccess,
				"skipped": true,
				"reason":  asString(call.Args["reason"]),
			},
			NextPrompt: "\n",
		}, nil
	}
	candidates := parseMemoryCandidates(call.Args["candidates"])
	if len(candidates) == 0 {
		return agent.Outcome{
			Data: agent.NewToolError(
				"no_candidates",
				"memory_propose_update requires candidates or skip=true",
				"如果没有值得长期沉淀的信息，请用 skip=true；否则提供 candidates 数组。",
			),
			NextPrompt: "\n",
		}, nil
	}
	proposed := make([]evolution.ProposedCandidate, 0, len(candidates))
	validCount := 0
	for _, candidate := range candidates {
		validation := t.manager.ValidateCandidate(candidate, call.Evidence)
		if validation.Valid {
			validCount++
		}
		proposed = append(proposed, evolution.ProposedCandidate{
			Candidate:  candidate,
			Validation: validation,
		})
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":      agent.ToolStatusSuccess,
			"valid_count": validCount,
			"candidates":  proposed,
			"next_step":   "Only call memory_apply_update for candidates whose validation.valid is true. Invalid or uncertain candidates must be skipped or revised with stronger evidence.",
		},
		NextPrompt: "\n",
	}, nil
}

// MemoryApplyUpdate 追加一条已验证的候选，并写入审计记录。
type MemoryApplyUpdate struct {
	manager evolution.Manager
}

func NewMemoryApplyUpdate(workspace string) *MemoryApplyUpdate {
	return &MemoryApplyUpdate{manager: evolution.NewManager(workspace)}
}

func (t *MemoryApplyUpdate) Name() string { return ToolNameMemoryApplyUpdate }

func (t *MemoryApplyUpdate) Schema() llm.ToolSchema {
	return llm.ToolSchema{Type: "function", Function: llm.FunctionSchema{
		Name:        t.Name(),
		Description: "Apply one validated low-risk memory update. P0 allows append to memory/global.md, project-specific memory, or memory/reflection/sop_candidates.md and records memory/audit.jsonl.",
		Parameters: objectSchema(map[string]any{
			"candidate": map[string]any{
				"type":        "object",
				"description": "A candidate previously validated by memory_propose_update.",
				"properties": map[string]any{
					"type":             stringProp("Memory type, for example project_lesson, user_preference, or sop_candidate."),
					"target":           stringProp("Allowed target: memory/global.md, the project memory path returned by start_long_term_update, or memory/reflection/sop_candidates.md."),
					"scene":            stringProp("Short reusable scene."),
					"trigger_keywords": stringArrayProp("Keywords that should retrieve this memory later."),
					"lesson":           stringProp("The durable lesson to remember. Prefer this over free-form content."),
					"recommended_steps": map[string]any{
						"type":        "array",
						"description": "Concrete repeatable steps for this lesson.",
						"items":       map[string]any{"type": "string"},
					},
					"content": stringProp("Optional additional notes for compatibility."),
					"evidence_ids": map[string]any{
						"type":        "array",
						"description": "Verified EvidenceLedger IDs returned by start_long_term_update.",
						"items":       map[string]any{"type": "string"},
					},
					"risk":                       stringProp("Risk level: low, medium, or high."),
					"action":                     stringProp("Only append is allowed in P0."),
					"promote_to_sop":             boolProp("True when this repeated stable workflow should be recorded as a reviewed SOP candidate.", false),
					"sop_title":                  stringProp("Optional SOP candidate title when promote_to_sop is true."),
					"sop_path":                   stringProp("Optional proposed SOP path under sops/, for example sops/lark_browser_automation.md."),
					"requires_user_confirmation": boolProp("True if this should not be applied automatically.", false),
				},
			},
		}, "candidate"),
	}}
}

func (t *MemoryApplyUpdate) Run(ctx context.Context, call agent.ToolCallContext) (agent.Outcome, error) {
	_ = ctx
	candidate, ok := parseMemoryCandidate(call.Args["candidate"])
	if !ok {
		return agent.Outcome{
			Data: agent.NewToolError(
				"bad_candidate",
				"memory_apply_update requires a candidate object",
				"请传入 memory_propose_update 返回且 validation.valid=true 的单个 candidate。",
			),
			NextPrompt: "\n",
		}, nil
	}
	result, err := t.manager.ApplyCandidate(candidate, call.Evidence, call.SessionID)
	if err != nil {
		return agent.Outcome{
			Data: agent.NewToolError(
				"unsafe_memory_update",
				err.Error(),
				"不要写入未验证、敏感、一次性或超出 P0 allowlist 的记忆；必要时改为 skip。",
			),
			NextPrompt: "\n",
		}, nil
	}
	return agent.Outcome{
		Data: map[string]any{
			"status":              agent.ToolStatusSuccess,
			"applied":             true,
			"audit_record":        result.AuditRecord,
			"memory_root":         result.MemoryRoot,
			"target_path":         result.TargetPath,
			"sop_candidate_path":  result.SOPCandidatePath,
			"read_back_confirmed": result.ReadBackConfirmed,
			"read_back_bytes":     result.ReadBackBytes,
		},
		NextPrompt: "\n",
	}, nil
}

func parseMemoryCandidates(value any) []evolution.Candidate {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	candidates := make([]evolution.Candidate, 0, len(values))
	for _, item := range values {
		candidate, ok := parseMemoryCandidate(item)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func parseMemoryCandidate(value any) (evolution.Candidate, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return evolution.Candidate{}, false
	}
	return evolution.Candidate{
		Type:                     strings.TrimSpace(asString(object["type"])),
		Target:                   strings.TrimSpace(asString(object["target"])),
		Content:                  strings.TrimSpace(asString(object["content"])),
		Scene:                    strings.TrimSpace(asString(object["scene"])),
		TriggerKeywords:          parseStringSlice(object["trigger_keywords"]),
		Lesson:                   strings.TrimSpace(asString(object["lesson"])),
		RecommendedSteps:         parseStringSlice(object["recommended_steps"]),
		PromoteToSOP:             asBool(object["promote_to_sop"], false),
		SOPTitle:                 strings.TrimSpace(asString(object["sop_title"])),
		SOPPath:                  strings.TrimSpace(asString(object["sop_path"])),
		EvidenceIDs:              parseStringSlice(object["evidence_ids"]),
		Risk:                     strings.TrimSpace(asString(object["risk"])),
		Action:                   strings.TrimSpace(asString(object["action"])),
		RequiresUserConfirmation: asBool(object["requires_user_confirmation"], false),
	}, true
}

func parseStringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item := strings.TrimSpace(asString(value)); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func stringArrayProp(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}

func formatMemoryToolError(code string, err error, hint string) agent.ToolErrorData {
	return agent.NewToolError(code, fmt.Sprint(err), hint)
}
