// Package foundation 提供多智能体框架的基础数据模型。
// 本文件定义 Agent 之间的通信单元——Message。
package foundation

import "time"

// 特殊路由地址
const (
	RouteToAll  = "<all>"  // 广播给所有角色
	RouteToSelf = "<self>" // 回环给自己
)

// Message 角色常量
const (
	RoleUser      = "user"
	RoleSystem    = "system"
	RoleAssistant = "assistant"
)

// Message 是智能体之间的通信单元，也是整个框架的数据载体。
//
// 两个核心路由字段：
//   - CauseBy: 标识消息由哪个 Action 产生，是 Role 订阅（watch）的依据
//   - SendTo:  标识消息发送给哪些 Role，是 Environment 路由的依据
type Message struct {
	ID              string         `json:"id"`               // 唯一标识
	Content         string         `json:"content"`          // 自然语言内容
	InstructContent any            `json:"instruct_content"` // 结构化数据（PRD、代码等）
	Role            string         `json:"role"`             // user / system / assistant
	CauseBy         string         `json:"cause_by"`         // 由哪个 Action 产生（路由依据）
	SentFrom        string         `json:"sent_from"`        // 由哪个 Role 发送
	SendTo          []string       `json:"send_to"`          // 接收者列表
	Metadata        map[string]any `json:"metadata"`         // 扩展元数据
	Timestamp       time.Time      `json:"timestamp"`        // 创建时间
}

// NewUserMessage 创建一条用户消息（广播给所有角色）。
func NewUserMessage(content string) *Message {
	return &Message{
		Content:   content,
		Role:      RoleUser,
		CauseBy:   "UserRequirement",
		SentFrom:  "User",
		SendTo:    []string{RouteToAll},
		Timestamp: time.Now(),
	}
}

// NewSystemMessage 创建一条系统消息（由 Agent 产生）。
func NewSystemMessage(content string, causedBy, sentFrom string) *Message {
	return &Message{
		Content:   content,
		Role:      RoleSystem,
		CauseBy:   causedBy,
		SentFrom:  sentFrom,
		SendTo:    []string{RouteToAll},
		Timestamp: time.Now(),
	}
}

// ShouldSendTo 判断消息是否应该发送给指定角色。
func (m *Message) ShouldSendTo(roleName string) bool {
	for _, target := range m.SendTo {
		if target == RouteToAll || target == roleName || target == "*" {
			return true
		}
	}
	return false
}

// ShouldObserve 判断指定角色是否应该关注此消息。
// watchKeys 是该角色关注的 cause_by 集合；空集合表示关注所有消息。
func (m *Message) ShouldObserve(watchKeys map[string]bool) bool {
	if len(watchKeys) == 0 {
		return true
	}
	return watchKeys[m.CauseBy]
}
