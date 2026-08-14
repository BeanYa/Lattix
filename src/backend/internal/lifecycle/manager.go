package lifecycle

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"lattix/shared"
)

var ErrInvalidState = errors.New("invalid panel lifecycle state")

// ErrIllegalTransition 表示生命周期转换不在合法转换表内。
var ErrIllegalTransition = errors.New("illegal panel lifecycle transition")

// panelLifecycleTransitions 定义进程内合法的 Panel 生命周期转换（设计文档 §2）：
//   - startup → active | updating（启动窗口内收到更新请求）| faulted；
//   - active  → updating | faulted；
//   - updating → active | faulted；
//   - faulted 为进程内终态：恢复必须重启进程回到 startup 并重新执行初始化检查。
var panelLifecycleTransitions = map[string]map[string]bool{
	shared.PanelStateStartup: {
		shared.PanelStateActive:   true,
		shared.PanelStateUpdating: true,
		shared.PanelStateFaulted:  true,
	},
	shared.PanelStateActive: {
		shared.PanelStateUpdating: true,
		shared.PanelStateFaulted:  true,
	},
	shared.PanelStateUpdating: {
		shared.PanelStateActive:  true,
		shared.PanelStateFaulted: true,
	},
	shared.PanelStateFaulted: {},
}

func validLifecycleTransition(from, to string) bool {
	if from == to {
		return true // 幂等：同状态更新（如 fault 详情变化）允许
	}
	targets, ok := panelLifecycleTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

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
	if !validLifecycleTransition(m.snapshot.State, state) {
		return m.snapshot, false, fmt.Errorf("%w: %s → %s", ErrIllegalTransition, m.snapshot.State, state)
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
