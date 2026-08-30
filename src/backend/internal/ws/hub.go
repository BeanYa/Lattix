package ws

import (
	"context"
	"log"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

// writeTimeout 是单次写 WS 连接的超时；超时即判定连接死亡。
const writeTimeout = 10 * time.Second

// Agent 每 30s 主动 Ping；Panel 90s 未收到任何字节即判定连接死亡。
const pongTimeoutDefault = 90 * time.Second

// sendBuffer 是每连接发送队列长度。MVP 命令量极小，写满即视为慢连接并断开（重连后补发）。
const sendBuffer = 256

// inboundBuffer 是每连接上行消息处理队列长度（读循环 → handlerPump），与发送队列
// 同量级：写满即视为慢处理并按慢连接策略断开，未处理消息由 agent 重连后补发。
const inboundBuffer = 256

// Hub 是 agent 连接的注册表，同时实现 AgentRequester（§2 WS 传输实现）。
type Hub struct {
	// Auth 校验 Upgrade 凭据并打开 session（由 dispatcher 实现，注入）。
	Auth      Authenticator
	Lifecycle LifecycleProvider
	// OnUpgrade 在 HTTP 成功升级为 WebSocket 后立即调用，用于请求日志记录 101 握手。
	OnUpgrade func(r *http.Request)
	// OnConnect 在 session.ready 完成、连接登记后调用（用于触发离线命令补发，§2）。
	OnConnect func(serverID int64)
	// OnOnline 在服务器从无连接变为有连接时调用。已有连接被新连接顶替时不触发，
	// 与 OnDisconnect 共同表示真实的 offline↔online 状态跃迁。
	OnOnline func(serverID int64)
	// OnReconnect 在服务器已有连接被新的认证连接替换时调用。此时服务器始终在线，
	// 单独留痕而不伪造 offline→online 状态跃迁。
	OnReconnect func(serverID int64)
	// OnMessage 上抛认证后收到的所有业务信封（apply_result 等）；
	// 由每连接的 handlerPump goroutine 顺序调用（连接内保序，连接之间可并发）。
	OnMessage func(serverID int64, env shared.Envelope)
	// OnProtocolError 只记录无正常业务响应的 WS 协议错误；遥测和 ping/pong 不调用。
	OnProtocolError func(serverID int64, requestID, traceID, rpcType, message string)
	// OnDisconnect 在 online→offline 跃迁时调用（连接从注册表实际移除；
	// 被新连接顶替的旧连接注销不触发，session 重连不重复报，§19 告警挂点）。
	OnDisconnect func(serverID int64)

	pongTimeout time.Duration

	mu       sync.RWMutex
	conns    map[int64]*agentConn
	states   map[int64]ConnectionSnapshot
	draining bool
	wg       sync.WaitGroup
}

func NewHub() *Hub {
	return &Hub{
		conns:       make(map[int64]*agentConn),
		states:      make(map[int64]ConnectionSnapshot),
		pongTimeout: pongTimeoutDefault,
	}
}

type LifecycleProvider interface {
	Snapshot() shared.PanelLifecycleSnapshot
}

type ConnectionSnapshot struct {
	State       string    `json:"state"`
	SessionID   string    `json:"session_id,omitempty"`
	SessionKind string    `json:"session_kind,omitempty"`
	ChangedAt   time.Time `json:"changed_at"`
}

// connectionTransitions 定义面板侧 Agent 连接状态的合法转换（设计文档 §2）。
// 同状态写入幂等；非法转换拒绝并记录日志（保持显示状态不被错误覆盖）。
// auth_rejected 由面板在 token 轮换时主动标记：轮换后 Agent 重试将得到明确
// HTTP 403 并停止自动重连，直到重新绑定（握手 403 本身无法从失效 token 归属服务器）。
var connectionTransitions = map[string]map[string]bool{
	shared.ConnectionStateNeverConnected: {
		shared.ConnectionStateConnecting:   true,
		shared.ConnectionStateOnline:       true,
		shared.ConnectionStateAuthRejected: true,
	},
	shared.ConnectionStateConnecting: {
		shared.ConnectionStateOnline:         true,
		shared.ConnectionStateOffline:        true,
		shared.ConnectionStateNeverConnected: true,
		shared.ConnectionStateAuthRejected:   true,
	},
	shared.ConnectionStateReconnecting: {
		shared.ConnectionStateOnline:       true,
		shared.ConnectionStateOffline:      true,
		shared.ConnectionStateAuthRejected: true,
	},
	shared.ConnectionStateOnline: {
		shared.ConnectionStateConnecting:   true,
		shared.ConnectionStateReconnecting: true,
		shared.ConnectionStateOnline:       true, // 新连接顶替旧连接
		shared.ConnectionStateOffline:      true,
		shared.ConnectionStateAuthRejected: true,
	},
	shared.ConnectionStateOffline: {
		shared.ConnectionStateConnecting:   true,
		shared.ConnectionStateReconnecting: true,
		shared.ConnectionStateOnline:       true,
		shared.ConnectionStateAuthRejected: true,
	},
	shared.ConnectionStateAuthRejected: {
		shared.ConnectionStateConnecting:   true, // 重新绑定后新握手
		shared.ConnectionStateReconnecting: true,
		shared.ConnectionStateOnline:       true,
		shared.ConnectionStateAuthRejected: true,
	},
}

func validConnectionTransition(from, to string) bool {
	if from == to {
		return true
	}
	targets, ok := connectionTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

func (h *Hub) ConnectionState(serverID int64, everConnected bool) ConnectionSnapshot {
	h.mu.RLock()
	state, ok := h.states[serverID]
	_, online := h.conns[serverID]
	h.mu.RUnlock()
	if ok {
		return state
	}
	value := shared.ConnectionStateNeverConnected
	if everConnected {
		value = shared.ConnectionStateOffline
	}
	if online {
		value = shared.ConnectionStateOnline
	}
	return ConnectionSnapshot{State: value}
}

func (h *Hub) setConnectionState(serverID int64, state, sessionID, sessionKind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.states[serverID]; ok && !validConnectionTransition(prev.State, state) {
		log.Printf("ws: server %d: illegal connection state transition %s → %s (ignored)", serverID, prev.State, state)
		return
	}
	h.states[serverID] = ConnectionSnapshot{
		State: state, SessionID: sessionID, SessionKind: sessionKind, ChangedAt: time.Now().UTC(),
	}
}

// setDisconnectedIfIdle 仅当服务器当前没有已登记连接时才回写断开状态：
// 失败的新握手不得把仍在线连接的显示状态错误覆盖为 offline/never_connected。
func (h *Hub) setDisconnectedIfIdle(serverID int64, state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, online := h.conns[serverID]; online {
		return
	}
	if prev, ok := h.states[serverID]; ok && !validConnectionTransition(prev.State, state) {
		log.Printf("ws: server %d: illegal connection state transition %s → %s (ignored)", serverID, prev.State, state)
		return
	}
	h.states[serverID] = ConnectionSnapshot{State: state, ChangedAt: time.Now().UTC()}
}

// MarkAuthRejected 将服务器连接状态置为 auth_rejected（token 轮换等场景，
// §graceful-shutdown-agent-settings-design §6）：旧凭证重试将得到明确 403，
// Agent 停止自动重连，直到管理员重新绑定。
func (h *Hub) MarkAuthRejected(serverID int64) {
	h.setConnectionState(serverID, shared.ConnectionStateAuthRejected, "", "")
}

// IsOnline 实现 Requester。
func (h *Hub) IsOnline(serverID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[serverID]
	return ok
}

// Send 实现 Requester：投递到连接的发送队列（由 writePump 串行写出）。
func (h *Hub) Send(ctx context.Context, serverID int64, env shared.Envelope) error {
	h.mu.RLock()
	c, ok := h.conns[serverID]
	draining := h.draining
	h.mu.RUnlock()
	if draining {
		return ErrDraining
	}
	if !ok {
		return ErrOffline
	}
	select {
	case c.send <- env:
		return nil
	case <-c.done:
		return ErrOffline
	case <-ctx.Done():
		return ctx.Err()
	default:
		// 发送队列满：慢连接，断开（ queued 命令会在重连后补发）。
		log.Printf("ws: server %d send buffer full, closing connection", serverID)
		c.close()
		return ErrOffline
	}
}

// BeginDrain rejects new work and closes all persistent Agent WebSockets with
// RFC 6455 code 1012 so Agents can use their fast service-restart retry path.
func (h *Hub) BeginDrain() {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return
	}
	h.draining = true
	conns := make([]*agentConn, 0, len(h.conns))
	for _, conn := range h.conns {
		conns = append(conns, conn)
	}
	h.mu.Unlock()
	for _, conn := range conns {
		conn.closeWithCode(websocket.CloseServiceRestart, "service restart")
	}
}

func (h *Hub) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) IsDraining() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.draining
}

