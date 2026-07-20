package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultCommandTimeout = 45 * time.Second

	messageTypeReady      = "ext_ready"
	messageTypeTabsUpdate = "tabs_update"
	messageTypeResult     = "result"
	messageTypeError      = "error"
	messageTypePing       = "ping"
)

// Bridge 是 Cohert Go 进程里的浏览器桥服务。
//
// 它监听本机 WebSocket，等待 Chrome 插件主动连接。工具层调用 Tabs/Open/Scan 时，
// Bridge 会把命令发给插件，再根据 request id 等待对应响应。
type Bridge struct {
	addr string
	path string

	server *http.Server

	mu      sync.RWMutex
	conn    *websocket.Conn
	tabs    []Tab
	pending map[string]chan bridgeResponse

	writeMu sync.Mutex
}

// NewBridge 创建浏览器桥实例。调用 Start 后才会真正监听端口。
func NewBridge(addr string, path string) *Bridge {
	if addr == "" {
		addr = DefaultListenAddr
	}
	if path == "" {
		path = DefaultPath
	}
	return &Bridge{
		addr:    addr,
		path:    path,
		pending: map[string]chan bridgeResponse{},
	}
}

// Start 启动 WebSocket server。
//
// 这里先 net.Listen，再异步 Serve，是为了在启动阶段就能发现端口占用错误；
// 如果直接 go ListenAndServe，错误只会出现在 goroutine 里，工具层很难知道桥没起来。
func (b *Bridge) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(b.path, b.handleWebSocket)
	b.server = &http.Server{Addr: b.addr, Handler: mux}

	ln, err := net.Listen("tcp", b.addr)
	if err != nil {
		return err
	}
	b.addr = ln.Addr().String()
	go func() {
		err := b.server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.closeConnection()
		}
	}()
	return nil
}

// Addr 返回实际监听地址。测试里传 127.0.0.1:0 时，系统会分配随机端口。
func (b *Bridge) Addr() string {
	return b.addr
}

// Close 关闭桥服务。当前 CLI 退出时进程会结束，但测试里需要显式释放端口。
func (b *Bridge) Close(ctx context.Context) error {
	b.closeConnection()
	if b.server == nil {
		return nil
	}
	return b.server.Shutdown(ctx)
}

// Connected 表示当前是否已有 Chrome 插件连接。
func (b *Bridge) Connected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.conn != nil
}

// CachedTabs 返回最近一次插件上报的 tab 快照。
func (b *Bridge) CachedTabs() []Tab {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneTabs(b.tabs)
}

func (b *Bridge) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// 只允许本机插件连接。Chrome 扩展发起 WebSocket 时 Origin 可能是 chrome-extension://...
			// 这里依赖监听地址 127.0.0.1 做第一层边界，不按 Origin 拦截开发态插件。
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			return err == nil && (host == "127.0.0.1" || host == "::1")
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	b.replaceConnection(conn)
	b.readLoop(conn)
}

func (b *Bridge) replaceConnection(conn *websocket.Conn) {
	b.mu.Lock()
	old := b.conn
	b.conn = conn
	b.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (b *Bridge) readLoop(conn *websocket.Conn) {
	defer func() {
		b.mu.Lock()
		if b.conn == conn {
			b.conn = nil
		}
		b.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		b.handleIncoming(raw)
	}
}

func (b *Bridge) handleIncoming(raw []byte) {
	var envelope bridgeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}

	switch envelope.Type {
	case messageTypeReady, messageTypeTabsUpdate:
		var msg tabsMessage
		if err := json.Unmarshal(raw, &msg); err == nil {
			b.setTabs(msg.Tabs)
		}
	case messageTypeResult, messageTypeError:
		b.resolvePending(raw)
	case messageTypePing:
		// 插件 keepalive，无需处理。
	}
}

func (b *Bridge) setTabs(tabs []Tab) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tabs = cloneTabs(tabs)
}

func (b *Bridge) resolvePending(raw []byte) {
	var resp bridgeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return
	}
	if resp.ID == "" {
		return
	}

	b.mu.Lock()
	ch := b.pending[resp.ID]
	if ch != nil {
		delete(b.pending, resp.ID)
	}
	b.mu.Unlock()

	if ch != nil {
		ch <- resp
		close(ch)
	}
}

