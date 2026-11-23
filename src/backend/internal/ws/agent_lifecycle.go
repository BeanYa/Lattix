package ws

import (
	"log"
	"time"

	"lattix/shared"
)

// Lifecycle events are consumed on the read path, before an immediately
// following disconnect can race a queued business handler.
func (h *Hub) acceptAgentLifecycle(c *agentConn, env shared.Envelope) {
	var payload shared.AgentLifecyclePayload
	if err := strictUnmarshal(env.Data, &payload); err != nil ||
		(payload.State != shared.ConnectionStateUpdating && payload.State != shared.ConnectionStateOnline) {
		log.Printf("ws: server %d invalid agent lifecycle event", c.serverID)
		return
	}
	h.mu.Lock()
	if h.conns[c.serverID] != c {
		h.mu.Unlock()
		return
	}
	previous := h.states[c.serverID]
	if !validConnectionTransition(previous.State, payload.State) || previous.State == payload.State {
		h.mu.Unlock()
		return
	}
	snapshot := ConnectionSnapshot{State: payload.State, SessionID: c.sessionID, SessionKind: c.sessionKind, ChangedAt: time.Now().UTC()}
	if payload.State == shared.ConnectionStateUpdating {
		snapshot.updateDeadline = time.Now().Add(h.updateTimeout)
	}
	h.states[c.serverID] = snapshot
	h.mu.Unlock()
	if !snapshot.updateDeadline.IsZero() {
		time.AfterFunc(time.Until(snapshot.updateDeadline), func() { h.expireAgentUpdate(c.serverID, snapshot) })
	}
}

func (h *Hub) expireAgentUpdate(serverID int64, expected ConnectionSnapshot) {
	h.mu.Lock()
	current := h.states[serverID]
	if current.State != shared.ConnectionStateUpdating || current.SessionID != expected.SessionID ||
		!current.updateDeadline.Equal(expected.updateDeadline) {
		h.mu.Unlock()
		return
	}
	_, connected := h.conns[serverID]
	current.State = shared.ConnectionStateOffline
	if connected {
		current.State = shared.ConnectionStateOnline
	}
	current.ChangedAt = time.Now().UTC()
	current.updateDeadline = time.Time{}
	h.states[serverID] = current
	h.mu.Unlock()
	if !connected && !h.IsDraining() && h.OnDisconnect != nil {
		h.OnDisconnect(serverID)
	}
}

// IsUpdating is maintenance observation only; Send and IsOnline continue to
// report real transport availability, so business commands are never lost.
func (h *Hub) IsUpdating(serverID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	snapshot := h.states[serverID]
	return snapshot.State == shared.ConnectionStateUpdating && time.Now().Before(snapshot.updateDeadline)
}
