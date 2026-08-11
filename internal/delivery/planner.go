package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type PlanRequest struct {
	Delivery    Delivery
	ProjectRoot string
}

type PlanGenerator func(context.Context, PlanRequest) (PlanDraft, error)

type PlanService struct {
	Store Store
}

func (s PlanService) Plan(ctx context.Context, requirement string, generate PlanGenerator) (Delivery, AcceptanceContract, TaskGraph, error) {
	if generate == nil {
		return Delivery{}, AcceptanceContract{}, TaskGraph{}, errors.New("delivery plan generator is required")
	}
	baseCommit, dirty, err := RepositoryState(ctx, s.Store.ProjectRoot)
	if err != nil {
		return Delivery{}, AcceptanceContract{}, TaskGraph{}, err
	}
	delivery, err := s.Store.CreateDraft(requirement, baseCommit, dirty, DefaultBudget())
	if err != nil {
		return Delivery{}, AcceptanceContract{}, TaskGraph{}, err
	}
	draft, err := generate(ctx, PlanRequest{Delivery: delivery, ProjectRoot: s.Store.ProjectRoot})
	if err != nil {
		failed, failErr := s.Store.Fail(delivery.ID, fmt.Errorf("plan generator: %w", err))
		if failErr == nil {
			delivery = failed
		}
		return delivery, AcceptanceContract{}, TaskGraph{}, err
	}
	draft.Contract.RequirementHash = delivery.RequirementHash
	draft.Contract.BaseCommit = delivery.BaseCommit
	draft.Graph.DeliveryID = delivery.ID
	draft.Graph.BaseCommit = delivery.BaseCommit
	planned, err := s.Store.SavePlan(delivery.ID, draft.Contract, draft.Graph)
	if err != nil {
		failed, failErr := s.Store.Fail(delivery.ID, fmt.Errorf("validate generated plan: %w", err))
		if failErr == nil {
			delivery = failed
		}
		return delivery, draft.Contract, draft.Graph, err
	}
	delivery = planned
	contract, graph, err := s.Store.LoadPlan(delivery.ID)
	return delivery, contract, graph, err
}

