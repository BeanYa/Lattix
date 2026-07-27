package ws

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

// writeTimeout 是单次写 WS 连接的超时；超时即判定连接死亡。
const writeTimeout = 10 * time.Second

// 应用层心跳（§2）：pingInterval 为 panel 向 agent 发 WS ping 的周期；
// pongTimeout 为读超时——任一侧超过该时长无任何字节（含 pong）即判定连接死亡，
// 避免半开 TCP 假在线。遥测 60s 一帧 + ping 30s，90s 窗口内必有流量。
const (
	pingIntervalDefault = 30 * time.Second
	pongFactor          = 3 // pongTimeout = pingInterval * pongFactor
)

// sendBuffer 是每连接发送队列长度。MVP 命令量极小，写满即视为慢连接并断开（重连后补发）。
const sendBuffer = 256

// Hub 是 agent 连接的注册表，同时实现 Requester（§2 WS 传输实现）。
type Hub struct {
	// Auth 校验 hello（由 dispatcher 实现，注入）。
	Auth Authenticator
	// OnUpgrade 在 HTTP 成功升级为 WebSocket 后立即调用，用于请求日志记录 101 握手。
	OnUpgrade func(r *http.Request)
	// OnConnect 在 hello 认证完成、连接登记后调用（用于触发离线命令补发，§2）。
	OnConnect func(serverID int64)
	// OnOnline 在服务器从无连接变为有连接时调用。已有连接被新连接顶替时不触发，
	// 与 OnDisconnect 共同表示真实的 offline↔online 状态跃迁。
	OnOnline func(serverID int64)
	// OnReconnect 在服务器已有连接被新的认证连接替换时调用。此时服务器始终在线，
	// 单独留痕而不伪造 offline→online 状态跃迁。
	OnReconnect func(serverID int64)
	// OnMessage 上抛认证后收到的所有业务信封（apply_result 等）。
	OnMessage func(serverID int64, env shared.Envelope)
	// OnDisconnect 在 online→offline 跃迁时调用（连接从注册表实际移除；
	// 被新连接顶替的旧连接注销不触发，hello 重连不重复报，§19 告警挂点）。
	OnDisconnect func(serverID int64)

	pingInterval time.Duration
	pongTimeout  time.Duration

	mu    sync.RWMutex
	conns map[int64]*agentConn
}

// NewHub 创建连接注册表。LATTIX_WS_PING_INTERVAL（Go duration）可覆盖心跳周期
// （pong 超时 = 3 倍 ping 周期），供 dev/e2e 加速离线判定（参照 LATTIX_EXPIRY_SWEEP_INTERVAL 先例）。
func NewHub() *Hub {
	ping := pingIntervalDefault
	if v := os.Getenv("LATTIX_WS_PING_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("LATTIX_WS_PING_INTERVAL 无效: %v", err)
		}
		ping = d
	}
	return &Hub{conns: make(map[int64]*agentConn), pingInterval: ping, pongTimeout: ping * pongFactor}
}

// IsOnline 实现 Requester。
func (h *Hub) IsOnline(serverID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[serverID]
	return ok
}

// Send 实现 Requester：投递到连接的发送队列（由 writePump 串行写出）。
func (h *Hub) Send(_ context.Context, serverID int64, env shared.Envelope) error {
	h.mu.RLock()
	c, ok := h.conns[serverID]
	h.mu.RUnlock()
	if !ok {
		return ErrOffline
	}
	select {
	case c.send <- env:
		return nil
	case <-c.done:
		return ErrOffline
	default:
		// 发送队列满：慢连接，断开（ queued 命令会在重连后补发）。
		log.Printf("ws: server %d send buffer full, closing connection", serverID)
		c.close()
		return ErrOffline
	}
}

// register 登记连接；同一服务器已有旧连接时踢掉旧的（重连场景）。
// 返回值表示服务器是否发生 offline→online 跃迁。
func (h *Hub) register(c *agentConn) bool {
	h.mu.Lock()
	old, wasOnline := h.conns[c.serverID]
	if wasOnline && old != c {
		old.close()
	}
	h.conns[c.serverID] = c
	h.mu.Unlock()
	return !wasOnline
}

func (h *Hub) notifyConnectionEstablished(serverID int64, becameOnline bool) {
	if becameOnline {
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
	}
	h.mu.Unlock()
	if transition && h.OnDisconnect != nil {
		h.OnDisconnect(c.serverID)
	}
}

// agentConn 是一条 agent WS 连接：读在 ServeHTTP 读循环，写在 writePump（gorilla 不允许并发写）。
type agentConn struct {
	hub      *Hub
	serverID int64
	ws       *websocket.Conn
	send     chan shared.Envelope
	done     chan struct{}
	once     sync.Once
}

func (c *agentConn) close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

// writePump 串行消费发送队列直到出错或连接关闭；空闲时按周期发 WS ping（应用层心跳，§2）。
func (c *agentConn) writePump() {
	ticker := time.NewTicker(c.hub.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case env := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteJSON(env); err != nil {
				return
			}
		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
