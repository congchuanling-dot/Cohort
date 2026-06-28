package role

import (
	"cohort/internal/foundation"
)

// RoleContext 将 Role 的可变运行时状态集中管理。
// 分离出来是为了便于序列化/反序列化（Phase 5 的存档功能）。
//
// 注意：channel 和 Memory 指针不参与 JSON 序列化（json:"-"）。
type RoleContext struct {
	Env       MessagePublisher        `json:"-"` // 消息发布接口
	MsgBuffer chan *foundation.Message `json:"-"` // 消息邮箱
	Memory    *foundation.Memory       `json:"-"` // 消息历史
	State     int                      `json:"state"`     // 当前状态（-1 = idle）
	Todo      string                   `json:"todo"`      // 待执行的 action 名称
	Watch     map[string]bool          `json:"watch"`     // 关注的 cause_by
	Observed  []string                 `json:"observed"`  // 已观察消息 ID
}

// NewRoleContext 创建一个新的运行时上下文。
func NewRoleContext(memory *foundation.Memory) *RoleContext {
	return &RoleContext{
		MsgBuffer: make(chan *foundation.Message, 100),
		Memory:    memory,
		State:     0,
		Watch:     make(map[string]bool),
	}
}

// SyncToRole 将上下文状态同步回 Role 结构体。
// 用于从序列化数据恢复 Role 状态。
func (rc *RoleContext) SyncToRole(r *Role) {
	if rc == nil || r == nil {
		return
	}
	r.state = rc.State
	r.watch = rc.Watch
	r.observed = rc.Observed
	r.env = rc.Env
	r.memory = rc.Memory
	r.msgBuffer = rc.MsgBuffer
}

// SyncFromRole 从 Role 结构体提取当前状态。
func (rc *RoleContext) SyncFromRole(r *Role) {
	if rc == nil || r == nil {
		return
	}
	rc.State = r.state
	rc.Watch = r.watch
	rc.Observed = r.observed
	rc.Env = r.env
	rc.Memory = r.memory
	rc.MsgBuffer = r.msgBuffer
}
