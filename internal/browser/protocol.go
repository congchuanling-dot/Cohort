package browser

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotConnected 表示 Go 侧 bridge server 已启动，但还没有 Chrome 插件连接上来。
var ErrNotConnected = errors.New("browser bridge is not connected")

// bridgeEnvelope 只解析每条 WebSocket 消息最外层的 type/id。
// 先看 type 再决定按 tabs_update、result、error 等具体结构继续解析。
type bridgeEnvelope struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// tabsMessage 对应插件发来的 ext_ready / tabs_update。
type tabsMessage struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Tabs    []Tab  `json:"tabs"`
}

// bridgeResponse 对应插件对某次命令的 result/error 响应。
// Result 用 RawMessage 保留原始 JSON，等 command() 知道目标类型后再反序列化。
type bridgeResponse struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  bridgeError     `json:"error,omitempty"`
}

type bridgeError struct {
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
