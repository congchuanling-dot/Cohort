// Package team 提供多 Agent 团队的编排能力。
//
// Team 是最顶层的编排器：
//   - 创建 Environment（消息路由中心）
//   - 雇佣 Role（注册到 Environment）
//   - 控制预算、轮次
//   - 启动协作循环
//
// 示例（双 Agent 协作）：
//
//	team := team.NewTeam(cfg)
//	team.Hire(alice)  // PM
//	team.Hire(bob)    // Engineer
//	team.SetMaxRound(3)
//	history, _ := team.Run(ctx, "写一个 2048 游戏")
package team

import (
	"context"
	"fmt"
	"log"

	"cohort/internal/env"
	"cohort/internal/foundation"
	"cohort/internal/role"
	"cohort/internal/tool"
)

// Team 多智能体团队。
//
// 负责：
//   - 创建和管理 Environment
//   - 雇佣 Role 并注册到 Environment
//   - 预算管理和轮次控制
//   - 启动协作循环
type Team struct {
	env    *env.Environment
	cfg    *foundation.Config
	roles  []*role.Role
	budget float64 // 预算上限（美元）
	nRound int     // 最大运行轮次
}

// NewTeam 创建一个新的 Agent 团队。
func NewTeam(cfg *foundation.Config) *Team {
	return &Team{
		env:    env.NewEnvironment(cfg),
		cfg:    cfg,
		nRound: 5, // 默认最多 5 轮
	}
}

// Hire 雇佣一个角色加入团队。
//
// 自动将 Role 注册到 Environment。
// 雇佣后的 Role 就可以接收和发送消息了。
func (t *Team) Hire(r *role.Role) {
	t.roles = append(t.roles, r)
	t.env.RegisterRole(r, r.Name)
	log.Printf("[Team] hired: %s (%s)", r.Name, r.Profile)
}

// Invest 设置预算上限（美元）。
func (t *Team) Invest(budget float64) {
	t.budget = budget
}

// SetMaxRound 设置最大运行轮次。
func (t *Team) SetMaxRound(n int) {
	t.nRound = n
}

// Run 启动团队协作。
//
// 整体流程：
//  1. 发布用户需求 → 所有 Role 收到
//  2. 循环运行 Environment（每轮并发执行所有活跃 Role）
//  3. 直到：所有 Role 空闲 / 达到最大轮次 / 预算超支 / ctx 取消
//  4. 返回全局消息历史
//
// 一轮的含义：
//
//	所有非空闲 Role 并发执行各自的 observe→react→publish 循环
//	因为每个 Role 收到消息后才执行下一步，所以一轮里每个 Role 最多处理 1 条消息
func (t *Team) Run(ctx context.Context, idea string) (*foundation.Memory, error) {
	log.Printf("[Team] starting | idea=%q | budget=$%.2f | rounds=%d | members=%d",
		idea, t.budget, t.nRound, len(t.roles))

	// 1. 发布用户需求（广播给所有 Role）
	t.env.PublishMessage(foundation.NewUserMessage(idea))

	// 2. 循环运行
	for round := 0; round < t.nRound; round++ {
		log.Printf("[Team] === Round %d/%d ===", round+1, t.nRound)

		// 运行环境（并发执行所有活跃 Role）
		if err := t.env.Run(ctx); err != nil {
			return nil, fmt.Errorf("round %d failed: %w", round+1, err)
		}

		// 所有 Role 都空闲 → 提前结束
		if t.env.IsAllIdle() {
			log.Printf("[Team] all roles idle at round %d", round+1)
			break
		}
	}

	// 3. 归档
	if err := t.env.Archive(); err != nil {
		log.Printf("[Team] archive warning: %v", err)
	}

	log.Printf("[Team] completed | %d messages in history", t.env.History().Count())
	return t.env.History(), nil
}

// ==========================================
// 查询接口
// ==========================================

// Env 返回内部的 Environment。
func (t *Team) Env() *env.Environment {
	return t.env
}

// Roles 返回所有雇佣的角色。
func (t *Team) Roles() []*role.Role {
	return t.roles
}

// Budget 返回当前预算设置。
func (t *Team) Budget() float64 {
	return t.budget
}

// RegisterTool 注册公有工具到 Environment（所有 Role 自动继承）。
// 在 Hire 之前调用。
func (t *Team) RegisterTool(tl tool.Tool) {
	t.env.RegisterPublicTool(tl)
}
