package ws

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

// clientIP（§9）：直连取 RemoteAddr 的 host；受信回环代理取 XFF 首个 IP；
// 非回环对端不信任 XFF（防伪造）。
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"直连取 RemoteAddr host", "1.2.3.4:5678", "", "1.2.3.4"},
		// 代理追加语义（$proxy_add_x_forwarded_for）：最右侧是代理亲见的客户端地址，
		// 左侧首项可被客户端伪造，从右向左取第一个不可信地址（nettrust.ClientIP）。
		// 内网/容器网段属内建可信：内网中转代理会被跳过，取到真实公网客户端。
		{"回环代理跳过内网中转取真实客户端", "127.0.0.1:8080", "9.9.9.9, 10.0.0.1", "9.9.9.9"},
		{"回环代理 XFF 含空格", "127.0.0.1:8080", " 9.9.9.9 , 10.0.0.1", "9.9.9.9"},
		{"回环代理取最右侧非可信 IP", "127.0.0.1:8080", "10.0.0.1, 9.9.9.9", "9.9.9.9"},
		{"回环代理无 XFF 回退回环", "127.0.0.1:8080", "", "127.0.0.1"},
		{"IPv6 回环代理取 XFF", "[::1]:8080", "9.9.9.9", "9.9.9.9"},
		{"docker 桥接代理取 XFF", "172.18.0.3:8080", "9.9.9.9", "9.9.9.9"},
		{"非可信对端不信任 XFF", "1.2.3.4:5678", "9.9.9.9", "1.2.3.4"},
		{"无端口的 RemoteAddr", "1.2.3.4", "", "1.2.3.4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: c.remoteAddr, Header: http.Header{}}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := clientIP(r); got != c.want {
				t.Errorf("clientIP(%q, xff=%q) = %q, want %q", c.remoteAddr, c.xff, got, c.want)
			}
		})
	}
}

func TestHandshakeAuthenticationErrorIsStructuredRPCResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeHandshakeError(recorder, http.StatusForbidden, shared.CodeAuthInvalidCredentials, "authentication failed")
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get(protocolHeader); got != protocolVersion {
		t.Fatalf("protocol header = %q", got)
	}
	var envelope shared.Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}
	if envelope.Kind != shared.KindResponse || envelope.Type != shared.TypeSessionOpen ||
		envelope.Code != shared.CodeAuthInvalidCredentials {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
}

// 慢 handler 不得阻塞读循环：第一条消息处理阻塞期间，读循环仍须读取后续消息
//（lifecycle ack 快速路径在读循环内完成）；handler 放行后按序消费积压消息。
func TestSlowHandlerDoesNotBlockReadLoop(t *testing.T) {
	h := NewHub()
	h.Auth = stubAuthenticator{}
	entered := make(chan string, 8)
	release := make(chan struct{})
	h.OnMessage = func(_ int64, env shared.Envelope) {
		entered <- env.RequestID
		<-release
	}
	client := dialTestAgent(t, h)

	h.mu.RLock()
	serverConn := h.conns[7]
	h.mu.RUnlock()
	if serverConn == nil {
		t.Fatal("connection not registered")
	}
	ackID := shared.NewMessageID()
	ack := serverConn.registerLifecycleAck(ackID)

	blockedID := shared.NewMessageID()
	writeTestEnvelope(t, client, shared.Envelope{
		Kind: shared.KindEvent, Type: shared.TypeTelemetry,
		RequestID: blockedID, TraceID: blockedID, Data: json.RawMessage(`{}`),
	})
	if got := receiveHandledID(t, entered); got != blockedID {
		t.Fatalf("first handled message = %s, want %s", got, blockedID)
	}

	// handler 阻塞期间，lifecycle ack 快速路径（读循环）仍须立即完成。
	writeTestEnvelope(t, client, shared.Envelope{
		Kind: shared.KindResponse, Type: shared.TypeLifecycleChanged,
		RequestID: ackID, TraceID: ackID, Code: shared.CodeOK, Data: json.RawMessage(`{}`),
	})
	select {
	case <-ack:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop blocked by slow handler: lifecycle ack was not resolved")
	}

	// 放行后 handler 按发送顺序消费积压消息。
	queuedID := shared.NewMessageID()
	writeTestEnvelope(t, client, shared.Envelope{
		Kind: shared.KindEvent, Type: shared.TypeTelemetry,
		RequestID: queuedID, TraceID: queuedID, Data: json.RawMessage(`{}`),
	})
	close(release)
	if got := receiveHandledID(t, entered); got != queuedID {
		t.Fatalf("second handled message = %s, want %s (order must be preserved)", got, queuedID)
	}
}

