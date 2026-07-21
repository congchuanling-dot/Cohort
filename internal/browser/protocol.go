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
	// Type 是插件消息类型，用于决定后续反序列化结构。
	Type string `json:"type"`
	// ID 是命令响应对应的请求标识。
	ID string `json:"id,omitempty"`
}

// tabsMessage 对应插件发来的 ext_ready / tabs_update。
type tabsMessage struct {
	// Type 是插件消息类型，通常是 ext_ready 或 tabs_update。
	Type string `json:"type"`
	// Name 是插件名称，用于 ready 消息展示和排查。
	Name string `json:"name,omitempty"`
	// Version 是插件版本号，用于协议兼容排查。
	Version string `json:"version,omitempty"`
	// Tabs 是插件当前可见标签页快照。
	Tabs []Tab `json:"tabs"`
}

// bridgeResponse 对应插件对某次命令的 result/error 响应。
// Result 用 RawMessage 保留原始 JSON，等 command() 知道目标类型后再反序列化。
type bridgeResponse struct {
	// Type 是响应类型，result 表示成功，error 表示插件侧失败。
	Type string `json:"type"`
	// ID 是响应对应的请求标识。
	ID string `json:"id"`
	// Result 是成功响应的原始 JSON 结果。
	Result json.RawMessage `json:"result,omitempty"`
	// Error 是失败响应的错误详情。
	Error bridgeError `json:"error,omitempty"`
}

type bridgeError struct {
	// Message 是插件侧返回的错误摘要。
	Message string `json:"message"`
	// Stack 是插件侧错误堆栈，存在时用于调试。
	Stack   string `json:"stack,omitempty"`
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
