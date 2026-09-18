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
	d.handleChainHopResult(serverID, shared.ApplyResultPayload{HopID: hopID, Kind: kind, RealizedConfig: rc}, "")
}

func TestSingleHopChainBecomesActiveAfterServiceApply(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "direct", Address: "direct.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "direct", serverID, shared.ProtocolVLESS, nil, config)
	chainID, _ := st.InsertChain(ctx, "direct")
	hopID, _ := st.InsertChainHop(ctx, chainID, 0, serverID, store.HopRoleExit, nodeID, 0, "")
	if _, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "direct", ServiceNodeID: nodeID, ServiceServerID: serverID,
		ServiceConfig: config, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{{
			HopID: hopID, ServerID: serverID, Role: store.HopRoleExit,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{serverID: true}}
	d := New(st, req, Options{}, Events{})
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(&shared.RealizedConfig{Port: 443})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	d.advanceChainByNode(ctx, nodeID)
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("chain status = %s", chain.Status)
	}
	hop, _ := st.ChainHopByID(ctx, hopID)
	if hop.Status != store.HopStatusActive || hop.ForwardPort != 443 {
		t.Fatalf("hop = %+v", hop)
	}
	published, err := st.PublishedChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(published.Snapshot.Hops) != 1 || published.Snapshot.Hops[0].ForwardPort != 443 {
		t.Fatalf("published single-hop snapshot = %+v", published.Snapshot.Hops)
	}
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
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: addr, BootstrapToken: "tok-"+alias, MachineType: mtype, AllowedPorts: ports, CountryCode: "US", Location: "Test"})
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
	d := New(st, req, Options{DestCandidates: []string{"dl.google.com:443"}}, Events{})

	// 阶段 1：出口业务 apply_node（仅出口档无端口段 → 无 port_candidates）。
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	c := lastHopCommand(t, st, shared.TypeApplyNode, exitID)
	var anp shared.ApplyNodePayload
	if err := json.Unmarshal(c.Data, &anp); err != nil {
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
	if err := json.Unmarshal(pc.Data, &portal); err != nil {
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
	if err := json.Unmarshal(bc.Data, &bridge); err != nil {
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
	if err := json.Unmarshal(mc.Data, &fwdMid); err != nil {
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
	if err := json.Unmarshal(ec.Data, &fwdEntry); err != nil {
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
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "e.com", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Entry"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "x.com", BootstrapToken: "tok2", MachineType: store.MachineTypeNAT, CountryCode: "JP", Location: "Exit"})
	vcJSON, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "测试出口节点", exitID, shared.ProtocolVLESS, nil, vcJSON)
	chainID, _ := st.InsertChain(ctx, "测试链路")
	hop1, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 0, "t-uuid")
	st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")

	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, Options{DestCandidates: []string{"dl.google.com:443"}}, Events{})
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
	d.handleChainHopResult(entryID, shared.ApplyResultPayload{HopID: hop1, Kind: shared.HopKindPortal}, "boom")
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

// TestAdvanceChainSkipsReusedPieces 验证全复用 revision（仅改名/倍率变更，ApplyKeys 为空）
// 的编排不重发任何 piece：reused forward/portal/bridge 视为已 acked，链直接 active。
// 回归场景：reused 标记写入的键与 pieces 读取键命名空间不一致（revisionPieceKey vs
// pieceKey）→ advanceChain 对已复用 piece 再次 enqueue，因任务不存在而失败，
// 跳卡 applying、链卡 applying，编辑永不完成。
func TestAdvanceChainSkipsReusedPieces(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "e.com", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Entry"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "x.com", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "JP", Location: "Exit"})
	vcJSON, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "测试出口节点", exitID, shared.ProtocolVLESS, nil, vcJSON)
	realized, _ := json.Marshal(&shared.RealizedConfig{Port: 4433})
	_ = st.SetNodeActive(ctx, nodeID, realized)
	chainID, _ := st.InsertChain(ctx, "测试链路")
	hop1, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 18443, "")
	hop2, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")

	// 已发布的初始 revision（拓扑与配置快照）。
	snapshot := store.ChainRevisionSnapshot{
		Name: "测试链路", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: vcJSON, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: hop1, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 18443},
			{HopID: hop2, ServerID: exitID, Role: store.HopRoleExit, ForwardPort: 0},
		},
	}
	revision, err := st.CreateChainRevision(ctx, chainID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	// 模拟 handleEditChain：同拓扑同配置的新 revision（ApplyKeys 为空 = 全部复用），
	// chain_hops 重置为 pending（ReplaceWorkingChainTopology 语义）。
	desired, err := st.CreateChainRevision(ctx, chainID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(desired.Snapshot.ApplyKeys) != 0 {
		t.Fatalf("fixture 应全复用，ApplyKeys = %v", desired.Snapshot.ApplyKeys)
	}
	if err := st.ReplaceWorkingChainTopology(ctx, desired, shared.ProtocolVLESS, nil, false); err != nil {
		t.Fatal(err)
	}

	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, Options{DestCandidates: []string{"dl.google.com:443"}}, Events{})
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}

	cmds, err := st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if err != nil || len(cmds) != 0 {
		t.Fatalf("全复用 revision 不应下发任何 apply_chain_hop（%d 条）", len(cmds))
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("链状态 %s，期望 active（全复用应直接完成）", chain.Status)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if h.Status != store.HopStatusActive {
			t.Fatalf("跳 %d 状态 %s，期望 active", h.ID, h.Status)
		}
	}
}

