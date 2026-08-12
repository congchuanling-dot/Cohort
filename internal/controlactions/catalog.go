package controlactions

import (
	"context"
	"time"

	"cohort/internal/controlplane"
)

func NewCatalog(configPaths ...string) (*controlplane.Catalog, error) {
	configPath := ""
	if len(configPaths) > 0 {
		configPath = configPaths[0]
	}
	specs := []controlplane.ActionSpec{
		{
			ID:          "system.ping",
			Category:    "system",
			Label:       "测试控制面连接",
			Description: "执行一次无副作用的控制面连通性检查。",
			Keywords:    []string{"ping", "连接", "健康"},
			Risk:        controlplane.RiskRead,
			Handler: func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				return controlplane.ActionResult{
					Summary: "control plane is ready",
					Data: map[string]any{
						"ok":           true,
						"project_root": request.ProjectRoot,
						"time":         time.Now().UTC(),
					},
				}, nil
			},
		},
	}
	specs = append(specs, deliveryActions()...)
	specs = append(specs, hermesActions()...)
	specs = append(specs, capabilityActions()...)
	specs = append(specs, mcpActions()...)
	specs = append(specs, skillActions()...)
	specs = append(specs, lspActions()...)
	specs = append(specs, reflectionActions()...)
	specs = append(specs, runtimeOptimizationActions()...)
	specs = append(specs, agentActions(configPath)...)
	specs = append(specs, settingsActions(configPath)...)
	return controlplane.NewCatalog(specs...)
}
