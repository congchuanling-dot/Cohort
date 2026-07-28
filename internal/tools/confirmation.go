package tools

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// desktopAXPressOperation 标识基于辅助功能节点的语义 Press 动作。
	desktopAXPressOperation = "desktop_ax_press"
	// desktopClickOperation 标识基于辅助功能节点中心的物理点击动作。
	desktopClickOperation = "desktop_click"
	// desktopVisualClickOperation 标识基于截图坐标映射的视觉点击动作。
	desktopVisualClickOperation = "desktop_visual_click"
	// desktopPressKeyOperation 标识向目标进程发送受限按键的动作。
	desktopPressKeyOperation = "desktop_press_key"
	// computerDragOperation 标识基于缓存 target 的受控拖拽动作。
	computerDragOperation = "computer_drag"
)

// ActionApproval 绑定用户确认到一个具体的高风险桌面动作。
// 绑定 pid、node_id/key 和 reason，避免确认令牌被复用于其他控件、按键或任务。
type ActionApproval struct {
	// Operation 是令牌允许的唯一桌面操作类型。
	Operation string
	// PID 将授权绑定到一个已检查的应用进程。
	PID int
	// NodeID 将 AX 动作或节点点击绑定到当前快照中的控件。
	NodeID string
	// ImagePath 将视觉点击绑定到产生定位框的截图文件。
	ImagePath string
	// BBox 是截图局部坐标中的目标框，作为视觉点击的精确目标。
	BBox string
	// Key 是按键操作的精确键值或组合键。
	Key string
	// Reason 是向用户展示且执行时再次匹配的动作原因。
	Reason string
}

// storedApproval 把授权内容和过期时间保存在内存中，绝不写入会话或日志。
type storedApproval struct {
	approval ActionApproval
	expires  time.Time
}

// VisualFocusGrant 记录一次由 desktop_visual_click 建立的短期视觉焦点。
// 它不授权发送或提交，只允许 desktop_type_text 在 AX 无法证明 WebView
// 输入框焦点时执行一次文本起草。
type VisualFocusGrant struct {
	// PID 限制令牌只能用于同一前台应用。
	PID int
	// ImagePath 是建立视觉焦点时所依据的截图。
	ImagePath string
	// BBox 是截图中被确认是输入框或搜索框的区域。
	BBox string
	// Reason 是该焦点可用于起草而不可提交的原因说明。
	Reason string
}

// storedVisualFocus 是附带过期时间的视觉焦点授权记录。
type storedVisualFocus struct {
	grant   VisualFocusGrant
	expires time.Time
}

// ConfirmationStore 保存短时、一次性的用户确认令牌。
// 它只在一个 Cohert 进程内生效，令牌消费后立即失效。
type ConfirmationStore struct {
	// mu 保护 entries，也让一次性消费与并发请求原子化。
	mu sync.Mutex
	// entries 按不可预测令牌保存待消费授权。
	entries map[string]storedApproval
	// now 可在测试中替换，稳定验证过期行为。
	now func() time.Time
	// ttl 是用户确认有效期，过期后必须重新询问用户。
	ttl time.Duration
}

// NewConfirmationStore 创建仅进程内有效、默认五分钟过期的一次性授权存储。
func NewConfirmationStore() *ConfirmationStore {
	return &ConfirmationStore{
		entries: make(map[string]storedApproval),
		now:     time.Now,
		ttl:     5 * time.Minute,
	}
}

// Issue 为精确动作签发随机令牌，并在写入前清理已过期的旧令牌。
func (s *ConfirmationStore) Issue(approval ActionApproval) (string, error) {
	if s == nil {
		return "", errors.New("confirmation store is not configured")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, entry := range s.entries {
		if !entry.expires.After(now) {
			delete(s.entries, key)
		}
	}
	s.entries[token] = storedApproval{
		approval: approval,
		expires:  now.Add(s.ttl),
	}
	return token, nil
}

// Consume 只在令牌未过期且完全匹配目标动作时返回 true。
func (s *ConfirmationStore) Consume(token string, expected ActionApproval) bool {
	if s == nil || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok {
		return false
	}
	delete(s.entries, token)
	if !entry.expires.After(s.now()) {
		return false
	}
	return entry.approval == expected
}

// approvalAnswerAccepted 只将明确的中英文同意词视为授权，其他输入一律拒绝。
func approvalAnswerAccepted(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "确认", "同意", "允许", "继续", "是", "好", "yes", "y", "ok", "approve", "approved":
		return true
	default:
		return false
	}
}

// VisualFocusStore 保存由视觉点击签发的短时、一次性焦点令牌。
type VisualFocusStore struct {
	// mu 保护 entries 并保证令牌只能被一个调用消费。
	mu sync.Mutex
	// entries 按令牌存储视觉焦点授权。
	entries map[string]storedVisualFocus
	// now 可注入测试时钟。
	now func() time.Time
	// ttl 比确认令牌更短，降低界面焦点变化后误输入的风险。
	ttl time.Duration
}

// NewVisualFocusStore 创建默认四十五秒有效的单次视觉焦点令牌存储。
func NewVisualFocusStore() *VisualFocusStore {
	return &VisualFocusStore{
		entries: make(map[string]storedVisualFocus),
		now:     time.Now,
		ttl:     45 * time.Second,
	}
}

// Issue 签发只允许起草文本的视觉焦点令牌，并回传其有效期供调用方提示模型。
func (s *VisualFocusStore) Issue(grant VisualFocusGrant) (string, time.Duration, error) {
	if s == nil {
		return "", 0, errors.New("visual focus store is not configured")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, entry := range s.entries {
		if !entry.expires.After(now) {
			delete(s.entries, key)
		}
	}
	s.entries[token] = storedVisualFocus{
		grant:   grant,
		expires: now.Add(s.ttl),
	}
	return token, s.ttl, nil
}

// Consume 仅当令牌未过期且 PID 匹配时返回授权，并无条件删除令牌以保证单次使用。
func (s *VisualFocusStore) Consume(token string, pid int) (VisualFocusGrant, bool) {
	if s == nil || token == "" || pid <= 0 {
		return VisualFocusGrant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok {
		return VisualFocusGrant{}, false
	}
	delete(s.entries, token)
	if !entry.expires.After(s.now()) || entry.grant.PID != pid {
		return VisualFocusGrant{}, false
	}
	return entry.grant, true
}
