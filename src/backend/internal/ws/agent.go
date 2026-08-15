package ws

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"lattix/backend/internal/nettrust"
	"lattix/shared"
)

const (
	sessionOpenTimeout = 10 * time.Second
	protocolHeader     = "X-Lattix-Protocol"
	protocolVersion    = "1"
	maxMessageBytes    = 1 << 20
)

var upgrader = websocket.Upgrader{
	// MVP 运行于本地/受信网络（§12）。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServeHTTP authenticates the HTTP Upgrade, initializes one application
// session, then registers it for business RPC delivery after session.ready.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.wg.Add(1)
	defer h.wg.Done()
	if h.IsDraining() {
		writeHandshakeError(w, http.StatusServiceUnavailable, shared.CodeServiceUnavailable, "panel is restarting")
		return
	}
	panelState := h.lifecycleSnapshot()
	if panelState.State == shared.PanelStateFaulted {
		writeHandshakeError(w, http.StatusServiceUnavailable, shared.CodeServiceUnavailable, "panel is unavailable")
		return
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeHandshakeError(w, http.StatusForbidden, shared.CodeAuthInvalidCredentials, "authentication failed")
		return
	}
	auth, err := h.Auth.AuthenticateToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			writeHandshakeError(w, http.StatusForbidden, shared.CodeAuthInvalidCredentials, "authentication failed")
		} else {
			log.Printf("ws auth unavailable from %s: %v", r.RemoteAddr, err)
			writeHandshakeError(w, http.StatusServiceUnavailable, shared.CodeServiceUnavailable, "panel is unavailable")
		}
		return
	}
	sessionKind := shared.SessionKindInitial
	connectionState := shared.ConnectionStateConnecting
	if auth.Reconnect {
		sessionKind = shared.SessionKindReconnect
		connectionState = shared.ConnectionStateReconnecting
	}
	sessionID := shared.NewMessageID()
	h.setConnectionState(auth.ServerID, connectionState, sessionID, sessionKind)
	responseHeaders := http.Header{}
	responseHeaders.Set(protocolHeader, protocolVersion)
	conn, err := upgrader.Upgrade(w, r, responseHeaders)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		h.setDisconnectedIfIdle(auth.ServerID, disconnectedState(auth.Reconnect))
		return
	}
	conn.SetReadLimit(maxMessageBytes)
	if h.OnUpgrade != nil {
		h.OnUpgrade(r)
	}

	registered := false
	defer func() {
		if !registered {
			// 握手失败回写断开状态；若已有登记连接（旧连接/并发新连接）则不覆盖其 online 显示。
			h.setDisconnectedIfIdle(auth.ServerID, disconnectedState(auth.Reconnect))
		}
	}()

	// The first application frame opens the authenticated session.
	conn.SetReadDeadline(time.Now().Add(sessionOpenTimeout))
	var open shared.Envelope
	if err := readEnvelope(conn, &open); err != nil ||
		open.Kind != shared.KindRequest || open.Type != shared.TypeSessionOpen {
		log.Printf("ws: first frame is not session.open: %v", err)
		h.protocolError(auth.ServerID, open, "first frame must be agent.session.open")
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4002, "invalid protocol message"), time.Now().Add(writeTimeout))
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	// Agent 主动发送 Ping；Panel 原样 Pong，并以收到的控制帧续期读超时。
	conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
	conn.SetPingHandler(func(data string) error {
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout))
		if err != nil {
			return err
		}
		conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
		return nil
	})

	var payload shared.SessionOpenPayload
	if err := strictUnmarshal(open.Data, &payload); err != nil || payload.ProtocolVersion != 1 {
		log.Printf("ws: bad session.open data: %v", err)
		h.protocolError(auth.ServerID, open, "invalid agent.session.open data")
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4002, "invalid session data"), time.Now().Add(writeTimeout))
		conn.Close()
		return
	}
	remoteHost := clientIP(r)
	openResult, err := h.Auth.OpenSession(r.Context(), auth, payload, remoteHost)
	if err != nil {
		log.Printf("ws: open session failed from %s: %v", r.RemoteAddr, err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "session unavailable"))
		conn.Close()
		return
	}

	c := &agentConn{
		hub:           h,
		serverID:      auth.ServerID,
		sessionID:     sessionID,
		sessionKind:   sessionKind,
		ws:            conn,
		send:          make(chan shared.Envelope, sendBuffer),
		done:          make(chan struct{}),
		lifecycleAcks: make(map[string]chan struct{}),
	}
	go c.writePump()

	result := shared.SessionOpenResult{
		ServerID: auth.ServerID, SessionID: sessionID, SessionKind: sessionKind,
		IssuedToken: openResult.IssuedToken, CredentialExchangeID: openResult.ExchangeID,
		PanelState: panelState,
	}
	c.send <- shared.Envelope{
		Kind:      shared.KindResponse,
		Type:      shared.TypeSessionOpen,
		RequestID: open.RequestID,
		TraceID:   open.TraceID,
		Code:      shared.CodeOK,
		Data:      mustJSON(result),
	}

	// Only credential.commit and session.ready are accepted before registration.
	for !registered {
		var env shared.Envelope
		if err := readEnvelope(conn, &env); err != nil {
			c.close()
			return
		}
		conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
		if env.Kind != shared.KindRequest {
			replyDirect(c, env, shared.CodeUnsupportedAction, "session is not ready", nil)
			continue
		}
		switch env.Type {
		case shared.TypeCredentialCommit:
			var commit shared.CredentialCommitPayload
			if err := strictUnmarshal(env.Data, &commit); err != nil || commit.ExchangeID == "" {
				replyDirect(c, env, shared.CodeInvalidArgument, "invalid credential commit", nil)
				continue
			}
			if err := h.Auth.CommitCredential(r.Context(), auth.ServerID, commit.ExchangeID); err != nil {
				replyDirect(c, env, shared.CodeConflict, err.Error(), nil)
				continue
			}
			replyDirect(c, env, shared.CodeOK, "", struct{}{})
		case shared.TypeSessionReady:
			var ready shared.SessionReadyPayload
			if err := strictUnmarshal(env.Data, &ready); err != nil || ready.SessionID != sessionID {
				replyDirect(c, env, shared.CodeInvalidArgument, "invalid session ready", nil)
				continue
			}
			current := h.lifecycleSnapshot()
			if ready.Lifecycle != current.Version() {
				replyDirect(c, env, shared.CodeConflict, "lifecycle changed", current)
				continue
			}
			replyDirect(c, env, shared.CodeOK, "", struct{}{})
			registered = true
		default:
			replyDirect(c, env, shared.CodeUnsupportedAction, "session is not ready", nil)
		}
	}

	becameOnline, accepted := h.register(c)
	if !accepted {
		c.closeWithCode(websocket.CloseServiceRestart, "service restart")
		return
	}
	log.Printf("agent connected: server=%d session=%s kind=%s addr=%s xray=%s",
		auth.ServerID, sessionID, sessionKind, r.RemoteAddr, payload.XrayVersion)

	// 触发离线命令补发（§2）。
	if h.OnConnect != nil {
		h.OnConnect(auth.ServerID)
	}
	h.notifyConnectionEstablished(auth.ServerID, becameOnline, auth.Reconnect)

	// 读循环：上抛业务信封直到断开。
	defer func() {
		h.unregister(c)
		c.close()
		if !h.IsDraining() {
			log.Printf("agent disconnected: server=%d", auth.ServerID)
		}
	}()
	for {
		var env shared.Envelope
		if err := readEnvelope(conn, &env); err != nil {
			log.Printf("ws: server %d invalid message: %v", auth.ServerID, err)
			h.protocolError(auth.ServerID, env, "invalid protocol message")
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(4002, "invalid protocol message"), time.Now().Add(writeTimeout))
			return
		}
		conn.SetReadDeadline(time.Now().Add(h.pongTimeout)) // 任何消息到达即续期
		if env.Kind == shared.KindResponse && env.Type == shared.TypeLifecycleChanged &&
			env.Code == shared.CodeOK && c.resolveLifecycleAck(env.RequestID) {
			continue
		}
		if h.OnMessage != nil {
			h.OnMessage(auth.ServerID, env)
		}
	}
}

