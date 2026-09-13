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

// TestPortOccupantsHy2HopRange 验证 hy2 节点/共享端点的跳跃段以段行形式进入占用画像（P4 §3.2）。
func TestPortOccupantsHy2HopRange(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// hy2 节点：port=21000，port_hop="30000-30031" → 单端口行 + 段行。
	hy2Port := 21000
	if _, err := st.InsertNode(ctx, "n-hy2", 1, shared.ProtocolHysteria2, &hy2Port,
		json.RawMessage(`{"protocol":"hysteria","port":21000,"port_hop":"30000-30031","template":{}}`)); err != nil {
		t.Fatal(err)
	}
	// 无 port_hop 的 hy2 节点只产单端口行；vless 节点不受影响。
	plainPort := 21001
	if _, err := st.InsertNode(ctx, "n-hy2-plain", 1, shared.ProtocolHysteria2, &plainPort,
		json.RawMessage(`{"protocol":"hysteria","port":21001,"template":{}}`)); err != nil {
		t.Fatal(err)
	}
	vlessPort := 21002
	if _, err := st.InsertNode(ctx, "n-vless", 1, shared.ProtocolVLESS, &vlessPort, json.RawMessage(`{"protocol":"vless"}`)); err != nil {
		t.Fatal(err)
	}
	// hy2 共享端点带跳跃段 → 段行（endpoint）。
	if _, _, err := st.EnsureSharedEndpoint(ctx, 1, shared.ProtocolHysteria2, 21003, "profile-hy2",
		json.RawMessage(`{"protocol":"hysteria","port":21003,"port_hop":"31000-31031","template":{}}`)); err != nil {
		t.Fatal(err)
	}

	occ, err := st.PortOccupants(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var single, nodeSpan, epSpan *PortOccupant
	for i := range occ {
		o := &occ[i]
		switch {
		case o.Port == 21000 && o.PortEnd == 0 && o.Layers == "udp":
			single = o
		case o.Port == 30000 && o.PortEnd == 30031 && o.Layers == "udp":
			nodeSpan = o
		case o.Port == 31000 && o.PortEnd == 31031 && o.Layers == "udp":
			epSpan = o
		}
		if o.Port == 21001 && o.PortEnd != 0 {
			t.Errorf("无 port_hop 的 hy2 节点不应产段行: %+v", o)
		}
		if o.PortEnd != 0 && o.Protocol == shared.ProtocolVLESS {
			t.Errorf("vless 节点不应产段行: %+v", o)
		}
	}
	if single == nil || nodeSpan == nil || epSpan == nil {
		t.Fatalf("hy2 占用画像缺行（单端口/节点段/端点段）: %+v", occ)
	}
	if nodeSpan.Source != "node" || nodeSpan.RefName != "n-hy2" || nodeSpan.Protocol != shared.ProtocolHysteria2 {
		t.Errorf("节点段行来源不符: %+v", nodeSpan)
	}
	if epSpan.Source != "endpoint" {
		t.Errorf("端点段行来源不符: %+v", epSpan)
	}
}

// TestPortOccupantsHy2ChainHopSpan 验证 hy2 出口链的逐跳转发保留段（端到端跳跃）：
// 段 = [forward_port, forward_port + 段长 - 1]，段长取出口节点 config_template 的 port_hop。
func TestPortOccupantsHy2ChainHopSpan(t *testing.T) {
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

	hy2Chain, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链HY2", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig:          json.RawMessage(`{"protocol":"hysteria","port_hop":"30000-30031","template":{}}`),
		TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 40000, TunnelUUID: "tunnel-hy2"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// vless 出口链：即使 forward_port 相同形态也不产段行（段长来源非 hy2）。
	if _, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链VLESS", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: json.RawMessage(`{"protocol":"vless"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 41000, TunnelUUID: "tunnel-v"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	}); err != nil {
		t.Fatal(err)
	}

	occ, err := st.PortOccupants(ctx, entryID)
	if err != nil {
		t.Fatal(err)
	}
	var fwd, span *PortOccupant
	for i := range occ {
		o := &occ[i]
		switch {
		case o.Port == 40000 && o.PortEnd == 0:
			fwd = o
		case o.Port == 40000 && o.PortEnd == 40031:
			span = o
		}
		if o.Port == 41000 && o.PortEnd != 0 {
			t.Errorf("vless 出口链不应产段行: %+v", o)
		}
	}
	if fwd == nil || span == nil {
		t.Fatalf("hy2 链占用画像缺行（单端口 forward/跳跃段）: %+v", occ)
	}
	if span.Source != "chain_forward" || span.ChainID != hy2Chain.ChainID || span.Layers != "udp" || span.RefName != "链HY2" {
		t.Errorf("链段行不符: %+v", span)
	}
}
