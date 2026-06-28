// Package role 提供智能体的核心抽象——Role。
//
// Role 是整个框架的心脏。每个 Role 是一个独立的执行单元，
// 运行在自己的 goroutine 中，通过 channel 接收消息，
// 执行 observe → think → act → publish 循环。
//
// 三种 React 模式：
//   - ReactByOrder:  按 Actions 列表顺序依次执行（软件公司 SOP 模式）
//   - ReactReAct:    LLM 动态选择下一个 Action
//   - ReactPlanAndAct: 先规划后执行（Phase 4 实现）
package role

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"cohort/internal/action"
	"cohort/internal/foundation"
)

// ==========================================
// React 模式
// ==========================================

// ReactMode 定义 Role 如何选择下一个 Action。
type ReactMode int

const (
	ReactByOrder    ReactMode = iota // 按 Actions 列表顺序执行（SOP 模式，最常用）
	ReactReAct                       // LLM 根据当前状态动态选择下一个 Action
	ReactPlanAndAct                  // 先让 LLM 规划步骤，再逐步执行
)

func (m ReactMode) String() string {
	switch m {
	case ReactByOrder:
		return "ByOrder"
	case ReactReAct:
		return "ReAct"
	case ReactPlanAndAct:
		return "PlanAndAct"
	default:
		return "Unknown"
	}
}

// ==========================================
// MessagePublisher —— Environment 的前置接口
// ==========================================
// Role 只需要知道"能把消息发出去"，不需要依赖完整的 Environment。
// Environment（Phase 4）实现此接口即可接入。

// MessagePublisher 消息发布接口。
// Environment 实现此接口，Role 通过它把执行结果发布给其他 Role。
type MessagePublisher interface {
	PublishMessage(msg *foundation.Message)
}

// ==========================================
// Role —— 智能体的核心抽象
// ==========================================

