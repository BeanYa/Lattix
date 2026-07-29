package lifecycle

import (
	"testing"

	"lattix/shared"
)

func TestTransitionVersionsLifecycleSnapshot(t *testing.T) {
	m := New("panel-1")
	initial := m.Snapshot()
	if initial.State != shared.PanelStateStartup || initial.Revision != 1 || initial.Epoch == "" {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	active, changed, err := m.Transition(shared.PanelStateActive, "")
	if err != nil || !changed {
		t.Fatalf("transition changed=%v err=%v", changed, err)
	}
	if active.Epoch != initial.Epoch || active.Revision != 2 {
		t.Fatalf("active version = %#v", active.Version())
	}
	again, changed, err := m.Transition(shared.PanelStateActive, "")
	if err != nil || changed || again.Revision != active.Revision {
		t.Fatalf("idempotent transition = %#v changed=%v err=%v", again, changed, err)
	}
}

func TestTransitionRejectsUnknownState(t *testing.T) {
	m := New("panel-1")
	if _, _, err := m.Transition("paused", ""); err == nil {
		t.Fatal("unknown state must be rejected")
	}
}
