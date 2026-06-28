package foundation

import "sync"

// Memory 是并发安全的消息存储器。
//
// 设计要点：
//   - 内存存储 + cause_by 索引（O(1) 按 Action 查找）
//   - sync.RWMutex 保证并发安全（大量读取场景下读锁不互斥）
//   - FIFO 淘汰策略，防止内存无限膨胀
type Memory struct {
	mu      sync.RWMutex
	storage []*Message            // 有序消息列表
	index   map[string][]*Message // cause_by → 消息列表索引
	maxSize int                   // 容量上限（0 表示不限制）
}

// NewMemory 创建一个新的消息存储器。
func NewMemory(maxSize int) *Memory {
	return &Memory{
		storage: make([]*Message, 0, maxSize),
		index:   make(map[string][]*Message),
		maxSize: maxSize,
	}
}

// Add 添加一条消息（并发安全）。
func (m *Memory) Add(msg *Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.storage = append(m.storage, msg)
	m.index[msg.CauseBy] = append(m.index[msg.CauseBy], msg)

	// 超出容量时淘汰最旧的消息（FIFO）
	if m.maxSize > 0 && len(m.storage) > m.maxSize {
		removed := m.storage[0]
		m.storage = m.storage[1:]
		m.removeFromIndex(removed)
	}
}

// Get 获取最近 k 条消息，k=0 返回全部。
func (m *Memory) Get(k int) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if k == 0 || k > len(m.storage) {
		k = len(m.storage)
	}
	result := make([]*Message, k)
	copy(result, m.storage[len(m.storage)-k:])
	return result
}

// GetByAction 按 cause_by 查找消息（O(1)）。
func (m *Memory) GetByAction(actionName string) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs, ok := m.index[actionName]
	if !ok {
		return nil
	}
	result := make([]*Message, len(msgs))
	copy(result, msgs)
	return result
}

// GetByActions 按多个 cause_by 查找（OR 逻辑）。
func (m *Memory) GetByActions(actionNames ...string) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Message
	for _, name := range actionNames {
		if msgs, ok := m.index[name]; ok {
			result = append(result, msgs...)
		}
	}
	return result
}

// GetByRole 按发送者角色名查找。
func (m *Memory) GetByRole(roleName string) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Message
	for _, msg := range m.storage {
		if msg.SentFrom == roleName {
			result = append(result, msg)
		}
	}
	return result
}

// Last 返回最后一条消息，没有则返回 nil。
func (m *Memory) Last() *Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.storage) == 0 {
		return nil
	}
	return m.storage[len(m.storage)-1]
}

// FindNews 查找观察者尚未看到的最新消息（最多 k 条）。
// observed 是已读消息 ID 列表。
func (m *Memory) FindNews(observed []string, k int) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	observedSet := make(map[string]bool, len(observed))
	for _, id := range observed {
		observedSet[id] = true
	}

	var news []*Message
	for i := len(m.storage) - 1; i >= 0 && len(news) < k; i-- {
		if !observedSet[m.storage[i].ID] {
			news = append(news, m.storage[i])
		}
	}
	return news
}

// Count 返回消息总数。
func (m *Memory) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.storage)
}

// Clear 清空所有消息。
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storage = m.storage[:0]
	m.index = make(map[string][]*Message)
}

// removeFromIndex 从索引中移除一条消息（调用方需持有写锁）。
func (m *Memory) removeFromIndex(msg *Message) {
	msgs := m.index[msg.CauseBy]
	for i, indexed := range msgs {
		if indexed.ID == msg.ID {
			m.index[msg.CauseBy] = append(msgs[:i], msgs[i+1:]...)
			break
		}
	}
}
