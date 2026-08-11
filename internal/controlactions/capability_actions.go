package controlactions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/capability"
	"cohort/internal/controlplane"
	"cohort/internal/evolution"
	"cohort/internal/llm"
	"cohort/internal/lsp"
	"cohort/internal/mcp"
	"cohort/internal/session"
	"cohort/internal/skill"
)

func capabilityActions() []controlplane.ActionSpec {
	capabilityID := controlplane.InputField{
		Name: "capability_id", Label: "Capability", Type: controlplane.FieldEntity, Required: true,
		Entity: &controlplane.EntitySelector{Kind: controlplane.EntityCapability, RecentFirst: true},
	}
	proposalID := controlplane.InputField{Name: "proposal_id", Label: "Proposal ID", Type: controlplane.FieldString, Required: true}
	return []controlplane.ActionSpec{
		{
			ID: "capability.propose", Category: "capability", Label: "记录能力缺口",
			Description: "记录 Gap，并生成可审查的 Capability Proposal。",
			Keywords:    []string{"capability", "gap", "proposal", "能力缺口"}, Risk: controlplane.RiskExecute,
			Inputs: []controlplane.InputField{{Name: "task", Label: "缺失能力或任务", Type: controlplane.FieldText, Required: true}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := capability.NewStore(request.ProjectRoot)
				gap, err := store.AddGap(capability.NewGapFromTask(textInput(request, "task")))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				proposal, err := store.AddProposal(capability.NewProposalFromGap(gap))
				return controlplane.ActionResult{Summary: "capability proposal created", Data: map[string]any{"gap": gap, "proposal": proposal}}, err
			},
		},
		{
			ID: "capability.build", Category: "capability", Label: "构建 Skill 骨架",
			Description: "从 Proposal 生成项目级 Skill 候选和结构化 smoke test。",
			Keywords:    []string{"build", "skill", "构建能力"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "BUILD", Inputs: []controlplane.InputField{proposalID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := capability.NewStore(request.ProjectRoot).Build(textInput(request, "proposal_id"))
				return controlplane.ActionResult{Summary: "capability scaffold built", Data: item}, err
			},
		},
		{
			ID: "capability.verify", Category: "capability", Label: "验证 Capability",
			Description: "运行 Capability 自带的行为级 smoke test 并持久化验证证据。",
			Keywords:    []string{"verify", "smoke", "验证能力"}, Risk: controlplane.RiskExecute, Async: true,
			Inputs: []controlplane.InputField{capabilityID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, output, err := capability.NewStore(request.ProjectRoot).Verify(textInput(request, "capability_id"))
				return controlplane.ActionResult{Summary: "capability verification finished", Data: map[string]any{"capability": item, "output": output}}, err
			},
		},
		{
			ID: "capability.promote", Category: "capability", Label: "晋级 Capability",
			Description: "重新验证并将 Capability 标记为 available。只允许验证通过的能力晋级。",
			Keywords:    []string{"promote", "晋级", "启用能力"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "PROMOTE", Inputs: []controlplane.InputField{capabilityID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := capability.NewStore(request.ProjectRoot).Promote(textInput(request, "capability_id"))
				return controlplane.ActionResult{Summary: "capability promoted", Data: item}, err
			},
		},
		{
			ID: "capability.disable", Category: "capability", Label: "禁用 Capability",
			Description: "从可用能力索引中禁用 Capability，保留注册记录和证据。",
			Keywords:    []string{"disable", "禁用能力"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "DISABLE", Inputs: []controlplane.InputField{capabilityID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := capability.NewStore(request.ProjectRoot).Disable(textInput(request, "capability_id"))
				return controlplane.ActionResult{Summary: "capability disabled", Data: item}, err
			},
		},
		{
			ID: "capability.doctor", Category: "capability", Label: "诊断 Capability",
			Description: "检查产物、依赖和验证状态，不修改注册表。",
			Keywords:    []string{"capability", "doctor", "诊断"}, Risk: controlplane.RiskRead,
			Inputs: []controlplane.InputField{capabilityID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := capability.NewStore(request.ProjectRoot).Doctor(textInput(request, "capability_id"))
				return controlplane.ActionResult{Summary: "capability doctor finished", Data: result}, err
			},
		},
		{
			ID: "capability.adapter.build", Category: "capability", Label: "构建 Tool/MCP Adapter",
			Description: "从 Proposal 生成可审查的 Tool 或 MCP Adapter 骨架。",
			Keywords:    []string{"capability", "adapter", "tool", "mcp"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "BUILD_ADAPTER",
			Inputs: []controlplane.InputField{
				proposalID,
				{Name: "type", Label: "Adapter 类型", Type: controlplane.FieldSelect, Required: true, Default: "tool", Options: []string{"tool", "mcp"}},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, artifacts, err := capability.NewStore(request.ProjectRoot).BuildAdapter(textInput(request, "proposal_id"), textInput(request, "type"))
				return controlplane.ActionResult{Summary: "capability adapter built", Data: map[string]any{"capability": item, "artifacts": artifacts}}, err
			},
		},
		{
			ID: "capability.adapter.enable", Category: "capability", Label: "启用 Adapter",
			Description: "启用已晋级且验证通过的 Tool/MCP Adapter。",
			Keywords:    []string{"capability", "adapter", "enable"}, Risk: controlplane.RiskDanger,
			ConfirmationText: "ENABLE_ADAPTER", Inputs: []controlplane.InputField{capabilityID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := capability.NewStore(request.ProjectRoot).EnableAdapter(textInput(request, "capability_id"))
				return controlplane.ActionResult{Summary: "capability adapter enabled", Data: result}, err
			},
		},
		{
			ID: "capability.dependencies.plan", Category: "capability", Label: "生成依赖安装计划",
			Description: "只生成固定 argv 安装计划，不执行安装。",
			Keywords:    []string{"capability", "dependency", "plan"}, Risk: controlplane.RiskRead,
			Inputs: []controlplane.InputField{proposalID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := capability.NewStore(request.ProjectRoot).PlanDependencies(textInput(request, "proposal_id"))
				return controlplane.ActionResult{Summary: "dependency plan generated", Data: result}, err
			},
		},
		{
			ID: "capability.dependencies.approve", Category: "capability", Label: "批准依赖计划",
			Description: "显式批准绑定的依赖安装计划。",
			Keywords:    []string{"capability", "dependency", "approve"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "APPROVE_DEPENDENCIES",
			Inputs:           []controlplane.InputField{{Name: "plan_id", Label: "Plan ID", Type: controlplane.FieldString, Required: true}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := capability.NewStore(request.ProjectRoot).ApproveDependencyPlan(textInput(request, "plan_id"))
				return controlplane.ActionResult{Summary: "dependency plan approved", Data: result}, err
			},
		},
		{
			ID: "capability.dependencies.install", Category: "capability", Label: "安装已批准依赖",
			Description: "仅执行计划中已审核的固定 argv，并记录每项安装审计。",
			Keywords:    []string{"capability", "dependency", "install"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "INSTALL_DEPENDENCIES",
			Inputs: []controlplane.InputField{
				{Name: "plan_id", Label: "Plan ID", Type: controlplane.FieldString, Required: true},
				{Name: "dry_run", Label: "仅演练", Type: controlplane.FieldBoolean, Default: false},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				plan, records, err := capability.NewStore(request.ProjectRoot).InstallDependencyPlan(
					textInput(request, "plan_id"), capability.DependencyInstallOptions{DryRun: boolInput(request, "dry_run")},
				)
				return controlplane.ActionResult{Summary: "dependency installation finished", Data: map[string]any{"plan": plan, "records": records}}, err
			},
		},
	}
}

func mcpActions() []controlplane.ActionSpec {
	scope := controlplane.InputField{Name: "scope", Label: "配置范围", Type: controlplane.FieldSelect, Required: true, Default: "project", Options: []string{"project", "local", "user"}}
	name := controlplane.InputField{Name: "name", Label: "Server 名称", Type: controlplane.FieldString, Required: true}
	existingName := controlplane.InputField{
		Name: "name", Label: "MCP Server", Type: controlplane.FieldEntity, Required: true,
		Entity: &controlplane.EntitySelector{Kind: controlplane.EntityMCPServer},
	}
	return []controlplane.ActionSpec{
		{
			ID: "mcp.add", Category: "mcp", Label: "添加 MCP Server",
			Description: "写入经过校验的 stdio 或 HTTP MCP 配置。环境变量值不通过控制台读取。",
			Keywords:    []string{"mcp", "server", "add", "添加"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "ADD_MCP",
			Inputs: []controlplane.InputField{
				scope, name,
				{Name: "type", Label: "Transport", Type: controlplane.FieldSelect, Required: true, Default: "stdio", Options: []string{"stdio", "http"}},
				{Name: "command", Label: "Command", Type: controlplane.FieldString, Placeholder: "npx"},
				{Name: "args", Label: "Arguments（每行一个）", Type: controlplane.FieldText, Sensitive: true, Description: "Operation 审计中自动脱敏"},
				{Name: "url", Label: "HTTP URL", Type: controlplane.FieldString, Sensitive: true, Description: "凭据和 query 不会出现在资源摘要中"},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				parsedScope, err := mcp.ParseScope(textInput(request, "scope"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				server := mcp.ServerConfig{
					Name: textInput(request, "name"), Type: textInput(request, "type"),
					Command: textInput(request, "command"), URL: textInput(request, "url"),
					Args: nonEmptyLines(textInput(request, "args")),
				}
				if err := mcp.NewStore(request.ProjectRoot).Add(parsedScope, server); err != nil {
					return controlplane.ActionResult{}, err
				}
				return controlplane.ActionResult{Summary: "MCP server added", Data: map[string]any{"name": server.Name, "scope": parsedScope}}, nil
			},
		},
		{
			ID: "mcp.probe", Category: "mcp", Label: "探测 MCP Server",
			Description: "执行 initialize 和 tools/list，返回可用性与工具数量。",
			Keywords:    []string{"mcp", "probe", "tools", "探测"}, Risk: controlplane.RiskRead, Async: true,
			Inputs: []controlplane.InputField{existingName},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				servers, err := mcp.NewStore(request.ProjectRoot).LoadEffective()
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				target := textInput(request, "name")
				manager := mcp.NewManager()
				defer manager.Close()
				for _, server := range servers {
					if server.Name == target {
						manager.Load(ctx, []mcp.ServerConfig{server})
						statuses := manager.Statuses()
						if len(statuses) == 0 || !statuses[0].Available {
							return controlplane.ActionResult{Data: statuses}, fmt.Errorf("MCP server %q is unavailable", target)
						}
						registered := manager.Tools()
						tools := make([]mcp.ToolDefinition, 0, len(registered))
						for _, tool := range registered {
							tools = append(tools, tool.Tool)
						}
						return controlplane.ActionResult{Summary: "MCP probe passed", Data: map[string]any{"status": statuses[0], "tools": tools}}, nil
					}
				}
				return controlplane.ActionResult{}, fmt.Errorf("MCP server %q not found", target)
			},
		},
		{
			ID: "mcp.remove", Category: "mcp", Label: "移除 MCP Server",
			Description: "仅从指定 scope 删除 MCP 配置，不执行任何 Server 命令。",
			Keywords:    []string{"mcp", "remove", "删除"}, Risk: controlplane.RiskDanger,
			ConfirmationText: "REMOVE_MCP", Inputs: []controlplane.InputField{scope, existingName},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				parsedScope, err := mcp.ParseScope(textInput(request, "scope"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				removed, err := mcp.NewStore(request.ProjectRoot).Remove(parsedScope, textInput(request, "name"))
				return controlplane.ActionResult{Summary: "MCP server removed", Data: map[string]any{"removed": removed}}, err
			},
		},
		{
			ID: "mcp.import", Category: "mcp", Label: "导入 MCP 配置",
			Description: "从项目根目录内导入 Claude-compatible MCP JSON。",
			Keywords:    []string{"mcp", "import", "导入"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "IMPORT_MCP",
			Inputs: []controlplane.InputField{
				scope,
				{Name: "path", Label: "项目内 JSON 路径", Type: controlplane.FieldPath, Required: true},
				{Name: "merge", Label: "合并现有配置", Type: controlplane.FieldBoolean, Default: true},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				parsedScope, err := mcp.ParseScope(textInput(request, "scope"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				path, err := projectPath(request.ProjectRoot, textInput(request, "path"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				count, err := mcp.NewStore(request.ProjectRoot).Import(parsedScope, path, boolInput(request, "merge"))
				return controlplane.ActionResult{Summary: "MCP config imported", Data: map[string]any{"servers": count}}, err
			},
		},
		{
			ID: "mcp.export", Category: "mcp", Label: "导出 MCP 配置",
			Description: "把指定 scope 导出到项目根目录内，权限固定为 0600。",
			Keywords:    []string{"mcp", "export", "导出"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "EXPORT_MCP",
			Inputs:           []controlplane.InputField{scope, {Name: "path", Label: "项目内输出路径", Type: controlplane.FieldPath, Required: true}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				parsedScope, err := mcp.ParseScope(textInput(request, "scope"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				path, err := projectPath(request.ProjectRoot, textInput(request, "path"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				err = mcp.NewStore(request.ProjectRoot).Export(parsedScope, path)
				return controlplane.ActionResult{Summary: "MCP config exported", Data: map[string]any{"path": path}}, err
			},
		},
		{
			ID: "mcp.policy.set", Category: "mcp", Label: "设置 MCP Tool Policy",
			Description: "设置单个 server/tool 的 risk、decision 和参数授权范围。",
			Keywords:    []string{"mcp", "policy", "permission", "权限"}, Risk: controlplane.RiskDanger,
			ConfirmationText: "SET_MCP_POLICY",
			Inputs: []controlplane.InputField{
				{
					Name: "server", Label: "MCP Server", Type: controlplane.FieldEntity, Required: true,
					Entity: &controlplane.EntitySelector{Kind: controlplane.EntityMCPServer},
				},
				{Name: "tool", Label: "Tool", Type: controlplane.FieldString, Required: true},
				{Name: "decision", Label: "Decision", Type: controlplane.FieldSelect, Required: true, Default: "ask", Options: []string{"allow", "ask", "deny"}},
				{Name: "risk", Label: "Risk", Type: controlplane.FieldSelect, Required: true, Default: "R2", Options: []string{"R1", "R2", "R3"}},
				{Name: "args_policy", Label: "参数授权范围", Type: controlplane.FieldSelect, Required: true, Default: "exact_args", Options: []string{"exact_args", "tool_scope"}},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := mcp.NewStore(request.ProjectRoot).SetPermissionRule(
					textInput(request, "server"), textInput(request, "tool"),
					mcp.ToolPermissionRule{
						Decision: mcp.PermissionDecision(textInput(request, "decision")),
						Risk:     mcp.Risk(textInput(request, "risk")), ArgsPolicy: mcp.ArgsPolicy(textInput(request, "args_policy")),
					},
				)
				return controlplane.ActionResult{Summary: "MCP policy updated", Data: result}, err
			},
		},
		{
			ID: "mcp.policy.remove", Category: "mcp", Label: "删除 MCP Tool Policy",
			Description: "删除单个显式 Tool Policy，恢复默认 R2 + ask。",
			Keywords:    []string{"mcp", "policy", "remove"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "REMOVE_MCP_POLICY",
			Inputs: []controlplane.InputField{
				{
					Name: "server", Label: "MCP Server", Type: controlplane.FieldEntity, Required: true,
					Entity: &controlplane.EntitySelector{Kind: controlplane.EntityMCPServer},
				},
				{Name: "tool", Label: "Tool", Type: controlplane.FieldString, Required: true},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				removed, config, err := mcp.NewStore(request.ProjectRoot).DeletePermissionRule(textInput(request, "server"), textInput(request, "tool"))
				return controlplane.ActionResult{Summary: "MCP policy removed", Data: map[string]any{"removed": removed, "config": config}}, err
			},
		},
	}
}

func skillActions() []controlplane.ActionSpec {
	skillID := controlplane.InputField{
		Name: "skill_id", Label: "Skill", Type: controlplane.FieldEntity, Required: true,
		Entity: &controlplane.EntitySelector{Kind: controlplane.EntitySkill, RecentFirst: true},
	}
	return []controlplane.ActionSpec{
		{
			ID: "skill.install", Category: "skill", Label: "安装 Skill",
			Description: "从本地路径或 Git URL 预检并安装 Skill；目标位置受 project/user scope 约束。",
			Keywords:    []string{"skill", "install", "安装"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "INSTALL_SKILL",
			Inputs: []controlplane.InputField{
				{Name: "source", Label: "本地路径或 Git URL", Type: controlplane.FieldString, Required: true, Sensitive: true, Description: "可能包含 Git 凭据，Operation 审计中自动脱敏"},
				{Name: "scope", Label: "安装范围", Type: controlplane.FieldSelect, Required: true, Default: "project", Options: []string{"project", "user"}},
				{Name: "pin", Label: "Git Ref", Type: controlplane.FieldString},
				{Name: "force", Label: "覆盖同名 Skill", Type: controlplane.FieldBoolean, Default: false},
				{Name: "dry_run", Label: "仅预览", Type: controlplane.FieldBoolean, Default: false},
			},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				scope, err := skill.ParseScope(textInput(request, "scope"))
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				result, err := skill.Install(ctx, skill.InstallOptions{
					Source: textInput(request, "source"), Scope: scope, Pin: textInput(request, "pin"),
					Force: boolInput(request, "force"), DryRun: boolInput(request, "dry_run"),
					ProjectRoot: request.ProjectRoot,
				})
				return controlplane.ActionResult{Summary: "skill install finished", Data: result}, err
			},
		},
		{
			ID: "skill.doctor", Category: "skill", Label: "诊断 Skill",
			Description: "检查 SKILL.md、依赖、MCP、环境变量声明和权限。",
			Keywords:    []string{"skill", "doctor", "诊断"}, Risk: controlplane.RiskRead,
			Inputs: []controlplane.InputField{skillID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := skill.NewStore(request.ProjectRoot, "")
				if err := store.Reload(); err != nil {
					return controlplane.ActionResult{}, err
				}
				result, err := store.Doctor(textInput(request, "skill_id"))
				return controlplane.ActionResult{Summary: "skill doctor finished", Data: result}, err
			},
		},
		{
			ID: "skill.uninstall", Category: "skill", Label: "卸载 Skill",
			Description: "删除已安装的项目级或用户级 Skill。Builtin Skill 不允许卸载。",
			Keywords:    []string{"skill", "uninstall", "卸载"}, Risk: controlplane.RiskDanger,
			ConfirmationText: "UNINSTALL_SKILL", Inputs: []controlplane.InputField{skillID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := skill.NewStore(request.ProjectRoot, "")
				if err := store.Reload(); err != nil {
					return controlplane.ActionResult{}, err
				}
				result, err := store.Uninstall(textInput(request, "skill_id"))
				return controlplane.ActionResult{Summary: "skill uninstalled", Data: result}, err
			},
		},
		{
			ID: "skill.update.check", Category: "skill", Label: "检查 Skill 更新",
			Description: "解析安装来源并比较内容哈希，不修改安装目录。",
			Keywords:    []string{"skill", "update", "check"}, Risk: controlplane.RiskRead, Async: true,
			Inputs: []controlplane.InputField{skillID, {Name: "pin", Label: "Git Ref", Type: controlplane.FieldString}},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := skill.NewStore(request.ProjectRoot, "")
				if err := store.Reload(); err != nil {
					return controlplane.ActionResult{}, err
				}
				result, err := store.CheckUpdate(ctx, skill.UpdateOptions{ID: textInput(request, "skill_id"), Pin: textInput(request, "pin")})
				return controlplane.ActionResult{Summary: "skill update check finished", Data: result}, err
			},
		},
		{
			ID: "skill.update", Category: "skill", Label: "更新 Skill",
			Description: "从已记录来源更新 Skill，并保留来源和内容哈希审计。",
			Keywords:    []string{"skill", "update", "更新"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "UPDATE_SKILL",
			Inputs:           []controlplane.InputField{skillID, {Name: "pin", Label: "Git Ref", Type: controlplane.FieldString}},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := skill.NewStore(request.ProjectRoot, "")
				if err := store.Reload(); err != nil {
					return controlplane.ActionResult{}, err
				}
				result, err := store.UpdateWithOptions(ctx, skill.UpdateOptions{ID: textInput(request, "skill_id"), Pin: textInput(request, "pin")})
				return controlplane.ActionResult{Summary: "skill updated", Data: result}, err
			},
		},
	}
}

func lspActions() []controlplane.ActionSpec {
	language := controlplane.InputField{Name: "language", Label: "语言", Type: controlplane.FieldSelect, Required: true, Default: "all", Options: []string{"all", "go", "typescript", "python"}}
	return []controlplane.ActionSpec{
		{
			ID: "lsp.doctor", Category: "lsp", Label: "LSP Doctor",
			Description: "检查 gopls、TypeScript、Python 诊断后端和版本。",
			Keywords:    []string{"lsp", "doctor", "诊断"}, Risk: controlplane.RiskRead, Async: true,
			Inputs: []controlplane.InputField{language},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result := (lsp.Diagnostics{Root: request.ProjectRoot}).Doctor(ctx, textInput(request, "language"))
				return controlplane.ActionResult{Summary: "LSP doctor finished", Data: result}, nil
			},
		},
		{
			ID: "lsp.diagnostics", Category: "lsp", Label: "运行 LSP Diagnostics",
			Description: "对指定语言和相对路径执行只读诊断。",
			Keywords:    []string{"lsp", "diagnostics", "check"}, Risk: controlplane.RiskRead, Async: true,
			Inputs: []controlplane.InputField{
				{Name: "language", Label: "语言", Type: controlplane.FieldSelect, Required: true, Default: "go", Options: []string{"go", "typescript", "python"}},
				{Name: "targets", Label: "目标路径（每行一个）", Type: controlplane.FieldText},
			},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := (lsp.Diagnostics{Root: request.ProjectRoot}).Check(ctx, textInput(request, "language"), nonEmptyLines(textInput(request, "targets")))
				return controlplane.ActionResult{Summary: "LSP diagnostics finished", Data: result}, err
			},
		},
		{
			ID: "lsp.install", Category: "lsp", Label: "安装缺失 LSP",
			Description: "通过固定 npm argv 安装缺失的 TypeScript/Python 后端；Go 后端不会自动安装。",
			Keywords:    []string{"lsp", "install", "安装"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "INSTALL_LSP", Inputs: []controlplane.InputField{language},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result := (lsp.Diagnostics{Root: request.ProjectRoot}).InstallMissing(ctx, textInput(request, "language"))
				return controlplane.ActionResult{Summary: "LSP installation finished", Data: result}, nil
			},
		},
		{
			ID: "lsp.query", Category: "lsp", Label: "查询定义、引用或符号",
			Description: "执行 definition/references/hover/symbols 查询；路径只能位于当前项目。",
			Keywords:    []string{"lsp", "definition", "references", "hover", "symbols"}, Risk: controlplane.RiskRead, Async: true,
			Inputs: []controlplane.InputField{
				{Name: "language", Label: "语言", Type: controlplane.FieldSelect, Required: true, Default: "go", Options: []string{"go", "typescript", "python"}},
				{Name: "kind", Label: "查询类型", Type: controlplane.FieldSelect, Required: true, Default: "definition", Options: []string{"definition", "references", "hover", "symbols"}},
				{Name: "position", Label: "位置 file:line:column", Type: controlplane.FieldString},
				{Name: "target", Label: "Symbols 目标路径", Type: controlplane.FieldPath},
				{Name: "include_declaration", Label: "引用包含声明", Type: controlplane.FieldBoolean, Default: false},
			},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				position := textInput(request, "position")
				target := textInput(request, "target")
				var err error
				if position != "" {
					position, err = projectPosition(request.ProjectRoot, position)
					if err != nil {
						return controlplane.ActionResult{}, err
					}
				}
				if target != "" {
					target, err = projectPath(request.ProjectRoot, target)
					if err != nil {
						return controlplane.ActionResult{}, err
					}
				}
				result, err := (lsp.Diagnostics{Root: request.ProjectRoot}).Query(ctx, lsp.QueryOptions{
					Language: textInput(request, "language"), Kind: textInput(request, "kind"),
					Position: position, Target: target,
					IncludeDeclaration: boolInput(request, "include_declaration"),
				})
				return controlplane.ActionResult{Summary: "LSP query finished", Data: result}, err
			},
		},
		{
			ID: "lsp.server.restart", Category: "lsp", Label: "重启 LSP Server",
			Description: "重启当前项目的 TypeScript 或 Python 持久语言服务器。",
			Keywords:    []string{"lsp", "server", "restart"}, Risk: controlplane.RiskExecute, Async: true,
			Inputs: []controlplane.InputField{{Name: "language", Label: "语言", Type: controlplane.FieldSelect, Required: true, Options: []string{"typescript", "python"}}},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				err := lsp.RestartServer(ctx, request.ProjectRoot, textInput(request, "language"))
				return controlplane.ActionResult{Summary: "LSP server restarted", Data: lsp.ServerStatuses(request.ProjectRoot)}, err
			},
		},
		{
			ID: "lsp.server.stop", Category: "lsp", Label: "停止 LSP Server",
			Description: "停止当前项目的持久语言服务器并清理进程。",
			Keywords:    []string{"lsp", "server", "stop"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "STOP_LSP",
			Inputs:           []controlplane.InputField{{Name: "language", Label: "语言", Type: controlplane.FieldSelect, Required: true, Default: "all", Options: []string{"all", "typescript", "python"}}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				err := lsp.StopServer(request.ProjectRoot, textInput(request, "language"))
				return controlplane.ActionResult{Summary: "LSP server stopped", Data: lsp.ServerStatuses(request.ProjectRoot)}, err
			},
		},
	}
}

func reflectionActions() []controlplane.ActionSpec {
	return []controlplane.ActionSpec{
		{
			ID: "reflection.drain", Category: "reflection", Label: "消费 Reflection Queue",
			Description: "Claim 并处理当前可用的反思任务，遵循 lease、retry 和 dead-letter 规则。",
			Keywords:    []string{"reflection", "drain", "反思队列"}, Risk: controlplane.RiskExecute, Async: true,
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				result, err := evolution.NewReflectionWorker(
					evolution.NewReflectionQueue(request.ProjectRoot), evolution.ReflectionWorkerConfig{},
				).Drain(ctx)
				return controlplane.ActionResult{Summary: "reflection queue drained", Data: result}, err
			},
		},
		{
			ID: "reflection.retry", Category: "reflection", Label: "重试 Reflection Job",
			Description: "把 failed/dead Reflection Job 重新放回可用队列。",
			Keywords:    []string{"reflection", "retry", "重试"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "RETRY",
			Inputs:           []controlplane.InputField{{Name: "job_id", Label: "Job ID", Type: controlplane.FieldString, Required: true}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := evolution.NewReflectionQueue(request.ProjectRoot).Retry(textInput(request, "job_id"))
				return controlplane.ActionResult{Summary: "reflection job retried", Data: item}, err
			},
		},
	}
}

func agentActions(configPath string) []controlplane.ActionSpec {
	task := controlplane.InputField{Name: "task", Label: "任务", Type: controlplane.FieldText, Required: true}
	return []controlplane.ActionSpec{
		{
			ID: "agent.run", Category: "session", Label: "运行 Agent 任务",
			Description: "启动真实 Cohort Runner，输出和 Tool Call 会作为 Operation 审计结果持久化。",
			Keywords:    []string{"agent", "session", "ask", "运行任务"}, Risk: controlplane.RiskExecute, Async: true,
			Inputs:  []controlplane.InputField{task},
			Handler: agentRunHandler(configPath, false),
		},
		{
			ID: "agent.continue", Category: "session", Label: "继续 Session",
			Description: "恢复指定 Session 的历史，再运行一条新任务。",
			Keywords:    []string{"agent", "session", "continue", "resume", "继续"}, Risk: controlplane.RiskExecute, Async: true,
			Inputs: []controlplane.InputField{
				{
					Name: "session_id", Label: "Session", Type: controlplane.FieldEntity, Required: true,
					Entity: &controlplane.EntitySelector{Kind: controlplane.EntitySession, RecentFirst: true},
				}, task,
			},
			Handler: agentRunHandler(configPath, true),
		},
	}
}

func settingsActions(configPath string) []controlplane.ActionSpec {
	return []controlplane.ActionSpec{
		{
			ID: "settings.model.activate", Category: "settings", Label: "切换模型 Profile",
			Description: "原子更新 llm.active_profile，并保留其余配置、注释和文件权限。",
			Keywords:    []string{"settings", "model", "profile", "切换模型"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "SWITCH_MODEL",
			Inputs: []controlplane.InputField{{
				Name: "profile_id", Label: "Model Profile", Type: controlplane.FieldEntity, Required: true,
				Entity: &controlplane.EntitySelector{Kind: controlplane.EntityModelProfile},
			}},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				profileID := textInput(request, "profile_id")
				if err := app.UpdateActiveProfile(configPath, profileID); err != nil {
					return controlplane.ActionResult{}, err
				}
				return controlplane.ActionResult{Summary: "active model profile updated", Data: map[string]any{"active_profile": profileID}}, nil
			},
		},
	}
}

func agentRunHandler(configPath string, resume bool) controlplane.ActionHandler {
	return func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		cfg, err := app.LoadConfig(configPath)
		if err != nil {
			return controlplane.ActionResult{}, err
		}
		cfg.Workspace = request.ProjectRoot
		runner, err := app.NewRunner(cfg)
		if err != nil {
			return controlplane.ActionResult{}, err
		}
		defer runner.Close()
		if resume {
			store := session.NewStore(filepath.Join(request.ProjectRoot, session.DefaultRootDir))
			id := textInput(request, "session_id")
			meta, loadErr := store.LoadMeta(id)
			if loadErr != nil {
				return controlplane.ActionResult{}, loadErr
			}
			history, loadErr := store.LoadHistory(id)
			if loadErr != nil {
				return controlplane.ActionResult{}, loadErr
			}
			runner.ResumeSession(meta.ID, history)
		}
		sink := &operationSink{ctx: ctx}
		result, err := runner.Run(ctx, textInput(request, "task"), sink)
		return controlplane.ActionResult{
			Summary: "agent run " + result.Status,
			Data:    map[string]any{"status": result.Status, "output": sink.String(), "events": sink.Events()},
		}, err
	}
}

type operationSink struct {
	ctx           context.Context
	mu            sync.Mutex
	output        strings.Builder
	events        []map[string]string
	lastPublished time.Time
	lastSize      int
}

const maxOperationOutputBytes = 200_000

func (s *operationSink) WriteText(text string) {
	s.mu.Lock()
	s.output.WriteString(text)
	s.publishLocked(false)
	s.mu.Unlock()
}

func (s *operationSink) WriteToolCall(call llm.ToolCall) {
	s.mu.Lock()
	s.events = append(s.events, map[string]string{"type": "tool_call", "name": call.Function.Name})
	s.publishLocked(true)
	s.mu.Unlock()
}

func (s *operationSink) WriteToolResult(name string, result string) {
	s.mu.Lock()
	s.events = append(s.events, map[string]string{"type": "tool_result", "name": name, "bytes": fmt.Sprintf("%d", len(result))})
	s.publishLocked(true)
	s.mu.Unlock()
}

func (s *operationSink) WriteError(error) {
	s.mu.Lock()
	s.events = append(s.events, map[string]string{"type": "error"})
	s.publishLocked(true)
	s.mu.Unlock()
}

func (s *operationSink) publishLocked(force bool) {
	now := time.Now()
	size := s.output.Len()
	if !force && size-s.lastSize < 1024 && !s.lastPublished.IsZero() && now.Sub(s.lastPublished) < 500*time.Millisecond {
		return
	}
	data := map[string]any{
		"status": "streaming", "output": s.outputSnapshotLocked(),
		"events": append([]map[string]string(nil), recentOperationEvents(s.events)...),
	}
	s.lastPublished = now
	s.lastSize = size
	controlplane.ReportProgress(s.ctx, "agent streaming", data)
}

func (s *operationSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputSnapshotLocked()
}

func (s *operationSink) Events() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), recentOperationEvents(s.events)...)
}

func (s *operationSink) outputSnapshotLocked() string {
	value := s.output.String()
	if len(value) <= maxOperationOutputBytes {
		return value
	}
	head := maxOperationOutputBytes * 3 / 4
	tail := maxOperationOutputBytes - head
	return value[:head] + "\n...[operation output truncated]...\n" + value[len(value)-tail:]
}

func recentOperationEvents(events []map[string]string) []map[string]string {
	if len(events) > 500 {
		return events[len(events)-500:]
	}
	return events
}

func boolInput(request controlplane.ActionRequest, name string) bool {
	value, _ := request.Input[name].(bool)
	return value
}

func nonEmptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func projectPath(projectRoot string, value string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = resolvePathSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must remain inside project root")
	}
	return path, nil
}

func resolvePathSymlinks(path string) (string, error) {
	cursor := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cursor)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
}

func projectPosition(projectRoot string, value string) (string, error) {
	last := strings.LastIndex(value, ":")
	if last <= 0 {
		return "", fmt.Errorf("position must use file:line:column")
	}
	beforeColumn := value[:last]
	second := strings.LastIndex(beforeColumn, ":")
	if second <= 0 {
		return "", fmt.Errorf("position must use file:line:column")
	}
	path, err := projectPath(projectRoot, beforeColumn[:second])
	if err != nil {
		return "", err
	}
	return path + beforeColumn[second:] + value[last:], nil
}

var _ agent.OutputSink = (*operationSink)(nil)