// CloseAgent revokes one live connection, for example after token rotation.
func (h *Hub) CloseAgent(serverID int64, code int, reason string) {
	h.mu.RLock()
	conn := h.conns[serverID]
	h.mu.RUnlock()
	if conn != nil {
		conn.closeWithCode(code, reason)
	}
}

// ForgetAgent closes any live session and removes all in-memory connection
// state for a server whose authoritative database record has been deleted.
func (h *Hub) ForgetAgent(serverID int64, code int, reason string) {
	h.mu.Lock()
	connection := h.conns[serverID]
	delete(h.conns, serverID)
	delete(h.states, serverID)
	h.mu.Unlock()
	if connection != nil {
		connection.closeWithCode(code, reason)
	}
}

func (h *Hub) CloseAllAgents(code int, reason string) {
	h.mu.RLock()
	connections := make([]*agentConn, 0, len(h.conns))
	for _, connection := range h.conns {
		connections = append(connections, connection)
	}
	h.mu.RUnlock()
	for _, connection := range connections {
		connection.closeWithCode(code, reason)
	}
}

// register 登记连接；同一服务器已有旧连接时踢掉旧的（重连场景）。
// 返回值表示服务器是否发生 offline→online 跃迁。
func (h *Hub) register(c *agentConn) (becameOnline, accepted bool) {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return false, false
	}
	old, wasOnline := h.conns[c.serverID]
	if wasOnline && old != c {
		old.close()
	}
	h.conns[c.serverID] = c
	h.states[c.serverID] = ConnectionSnapshot{
		State: shared.ConnectionStateOnline, SessionID: c.sessionID,
		SessionKind: c.sessionKind, ChangedAt: time.Now().UTC(),
	}
	h.mu.Unlock()
	return !wasOnline, true
}

