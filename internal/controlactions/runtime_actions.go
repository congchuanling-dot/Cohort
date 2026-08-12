package controlactions

import (
	"context"
	"errors"
	"strings"

	"cohort/internal/capability"
	"cohort/internal/controlplane"
)

func runtimeOptimizationActions() []controlplane.ActionSpec {
	return []controlplane.ActionSpec{{
		ID:          "runtime.optimization.propose",
		Category:    "runtime",
		Label:       "生成运行优化 Proposal",
		Description: "自动选择成功基线，对比 Run，并把有证据的优化建议送入 Capability 审批与验证闭环。",
		Keywords:    []string{"runtime", "compare", "optimization", "proposal", "运行优化"},
		Risk:        controlplane.RiskExecute,
		Inputs: []controlplane.InputField{
			{
				Name: "session_id", Label: "Session", Type: controlplane.FieldEntity, Required: true,
				Entity: &controlplane.EntitySelector{Kind: controlplane.EntitySession, RecentFirst: true},
			},
			{Name: "run_id", Label: "Run ID", Type: controlplane.FieldString, Required: true},
		},
		Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
			comparison, err := compareQualityRun(
				request.ProjectRoot,
				textInput(request, "session_id"),
				textInput(request, "run_id"),
			)
			if err != nil {
				return controlplane.ActionResult{}, err
			}
			if comparison.Baseline == nil {
				return controlplane.ActionResult{}, errors.New("no successful baseline is available for this run")
			}
			store := capability.NewStore(request.ProjectRoot)
			gap, err := store.AddGap(capability.Gap{
				Task:              comparison.Proposal.Summary,
				MissingCapability: "runtime_optimization_" + comparison.Current.RunID,
				Source:            "runtime_compare",
				Status:            capability.StatusMissing,
				Evidence:          append([]string(nil), comparison.Proposal.Evidence...),
				SuggestedActions:  append([]string(nil), comparison.Proposal.Recommendations...),
			})
			if err != nil {
				return controlplane.ActionResult{}, err
			}
			proposal, err := store.AddProposal(capability.Proposal{
				GapID:        gap.ID,
				Summary:      comparison.Proposal.Summary + ": " + strings.Join(comparison.Proposal.Recommendations, " "),
				InstallScope: "project",
				Artifacts:    append([]string(nil), comparison.Proposal.Evidence...),
				Risk:         comparison.Proposal.Risk,
				Verification: capability.Verification{
					SampleTask: "Re-run the acceptance task and compare against baseline " + comparison.Baseline.RunID,
				},
				Status: capability.StatusProposed,
			})
			return controlplane.ActionResult{
				Summary: "runtime optimization proposal created",
				Data: map[string]any{
					"comparison": comparison,
					"gap":        gap,
					"proposal":   proposal,
				},
			}, err
		},
	}}
}
