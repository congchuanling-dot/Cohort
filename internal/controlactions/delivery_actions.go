package controlactions

import (
	"context"
	"strings"

	"cohort/internal/controlplane"
	"cohort/internal/delivery"
	"cohort/internal/hermes"
)

func deliveryActions() []controlplane.ActionSpec {
	deliveryID := controlplane.InputField{
		Name: "delivery_id", Label: "Delivery ID", Type: controlplane.FieldString,
		Required: true, Placeholder: "delivery_...",
	}
	return []controlplane.ActionSpec{
		{
			ID: "delivery.integrate", Category: "delivery", Label: "执行集成验证",
			Description: "在隔离 Integration Worktree 合并候选并重新生成确定性 Evidence。",
			Keywords:    []string{"integrate", "集成", "证据"}, Risk: controlplane.RiskExecute, Async: true,
			Inputs: []controlplane.InputField{deliveryID},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				state, err := (delivery.Integrator{Store: delivery.NewStore(request.ProjectRoot)}).Run(ctx, textInput(request, "delivery_id"))
				return controlplane.ActionResult{Summary: "integration " + string(state.Status), Data: state}, err
			},
		},
		{
			ID: "delivery.review", Category: "delivery", Label: "生成 Review 报告",
			Description: "校验证据新鲜度并生成 Acceptance、Finding、成本和变更文件报告。",
			Keywords:    []string{"review", "报告", "验收"}, Risk: controlplane.RiskExecute,
			Inputs: []controlplane.InputField{deliveryID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				store := delivery.NewStore(request.ProjectRoot)
				id := textInput(request, "delivery_id")
				report, err := store.BuildReviewReport(id)
				if err != nil {
					return controlplane.ActionResult{}, err
				}
				output := delivery.DefaultReviewPath(store, id)
				if err := delivery.WriteReviewHTML(report, output); err != nil {
					return controlplane.ActionResult{}, err
				}
				return controlplane.ActionResult{Summary: "review report generated", Data: map[string]any{"path": output, "report": report}}, nil
			},
		},
		{
			ID: "delivery.approve", Category: "delivery", Label: "批准 Delivery",
			Description: "记录绑定当前 Contract、Integration tree 与 Verification round 的人工批准。",
			Keywords:    []string{"approve", "批准", "审批"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "APPROVE",
			Inputs: []controlplane.InputField{
				deliveryID,
				{Name: "approved_by", Label: "批准人", Type: controlplane.FieldString, Default: "local-user", Required: true},
			},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				record, err := (delivery.MergeService{Store: delivery.NewStore(request.ProjectRoot)}).Approve(
					textInput(request, "delivery_id"), textInput(request, "approved_by"),
				)
				return controlplane.ActionResult{Summary: "delivery approved", Data: record}, err
			},
		},
		{
			ID: "delivery.merge", Category: "delivery", Label: "事务合并并复验",
			Description: "执行 no-commit 合并、Gate 完整性校验、merge commit 和 post-merge verification。",
			Keywords:    []string{"merge", "合并", "复验"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "MERGE",
			Inputs:           []controlplane.InputField{deliveryID},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				state, err := (delivery.MergeService{Store: delivery.NewStore(request.ProjectRoot)}).Merge(ctx, textInput(request, "delivery_id"))
				return controlplane.ActionResult{Summary: "merge " + string(state.Status), Data: state}, err
			},
		},
		{
			ID: "delivery.accept", Category: "delivery", Label: "批准、合并并复验",
			Description: "一次完成显式人工批准、事务合并和 merge commit 复验。",
			Keywords:    []string{"accept", "接受", "批准合并"}, Risk: controlplane.RiskDanger, Async: true,
			ConfirmationText: "ACCEPT",
			Inputs: []controlplane.InputField{
				deliveryID,
				{Name: "approved_by", Label: "批准人", Type: controlplane.FieldString, Default: "local-user", Required: true},
			},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				service := delivery.MergeService{Store: delivery.NewStore(request.ProjectRoot)}
				if _, err := service.Approve(textInput(request, "delivery_id"), textInput(request, "approved_by")); err != nil {
					return controlplane.ActionResult{}, err
				}
				state, err := service.Merge(ctx, textInput(request, "delivery_id"))
				return controlplane.ActionResult{Summary: "delivery " + string(state.Status), Data: state}, err
			},
		},
		{
			ID: "delivery.recover", Category: "delivery", Label: "恢复中断合并",
			Description: "恢复 commit 前事务或继续 merge commit 的 post-merge verification。",
			Keywords:    []string{"recover", "恢复", "中断"}, Risk: controlplane.RiskConfirm, Async: true,
			ConfirmationText: "RECOVER", Inputs: []controlplane.InputField{deliveryID},
			Handler: func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				state, err := (delivery.MergeService{Store: delivery.NewStore(request.ProjectRoot)}).Recover(ctx, textInput(request, "delivery_id"))
				return controlplane.ActionResult{Summary: "recovery " + string(state.Status), Data: state}, err
			},
		},
		{
			ID: "delivery.cancel", Category: "delivery", Label: "取消 Delivery",
			Description: "在状态机允许的阶段取消 Delivery，不删除任何 Evidence 或 Artifact。",
			Keywords:    []string{"cancel", "取消"}, Risk: controlplane.RiskDanger,
			ConfirmationText: "CANCEL", Inputs: []controlplane.InputField{deliveryID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := delivery.NewStore(request.ProjectRoot).Transition(
					textInput(request, "delivery_id"), delivery.StatusCancelled, "DeliveryCancelledFromControlCenter", nil,
				)
				return controlplane.ActionResult{Summary: "delivery cancelled", Data: item}, err
			},
		},
	}
}

func hermesActions() []controlplane.ActionSpec {
	actionID := controlplane.InputField{Name: "action_id", Label: "Action ID", Type: controlplane.FieldString, Required: true}
	return []controlplane.ActionSpec{
		{
			ID: "hermes.action.acknowledge", Category: "hermes", Label: "确认 Action",
			Description: "将 Hermes Action 标记为 acknowledged。",
			Keywords:    []string{"ack", "确认", "action"}, Risk: controlplane.RiskExecute,
			Inputs: []controlplane.InputField{actionID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := hermes.UpdateActionStatus(hermes.NewStore(request.ProjectRoot), textInput(request, "action_id"), hermes.QueueStatusAcknowledged)
				return controlplane.ActionResult{Summary: "action acknowledged", Data: item}, err
			},
		},
		{
			ID: "hermes.action.dismiss", Category: "hermes", Label: "忽略 Action",
			Description: "将 Action 标记为 dismissed；不会删除历史事件。",
			Keywords:    []string{"dismiss", "忽略"}, Risk: controlplane.RiskConfirm,
			ConfirmationText: "DISMISS", Inputs: []controlplane.InputField{actionID},
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				item, err := hermes.UpdateActionStatus(hermes.NewStore(request.ProjectRoot), textInput(request, "action_id"), hermes.QueueStatusDismissed)
				return controlplane.ActionResult{Summary: "action dismissed", Data: item}, err
			},
		},
	}
}

func textInput(request controlplane.ActionRequest, name string) string {
	value, _ := request.Input[name].(string)
	return strings.TrimSpace(value)
}
