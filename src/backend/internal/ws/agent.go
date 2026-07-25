package ws

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

// helloTimeout 是等待首帧 hello 的超时。
const helloTimeout = 10 * time.Second

var upgrader = websocket.Upgrader{
	// MVP 运行于本地/受信网络（§12）。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServeHTTP 处理 GET /api/agent/ws：升级 → hello 认证（§5）→ 登记连接 → 读循环。
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	// 首帧必须是 hello（带超时）：token（bootstrap 或长期）、agent/xray 版本与运行状态。
	conn.SetReadDeadline(time.Now().Add(helloTimeout))
	var hello shared.Envelope
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != shared.TypeHello {
		log.Printf("ws: first frame is not hello: %v", err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	// 应用层心跳（§2）：读超时 pongTimeout，任何消息到达（含 pong）即续期；ping 由 writePump 周期发出。
	conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
		return nil
	})

	var hp shared.HelloPayload
	if err := json.Unmarshal(hello.Payload, &hp); err != nil {
		log.Printf("ws: bad hello payload: %v", err)
		conn.Close()
		return
	}

	remoteHost := clientIP(r)
	serverID, result, err := h.Auth.AuthenticateHello(r.Context(), hp, remoteHost)
	if err != nil {
		log.Printf("ws: hello auth failed from %s: %v", r.RemoteAddr, err)
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(4001, "authentication failed"))
		conn.Close()
		return
	}

	c := &agentConn{
		hub:      h,
		serverID: serverID,
		ws:       conn,
		send:     make(chan shared.Envelope, sendBuffer),
		done:     make(chan struct{}),
	}
	go c.writePump()

	// 回 HelloResult（与请求同 type、同 id 即响应帧），必须先于任何补发命令到达 agent。
	c.send <- shared.Envelope{
		ID:      hello.ID,
		Type:    shared.TypeHello,
		Payload: mustJSON(result),
	}
	h.register(c)
	log.Printf("agent connected: server=%d addr=%s xray=%s", serverID, r.RemoteAddr, hp.XrayVersion)

	// 触发离线命令补发（§2）。
	if h.OnConnect != nil {
		h.OnConnect(serverID)
	}

	// 读循环：上抛业务信封直到断开。
	defer func() {
		h.unregister(c)
		c.close()
		log.Printf("agent disconnected: server=%d", serverID)
	}()
	for {
		var env shared.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		conn.SetReadDeadline(time.Now().Add(h.pongTimeout)) // 任何消息到达即续期
		if h.OnMessage != nil {
			h.OnMessage(serverID, env)
		}
	}
}

// 确保 Hub 满足 http.Handler。
var _ http.Handler = (*Hub)(nil)

// clientIP 取 WS 对端 IP（自动学习公网地址的依据，§9）。当对端是受信回环代理
// （panel 前置本机 nginx/caddy 反代）时，改用 X-Forwarded-For 的首个 IP；
// 非回环对端直连场景不信任该头，防止伪造。
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
				return strings.TrimSpace(first)
			}
		}
	}
	return host
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