// TestNatPortsCarryCandidatesForManualPorts 验证受限直连 NAT 机上手动端口也携带
// 监听侧候选（§21 双保险：面板校验后 Agent 再做段内校验/挑选）：
// 出口业务节点手动端口 → apply_node 携带候选；入口 forward 手动端口 → 携带候选。
func TestNatPortsCarryCandidatesForManualPorts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const segments = `[{"pub_start":20000,"pub_end":20004,"listen_start":30000,"listen_end":30004}]`
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.com", BootstrapToken: "tok1", MachineType: store.MachineTypeNAT, AllowedPorts: segments, CountryCode: "US", Location: "Entry"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.com", BootstrapToken: "tok2", MachineType: store.MachineTypeNAT, AllowedPorts: segments, CountryCode: "JP", Location: "Exit"})
	port := 18443
	vcJSON, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "测试出口节点", exitID, shared.ProtocolVLESS, &port, vcJSON)
	chainID, _ := st.InsertChain(ctx, "测试链路")
	st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, port, "")
	st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")

	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, Options{DestCandidates: []string{"dl.google.com:443"}}, Events{})
	if err := d.StartChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}

	// 阶段 1：出口业务节点手动端口 → apply_node 仍携带段内候选。
	c := lastHopCommand(t, st, shared.TypeApplyNode, exitID)
	var anp shared.ApplyNodePayload
	if err := json.Unmarshal(c.Data, &anp); err != nil {
		t.Fatal(err)
	}
	if anp.NodeID != nodeID || len(anp.PortCandidates) != 5 || anp.PortCandidates[0] != 30000 {
		t.Fatalf("apply_node 手动端口候选不符: %+v", anp)
	}
	realized, _ := json.Marshal(&shared.RealizedConfig{Port: port})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	d.advanceChainByNode(ctx, nodeID)

	// 阶段 4：入口 forward 手动端口 → 携带段内候选（非回环监听）。
	ec := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	var fwd shared.ApplyChainHopPayload
	if err := json.Unmarshal(ec.Data, &fwd); err != nil {
		t.Fatal(err)
	}
	if fwd.Kind != shared.HopKindForward || fwd.Forward == nil || fwd.Forward.Port != port ||
		len(fwd.Forward.PortCandidates) != 5 || fwd.Forward.PortCandidates[0] != 30000 {
		t.Fatalf("forward 手动端口候选不符: %+v", fwd.Forward)
	}
}

// chainHopCommands 返回指定链的全部 apply_chain_hop 命令（按 id 升序）。
func chainHopCommands(t *testing.T, st *store.Store, chainID int64) []store.Command {
	t.Helper()
	cmds, err := st.CommandsByType(context.Background(), shared.TypeApplyChainHop)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Command
	for _, c := range cmds {
		var p shared.ApplyChainHopPayload
		if err := json.Unmarshal(c.Data, &p); err == nil && p.ChainID == chainID {
			out = append(out, c)
		}
	}
	return out
}