// Tabs 主动向插件请求最新标签页。
// 如果插件暂时没连接，但 Go 侧有缓存，就返回缓存；这样 popup 刚断线时仍能看到最近状态。
func (b *Bridge) Tabs(ctx context.Context) ([]Tab, error) {
	var result struct {
		Status string `json:"status"`
		Tabs   []Tab  `json:"tabs"`
	}
	if err := b.command(ctx, map[string]any{"command": "tabs"}, &result); err != nil {
		cached := b.CachedTabs()
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	b.setTabs(result.Tabs)
	return result.Tabs, nil
}

// Open 打开新标签页或导航指定标签页。
func (b *Bridge) Open(ctx context.Context, url string, tabID string, active bool) (OpenResult, error) {
	var result OpenResult
	err := b.command(ctx, map[string]any{
		"command": "open",
		"url":     url,
		"tab_id":  tabID,
		"active":  active,
	}, &result)
	return result, err
}

// Scan 读取页面标题、URL 和正文文本。
func (b *Bridge) Scan(ctx context.Context, tabID string, maxChars int) (PageSnapshot, error) {
	var result PageSnapshot
	err := b.command(ctx, map[string]any{
		"command":   "scan",
		"tab_id":    tabID,
		"max_chars": maxChars,
	}, &result)
	return result, err
}

// ExecuteJS 在指定或当前活动 tab 的页面上下文中执行 JavaScript。
func (b *Bridge) ExecuteJS(ctx context.Context, tabID string, script string, noMonitor bool, maxReturnChars int) (ExecuteJSResult, error) {
	var raw struct {
		Status    string `json:"status"`
		TabID     string `json:"tab_id"`
		Return    string `json:"return"`
		Truncated bool   `json:"truncated"`
		Diff      string `json:"diff"`
		Error     any    `json:"error"`
	}
	err := b.command(ctx, map[string]any{
		"command":          "execute_js",
		"tab_id":           tabID,
		"script":           script,
		"no_monitor":       noMonitor,
		"max_return_chars": maxReturnChars,
	}, &raw)
	if err != nil {
		return ExecuteJSResult{}, err
	}
	return ExecuteJSResult{
		Status:    raw.Status,
		TabID:     raw.TabID,
		JSReturn:  raw.Return,
		NewTabs:   []Tab{},
		Truncated: raw.Truncated,
		Diff:      raw.Diff,
		Error:     raw.Error,
	}, nil
}

// CDP 向插件透传一条 Chrome Debugger Protocol 命令。
// 这一层只负责协议转发，不理解具体 method 语义；点击、输入等高频动作由更上层工具封装。
func (b *Bridge) CDP(ctx context.Context, tabID string, method string, params map[string]any, noMonitor bool) (CDPResult, error) {
	var result CDPResult
	err := b.command(ctx, map[string]any{
		"command":    "cdp",
		"tab_id":     tabID,
		"method":     method,
		"params":     params,
		"no_monitor": noMonitor,
	}, &result)
	return result, err
}

// Click 在指定 viewport 坐标执行一次真实鼠标左键点击。
// 插件侧会在一次 debugger attach 生命周期内连续发送 mouseMoved/mousePressed/mouseReleased。
func (b *Bridge) Click(ctx context.Context, tabID string, x float64, y float64, noMonitor bool) (ClickResult, error) {
	var result ClickResult
	err := b.command(ctx, map[string]any{
		"command":    "click",
		"tab_id":     tabID,
		"x":          x,
		"y":          y,
		"no_monitor": noMonitor,
	}, &result)
	return result, err
}

// Type 向当前已聚焦的页面元素输入文本。
// 聚焦动作由 browser_click 或 browser_type_element 负责，这里只处理键盘输入。
func (b *Bridge) Type(ctx context.Context, tabID string, text string, clear bool, noMonitor bool) (TypeResult, error) {
	var result TypeResult
	err := b.command(ctx, map[string]any{
		"command":    "type",
		"tab_id":     tabID,
		"text":       text,
		"clear":      clear,
		"no_monitor": noMonitor,
	}, &result)
	return result, err
}

// PressKey 在当前焦点或页面上发送一个真实键盘按键或组合键。
// 组合键用高层字符串表达，例如 Cmd+Enter、Ctrl+Enter、Meta+A。
func (b *Bridge) PressKey(ctx context.Context, tabID string, key string, noMonitor bool) (PressKeyResult, error) {
	var result PressKeyResult
	err := b.command(ctx, map[string]any{
		"command":    "press_key",
		"tab_id":     tabID,
		"key":        key,
		"no_monitor": noMonitor,
	}, &result)
	return result, err
}

// Snapshot 返回当前页面的可交互元素摘要，帮助模型少写低层 DOM 探测脚本。
func (b *Bridge) Snapshot(ctx context.Context, tabID string, maxElements int) (InteractiveSnapshot, error) {
	var result InteractiveSnapshot
	err := b.command(ctx, map[string]any{
		"command":      "snapshot",
		"tab_id":       tabID,
		"max_elements": maxElements,
	}, &result)
	return result, err
}

// Wait 在浏览器侧轮询等待页面达到指定状态。
// 与 Go 侧循环相比，放在插件侧可以直接读取 tab.status 和页面 DOM，避免多次 WebSocket 往返。
func (b *Bridge) Wait(ctx context.Context, tabID string, mode string, params map[string]any, timeoutMS int, intervalMS int) (WaitResult, error) {
	payload := map[string]any{
		"command":     "wait",
		"tab_id":      tabID,
		"mode":        mode,
		"timeout_ms":  timeoutMS,
		"interval_ms": intervalMS,
	}
	for key, value := range params {
		payload[key] = value
	}
	var result WaitResult
	err := b.command(ctx, payload, &result)
	return result, err
}

func (b *Bridge) command(ctx context.Context, payload map[string]any, out any) error {
	conn := b.currentConnection()
	if conn == nil {
		return ErrNotConnected
	}

	id := newRequestID()
	payload["id"] = id
	respCh := make(chan bridgeResponse, 1)

	b.mu.Lock()
	b.pending[id] = respCh
	b.mu.Unlock()

	if err := b.writeJSON(conn, payload); err != nil {
		b.removePending(id)
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()

	select {
	case <-timeoutCtx.Done():
		b.removePending(id)
		return timeoutCtx.Err()
	case resp := <-respCh:
		if resp.Type == messageTypeError {
			return fmt.Errorf("browser extension error: %s", resp.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (b *Bridge) currentConnection() *websocket.Conn {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.conn
}

func (b *Bridge) writeJSON(conn *websocket.Conn, payload any) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return conn.WriteJSON(payload)
}

func (b *Bridge) removePending(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *Bridge) closeConnection() {
	b.mu.Lock()
	conn := b.conn
	b.conn = nil
	for id, ch := range b.pending {
		delete(b.pending, id)
		close(ch)
	}
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func cloneTabs(in []Tab) []Tab {
	if len(in) == 0 {
		return nil
	}
	out := make([]Tab, len(in))
	copy(out, in)
	return out
}
