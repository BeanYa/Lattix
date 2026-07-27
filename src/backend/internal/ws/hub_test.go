package ws

import (
	"testing"
)

func TestRegisterReportsOnlyOfflineToOnlineTransition(t *testing.T) {
	h := NewHub()
	first := inertAgentConn(7)
	replacement := inertAgentConn(7)

	if becameOnline := h.register(first); !becameOnline {
		t.Fatal("first connection should report offline-to-online transition")
	}
	if becameOnline := h.register(replacement); becameOnline {
		t.Fatal("replacement connection should not report another online transition")
	}

	h.unregister(replacement)
	afterDisconnect := inertAgentConn(7)
	if becameOnline := h.register(afterDisconnect); !becameOnline {
		t.Fatal("connection after disconnect should report offline-to-online transition")
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

	h.notifyConnectionEstablished(7, true)
	h.notifyConnectionEstablished(7, false)

	if len(online) != 1 || online[0] != 7 {
		t.Fatalf("online callbacks = %v, want [7]", online)
	}
	if len(reconnected) != 1 || reconnected[0] != 7 {
		t.Fatalf("reconnect callbacks = %v, want [7]", reconnected)
	}
}

// inertAgentConn creates a connection whose close method is a no-op, so registry
// transition behavior can be tested without opening a real WebSocket.
func inertAgentConn(serverID int64) *agentConn {
	c := &agentConn{serverID: serverID}
	c.once.Do(func() {})
	return c
}
