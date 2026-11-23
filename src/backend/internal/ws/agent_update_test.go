package ws

import (
	"encoding/json"
	"testing"
	"time"

	"lattix/shared"
)

func TestAgentUpdateSurvivesPlannedDisconnectAndReconnect(t *testing.T) {
	h := NewHub()
	h.Auth = stubAuthenticator{}
	disconnected := make(chan int64, 4)
	h.OnDisconnect = func(id int64) { disconnected <- id }
	client := dialTestAgent(t, h)
	id := shared.NewMessageID()
	writeTestEnvelope(t, client, shared.Envelope{Kind: shared.KindEvent, Type: "agent.lifecycle.changed", RequestID: id, TraceID: id, Data: json.RawMessage(`{"state":"updating"}`)})
	client.Close()
	waitForCondition(t, func() bool { return !h.IsOnline(7) })
	if got := h.ConnectionState(7, true).State; got != "updating" {
		t.Fatalf("planned update disconnect state = %s, want updating", got)
	}
	select {
	case <-disconnected:
		t.Fatal("planned update triggered offline callback")
	default:
	}
	// Failed reconnect handshakes must not erase the upgrade window.
	h.setConnectionState(7, shared.ConnectionStateReconnecting, "new-session", shared.SessionKindReconnect)
	h.setDisconnectedIfIdle(7, shared.ConnectionStateOffline)
	if got := h.ConnectionState(7, true).State; got != "updating" {
		t.Fatalf("failed handshake erased update state: %s", got)
	}
	replacement := dialTestAgent(t, h)
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateOnline {
		t.Fatalf("reconnected state=%s", got)
	}
	replacement.Close()
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("real disconnect must still notify")
	}
}

func TestAgentUpdateFailureReturnsOnline(t *testing.T) {
	h := NewHub()
	h.Auth = stubAuthenticator{}
	client := dialTestAgent(t, h)
	id := shared.NewMessageID()
	send := func(state string) {
		writeTestEnvelope(t, client, shared.Envelope{Kind: shared.KindEvent, Type: "agent.lifecycle.changed", RequestID: id, TraceID: id, Data: mustJSON(map[string]string{"state": state})})
	}
	send("updating")
	waitForCondition(t, func() bool { return h.ConnectionState(7, true).State == "updating" })
	send("invalid")
	send("online")
	waitForCondition(t, func() bool { return h.ConnectionState(7, true).State == "online" })
}

func TestAgentUpdateTimeoutReportsActualConnection(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(map[bool]string{false: "offline", true: "online"}[connected], func(t *testing.T) {
			h := NewHub()
			h.updateTimeout = 40 * time.Millisecond
			h.Auth = stubAuthenticator{}
			disconnected := make(chan int64, 2)
			h.OnDisconnect = func(id int64) { disconnected <- id }
			client := dialTestAgent(t, h)
			id := shared.NewMessageID()
			writeTestEnvelope(t, client, shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeAgentLifecycleChanged, RequestID: id, TraceID: id, Data: json.RawMessage(`{"state":"updating"}`)})
			waitForCondition(t, func() bool { return h.IsUpdating(7) })
			if !connected {
				client.Close()
				select {
				case <-disconnected:
				case <-time.After(2 * time.Second):
					t.Fatal("expired update did not report offline")
				}
			}
			want := "offline"
			if connected {
				want = "online"
			}
			waitForCondition(t, func() bool { return h.ConnectionState(7, true).State == want })
			if h.IsUpdating(7) {
				t.Fatal("expired update still active")
			}
		})
	}
}

func TestAgentUpdateOldSessionCannotOverwriteNewConnection(t *testing.T) {
	h := NewHub()
	old := inertAgentConn(7)
	old.sessionID = "old"
	h.register(old)
	next := inertAgentConn(7)
	next.sessionID = "next"
	h.register(next)
	h.acceptAgentLifecycle(old, shared.Envelope{Data: json.RawMessage(`{"state":"updating"}`)})
	if h.IsUpdating(7) {
		t.Fatal("old session overwrote new state")
	}
}

func TestAgentUpdateCredentialRejectionOverridesMaintenance(t *testing.T) {
	h := NewHub()
	c := inertAgentConn(7)
	c.sessionID = "updating"
	h.register(c)
	h.acceptAgentLifecycle(c, shared.Envelope{Data: json.RawMessage(`{"state":"updating"}`)})
	h.MarkAuthRejected(7)
	h.unregister(c)
	if got := h.ConnectionState(7, true).State; got != shared.ConnectionStateAuthRejected {
		t.Fatalf("rejection hidden by disconnect: %s", got)
	}
	if h.IsUpdating(7) {
		t.Fatal("credential rejection must end maintenance")
	}
}

func TestAgentUpdateWaitsForNewSessionLifecycleConfirmation(t *testing.T) {
	h := NewHub()
	old := inertAgentConn(7)
	old.sessionID = "old"
	h.register(old)
	h.acceptAgentLifecycle(old, shared.Envelope{Data: json.RawMessage(`{"state":"updating"}`)})
	original := h.ConnectionState(7, true)
	h.unregister(old)
	replacement := inertAgentConn(7)
	replacement.sessionID = "replacement"
	h.register(replacement)
	next := h.ConnectionState(7, true)
	if next.State != "updating" || next.SessionID != "replacement" {
		t.Fatalf("reconnect lost active upgrade: %+v", next)
	}
	if !next.updateDeadline.Equal(original.updateDeadline) {
		t.Fatal("reconnect must not extend maintenance deadline")
	}
	h.acceptAgentLifecycle(replacement, shared.Envelope{Data: json.RawMessage(`{"state":"online"}`)})
	h.expireAgentUpdate(7, original)
	if got := h.ConnectionState(7, true).State; got != "online" {
		t.Fatalf("new process confirmation failed: %s", got)
	}
}
