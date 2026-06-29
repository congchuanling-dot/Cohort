// Package env 提供多 Agent 的消息路由中心和生命周期管理。
//
// Environment 类比消息中间件：
//   - 每个 Role 是消息的"订阅者"
//   - PublishMessage 是"生产者"——根据消息的 SendTo 字段路由
//   - Run() 并发启动所有注册的 Role，等待全部完成或任一失败
package env

import (
	"context"
	"log"
	"sync"
	"time"

	"cohort/internal/foundation"
	"cohort/internal/role"
	"cohort/internal/tool"
)

// Environment 消息路由中心 + Role 生命周期管理器。
//
// 设计思想：
//   - 类比 Kafka/RabbitMQ，Environment 是 Topic 路由器
//   - 每个 Role 订阅一组地址（adresses），Environment 负责投递匹配的消息
//   - 所有 Role 独立并发运行，Environment 不控制执行顺序
type Environment struct {
	mu          sync.RWMutex
	roles       map[string]*role.Role      // 角色注册表：name → Role
	memberAddrs map[string]map[string]bool // roleName → 它注册的地址集合
	history     *foundation.Memory         // 全局消息历史（调试用）
	cfg         *foundation.Config         // 全局配置
	publicTools *tool.ToolRegistry         // ★ 公有工具注册表（所有 Role 共享）
}

// NewEnvironment 创建一个新的消息路由环境。
func NewEnvironment(cfg *foundation.Config) *Environment {
	return &Environment{
		roles:       make(map[string]*role.Role),
		memberAddrs: make(map[string]map[string]bool),
		history:     foundation.NewMemory(1000),
		cfg:         cfg,
		publicTools: tool.NewRegistry(),
	}
}

// RegisterRole 注册一个角色到环境中。
//
// 同时：
//  1. 将 Role 加入注册表
//  2. 建立 Role → Environment 的消息发布连接（SetPublisher）
//  3. 记录 Role 的地址（用于消息路由反查）
//
// addresses 是该角色注册的地址列表，通常就是角色名本身。
// 一条消息的 send_to 中只要包含任一地址，就会被投递给该角色。
func (e *Environment) RegisterRole(r *role.Role, addresses ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.roles[r.Name] = r
	r.SetPublisher(e) // ★ 建立 Role → Environment 的连接

	// ★ 注入公有工具到 Role
	r.MergePublicTools(e.publicTools)

	addrSet := make(map[string]bool)
	for _, addr := range addresses {
		addrSet[addr] = true
	}
	e.memberAddrs[r.Name] = addrSet

	log.Printf("[Env] registered: %s (addresses: %v, public_tools: %d)",
		r.Name, addresses, e.publicTools.Count())
}

// PublishMessage 发布消息到所有匹配的角色。
//
// 路由逻辑（实现 roole.MessagePublisher 接口）：
//  1. 消息存入全局历史
//  2. 遍历所有角色，用 Message.ShouldSendTo 检查是否匹配
//  3. 匹配的角色 → 非阻塞推入其 msgBuffer
//     （buffer 满则丢弃并告警 —— 避免慢消费者拖死整个系统）
func (e *Environment) PublishMessage(msg *foundation.Message) {
	// 1. 存储到全局历史
	e.history.Add(msg)
	log.Printf("[Env] published | cause_by=%s | sent_from=%s | content_len=%d",
		msg.CauseBy, msg.SentFrom, len(msg.Content))

	// 2. 路由到匹配的角色
	e.mu.RLock()
	defer e.mu.RUnlock()

	for name, r := range e.roles {
		if !msg.ShouldSendTo(name) {
			continue
		}

		// 非阻塞投递：buffer 满则丢弃并告警
		select {
		case r.MessageBuffer() <- msg:
			log.Printf("[Env] routed → %s", name)
		default:
			log.Printf("[Env] WARNING: %s msgBuffer full, dropping %s", name, msg.ID)
		}
	}
}

// Run 单轮执行：并发运行所有非空闲 Role，每个处理一条消息后返回。
//
// 与 MetaGPT 的 Environment.run() 行为一致：
//  1. 找出所有活跃 Role
//  2. 并发启动 goroutine，每个调用 Role.RunOnce()
//  3. RunOnce 处理一条消息后自然返回
//  4. wg.Wait() 等所有 goroutine 返回
//
// 调用方（Team.Run）会在循环中多次调用此方法，每轮处理一条消息。
func (e *Environment) Run(ctx context.Context) error {
	e.mu.RLock()
	active := make([]*role.Role, 0, len(e.roles))
	for _, r := range e.roles {
		if !r.IsIdle() {
			active = append(active, r)
		}
	}
	e.mu.RUnlock()

	if len(active) == 0 {
		log.Println("[Env] no active roles to run")
		return nil
	}

	log.Printf("[Env] running %d roles", len(active))

	// 带超时的 context：LLM 调用可能耗时很长
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	for _, r := range active {
		wg.Add(1)
		go func(role *role.Role) {
			defer wg.Done()
			hasMore, runErr := role.RunOnce(ctx)
			if runErr != nil {
				log.Printf("[Env] %s error: %v", role.Name, runErr)
			}
			if hasMore {
				log.Printf("[Env] %s has more work pending", role.Name)
			}
		}(r)
	}

	wg.Wait() // RunOnce 自然返回，无需 cancel
	return nil
}

// ==========================================
// 查询接口
// ==========================================

// History 返回全局消息历史。
func (e *Environment) History() *foundation.Memory {
	return e.history
}

// IsAllIdle 检查是否所有角色都已空闲。
func (e *Environment) IsAllIdle() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, r := range e.roles {
		if !r.IsIdle() {
			return false
		}
	}
	return true
}

// Roles 返回所有已注册的角色（只读副本）。
func (e *Environment) Roles() map[string]*role.Role {
	e.mu.RLock()
	defer e.mu.RUnlock()

	cp := make(map[string]*role.Role, len(e.roles))
	for k, v := range e.roles {
		cp[k] = v
	}
	return cp
}

// Config 返回全局配置。
func (e *Environment) Config() *foundation.Config {
	return e.cfg
}

// ==========================================
// 公有工具管理
// ==========================================

// RegisterPublicTool 注册一个公有工具（所有 Role 自动继承）。
//
// 在创建 Role 之前调用，后续注册的 Role 都会自动获得这个工具。
//
// 示例：
//
//	env.RegisterPublicTool(tools.NewWriteFileTool())
//	env.RegisterPublicTool(tools.NewRunCommandTool())
//	env.RegisterPublicTool(tools.NewReadFileTool())
func (e *Environment) RegisterPublicTool(t tool.Tool) {
	e.publicTools.Register(t)
	log.Printf("[Env] public tool registered: %s", t.Name())
}

// PublicTools 返回公有工具注册表。
func (e *Environment) PublicTools() *tool.ToolRegistry {
	return e.publicTools
}

// Archive 归档所有生成的文件。
// TODO: Phase 5 —— git init + git add + git commit
func (e *Environment) Archive() error {
	return nil
}
