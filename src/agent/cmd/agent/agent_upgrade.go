package main

import (
	"errors"
	"log"
	"sync"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// Upgrade execution outlives individual WebSockets. Attachments publish the
// current operation state and route its receipt through the current session.
// A new process starts online; a reconnect in this process remains updating.
type agentUpgradeTracker struct {
	mu         sync.Mutex
	state      string
	request    shared.Envelope
	conn       *safeConn
	saveStatus func(bool, string, error)
	attachment uint64
}

func newAgentUpgradeTracker() *agentUpgradeTracker {
	return &agentUpgradeTracker{state: state.ConnStateOnline}
}

func (u *agentUpgradeTracker) attach(sc *safeConn, saveStatus func(bool, string, error)) (uint64, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.attachment++
	u.conn, u.saveStatus = sc, saveStatus
	return u.attachment, u.reportLocked()
}

func (u *agentUpgradeTracker) detach(attachment uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.attachment == attachment {
		u.conn = nil
		u.saveStatus = nil
	}
}

func (u *agentUpgradeTracker) reportLocked() error {
	if u.conn == nil {
		return nil
	} // Replayed when the next session attaches.
	requestID, traceID := u.request.RequestID, u.request.TraceID
	if requestID == "" {
		requestID = shared.NewMessageID()
		traceID = requestID
	}
	if err := u.conn.writeJSON(shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeAgentLifecycleChanged,
		RequestID: requestID, TraceID: traceID, Data: mustJSON(shared.AgentLifecyclePayload{State: u.state})}); err != nil {
		return err
	}
	if u.saveStatus != nil {
		u.saveStatus(true, u.state, nil)
	}
	return nil
}

func (u *agentUpgradeTracker) begin(env shared.Envelope) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn == nil {
		return errors.New("agent upgrade has no active session")
	}
	previous := u.state
	u.state, u.request = state.ConnStateUpdating, env
	if err := u.reportLocked(); err != nil {
		u.state = previous
		return err
	}
	return nil
}

func (u *agentUpgradeTracker) complete(env shared.Envelope, upgraded bool, applyErr error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn != nil {
		replyResult(u.conn, env, resultOf(0, nil), applyErr)
	}
	if applyErr != nil || !upgraded {
		u.state = state.ConnStateOnline
		if err := u.reportLocked(); err != nil {
			log.Printf("agent.upgrade report online: %v", err)
		}
	}
}

// Successful replacement stays updating until the new process attaches.
func performAgentUpgrade(u *agentUpgradeTracker, env shared.Envelope, apply func() (bool, error), restart func()) {
	if err := u.begin(env); err != nil {
		log.Printf("agent.upgrade report updating: %v", err)
		return
	}
	upgraded, err := apply()
	u.complete(env, upgraded, err)
	if err != nil || !upgraded {
		return
	}
	log.Printf("upgrade_agent: 二进制已替换，退出等待服务管理器拉起")
	restart()
}
