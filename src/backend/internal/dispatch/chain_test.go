package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// fakeRequester 捕获下发信封的 Requester 桩（在线性可控）。
type fakeRequester struct {
	mu     sync.Mutex
	online map[int64]bool
	sent   []shared.Envelope
}

func (f *fakeRequester) Send(_ context.Context, serverID int64, env shared.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online[serverID] {
		return ws.ErrOffline
	}
	f.sent = append(f.sent, env)
	return nil
}

func (f *fakeRequester) IsOnline(serverID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online[serverID]
}

func (f *fakeRequester) setOnline(serverID int64, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.online[serverID] = on
}

// lastHopCommand 取指定服务器某类命令的最新一条（按 id 升序的末尾）。
func lastHopCommand(t *testing.T, st *store.Store, typ string, serverID int64) store.Command {
	t.Helper()
	cmds, err := st.CommandsByType(context.Background(), typ)
	if err != nil {
		t.Fatal(err)
	}
	var last *store.Command
	for i := range cmds {
		if cmds[i].ServerID == serverID {
			c := cmds[i]
			last = &c
		}
	}
	if last == nil {
		t.Fatalf("server %d 无 %s 命令", serverID, typ)
	}
	return *last
}

// ackHop 模拟 agent 回执：命令置 acked 并走回执路由推进编排。
func ackHop(t *testing.T, st *store.Store, d *Dispatcher, serverID, hopID int64, kind string, rc *shared.RealizedConfig) {
	t.Helper()
	c := lastHopCommand(t, st, shared.TypeApplyChainHop, serverID)
	if c.Status != store.CommandStatusSent {
		t.Fatalf("命令 %d 状态 %s，期望 sent", c.ID, c.Status)
	}
	if ok, err := st.MarkCommandAcked(context.Background(), c.ID); err != nil || !ok {
		t.Fatalf("ack 命令 %d: %v ok=%v", c.ID, err, ok)
	}
	d.handleChainHopResult(serverID, shared.ApplyResultPayload{HopID: hopID, Kind: kind, OK: true, RealizedConfig: rc})
}

