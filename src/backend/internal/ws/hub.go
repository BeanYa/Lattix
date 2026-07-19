package ws

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

// writeTimeout 是单次写 WS 连接的超时；超时即判定连接死亡。
const writeTimeout = 10 * time.Second

// sendBuffer 是每连接发送队列长度。MVP 命令量极小，写满即视为慢连接并断开（重连后补发）。
const sendBuffer = 256

// Hub 是 agent 连接的注册表，同时实现 Requester（§2 WS 传输实现）。
type Hub struct {
	// Auth 校验 hello（由 dispatcher 实现，注入）。
	Auth Authenticator
	// OnConnect 在 hello 认证完成、连接登记后调用（用于触发离线命令补发，§2）。
	OnConnect func(serverID int64)
	// OnMessage 上抛认证后收到的所有业务信封（apply_result 等）。
	OnMessage func(serverID int64, env shared.Envelope)

	mu    sync.RWMutex
	conns map[int64]*agentConn
}

// NewHub 创建连接注册表。
func NewHub() *Hub {
	return &Hub{conns: make(map[int64]*agentConn)}
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
func (h *Hub) register(c *agentConn) {
	h.mu.Lock()
	if old, ok := h.conns[c.serverID]; ok && old != c {
		old.close()
	}
	h.conns[c.serverID] = c
	h.mu.Unlock()
}

// unregister 注销连接（仅当注册表里的仍是同一连接，避免误删重连后的新连接）。
func (h *Hub) unregister(c *agentConn) {
	h.mu.Lock()
	if h.conns[c.serverID] == c {
		delete(h.conns, c.serverID)
	}
	h.mu.Unlock()
}

// agentConn 是一条 agent WS 连接：读在 ServeHTTP 读循环，写在 writePump（gorilla 不允许并发写）。
type agentConn struct {
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

// writePump 串行消费发送队列直到出错或连接关闭。
func (c *agentConn) writePump() {
	for {
		select {
		case env := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteJSON(env); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
