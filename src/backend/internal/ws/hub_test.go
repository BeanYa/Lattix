package ws

import (
	"context"
	"errors"
	"testing"

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