// TestChainOrchestration 覆盖 §21.1 五阶段编排与 degraded 推导：
// 拓扑 entry(direct) → mid(NAT 受限直连，20000-20009→30000-30009) → exit(NAT 仅出口档)；
// 链路 1→2 直连、2→3 反向（portal 在 mid、bridge 在 exit）。
func TestChainOrchestration(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mkServer := func(alias, addr, mtype, ports string) int64 {
		id, err := st.CreateServer(ctx, alias, addr, "tok-"+alias, mtype, ports, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	entryID := mkServer("entry", "entry.example.com", store.MachineTypeDirect, "")
	midID := mkServer("mid", "mid.example.com", store.MachineTypeNAT,
		`[{"pub_start":20000,"pub_end":20009,"listen_start":30000,"listen_end":30009}]`)
	exitID := mkServer("exit", "exit.example.com", store.MachineTypeNAT, "")

	vc := shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)}
	vcJSON, _ := json.Marshal(vc)
	nodeID, err := st.InsertNode(ctx, "测试出口节点", exitID, shared.ProtocolVLESS, nil, vcJSON)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "测试链路")
	if err != nil {
		t.Fatal(err)
	}
	hop1, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 18443, "")
	hop2, _ := st.InsertChainHop(ctx, chainID, 1, midID, store.HopRoleMiddle, 0, 0, "t-uuid")
	hop3, _ := st.InsertChainHop(ctx, chainID, 2, exitID, store.HopRoleExit, nodeID, 0, "")

	req := &fakeRequester{online: map[int64]bool{entryID: true, midID: true, exitID: true}}
	d := New(st, req, "dev")
	d.DestCandidates = []string{"dl.google.com:443"}

	// 阶段 1：出口业务 apply_node（仅出口档无端口段 → 无 port_candidates）。
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	c := lastHopCommand(t, st, shared.TypeApplyNode, exitID)
	var anp shared.ApplyNodePayload
	if err := json.Unmarshal(c.Payload, &anp); err != nil {
		t.Fatal(err)
	}
	if anp.NodeID != nodeID || len(anp.PortCandidates) != 0 {
		t.Fatalf("apply_node 载荷不符: %+v", anp)
	}
	if n, _ := st.NodeByID(ctx, nodeID); n.Status != store.NodeStatusApplying {
		t.Fatalf("节点状态 %s，期望 applying", n.Status)
	}
	realized, _ := json.Marshal(&shared.RealizedConfig{Port: 4433, PublicKey: "exit-pub", ShortID: "sid", ServerName: "dl.google.com"})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	d.advanceChainByNode(ctx, nodeID)

	// 阶段 2：portal 在 mid（反向链上游机），端口候选 = 监听侧段，tunnel_domain=c<chain>h<hop2>.lx。
	pc := lastHopCommand(t, st, shared.TypeApplyChainHop, midID)
	var portal shared.ApplyChainHopPayload
	if err := json.Unmarshal(pc.Payload, &portal); err != nil {
		t.Fatal(err)
	}
	if portal.Kind != shared.HopKindPortal || portal.Portal == nil {
		t.Fatalf("期望 portal piece: %+v", portal)
	}
	wantDomain := fmt.Sprintf("c%dh%d.lx", chainID, hop2)
	if portal.Portal.TunnelDomain != wantDomain || portal.Portal.Tag != shared.ChainPortalTag(hop2) {
		t.Fatalf("portal domain/tag 不符: %+v", portal.Portal)
	}
	if len(portal.Portal.PortCandidates) != 10 || portal.Portal.PortCandidates[0] != 30000 {
		t.Fatalf("portal port_candidates 不符: %v", portal.Portal.PortCandidates)
	}
	ackHop(t, st, d, midID, hop2, shared.HopKindPortal, &shared.RealizedConfig{Port: 30001, PublicKey: "portal-pub", ServerName: "portal-sni.example.com"})

	// 阶段 3：bridge 在 exit，携带 portal 回执（公网侧端口 30001→20001、实际 SNI）与派生 short_id。
	bc := lastHopCommand(t, st, shared.TypeApplyChainHop, exitID)
	var bridge shared.ApplyChainHopPayload
	if err := json.Unmarshal(bc.Payload, &bridge); err != nil {
		t.Fatal(err)
	}
	if bridge.Kind != shared.HopKindBridge || bridge.Bridge == nil {
		t.Fatalf("期望 bridge piece: %+v", bridge)
	}
	b := bridge.Bridge
	if b.PortalAddress != "mid.example.com" || b.PortalPort != 20001 || b.PublicKey != "portal-pub" ||
		b.TunnelUUID != "t-uuid" || b.ShortID == "" || b.ServerName != "portal-sni.example.com" || b.TunnelDomain != wantDomain {
		t.Fatalf("bridge 载荷不符: %+v", b)
	}
	ackHop(t, st, d, exitID, hop3, shared.HopKindBridge, nil)

	// 阶段 4a：mid 的 forward —— 反向目标 127.0.0.1:出口业务端口 + via_tunnel_domain。
	mc := lastHopCommand(t, st, shared.TypeApplyChainHop, midID)
	var fwdMid shared.ApplyChainHopPayload
	if err := json.Unmarshal(mc.Payload, &fwdMid); err != nil {
		t.Fatal(err)
	}
	if fwdMid.Kind != shared.HopKindForward || fwdMid.Forward == nil {
		t.Fatalf("期望 forward piece: %+v", fwdMid)
	}
	fm := fwdMid.Forward
	if fm.TargetAddress != "127.0.0.1" || fm.TargetPort != 4433 || fm.ViaTunnelDomain != wantDomain ||
		len(fm.PortCandidates) != 10 {
		t.Fatalf("mid forward 载荷不符: %+v", fm)
	}
	ackHop(t, st, d, midID, hop2, shared.HopKindForward, &shared.RealizedConfig{Port: 30002})

	// 阶段 4b：entry 的 forward —— 直连目标 mid 公网地址：公网端口（30002→20002），用户指定端口 18443。
	ec := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	var fwdEntry shared.ApplyChainHopPayload
	if err := json.Unmarshal(ec.Payload, &fwdEntry); err != nil {
		t.Fatal(err)
	}
	fe := fwdEntry.Forward
	if fe == nil || fe.Port != 18443 || fe.TargetAddress != "mid.example.com" || fe.TargetPort != 20002 ||
		fe.ViaTunnelDomain != "" {
		t.Fatalf("entry forward 载荷不符: %+v", fe)
	}
	ackHop(t, st, d, entryID, hop1, shared.HopKindForward, &shared.RealizedConfig{Port: 18443})

	// 阶段 5：全部跳 active → 链 active。
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("链状态 %s，期望 active（error=%s）", chain.Status, chain.Error)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if h.Status != store.HopStatusActive {
			t.Fatalf("跳 %d 状态 %s，期望 active", h.ID, h.Status)
		}
	}

	// degraded 推导：mid 离线 → degraded；恢复在线 → active。
	req.setOnline(midID, false)
	d.RecomputeChainsByServer(midID)
	if chain, _ := st.ChainByID(ctx, chainID); chain.Status != store.ChainStatusDegraded {
		t.Fatalf("mid 离线后链状态 %s，期望 degraded", chain.Status)
	}
	req.setOnline(midID, true)
	d.RecomputeChainsByServer(midID)
	if chain, _ := st.ChainByID(ctx, chainID); chain.Status != store.ChainStatusActive {
		t.Fatalf("mid 恢复后链状态 %s，期望 active", chain.Status)
	}
}

