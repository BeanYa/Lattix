package main

import (
	"sync"

	"lattix/shared"
)

type panelStateTracker struct {
	mu      sync.RWMutex
	value   shared.PanelLifecycleSnapshot
	changed chan struct{}
}

func newPanelStateTracker(initial *shared.PanelLifecycleSnapshot) *panelStateTracker {
	t := &panelStateTracker{changed: make(chan struct{})}
	if initial != nil && shared.ValidPanelState(initial.State) {
		t.value = *initial
	}
	return t
}

func (t *panelStateTracker) snapshot() (shared.PanelLifecycleSnapshot, <-chan struct{}) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.value, t.changed
}

func (t *panelStateTracker) apply(next shared.PanelLifecycleSnapshot, newSession bool) bool {
	if !shared.ValidPanelState(next.State) || next.Epoch == "" || next.Revision == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !newSession && t.value.Epoch != "" && t.value.Epoch != next.Epoch {
		return false
	}
	if !newSession && t.value.Epoch == next.Epoch {
		if next.Revision < t.value.Revision {
			return false
		}
		if next.Revision == t.value.Revision {
			return t.value.State == next.State && t.value.Fault == next.Fault
		}
	}
	t.value = next
	changed := t.changed
	t.changed = make(chan struct{})
	close(changed)
	return true
}
