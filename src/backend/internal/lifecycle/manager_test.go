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

func TestTransitionRejectsIllegalState(t *testing.T) {
	m := New("panel-1")
	// startup → active 合法，此后 active → startup 非法（进程内不可回退启动态）。
	if _, _, err := m.Transition(shared.PanelStateActive, ""); err != nil {
		t.Fatalf("startup → active: %v", err)
	}
	if _, _, err := m.Transition(shared.PanelStateStartup, ""); err == nil {
		t.Fatal("active → startup must be rejected")
	}
	// faulted 为进程内终态（恢复 = 重启）。
	if _, _, err := m.Transition(shared.PanelStateFaulted, "boom"); err != nil {
		t.Fatalf("active → faulted: %v", err)
	}
	if _, _, err := m.Transition(shared.PanelStateActive, ""); err == nil {
		t.Fatal("faulted → active must be rejected in-process")
	}
}

func TestTransitionAllowsUpdatingRoundTrip(t *testing.T) {
	m := New("panel-1")
	if _, _, err := m.Transition(shared.PanelStateActive, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Transition(shared.PanelStateUpdating, ""); err != nil {
		t.Fatalf("active → updating: %v", err)
	}
	if _, _, err := m.Transition(shared.PanelStateActive, ""); err != nil {
		t.Fatalf("updating → active: %v", err)
	}
}

func TestTransitionAllowsStartupToUpdating(t *testing.T) {
	// 启动窗口内到达的更新请求（HTTP 先于生命周期初始化完成）合法。
	m := New("panel-1")
	if _, _, err := m.Transition(shared.PanelStateUpdating, ""); err != nil {
		t.Fatalf("startup → updating: %v", err)
	}
}