// 处理队列写满按慢连接策略断开（未处理消息由 agent 重连后补发，与发送队列策略一致）。
func TestInboundBufferFullClosesSlowConnection(t *testing.T) {
	h := NewHub()
	h.Auth = stubAuthenticator{}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.OnMessage = func(_ int64, _ shared.Envelope) { <-release }
	client := dialTestAgent(t, h)

	// handler 永久阻塞在第 1 条；最迟第 inboundBuffer+2 条写满队列触发断开。
	for i := 0; i < inboundBuffer+2; i++ {
		id := shared.NewMessageID()
		err := client.WriteJSON(shared.Envelope{
			Kind: shared.KindEvent, Type: shared.TypeTelemetry,
			RequestID: id, TraceID: id, Data: json.RawMessage(`{}`),
		})
		if err != nil {
			break // 服务端已断开
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, _, err := client.ReadMessage(); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return // 非超时错误：连接已被服务端按慢连接策略关闭
		}
	}
	t.Fatal("connection was not closed after inbound buffer filled up")
}

// stubAuthenticator 通过任意 token 并固定归属 server 7。
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(context.Context, string) (AuthResult, error) {
	return AuthResult{ServerID: 7}, nil
}

func (stubAuthenticator) OpenSession(context.Context, AuthResult, shared.SessionOpenPayload, string) (OpenSessionResult, error) {
	return OpenSessionResult{}, nil
}

func (stubAuthenticator) CommitCredential(context.Context, int64, string) error { return nil }

// dialTestAgent 建立测试 WS 连接并完成 session.open/session.ready 握手，
// 待服务端登记完成后返回客户端连接。
func dialTestAgent(t *testing.T, h *Hub) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{"Authorization": []string{"Bearer test-token"}}
	client, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	openID := shared.NewMessageID()
	writeTestEnvelope(t, client, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeSessionOpen,
		RequestID: openID, TraceID: openID,
		Data: mustJSON(shared.SessionOpenPayload{ProtocolVersion: 1}),
	})
	var openResponse shared.Envelope
	readTestEnvelope(t, client, &openResponse)
	if openResponse.Code != shared.CodeOK {
		t.Fatalf("session.open code = %s", openResponse.Code)
	}
	var result shared.SessionOpenResult
	if err := json.Unmarshal(openResponse.Data, &result); err != nil {
		t.Fatalf("session.open result: %v", err)
	}

	readyID := shared.NewMessageID()
	writeTestEnvelope(t, client, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeSessionReady,
		RequestID: readyID, TraceID: readyID,
		Data: mustJSON(shared.SessionReadyPayload{SessionID: result.SessionID}),
	})
	var readyResponse shared.Envelope
	readTestEnvelope(t, client, &readyResponse)
	if readyResponse.Code != shared.CodeOK {
		t.Fatalf("session.ready code = %s", readyResponse.Code)
	}
	waitForCondition(t, func() bool { return h.IsOnline(7) })
	return client
}

func writeTestEnvelope(t *testing.T, conn *websocket.Conn, env shared.Envelope) {
	t.Helper()
	if err := conn.WriteJSON(env); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

func readTestEnvelope(t *testing.T, conn *websocket.Conn, env *shared.Envelope) {
	t.Helper()
	if err := conn.ReadJSON(env); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
}

func receiveHandledID(t *testing.T, handled <-chan string) string {
	t.Helper()
	select {
	case id := <-handled:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handled message")
		return ""
	}
}

func waitForCondition(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
