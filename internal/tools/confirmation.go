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
	desktopAXPressOperation     = "desktop_ax_press"
	desktopClickOperation       = "desktop_click"
	desktopVisualClickOperation = "desktop_visual_click"
	desktopPressKeyOperation    = "desktop_press_key"
)

// ActionApproval 绑定用户确认到一个具体的高风险桌面动作。
// 绑定 pid、node_id/key 和 reason，避免确认令牌被复用于其他控件、按键或任务。
type ActionApproval struct {
	Operation string
	PID       int
	NodeID    string
	ImagePath string
	BBox      string
	Key       string
	Reason    string
}

type storedApproval struct {
	approval ActionApproval
	expires  time.Time
}

// ConfirmationStore 保存短时、一次性的用户确认令牌。
// 它只在一个 Cohert 进程内生效，令牌消费后立即失效。
type ConfirmationStore struct {
	mu      sync.Mutex
	entries map[string]storedApproval
	now     func() time.Time
	ttl     time.Duration
}

func NewConfirmationStore() *ConfirmationStore {
	return &ConfirmationStore{
		entries: make(map[string]storedApproval),
		now:     time.Now,
		ttl:     5 * time.Minute,
	}
}

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

func approvalAnswerAccepted(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "确认", "同意", "允许", "继续", "是", "好", "yes", "y", "ok", "approve", "approved":
		return true
	default:
		return false
	}
}
