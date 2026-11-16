package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// externalChainDeployment 构造「托管入口 A → 外部直链出口 E」的初始部署（入口端点
// 手动建好，与面板 handleCreateChain 的落库结果一致：无出口节点、无 service piece）。
func externalChainDeployment(t *testing.T) (*store.Store, *Dispatcher, *fakeRequester, int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	aID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry-a", Address: "a.example.com",
		BootstrapToken: "token-a", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exit := shared.ExternalExit{Protocol: shared.ProtocolSocks, Address: "socks.example.com", Port: 2080,
		Username: "u", Password: "p"}
	eID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit-e", Address: exit.Address,
		BootstrapToken: "token-e", MachineType: store.MachineTypeNAT, CountryCode: "JP",
		Kind: store.ServerKindExternal, ExternalConfig: mustJSON(t, exit)})
	if err != nil {
		t.Fatal(err)
	}
	endpointJSON := mustJSONBytes(t, shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Security: shared.SecurityReality,
		Template: json.RawMessage(`{"tag":"{{TAG}}","protocol":"vless","port":"{{PORT}}","settings":{"clients":"{{CLIENTS}}","decryption":"none"}}`)})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, aID, shared.ProtocolVLESS, 0, "profile-external", endpointJSON)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "ext-chain", ServiceServerID: eID, EndpointID: endpoint.ID,
		ServiceConfig: json.RawMessage(`{}`), TrafficMultiplierMilli: 1000,
		ServiceExternal: &exit,
		Hops: []store.InitialChainHop{
			{ServerID: aID, Role: store.HopRoleEntry, Transport: "direct"},
			{ServerID: eID, Role: store.HopRoleExit, Transport: "direct"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 用户分配：入口端点路由按 assignment 输出（access:N），否则 routes 为空。
	userID, err := st.InsertUser(ctx, "alice", "11111111-2222-3333-4444-555555555555", "sub-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID}); err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{aID: true}}
	d := New(st, req, Options{}, Events{})
	return st, d, req, aID, eID, deployment.ChainID, deployment.RevisionID
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustJSONBytes(t *testing.T, value any) json.RawMessage {
	t.Helper()
	return json.RawMessage(mustJSON(t, value))
}

// TestAdvanceExternalExitChain 验证外部直链出口链编排：
// 无 apply_node（出口跳直接 active）→ 入口 forward 拨外部 host:port → ack 后链 active
// 且发布快照带外部规格 → 入口端点 reconcile 下发带 External 的路由。
func TestAdvanceExternalExitChain(t *testing.T) {
	ctx := context.Background()
	st, d, _, aID, eID, chainID, _ := externalChainDeployment(t)
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	hops, err := st.ChainHops(ctx, chainID)
	if err != nil || len(hops) != 2 {
		t.Fatalf("hops = %+v err=%v", hops, err)
	}
	if hops[0].ServerID != aID || hops[1].ServerID != eID || hops[1].NodeID != 0 {
		t.Fatalf("unexpected hops: %+v", hops)
	}
	// 出口跳无需 Agent：直接 active。
	if hops[1].Status != store.HopStatusActive {
		t.Fatalf("external exit hop status = %s, want active", hops[1].Status)
	}
	if _, err := st.CommandsByType(ctx, shared.TypeApplyNode); err == nil {
		if nodeCmds, _ := st.CommandsByType(ctx, shared.TypeApplyNode); len(nodeCmds) != 0 {
			t.Fatalf("外部出口链不应下发 apply_node: %+v", nodeCmds)
		}
	}
	// 入口 forward 拨外部 host:port，且入口监听仅回环（链有入口端点）。
	cmd := lastHopCommand(t, st, shared.TypeApplyChainHop, aID)
	var payload shared.ApplyChainHopPayload
	if err := json.Unmarshal(cmd.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != shared.HopKindForward || payload.Forward == nil {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Forward.TargetAddress != "socks.example.com" || payload.Forward.TargetPort != 2080 {
		t.Fatalf("forward target = %s:%d", payload.Forward.TargetAddress, payload.Forward.TargetPort)
	}
	if payload.Forward.Network != "tcp" || !payload.Forward.LocalOnly {
		t.Fatalf("forward spec = %+v", payload.Forward)
	}

	ackHop(t, st, d, aID, hops[0].ID, shared.HopKindForward, &shared.RealizedConfig{Port: 31080})
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	// 发布后端点先置 applying（链暂降级），端点回执 active 后恢复 active。
	if chain.Status != store.ChainStatusDegraded && chain.Status != store.ChainStatusActive {
		t.Fatalf("chain status = %s", chain.Status)
	}
	published, err := st.PublishedChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if published.Snapshot.ServiceExternal == nil || published.Snapshot.ServiceExternal.Port != 2080 {
		t.Fatalf("published external spec = %+v", published.Snapshot.ServiceExternal)
	}
	if published.Snapshot.Hops[0].ForwardPort != 31080 {
		t.Fatalf("published entry forward port = %d", published.Snapshot.Hops[0].ForwardPort)
	}

	// 入口端点 reconcile：路由带外部规格、拨入口回环 forward 端口、无 tunnel UUID。
	endpointCmds, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(endpointCmds) == 0 {
		t.Fatalf("endpoint commands = %d err=%v", len(endpointCmds), err)
	}
	var endpointPayload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(endpointCmds[len(endpointCmds)-1].Data, &endpointPayload); err != nil {
		t.Fatal(err)
	}
	if len(endpointPayload.Routes) != 1 {
		t.Fatalf("routes = %+v", endpointPayload.Routes)
	}
	route := endpointPayload.Routes[0]
	if route.External == nil || route.External.Protocol != shared.ProtocolSocks {
		t.Fatalf("route external = %+v", route.External)
	}
	if route.TargetAddress != "127.0.0.1" || route.TargetPort != 31080 || route.TunnelUUID != "" {
		t.Fatalf("route target = %s:%d tunnel=%q", route.TargetAddress, route.TargetPort, route.TunnelUUID)
	}
	// 端点部署回执 active → 链恢复 active（外部出口无 Agent，只在线的托管跳参与判定）。
	if err := d.efsm.Transition(ctx, endpointPayload.EndpointID, store.EndpointStatusActive,
		"部署回执确认", json.RawMessage(`{"port":30001}`)); err != nil {
		t.Fatal(err)
	}
	d.fsm.Evaluate(ctx, chainID)
	chain, err = st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("端点生效后链应恢复 active: %s", chain.Status)
	}
}
