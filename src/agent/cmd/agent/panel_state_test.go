package main

import (
	"testing"
	"time"

	"lattix/shared"
)

func lifecycleSnapshot(epoch, state string, revision uint64) shared.PanelLifecycleSnapshot {
	return shared.PanelLifecycleSnapshot{
		PanelInstanceID: "p_00000000000000000000000000000000",
		State:           state, Epoch: epoch, Revision: revision, EnteredAt: time.Now(),
	}
}

func TestPanelStateTrackerOrdersRevisionsAndEpochs(t *testing.T) {
	tracker := newPanelStateTracker(nil)
	if !tracker.apply(lifecycleSnapshot("epoch-a", shared.PanelStateStartup, 1), true) {
		t.Fatal("authenticated session snapshot must initialize tracker")
	}
	if tracker.apply(lifecycleSnapshot("epoch-a", shared.PanelStateActive, 1), false) {
		t.Fatal("same revision with different state must be rejected")
	}
	if !tracker.apply(lifecycleSnapshot("epoch-a", shared.PanelStateActive, 2), false) {
		t.Fatal("newer revision must be accepted")
	}
	if tracker.apply(lifecycleSnapshot("epoch-a", shared.PanelStateUpdating, 1), false) {
		t.Fatal("older revision must be rejected")
	}
	if tracker.apply(lifecycleSnapshot("epoch-b", shared.PanelStateStartup, 1), false) {
		t.Fatal("new epoch must be rejected inside an existing session")
	}
	if !tracker.apply(lifecycleSnapshot("epoch-b", shared.PanelStateStartup, 1), true) {
		t.Fatal("authenticated new session may establish a new epoch")
	}
}
