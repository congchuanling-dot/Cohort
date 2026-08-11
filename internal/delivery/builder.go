package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
)

type BuilderTaskPackage struct {
	DeliveryID        string      `json:"delivery_id"`
	Node              TaskNode    `json:"node"`
	CandidateID       string      `json:"candidate_id"`
	BaseCommit        string      `json:"base_commit"`
	DependencyCommits []string    `json:"dependency_commits,omitempty"`
	Criteria          []Criterion `json:"criteria"`
	Invariants        []Invariant `json:"invariants,omitempty"`
	AllowedScope      []string    `json:"allowed_scope"`
	ForbiddenScope    []string    `json:"forbidden_scope,omitempty"`
	RequiredGates     []GateSpec  `json:"required_gates"`
}

func BuildBuilderTaskPackage(item Delivery, contract AcceptanceContract, node TaskNode, candidate Candidate) BuilderTaskPackage {
	criteriaByID := map[string]Criterion{}
	for _, criterion := range contract.Criteria {
		criteriaByID[criterion.ID] = criterion
	}
	criteria := make([]Criterion, 0, len(node.Criteria))
	gateIDs := map[string]bool{}
	for _, criterionID := range node.Criteria {
		if criterion, exists := criteriaByID[criterionID]; exists {
			criteria = append(criteria, criterion)
			for _, gateID := range criterion.GateIDs {
				gateIDs[gateID] = true
			}
		}
	}
	var gates []GateSpec
	for _, gate := range contract.RequiredGates {
		if gateIDs[gate.ID] {
			gates = append(gates, gate)
		}
	}
	return BuilderTaskPackage{
		DeliveryID:        item.ID,
		Node:              node,
		CandidateID:       candidate.ID,
		BaseCommit:        candidate.BaseCommit,
		DependencyCommits: append([]string(nil), candidate.DependencyCommits...),
		Criteria:          criteria,
		Invariants:        append([]Invariant(nil), contract.Invariants...),
		AllowedScope:      append([]string(nil), contract.AllowedScope...),
		ForbiddenScope:    append([]string(nil), contract.ForbiddenScope...),
		RequiredGates:     gates,
	}
}

func BuilderSystemPrompt(language string) string {
	if strings.EqualFold(strings.TrimSpace(language), "en") {
		return builderSystemPromptEN
	}
	return builderSystemPromptZH
}

func BuilderTaskPrompt(task BuilderTaskPackage) (string, error) {
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Execute this immutable delivery task package:\n\n%s", data), nil
}

const builderSystemPromptZH = `[DELIVERY BUILDER]
你是运行在隔离 Git worktree 中的实现 Agent。只完成 Task Package 指定的单个节点。

硬约束：
1. 只能修改 node.declared_writes 覆盖的仓库相对路径；allowed_scope 不是扩写授权。
2. 禁止访问或修改主工作区、创建 branch/worktree、执行 git merge/rebase/commit/reset/clean。
3. 依赖节点的 commit 已由 Runtime 合并到当前 worktree，不得重新实现依赖工作。
4. 必须满足全部 criteria，并保持 invariants。
5. 运行 task package 中与本节点相关的 required_gates；不得伪造测试结果。
6. 禁止修改 .git、.cohort、凭据、发布配置和 forbidden_scope。
7. 不要只写计划。直接检查代码、实现、格式化并验证。
8. 遇到需求歧义或必须越界修改时停止并明确报告，不得自行扩大 scope。
9. 最终回答只总结实际改动、验证命令和仍存在的风险。Runtime 会独立检查 diff 并提交。`

const builderSystemPromptEN = `[DELIVERY BUILDER]
You are an implementation agent running inside an isolated Git worktree. Complete only the supplied task node. Modify only paths covered by node.declared_writes. Do not access the main worktree or run git branch/worktree/merge/rebase/commit/reset/clean operations. Dependency commits are already present. Satisfy every assigned criterion, preserve invariants, run applicable required gates, and never fabricate evidence. Stop when the task requires scope expansion. The runtime independently inspects and commits the diff.`
