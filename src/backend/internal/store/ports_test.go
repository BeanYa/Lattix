package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

func TestPortOccupants(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 服务器 1：一个 vless 节点（tcp）、一个 ss 节点（tcp,udp）、一个共享端点、一条链的 forward/portal
	nodePort := 10001
	if _, err := st.InsertNode(ctx, "n-vless", 1, shared.ProtocolVLESS, &nodePort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	ssPort := 10002
	if _, err := st.InsertNode(ctx, "n-ss", 1, shared.ProtocolShadowsocks, &ssPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// 服务器 2 的节点不应出现
	otherPort := 10003
	if _, err := st.InsertNode(ctx, "n-other", 2, shared.ProtocolVLESS, &otherPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, 1, shared.ProtocolVLESS, 10004, "profile-x", json.RawMessage(`{"protocol":"vless"}`)); err != nil {
		t.Fatal(err)
	}

	occupants, err := st.PortOccupants(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]PortOccupant{}
	for _, o := range occupants {
		byPort[o.Port] = o
	}
	vless, ok := byPort[10001]
	if !ok || vless.Source != "node" || vless.Layers != "tcp" || vless.RefName != "n-vless" {
		t.Errorf("vless 节点占用不符: %+v", vless)
	}
	ss, ok := byPort[10002]
	if !ok || ss.Layers != "tcp,udp" {
		t.Errorf("ss 节点应为 tcp,udp 双层: %+v", ss)
	}
	ep, ok := byPort[10004]
	if !ok || ep.Source != "endpoint" {
		t.Errorf("共享端点占用缺失: %+v", ep)
	}
	if _, ok := byPort[10003]; ok {
		t.Error("其他服务器的端口不应出现")
	}
}

func TestPortOccupantsChainSources(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	entryID, err := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.example.com", BootstrapToken: "entry-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "exit-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}

	// 活链：entry 跳 forward 21001 + portal 21002（反向传输，portal 落在上游入口机）。
	active, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链活", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: json.RawMessage(`{"protocol":"vless"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 21001, TunnelUUID: "tunnel-a"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainHopPortalRealized(ctx, active.Hops[0].HopID, 21002, "pub", "sni"); err != nil {
		t.Fatal(err)
	}

	// 软删链：forward 22001 + portal 22002，应被 deleted_at IS NULL 排除。
	deleted, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链删", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: json.RawMessage(`{"protocol":"vless"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 22001, TunnelUUID: "tunnel-b"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainHopPortalRealized(ctx, deleted.Hops[0].HopID, 22002, "pub", "sni"); err != nil {
		t.Fatal(err)
	}
	// DeleteChain 会硬删 chain_hops，无法制造"软删链仍有跳占用"的场景，故直接置 deleted_at。
	if _, err := st.db.ExecContext(ctx, `UPDATE chains SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, deleted.ChainID); err != nil {
		t.Fatal(err)
	}

	// ss 出口链：forward 管道应按出口协议标记 tcp,udp（§3.2 UDP 中转管道）。
	ssChain, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链SS", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolShadowsocks,
		ServiceConfig: json.RawMessage(`{"protocol":"shadowsocks"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 21003, TunnelUUID: "tunnel-c"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	occupants, err := st.PortOccupants(ctx, entryID)
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]PortOccupant{}
	for _, o := range occupants {
		if o.ChainID == deleted.ChainID {
			t.Errorf("软删链占用不应出现: %+v", o)
		}
		byPort[o.Port] = o
	}
	fwd, ok := byPort[21001]
	if !ok || fwd.Source != "chain_forward" || fwd.Layers != "tcp" || fwd.ChainID != active.ChainID || fwd.RefName != "链活" {
		t.Errorf("forward 占用不符: %+v", fwd)
	}
	portal, ok := byPort[21002]
	if !ok || portal.Source != "chain_portal" || portal.Layers != "tcp" || portal.ChainID != active.ChainID || portal.RefName != "链活" {
		t.Errorf("portal 占用不符: %+v", portal)
	}
	ssFwd, ok := byPort[21003]
	if !ok || ssFwd.Source != "chain_forward" || ssFwd.Layers != "tcp,udp" || ssFwd.ChainID != ssChain.ChainID {
		t.Errorf("ss 出口链 forward 应为 tcp,udp 双层: %+v", ssFwd)
	}
	if _, ok := byPort[22001]; ok {
		t.Error("软删链 forward 端口不应出现")
	}
	if _, ok := byPort[22002]; ok {
		t.Error("软删链 portal 端口不应出现")
	}
}