func disconnectedState(reconnect bool) string {
	if reconnect {
		return shared.ConnectionStateOffline
	}
	return shared.ConnectionStateNeverConnected
}

func (h *Hub) lifecycleSnapshot() shared.PanelLifecycleSnapshot {
	if h.Lifecycle == nil {
		return shared.PanelLifecycleSnapshot{State: shared.PanelStateActive}
	}
	return h.Lifecycle.Snapshot()
}

func bearerToken(value string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	return token, token != ""
}

func writeHandshakeError(w http.ResponseWriter, status int, code, message string) {
	id := shared.NewMessageID()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set(protocolHeader, protocolVersion)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(shared.Envelope{
		Kind: shared.KindResponse, Type: shared.TypeSessionOpen,
		RequestID: id, TraceID: id, Code: code, Message: message,
		Data: json.RawMessage(`{}`),
	})
}

func replyDirect(c *agentConn, request shared.Envelope, code, message string, data any) {
	if data == nil {
		data = struct{}{}
	}
	c.send <- shared.Envelope{
		Kind: shared.KindResponse, Type: request.Type,
		RequestID: request.RequestID, TraceID: request.TraceID,
		Code: code, Message: message, Data: mustJSON(data),
	}
}

func (h *Hub) protocolError(serverID int64, env shared.Envelope, message string) {
	if h.OnProtocolError == nil {
		return
	}
	requestID := env.RequestID
	if !shared.ValidMessageID(requestID) {
		requestID = shared.NewMessageID()
	}
	traceID := env.TraceID
	if !shared.ValidMessageID(traceID) {
		traceID = requestID
	}
	h.OnProtocolError(serverID, requestID, traceID, env.Type, message)
}

func readEnvelope(conn *websocket.Conn, target *shared.Envelope) error {
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if messageType != websocket.TextMessage {
		return errors.New("message must be a JSON text frame")
	}
	if err := strictUnmarshal(data, target); err != nil {
		return err
	}
	return target.Validate()
}

func strictUnmarshal(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// 确保 Hub 满足 http.Handler。
var _ http.Handler = (*Hub)(nil)

// clientIP 取 WS 对端 IP（自动学习公网地址的依据，§9）。对端为可信反代
// （回环，或 trusted_proxies 配置网段）时解析 X-Forwarded-For 的真实客户端；
// 非可信对端直连场景不信任该头，防止伪造。判定统一在 nettrust。
func clientIP(r *http.Request) string {
	return nettrust.Default.ClientIP(r)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