func RepositoryRoot(ctx context.Context, cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	command := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git project root: %w: %s", err, strings.TrimSpace(string(output)))
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", errors.New("git returned an empty project root")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func RepositoryState(ctx context.Context, projectRoot string) (baseCommit string, dirty bool, err error) {
	baseCommit, err = runGitText(ctx, projectRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := runGitText(ctx, projectRoot, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(baseCommit), strings.TrimSpace(status) != "", nil
}

func ParsePlanDraft(response string) (PlanDraft, error) {
	response = strings.TrimSpace(response)
	if response == "" {
		return PlanDraft{}, errors.New("planner returned an empty response")
	}
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start < 0 || end < start {
		return PlanDraft{}, errors.New("planner response does not contain a JSON object")
	}
	payload := response[start : end+1]
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	var draft PlanDraft
	if err := decoder.Decode(&draft); err != nil {
		return PlanDraft{}, fmt.Errorf("parse planner JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return PlanDraft{}, errors.New("planner returned multiple JSON values")
		}
		return PlanDraft{}, fmt.Errorf("parse trailing planner content: %w", err)
	}
	return draft, nil
}

func PlanningSystemPrompt(language string) string {
	if strings.EqualFold(strings.TrimSpace(language), "en") {
		return deliveryPlanningSystemEN
	}
	return deliveryPlanningSystemZH
}

func PlanningTaskPrompt(request PlanRequest) string {
	return fmt.Sprintf(`Create a repository-grounded delivery plan for the requirement below.

Requirement:
%s

Delivery ID: %s
Base commit: %s

Inspect the repository with the available read-only tools before deciding scope, gates, or dependencies.
Return exactly one JSON object matching the required contract. Do not wrap it in Markdown and do not add commentary.`,
		request.Delivery.Requirement,
		request.Delivery.ID,
		request.Delivery.BaseCommit,
	)
}

func runGitText(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

const deliveryPlanningSystemZH = `[DELIVERY PLANNER]
你是证据驱动软件交付系统的只读 Planner。必须先调查仓库，再输出可执行的验收契约和任务 DAG。

硬约束：
1. 只能读取和搜索，不得修改文件、运行带副作用命令或安装依赖。
2. 每个 mandatory criterion 必须具体、可判定，并声明 verification 与 evidence_policy。
3. evidence_policy=execution 的 mandatory criterion 必须引用至少一个 command gate。
4. builder/test_builder/revision_builder 节点必须声明仓库相对 declared_writes。
5. write set 重叠的节点必须通过 dependencies 串行化。
6. 只有 high/critical 节点可设置 candidate_count=2，其余必须为 1。
7. 不要虚构不存在的测试命令、目录或组件；证据不足时写入 blocking question。
8. 禁止用“代码质量良好”“功能正常”这类不可判定描述。
9. 第一版只生成 builder 和 test_builder 工作节点；Verifier 由 Runtime 按 risk_profile 自动装配。
10. 最终只能输出 JSON，不得输出 Markdown 或解释文字。

JSON 结构：
{
  "contract": {
    "schema_version": 1,
    "requirement_hash": "",
    "base_commit": "",
    "summary": "范围明确的实现摘要",
    "criteria": [
      {
        "id": "AC-1",
        "statement": "可判定行为",
        "mandatory": true,
        "verification": "command|file_assertion|api_contract|behavioral_eval|rubric|human",
        "target_paths": ["relative/path/**"],
        "evidence_policy": "execution|static|semantic|human",
        "gate_ids": ["gate-unit"]
      }
    ],
    "invariants": [{"id":"INV-1","statement":"必须保持的不变量","paths":["relative/path/**"]}],
    "allowed_scope": ["relative/path/**"],
    "forbidden_scope": [".git/**", ".cohort/**"],
    "risk_profile": {
      "level": "low|medium|high|critical",
      "reasons": ["原因"],
      "security_sensitive": false,
      "compatibility_sensitive": false,
      "performance_sensitive": false
    },
    "required_gates": [
      {
        "id": "gate-unit",
        "name": "focused unit tests",
        "kind": "command",
        "command": ["go", "test", "./internal/example"],
        "paths": ["internal/example/**"],
        "mandatory": true,
        "timeout_seconds": 300
      }
    ],
    "questions": [{"id":"Q-1","question":"无法从仓库推断的问题","blocking":true}]
  },
  "graph": {
    "schema_version": 1,
    "delivery_id": "",
    "base_commit": "",
    "nodes": [
      {
        "id": "implementation",
        "title": "节点标题",
        "objective": "只包含一个可交付目标",
        "role": "builder|test_builder",
        "status": "pending",
        "dependencies": [],
        "read_set": ["relative/path/**"],
        "declared_writes": ["relative/path/**"],
        "criteria": ["AC-1"],
        "risk": "low|medium|high|critical",
        "candidate_count": 1,
        "budget": {"max_turns":80,"max_tokens":120000,"max_duration_seconds":1800}
      }
    ],
    "created_at": "0001-01-01T00:00:00Z"
  }
}`

const deliveryPlanningSystemEN = `[DELIVERY PLANNER]
You are the read-only planner for an evidence-driven software delivery system. Inspect the repository before producing an executable acceptance contract and task DAG.

Return exactly one JSON object with top-level "contract" and "graph" fields. Mandatory criteria must be objectively verifiable. Execution criteria must reference command gates. Builder nodes must declare repository-relative write sets. Overlapping write sets must be dependency-ordered. Only high or critical risk nodes may request two candidates. Use blocking questions when product semantics cannot be inferred. Do not modify files or emit Markdown. Follow the JSON field names and enum values described by the user request.`
