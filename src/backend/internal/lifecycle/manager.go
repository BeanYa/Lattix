package lifecycle

import (
	"errors"
	"sync"
	"time"

	"lattix/shared"
)

var ErrInvalidState = errors.New("invalid panel lifecycle state")

type Manager struct {
	mu       sync.RWMutex
	snapshot shared.PanelLifecycleSnapshot
}

func New(panelInstanceID string) *Manager {
	return &Manager{snapshot: shared.PanelLifecycleSnapshot{
		PanelInstanceID:       panelInstanceID,
		State:                 shared.PanelStateStartup,
		Epoch:                 shared.NewMessageID(),
		Revision:              1,
		EnteredAt:             time.Now().UTC(),
		RetryPolicy:           retryPolicy(shared.PanelStateStartup),
		LatencyResumeWindowMS: 30000,
	}}
}

func (m *Manager) Snapshot() shared.PanelLifecycleSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *Manager) Transition(state, fault string) (shared.PanelLifecycleSnapshot, bool, error) {
	if !shared.ValidPanelState(state) {
		return shared.PanelLifecycleSnapshot{}, false, ErrInvalidState
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot.State == state && m.snapshot.Fault == fault {
		return m.snapshot, false, nil
	}
	m.snapshot.State = state
	m.snapshot.Revision++
	m.snapshot.EnteredAt = time.Now().UTC()
	m.snapshot.Fault = fault
	m.snapshot.RetryPolicy = retryPolicy(state)
	return m.snapshot, true, nil
}

func retryPolicy(state string) shared.RetryPolicy {
	switch state {
	case shared.PanelStateUpdating:
		return shared.RetryPolicy{MinMS: 5000, MaxMS: 15000}
	case shared.PanelStateFaulted:
		return shared.RetryPolicy{MinMS: 30000, MaxMS: 90000}
	default:
		return shared.RetryPolicy{MinMS: 500, MaxMS: 30000}
	}
}