// Role 是一个独立的 AI 智能体。
//
// 设计思想：
//   - 每个 Role 运行在自己的 goroutine 中（由 Environment.Run() 启动）
//   - msgBuffer（channel）是 Role 的"邮箱"，由 Environment 投递消息
//   - observe → think → act → publish 是核心循环，模拟人类认知过程
type Role struct {
	// === 身份信息（面试时给 Role 设定人设）===
	Name        string `json:"name"`        // 角色名，如 "Alex"
	Profile     string `json:"profile"`     // 角色简介，如 "Senior Engineer with 10 years..."
	Goal        string `json:"goal"`        // 目标，如 "Write high-quality Go code"
	Constraints string `json:"constraints"` // 约束，如 "Must follow Go idioms"
	Desc        string `json:"desc"`        // 详细描述

	// === 行为定义 ===
	actions   []action.Action // 可执行的动作列表
	reactMode ReactMode        // 反应模式
	watch     map[string]bool  // 关注的 cause_by 集合（空 = 关注所有）

	// === 运行时状态 ===
	state     int                     // 当前动作索引，-1 = idle/terminated
	msgBuffer chan *foundation.Message // 消息邮箱（buffered channel）
	memory    *foundation.Memory       // 消息历史存储
	env       MessagePublisher         // 消息发布接口
	observed  []string                 // 已观察过的消息 ID

	// === 生命周期 ===
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ==========================================
// 函数式选项模式
// ==========================================

// RoleOption 函数式选项。
type RoleOption func(*Role)

// WithActions 设置 Role 可执行的动作列表。
func WithActions(actions ...action.Action) RoleOption {
	return func(r *Role) { r.actions = actions }
}

// WithReactMode 设置反应模式。
func WithReactMode(mode ReactMode) RoleOption {
	return func(r *Role) { r.reactMode = mode }
}

// WithWatch 设置关注的 cause_by 列表。
// 仅关注列表中指定的 Action 产出；空 = 关注所有消息。
func WithWatch(causeByList ...string) RoleOption {
	return func(r *Role) {
		for _, c := range causeByList {
			r.watch[c] = true
		}
	}
}

// WithMemory 设置消息历史存储器。
func WithMemory(m *foundation.Memory) RoleOption {
	return func(r *Role) { r.memory = m }
}

// WithProfile 设置角色的身份描述。
func WithProfile(profile, goal, constraints string) RoleOption {
	return func(r *Role) {
		r.Profile = profile
		r.Goal = goal
		r.Constraints = constraints
	}
}

// ==========================================
// 构造函数
// ==========================================

// NewRole 创建一个新的 Role。
//
// 默认使用 ReactByOrder 模式，100 条消息缓冲。
//
// 示例：
//
//	alice := role.NewRole("Alice",
//	    role.WithProfile("Product Manager", "Define clear PRD", "Be concise"),
//	    role.WithActions(writePRD),
//	    role.WithWatch("UserRequirement"),
//	    role.WithMemory(mem),
//	)
func NewRole(name string, opts ...RoleOption) *Role {
	r := &Role{
		Name:      name,
		reactMode: ReactByOrder, // 默认 SOP 模式（最常用）
		watch:     make(map[string]bool),
		state:     0,                                     // 从第 0 个 action 开始
		msgBuffer: make(chan *foundation.Message, 100),   // 缓冲 100 条消息
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ==========================================
// 主循环 —— 整个框架最核心的代码
// ==========================================

// Run 启动 Role 的主循环。
// 阻塞直到 ctx 被取消或 msgBuffer 被关闭。
// 由 Environment.Run() 在 goroutine 中调用。
//
// 循环体：
//
//	select {
//	case <-ctx.Done():  // 优雅关闭
//	case msg := <-msgBuffer:  // 收到消息
//	    observe(msg)  →  react(ctx)  →  publish(rsp)
//	}
func (r *Role) Run(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)
	defer r.cancel()

	log.Printf("[%s] started | mode=%s | actions=%d | watch=%v",
		r.Name, r.reactMode, len(r.actions), r.watchKeys())

	for {
		select {
		case <-r.ctx.Done():
			log.Printf("[%s] context cancelled, exiting", r.Name)
			return r.ctx.Err()

		case msg, ok := <-r.msgBuffer:
			if !ok {
				log.Printf("[%s] msgBuffer closed, exiting", r.Name)
				return nil
			}

			// === Step 1: Observe ===
			if !r.shouldObserve(msg) {
				continue
			}
			r.memory.Add(msg)
			r.markObserved(msg)

			log.Printf("[%s] observed | cause_by=%s | content_len=%d",
				r.Name, msg.CauseBy, len(msg.Content))

			// === Step 2 & 3: Think + Act（合并为 React）===
			rsp, err := r.react(r.ctx)
			if err != nil {
				log.Printf("[%s] react error: %v", r.Name, err)
				continue
			}

			// === Step 4: Publish ===
			if rsp != nil && r.env != nil {
				r.env.PublishMessage(rsp)
				log.Printf("[%s] published | cause_by=%s", r.Name, rsp.CauseBy)
			}
		}
	}
}

// ==========================================
// React 分发
// ==========================================

// react 根据 reactMode 分发到具体实现。
//
// ★ 使用 context.WithoutCancel 保护 LLM 调用不被 env 的轮询超时打断。
// env 的 ctx 用于控制角色主循环的启停，但 LLM 调用一旦发起就必须完成。
func (r *Role) react(ctx context.Context) (*foundation.Message, error) {
	ctx = context.WithoutCancel(ctx)
	switch r.reactMode {
	case ReactByOrder:
		return r.reactByOrder(ctx)
	case ReactReAct:
		return r.reactReAct(ctx)
	case ReactPlanAndAct:
		return r.reactPlanAndAct(ctx)
	default:
		return nil, fmt.Errorf("unknown react mode: %v", r.reactMode)
	}
}

// ==========================================
// reactByOrder —— SOP 模式（最常用）
// ==========================================

// reactByOrder 按 Actions 列表顺序依次执行。
//
// 每次调用执行一个 Action，执行完 state++ 指向下一个。
// 所有 Action 执行完毕后 state = -1（idle）。
//
// 这是软件公司场景的主要模式：
//
//	PM:        WritePRD → (idle)
//	Architect: WriteDesign → (idle)
//	Engineer:  WriteCode → (idle)
//	QA:        WriteTest → (idle)
func (r *Role) reactByOrder(ctx context.Context) (*foundation.Message, error) {
	if r.state >= len(r.actions) {
		r.state = -1 // 所有 action 执行完毕
		return nil, nil
	}

	act := r.actions[r.state]
	history := r.memory.Get(0) // 获取全部历史消息

	log.Printf("[%s] executing [%d/%d]: %s",
		r.Name, r.state+1, len(r.actions), act.Name())

	output, err := act.Run(ctx, history)
	if err != nil {
		return nil, fmt.Errorf("action %s failed: %w", act.Name(), err)
	}

	r.state++
	if r.state >= len(r.actions) {
		r.state = -1
	}

	return &foundation.Message{
		Content:         output.Content,
		InstructContent: output.InstructContent,
		CauseBy:         act.Name(),
		SentFrom:        r.Name,
		SendTo:          []string{foundation.RouteToAll},
		Role:            foundation.RoleSystem,
	}, nil
}

// ==========================================
// reactReAct —— LLM 动态选择模式
// ==========================================

// reactReAct 让 LLM 根据当前状态动态选择下一个 Action。
//
// 流程：
//  1. 构建 prompt（Role 身份 + 历史消息 + 可用 Action 列表）
//  2. 调用 LLM 选择下一步
//  3. 执行选中的 Action
//
// 简化实现：使用第一个 Action 的 LLM 客户端做"思考"。
func (r *Role) reactReAct(ctx context.Context) (*foundation.Message, error) {
	if len(r.actions) == 0 {
		r.state = -1
		return nil, nil
	}

	history := r.memory.Get(10) // 最近 10 条消息

	// 使用第一个 Action 的 LLM 客户端来选择下一步
	// 注意：这里需要一个 ThinkAction，但当前简化为直接选
	selectedIdx := r.selectAction(ctx, history)

	act := r.actions[selectedIdx]
	log.Printf("[%s] ReAct selected [%d/%d]: %s",
		r.Name, selectedIdx+1, len(r.actions), act.Name())

	output, err := act.Run(ctx, r.memory.Get(0))
	if err != nil {
		return nil, err
	}

	return &foundation.Message{
		Content:  output.Content,
		CauseBy:  act.Name(),
		SentFrom: r.Name,
		SendTo:   []string{foundation.RouteToAll},
		Role:     foundation.RoleSystem,
	}, nil
}

// selectAction 让 LLM 从可用 Action 中选择下一步。
// 简化实现：默认返回当前 state 指向的 action。
// TODO: 解析 LLM 返回的 action name，匹配到具体索引。
func (r *Role) selectAction(ctx context.Context, history []*foundation.Message) int {
	// 如果有 LLM 客户端（第一个 action 的），尝试让 LLM 选择
	// 否则直接按索引顺序
	if r.state >= 0 && r.state < len(r.actions) {
		return r.state
	}
	return 0
}

// reactPlanAndAct 先规划后执行。
// TODO: Phase 4 实现完整的规划器。
func (r *Role) reactPlanAndAct(ctx context.Context) (*foundation.Message, error) {
	return r.reactByOrder(ctx) // 暂时回退到 byOrder
}

// ==========================================
// Runtime 方法
// ==========================================

// IsIdle 判断角色是否已空闲（所有 action 执行完毕）。
func (r *Role) IsIdle() bool {
	return r.state == -1
}

// MessageBuffer 返回消息邮箱（只写 channel）。
// 供 MessagePublisher/Environment 投递消息。
func (r *Role) MessageBuffer() chan<- *foundation.Message {
	return r.msgBuffer
}

// SetPublisher 设置消息发布接口。
// 由 Environment.RegisterRole() 调用，建立 Role → Environment 的连接。
func (r *Role) SetPublisher(env MessagePublisher) {
	r.env = env
}

// Actions 返回可执行的动作列表（只读）。
func (r *Role) Actions() []action.Action {
	return r.actions
}

// State 返回当前状态（-1 = idle, >=0 = 下一个要执行的 action 索引）。
func (r *Role) State() int {
	return r.state
}

// ==========================================
// 内部辅助
// ==========================================

// shouldObserve 判断是否应该关注此消息。
// watch 为空 = 关注所有；否则只关注 watch 中指定的 cause_by。
func (r *Role) shouldObserve(msg *foundation.Message) bool {
	if len(r.watch) == 0 {
		return true
	}
	return r.watch[msg.CauseBy]
}

// markObserved 记录已观察的消息 ID。
// 限制 1000 条防止内存泄露。
func (r *Role) markObserved(msg *foundation.Message) {
	r.observed = append(r.observed, msg.ID)
	if len(r.observed) > 1000 {
		r.observed = r.observed[100:] // 保留最近 900 条
	}
}

// watchKeys 返回 watch 的 key 列表（用于日志）。
func (r *Role) watchKeys() []string {
	keys := make([]string, 0, len(r.watch))
	for k := range r.watch {
		keys = append(keys, k)
	}
	return keys
}

// ==========================================
// 共享工具函数（同包内使用）
// ==========================================

// buildActionListPrompt 构建 Action 列表的描述文本。
func BuildActionListPrompt(actions []action.Action) string {
	var sb strings.Builder
	for i, a := range actions {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, a.Name())
	}
	return sb.String()
}

// FormatHistory 将历史消息格式化为文本（供 ReactReAct prompt 使用）。
func FormatHistory(msgs []*foundation.Message) string {
	var sb strings.Builder
	for _, msg := range msgs {
		fmt.Fprintf(&sb, "[%s] %s\n", msg.SentFrom, Truncate(msg.Content, 500))
	}
	return sb.String()
}

// Truncate 截断字符串到指定长度。
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
