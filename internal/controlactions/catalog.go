package controlactions

import (
	"context"
	"time"

	"cohort/internal/controlplane"
)

func NewCatalog() (*controlplane.Catalog, error) {
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
	return controlplane.NewCatalog(specs...)
}