func (h *Hub) notifyConnectionEstablished(serverID int64, becameOnline, reconnectHint bool) {
	if becameOnline && !reconnectHint {
		if h.OnOnline != nil {
			h.OnOnline(serverID)
		}
		return
	}
	if h.OnReconnect != nil {
		h.OnReconnect(serverID)
	}
}

// unregister 注销连接（仅当注册表里的仍是同一连接，避免误删重连后的新连接）。
// 实际移除即 online→offline 跃迁（被新连接顶替的旧连接注销不算），触发 OnDisconnect（§19）。
func (h *Hub) unregister(c *agentConn) {
	h.mu.Lock()
	transition := h.conns[c.serverID] == c
	if transition {
		delete(h.conns, c.serverID)
		h.states[c.serverID] = ConnectionSnapshot{
			State: shared.ConnectionStateOffline, SessionKind: c.sessionKind,
			ChangedAt: time.Now().UTC(),
		}
	}
	h.mu.Unlock()
	if transition && !h.IsDraining() && h.OnDisconnect != nil {
		h.OnDisconnect(c.serverID)
	}
}

// agentConn 是一条 agent WS 连接：读在 ServeHTTP 读循环，业务处理在 handlerPump，写在 writePump（gorilla 不允许并发写）。
type agentConn struct {
	hub           *Hub
	serverID      int64
	sessionID     string
	sessionKind   string
	ws            *websocket.Conn
	send          chan shared.Envelope
	inbound       chan shared.Envelope // 上行消息处理队列：读循环投递，handlerPump 顺序消费
	done          chan struct{}
	once          sync.Once
	ackMu         sync.Mutex
	lifecycleAcks map[string]chan struct{}
}

