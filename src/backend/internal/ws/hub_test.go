package ws

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"lattix/shared"
)

func TestRegisterReportsOnlyOfflineToOnlineTransition(t *testing.T) {
	h := NewHub()
	first := inertAgentConn(7)
	replacement := inertAgentConn(7)

	if becameOnline, accepted := h.register(first); !accepted || !becameOnline {
		t.Fatal("first connection should report offline-to-online transition")
	}
	if becameOnline, accepted := h.register(replacement); !accepted || becameOnline {
		t.Fatal("replacement connection should not report another online transition")
	}

	h.unregister(replacement)
	afterDisconnect := inertAgentConn(7)
	if becameOnline, accepted := h.register(afterDisconnect); !accepted || !becameOnline {
		t.Fatal("connection after disconnect should report offline-to-online transition")
	}
}

func TestBeginDrainRejectsSendAndSuppressesDisconnect(t *testing.T) {
	h := NewHub()
	var disconnected bool
	h.OnDisconnect = func(int64) { disconnected = true }
	conn := inertAgentConn(7)
	h.register(conn)
	h.BeginDrain()
	if err := h.Send(context.Background(), 7, shared.Envelope{}); !errors.Is(err, ErrDraining) {
		t.Fatalf("Send error = %v, want ErrDraining", err)
	}
	h.unregister(conn)
	if disconnected {
		t.Fatal("planned drain must suppress offline transition callback")
	}
}

type fixedLifecycle struct {
	snapshot shared.PanelLifecycleSnapshot
}

func (f fixedLifecycle) Snapshot() shared.PanelLifecycleSnapshot { return f.snapshot }

func TestStartupBlocksBusinessCommandsButAllowsControlMessages(t *testing.T) {
	h := NewHub()
	h.Lifecycle = fixedLifecycle{snapshot: shared.PanelLifecycleSnapshot{State: shared.PanelStateStartup}}
	conn := inertAgentConn(7)
	conn.send = make(chan shared.Envelope, 2)
	conn.done = make(chan struct{})
	h.register(conn)

	business := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeApplyNode}
	if err := h.Send(context.Background(), 7, business); !errors.Is(err, ErrPanelNotActive) {
		t.Fatalf("business send error = %v, want ErrPanelNotActive", err)
	}
	control := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeLifecycleChanged}
	if err := h.Send(context.Background(), 7, control); err != nil {
		t.Fatalf("control send error = %v", err)
	}
}

func TestSyncLifecycleWaitsForACKAndReportsTimeout(t *testing.T) {
	h := NewHub()
	acked := inertAgentConn(7)
	acked.send = make(chan shared.Envelope, 1)
	acked.done = make(chan struct{})
	acked.lifecycleAcks = make(map[string]chan struct{})
	missing := inertAgentConn(8)
	missing.send = make(chan shared.Envelope, 1)
	missing.done = make(chan struct{})
	missing.lifecycleAcks = make(map[string]chan struct{})
	h.register(acked)
	h.register(missing)
	go func() {
		envelope := <-acked.send
		acked.resolveLifecycleAck(envelope.RequestID)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got := h.SyncLifecycle(ctx, shared.PanelLifecycleSnapshot{State: shared.PanelStateUpdating})
	if !reflect.DeepEqual(got, []int64{8}) {
		t.Fatalf("missing ACKs = %v, want [8]", got)
	}
}

func TestForgetAgentRemovesConnectionSnapshot(t *testing.T) {
	h := NewHub()
	connection := inertAgentConn(7)
	h.register(connection)
	h.ForgetAgent(7, 1008, "deleted")
	if h.IsOnline(7) {
		t.Fatal("forgotten Agent must not remain online")
	}
	if _, exists := h.states[7]; exists {
		t.Fatal("forgotten Agent must not retain a connection snapshot")
	}
}

func TestNotifyConnectionEstablishedDistinguishesOnlineAndReconnect(t *testing.T) {
	h := NewHub()
	var online, reconnected []int64
	h.OnOnline = func(serverID int64) {
		online = append(online, serverID)
	}
	h.OnReconnect = func(serverID int64) {
		reconnected = append(reconnected, serverID)
	}

	h.notifyConnectionEstablished(7, true, false)
	h.notifyConnectionEstablished(7, false, false)

	if len(online) != 1 || online[0] != 7 {
		t.Fatalf("online callbacks = %v, want [7]", online)
	}
	if len(reconnected) != 1 || reconnected[0] != 7 {
		t.Fatalf("reconnect callbacks = %v, want [7]", reconnected)
	}
}

func TestReconnectHintSurvivesPanelProcessRestart(t *testing.T) {
	h := NewHub()
	var online, reconnect int
	h.OnOnline = func(int64) { online++ }
	h.OnReconnect = func(int64) { reconnect++ }
	h.notifyConnectionEstablished(7, true, true)
	if online != 0 || reconnect != 1 {
		t.Fatalf("online=%d reconnect=%d", online, reconnect)
	}
}

// inertAgentConn creates a connection whose close method is a no-op, so registry
// transition behavior can be tested without opening a real WebSocket.
func inertAgentConn(serverID int64) *agentConn {
	c := &agentConn{serverID: serverID}
	c.once.Do(func() {})
	return c
}

func TestConnectionStateTransitions(t *testing.T) {
	h := NewHub()
	// 正常握手序列：never_connected → connecting → online → offline
	h.setConnectionState(7, shared.ConnectionStateConnecting, "s", "initial")
	if got := h.ConnectionState(7, false).State; got != shared.ConnectionStateConnecting {
		t.Fatalf("state = %s", got)
	}
	h.setConnectionState(7, shared.ConnectionStateOnline, "s", "initial")
	if got := h.ConnectionState(7, false).State; got != shared.ConnectionStateOnline {
		t.Fatalf("state = %s", got)
	}
	h.setConnectionState(7, shared.ConnectionStateOffline, "", "initial")
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOffline {
		t.Fatalf("state = %s", got)
	}

	// 非法转换拒绝写入：offline → never_connected
	h.setConnectionState(7, shared.ConnectionStateNeverConnected, "", "")
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOffline {
		t.Fatalf("illegal transition applied, state = %s", got)
	}
	// auth_rejected 标记与恢复：auth_rejected → online（重新绑定后新握手）
	h.MarkAuthRejected(7)
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateAuthRejected {
		t.Fatalf("state = %s", got)
	}
	h.setConnectionState(7, shared.ConnectionStateOnline, "s2", "reconnect")
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOnline {
		t.Fatalf("state = %s", got)
	}
}

func TestSetDisconnectedIfIdleKeepsLiveConnectionState(t *testing.T) {
	h := NewHub()
	conn := inertAgentConn(7)
	h.register(conn)
	// 失败的新握手不得把在线连接覆盖为 offline。
	h.setDisconnectedIfIdle(7, shared.ConnectionStateOffline)
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOnline {
		t.Fatalf("live connection state overwritten: %s", got)
	}
	// 无连接时正常回写。
	h.unregister(conn)
	h.setDisconnectedIfIdle(7, shared.ConnectionStateOffline)
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOffline {
		t.Fatalf("state = %s", got)
	}
}
