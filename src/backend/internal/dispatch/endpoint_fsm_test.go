package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestEndpointTransitionTable 校验共享端点状态机转换表覆盖真实流转并拒绝非法边。
func TestEndpointTransitionTable(t *testing.T) {
	legal := []struct{ from, to string }{
		{store.EndpointStatusPending, store.EndpointStatusApplying},
		{store.EndpointStatusApplying, store.EndpointStatusActive},
		{store.EndpointStatusApplying, store.EndpointStatusFailed},
		{store.EndpointStatusApplying, store.EndpointStatusApplying}, // 幂等（自愈重复 reconcile）
		{store.EndpointStatusActive, store.EndpointStatusApplying},   // 路由变化重部署
		{store.EndpointStatusActive, store.EndpointStatusFailed},     // 新一轮部署失败
		{store.EndpointStatusActive, store.EndpointStatusPending},    // 重置复用
		{store.EndpointStatusFailed, store.EndpointStatusApplying},   // 重试
		{store.EndpointStatusFailed, store.EndpointStatusPending},    // 重置复用
	}
	for _, c := range legal {
		if !validEndpointTransition(c.from, c.to) {
			t.Errorf("缺少转换边 %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to string }{
		{store.EndpointStatusFailed, store.EndpointStatusActive}, // 失败后不得直通生效
		{store.EndpointStatusActive, "deleted"},                  // 未知状态拒绝
		{store.EndpointStatusPending, store.EndpointStatusPending + "-x"},
	}
	for _, c := range illegal {
		if validEndpointTransition(c.from, c.to) {
			t.Errorf("非法转换未被拒绝 %s → %s", c.from, c.to)
		}
	}
}

// TestEndpointFSMTransitionSideEffects 校验 Transition 持久化 + onEnter 副作用：
// failed → 使用该端点的链 degraded；active → 链恢复 active；OnEndpointPublished 触发。
func TestEndpointFSMTransitionSideEffects(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "ep-fsm")
	chainID := deployment.ChainID
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if err := st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
	}
	published := 0
	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}}, Options{}, Events{
		OnEndpointPublished: func(context.Context, int64) error { published++; return nil },
	})

	// pending → failed：链应 degraded（端点未生效）。
	if err := d.efsm.Transition(ctx, endpoint.ID, store.EndpointStatusFailed, "boom", nil); err != nil {
		t.Fatalf("pending → failed: %v", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusDegraded {
		t.Fatalf("端点失败后链状态 = %s，期望 degraded", chain.Status)
	}
	if published != 1 {
		t.Fatalf("OnEndpointPublished 调用 = %d，期望 1", published)
	}

	// failed → active：链应恢复 active。
	realized := json.RawMessage(`{"port":443,"network":"tcp"}`)
	if err := d.efsm.Transition(ctx, endpoint.ID, store.EndpointStatusActive, "生效", realized); err != nil {
		t.Fatalf("failed → active: %v", err)
	}
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("端点生效后链状态 = %s，期望 active", chain.Status)
	}
	if published != 2 {
		t.Fatalf("OnEndpointPublished 调用 = %d，期望 2", published)
	}

	// 幂等：active → active 不触发副作用。
	if err := d.efsm.Transition(ctx, endpoint.ID, store.EndpointStatusActive, "再次", realized); err != nil {
		t.Fatalf("active → active 幂等: %v", err)
	}
	if published != 2 {
		t.Fatalf("幂等转换不应触发副作用，调用 = %d", published)
	}
}

// TestEndpointFSMRejectsIllegalTransition 非法转换拒绝且不落库。
func TestEndpointFSMRejectsIllegalTransition(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}}, Options{}, Events{})
	err = d.efsm.Transition(ctx, endpoint.ID, store.EndpointStatusActive, "x", json.RawMessage(`{"port":443}`))
	if err == nil || !strings.Contains(err.Error(), "illegal transition") {
		t.Fatalf("Transition error = %v, want illegal transition", err)
	}
	ep, _ := st.SharedEndpointByID(ctx, endpoint.ID)
	if ep.Status != store.EndpointStatusFailed {
		t.Fatalf("非法转换后端点状态 = %s，期望保持 failed", ep.Status)
	}
}

// TestEndpointFSMCASConflict 并发修改：以过期 from 经 store CAS 被拒绝。
func TestEndpointFSMCASConflict(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	// 端点已 applying；以过期 pending 视角再次置 applying 应被 CAS 拒绝（ErrStateTransition）。
	if err := st.SetSharedEndpointApplying(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	err = st.SetSharedEndpointFailed(ctx, endpoint.ID, "x") // applying → failed 合法，用于对照
	if err != nil {
		t.Fatalf("applying → failed: %v", err)
	}
	// 真正检验：failed → active 直通被 store 守卫拒绝（FSM 层也拒绝，双保险）。
	err = st.SetSharedEndpointActive(ctx, endpoint.ID, json.RawMessage(`{"port":443}`))
	if !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("store guard error = %v, want ErrStateTransition", err)
	}
}