// TestAdvanceChainHy2SharedExit 验证 hy2 出口共享链的阶段 1：不发 apply_node，
// 先 reconcile 出口共享端点，端点 active 后出口节点镜像 realized 并推进后续阶段。
func TestAdvanceChainHy2SharedExit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Entry"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "JP", Location: "Exit"})
	svcCfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, svcCfg)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-e2e", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: svcCfg, ServiceEndpointID: epE.ID, ServiceUUID: "svc-hy2",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 25000},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, Options{}, Events{})
	if err := d.StartChain(ctx, dep.ChainID); err != nil {
		t.Fatal(err)
	}
	// 出口不走 apply_node；改为 reconcile 出口共享端点。
	if cmds, _ := st.CommandsByType(ctx, shared.TypeApplyNode); len(cmds) != 0 {
		t.Fatalf("hy2 出口共享链不应下发 apply_node（%d 条）", len(cmds))
	}
	epCmds, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(epCmds) != 1 {
		t.Fatalf("应下发一次 apply_shared_endpoint: %v %d", err, len(epCmds))
	}
	var epPayload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(epCmds[0].Data, &epPayload); err != nil {
		t.Fatal(err)
	}
	if epPayload.EndpointID != epE.ID || epCmds[0].ServerID != exitID {
		t.Fatalf("端点命令不符: endpoint=%d server=%d", epPayload.EndpointID, epCmds[0].ServerID)
	}
	if node, _ := st.NodeByID(ctx, dep.NodeID); node.Status != store.NodeStatusPending {
		t.Fatalf("端点未 active 前节点应保持 pending，实际 %s", node.Status)
	}

	// 端点回执 active → 出口节点镜像 realized 并继续推进到阶段 4。
	epRealized := json.RawMessage(`{"port":1443,"sni":"cdn.example.com","obfs_password":"ob","up_mbps":100,"down_mbps":200}`)
	if err := d.efsm.Transition(ctx, epE.ID, store.EndpointStatusActive, "部署回执确认", epRealized); err != nil {
		t.Fatal(err)
	}
	node, err := st.NodeByID(ctx, dep.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != store.NodeStatusActive {
		t.Fatalf("端点 active 后节点应镜像为 active，实际 %s", node.Status)
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(node.RealizedConfig, &rc); err != nil || rc.Port != 1443 || rc.SNI != "cdn.example.com" {
		t.Fatalf("节点 realized_config 应为端点 realized 镜像: %s", node.RealizedConfig)
	}
	fc := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	var fwd shared.ApplyChainHopPayload
	if err := json.Unmarshal(fc.Data, &fwd); err != nil {
		t.Fatal(err)
	}
	if fwd.Kind != shared.HopKindForward || fwd.Forward == nil {
		t.Fatalf("期望 forward piece: %+v", fwd)
	}
	if fwd.Forward.Network != "udp" || fwd.Forward.Port != 25000 ||
		fwd.Forward.TargetAddress != "exit.test" || fwd.Forward.TargetPort != 1443 {
		t.Fatalf("末段 forward spec 不符: %+v", fwd.Forward)
	}
	ackHop(t, st, d, entryID, dep.Hops[0].HopID, shared.HopKindForward, &shared.RealizedConfig{Port: 25000})
	chain, _ := st.ChainByID(ctx, dep.ChainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("链状态 %s，期望 active（error=%s）", chain.Status, chain.Error)
	}
	published, err := st.PublishedChainRevision(ctx, dep.ChainID)
	if err != nil {
		t.Fatal(err)
	}
	var pubRC shared.RealizedConfig
	if err := json.Unmarshal(published.Snapshot.ServiceRealized, &pubRC); err != nil || pubRC.Port != 1443 {
		t.Fatalf("发布快照 realized 应为端点镜像: %s", published.Snapshot.ServiceRealized)
	}
}

// TestAdvanceChainHy2LastMile 验证入口终结末段（transport="hy2"）：
// 末段 forward spec 携带 Hy2Target（地址=出口公网、端口=段起点/realized、auth=派生、
// SNI/pin/obfs/带宽/段透传）；2 跳链（入口即末跳）不产 forward piece（端点直拨）。
func TestAdvanceChainHy2LastMile(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Entry"})
	midID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "mid", Address: "mid.test", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "SG", Location: "Mid"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok3", MachineType: store.MachineTypeDirect, CountryCode: "JP", Location: "Exit"})
	svcCfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, svcCfg)
	if err != nil {
		t.Fatal(err)
	}
	epE2, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	// 出口共享端点预置 active（realized 携带完整 hy2 参数）。
	epRealized := json.RawMessage(`{"port":1443,"sni":"cdn.example.com","cert_sha256":"ab12","obfs_password":"ob","up_mbps":100,"down_mbps":200,"port_hop":"30000-30031"}`)
	if err := st.SetSharedEndpointActive(ctx, epE.ID, epRealized); err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-entry", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: svcCfg, EndpointID: epE2.ID, ServiceEndpointID: epE.ID, ServiceUUID: "svc-3hop",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct"},
			{ServerID: midID, Role: store.HopRoleMiddle, Transport: "hy2"},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{entryID: true, midID: true, exitID: true}}
	d := New(st, req, Options{}, Events{})
	if err := d.StartChain(ctx, dep.ChainID); err != nil {
		t.Fatal(err)
	}

	// 阶段 4：mid（末段，transport=hy2）的 forward 携带 Hy2Target。
	mc := lastHopCommand(t, st, shared.TypeApplyChainHop, midID)
	var fwdMid shared.ApplyChainHopPayload
	if err := json.Unmarshal(mc.Data, &fwdMid); err != nil {
		t.Fatal(err)
	}
	if fwdMid.Kind != shared.HopKindForward || fwdMid.Forward == nil {
		t.Fatalf("期望 mid forward piece: %+v", fwdMid)
	}
	fm := fwdMid.Forward
	if fm.Hy2Target == nil {
		t.Fatalf("末段 forward 应携带 Hy2Target: %+v", fm)
	}
	dial := fm.Hy2Target
	if dial.Address != "exit.test" || dial.Auth != shared.Hy2UserPassword("svc-3hop") ||
		dial.SNI != "cdn.example.com" || dial.CertSHA256 != "ab12" || dial.ObfsPassword != "ob" ||
		dial.UpMbps != 100 || dial.DownMbps != 200 || dial.PortHop != "30000-30031" {
		t.Fatalf("Hy2Target 字段不符: %+v", dial)
	}
	if dial.Port != 30000 {
		t.Fatalf("PortHop 非空时 Hy2Target.Port 应为段起点 30000，实际 %d", dial.Port)
	}
	if fm.Network != "udp" {
		t.Fatalf("末段 forward Network 应为 udp，实际 %q", fm.Network)
	}
	if fm.TargetAddress == "" || fm.TargetPort == 0 {
		t.Fatalf("dokodemo 必填目标字段应保留: %+v", fm)
	}
	ackHop(t, st, d, midID, dep.Hops[1].HopID, shared.HopKindForward, &shared.RealizedConfig{Port: 25001})

	// 入口跳（非末段）forward 不携带 Hy2Target（回环管道接入口端点）。
	ec := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	var fwdEntry shared.ApplyChainHopPayload
	if err := json.Unmarshal(ec.Data, &fwdEntry); err != nil {
		t.Fatal(err)
	}
	if fwdEntry.Forward == nil || fwdEntry.Forward.Hy2Target != nil {
		t.Fatalf("入口跳 forward 不应携带 Hy2Target: %+v", fwdEntry.Forward)
	}
	if !fwdEntry.Forward.LocalOnly {
		t.Fatalf("入口端点链 hop0 forward 应 LocalOnly: %+v", fwdEntry.Forward)
	}

	// 2 跳入口终结链（入口即末跳）：不产 forward piece（端点直拨出口）。
	dep2, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-entry-2hop", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: svcCfg, EndpointID: epE2.ID, ServiceEndpointID: epE.ID, ServiceUUID: "svc-2hop",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "hy2"},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range dep2.ApplyKeys {
		if key == fmt.Sprintf("forward/%d", dep2.Hops[0].HopID) {
			t.Fatalf("2 跳 hy2 链 hop0 不应在 apply_keys 中: %v", dep2.ApplyKeys)
		}
	}
	if err := d.StartChain(ctx, dep2.ChainID); err != nil {
		t.Fatal(err)
	}
	if cmds := chainHopCommands(t, st, dep2.ChainID); len(cmds) != 0 {
		t.Fatalf("2 跳 hy2 链不应下发任何 apply_chain_hop（%d 条）", len(cmds))
	}
	if _, err := st.PublishedChainRevision(ctx, dep2.ChainID); err != nil {
		t.Fatalf("2 跳 hy2 链应直接发布: %v", err)
	}
}