// TestChainHopResultFailure 覆盖回执失败定位：portal 失败 → 跳 failed + 链 failed 定位到跳。
func TestChainHopResultFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, "entry", "e.com", "tok1", store.MachineTypeDirect, "", "")
	exitID, _ := st.CreateServer(ctx, "exit", "x.com", "tok2", store.MachineTypeNAT, "", "")
	vcJSON, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "测试出口节点", exitID, shared.ProtocolVLESS, nil, vcJSON)
	chainID, _ := st.InsertChain(ctx, "测试链路")
	hop1, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 0, "t-uuid")
	st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")

	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, "dev")
	d.DestCandidates = []string{"dl.google.com:443"}
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	// 出口业务就绪 → 阶段 2 向 entry 下发 portal。
	realized, _ := json.Marshal(&shared.RealizedConfig{Port: 4433})
	st.SetNodeActive(ctx, nodeID, realized)
	d.advanceChainByNode(ctx, nodeID)
	c := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	if ok, _ := st.MarkCommandFailed(ctx, c.ID); !ok {
		t.Fatalf("命令 %d 置 failed 失败", c.ID)
	}
	d.handleChainHopResult(entryID, shared.ApplyResultPayload{HopID: hop1, Kind: shared.HopKindPortal, OK: false, Error: "boom"})
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusFailed {
		t.Fatalf("链状态 %s，期望 failed", chain.Status)
	}
	hop, _ := st.ChainHopByID(ctx, hop1)
	if hop.Status != store.HopStatusFailed || hop.Error == "" {
		t.Fatalf("跳状态 %s error=%q，期望 failed 带错误", hop.Status, hop.Error)
	}
	// 重试：失败跳复位 → 重新下发 portal piece。
	if err := d.RetryChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	c2 := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	if c2.ID == c.ID {
		t.Fatalf("重试未重发 portal 命令")
	}
	if chain, _ := st.ChainByID(ctx, chainID); chain.Status != store.ChainStatusApplying {
		t.Fatalf("重试后链状态 %s，期望 applying", chain.Status)
	}
}