func (c *agentConn) registerLifecycleAck(requestID string) <-chan struct{} {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	ch := make(chan struct{})
	c.lifecycleAcks[requestID] = ch
	return ch
}

func (c *agentConn) resolveLifecycleAck(requestID string) bool {
	c.ackMu.Lock()
	ch, ok := c.lifecycleAcks[requestID]
	if ok {
		delete(c.lifecycleAcks, requestID)
		close(ch)
	}
	c.ackMu.Unlock()
	return ok
}

func (c *agentConn) removeLifecycleAck(requestID string) {
	c.ackMu.Lock()
	delete(c.lifecycleAcks, requestID)
	c.ackMu.Unlock()
}

// SyncLifecycle sends a lifecycle request to every currently online Agent and
// waits until all acknowledgements arrive or the context expires.
func (h *Hub) SyncLifecycle(ctx context.Context, snapshot shared.PanelLifecycleSnapshot) []int64 {
	h.mu.RLock()
	conns := make([]*agentConn, 0, len(h.conns))
	for _, conn := range h.conns {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()
	type pending struct {
		serverID  int64
		requestID string
		conn      *agentConn
		ack       <-chan struct{}
	}
	waits := make([]pending, 0, len(conns))
	for _, conn := range conns {
		id := shared.NewMessageID()
		ack := conn.registerLifecycleAck(id)
		env := shared.Envelope{
			Kind: shared.KindRequest, Type: shared.TypeLifecycleChanged,
			RequestID: id, TraceID: id,
			Data: mustJSON(shared.LifecycleChangedPayload{PanelState: snapshot}),
		}
		if err := h.Send(ctx, conn.serverID, env); err != nil {
			conn.removeLifecycleAck(id)
			continue
		}
		waits = append(waits, pending{conn.serverID, id, conn, ack})
	}
	type result struct {
		serverID int64
		missing  bool
	}
	results := make(chan result, len(waits))
	for _, wait := range waits {
		go func(wait pending) {
			select {
			case <-wait.ack:
				results <- result{serverID: wait.serverID}
			case <-wait.conn.done:
				wait.conn.removeLifecycleAck(wait.requestID)
				results <- result{serverID: wait.serverID}
			case <-ctx.Done():
				wait.conn.removeLifecycleAck(wait.requestID)
				results <- result{serverID: wait.serverID, missing: true}
			}
		}(wait)
	}
	missing := make([]int64, 0, len(waits))
	for range waits {
		result := <-results
		if result.missing {
			missing = append(missing, result.serverID)
		}
	}
	slices.Sort(missing)
	return missing
}

func (c *agentConn) close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

func (c *agentConn) closeWithCode(code int, reason string) {
	c.once.Do(func() {
		close(c.done)
		if c.ws != nil {
			_ = c.ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(code, reason), time.Now().Add(writeTimeout))
			_ = c.ws.Close()
		}
	})
}

// writePump 串行消费业务发送队列；心跳由 Agent 主动 Ping，不在此额外发请求。
func (c *agentConn) writePump() {
	for {
		select {
		case env := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteJSON(env); err != nil {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}

// handlerPump 顺序消费上行消息处理队列（每连接一个 goroutine，保持消息顺序）：
// 回执落库等慢处理只拖慢本连接的处理队列，不阻塞读循环的读取与续期。
// done 关闭即退出（无泄漏）；队列残留消息随连接断开丢弃，由 agent 重连补发兜底。
func (c *agentConn) handlerPump() {
	for {
		select {
		case env := <-c.inbound:
			if c.hub.OnMessage != nil {
				c.hub.OnMessage(c.serverID, env)
			}
		case <-c.done:
			return
		}
	}
}