// TestAdvanceChainHy2HopPorts 验证端到端跳跃段下发：exit port_hop="30000-30031" 时
// 各跳 forward spec.Port=段起点（forward_port）、HopPortEnd=30031、Network="udp"。
func TestAdvanceChainHy2HopPorts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Entry"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "JP", Location: "Exit"})
	svcCfg := json.RawMessage(`{"protocol":"hysteria","port_hop":"30000-30031","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, svcCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, epE.ID, json.RawMessage(`{"port":1443,"port_hop":"30000-30031"}`)); err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-hop", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: svcCfg, ServiceEndpointID: epE.ID, ServiceUUID: "svc-hop",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 30000},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{entryID: true, exitID: true}}
	d := New(st, req, Options{}, Events{})
	if err := d.StartChain(ctx, dep.ChainID); err != nil {
		t.Fatal(err)
	}
	fc := lastHopCommand(t, st, shared.TypeApplyChainHop, entryID)
	var fwd shared.ApplyChainHopPayload
	if err := json.Unmarshal(fc.Data, &fwd); err != nil {
		t.Fatal(err)
	}
	if fwd.Forward == nil || fwd.Forward.Port != 30000 || fwd.Forward.HopPortEnd != 30031 || fwd.Forward.Network != "udp" {
		t.Fatalf("跳跃段 forward spec 不符: %+v", fwd.Forward)
	}
	if fwd.Forward.Hy2Target != nil {
		t.Fatalf("端到端链入口跳不应携带 Hy2Target: %+v", fwd.Forward)
	}
}
