package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// hy2ChainFixture 构建两台 direct 服务器（入口/出口）与面板测试桩。
func hy2ChainFixture(t *testing.T) (*store.Store, *Server, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	server := func(alias, token string) int64 {
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: token, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, cID := server("entry-a", "token-a"), server("exit-c", "token-c")
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester}
	return st, serverAPI, aID, cID
}

func postCreateChain(t *testing.T, serverAPI *Server, body any) (int, rpcEnvelope) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	serverAPI.handleCreateChain(recorder, httptest.NewRequest("POST", "/api/chain/create", bytes.NewReader(raw)))
	return recorder.Code, decodeRPC(t, recorder)
}

// TestCreateChainHy2ExitShared 验证 hy2 出口链挂出口侧共享监听：两条同机 hy2 链
// 共用同一 service_endpoint_id（spec §3.2 出口共享），端到端（无入口区块）时
// endpoint_id 保持 0。
func TestCreateChainHy2ExitShared(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)

	create := func(name string) store.Chain {
		t.Helper()
		code, env := postCreateChain(t, serverAPI, createChainRequest{
			Name: name, Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
			Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
			TrafficMultiplier: "1.000",
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("create %s = %d %s %s", name, code, env.Code, string(env.Data))
		}
		var dto chainDTO
		if err := json.Unmarshal(env.Data, &dto); err != nil {
			t.Fatal(err)
		}
		chain, err := st.ChainByID(ctx, dto.ID)
		if err != nil {
			t.Fatal(err)
		}
		return *chain
	}
	chain1 := create("hy2-a")
	chain2 := create("hy2-b")

	if chain1.ServiceEndpointID == 0 || chain2.ServiceEndpointID == 0 {
		t.Fatalf("hy2 链应挂出口共享监听: %d / %d", chain1.ServiceEndpointID, chain2.ServiceEndpointID)
	}
	if chain1.ServiceEndpointID != chain2.ServiceEndpointID {
		t.Fatalf("同机 hy2 链应并入同一共享监听: %d vs %d", chain1.ServiceEndpointID, chain2.ServiceEndpointID)
	}
	if chain1.EndpointID != 0 || chain2.EndpointID != 0 {
		t.Fatalf("端到端 hy2 链不应有入口端点: %d / %d", chain1.EndpointID, chain2.EndpointID)
	}
	endpoints, err := st.SharedEndpointsByServer(ctx, cID)
	if err != nil {
		t.Fatal(err)
	}
	hy2 := 0
	for _, ep := range endpoints {
		if ep.Protocol == shared.ProtocolHysteria2 {
			hy2++
		}
	}
	if hy2 != 1 {
		t.Fatalf("出口机 shared_endpoints hysteria 行数 = %d, want 1", hy2)
	}
	// 出口节点自身不再监听：service 快照端口为 0。
	revision, err := st.ChainRevisionByID(ctx, chain1.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc shared.VirtualConfig
	if err := json.Unmarshal(revision.Snapshot.ServiceConfig, &vc); err != nil {
		t.Fatal(err)
	}
	if vc.Port != 0 {
		t.Fatalf("共享监听承载后出口节点端口应为 0: %d", vc.Port)
	}
}

// TestCreateChainEntryProtocolBlock 验证入口协议区块：勾选 entry_node（vless+reality）
// 的 hy2 中转链 → endpoint_id != 0（入口机 vless 端点）且 service_endpoint_id != 0，
// hops[0].transport == "hy2"；v1 非法组合 400（单跳、出口协议 vmess、entry_node 协议非 vless）。
func TestCreateChainEntryProtocolBlock(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)

	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "entry-block", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		EntryNode:         &createNodeRequest{Protocol: shared.ProtocolVLESS},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s", code, env.Code)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.EndpointID == 0 || chain.ServiceEndpointID == 0 {
		t.Fatalf("入口区块 hy2 链应同时挂两端点: endpoint=%d service_endpoint=%d",
			chain.EndpointID, chain.ServiceEndpointID)
	}
	// 入口端点是入口机上的 vless 共享端点。
	entryEndpoint, err := st.SharedEndpointByID(ctx, chain.EndpointID)
	if err != nil {
		t.Fatal(err)
	}
	if entryEndpoint.ServerID != aID || entryEndpoint.Protocol != shared.ProtocolVLESS {
		t.Fatalf("入口端点 = server %d protocol %s, want 入口机 vless", entryEndpoint.ServerID, entryEndpoint.Protocol)
	}
	revision, err := st.ChainRevisionByID(ctx, chain.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Snapshot.Hops[0].Transport != "hy2" {
		t.Fatalf("末段 transport = %q, want hy2", revision.Snapshot.Hops[0].Transport)
	}
	// 入口区块配置回填到 DTO（前端编辑回填用）。
	if dto.EntryConfig == nil || dto.EntryConfig.Protocol != shared.ProtocolVLESS {
		t.Fatalf("entry_config 应回填 vless 入口配置: %+v", dto.EntryConfig)
	}

	// v1 非法组合：entry_node 协议非 vless。
	assertBad := func(name string, req createChainRequest, want string) {
		t.Helper()
		code, env := postCreateChain(t, serverAPI, req)
		if code != http.StatusOK || env.Code != shared.CodeInvalidArgument {
			t.Fatalf("%s = %d %s, want INVALID_ARGUMENT", name, code, env.Code)
		}
		if want != "" && !bytes.Contains([]byte(env.Message), []byte(want)) {
			t.Fatalf("%s 错误信息 = %q, want 含 %q", name, env.Message, want)
		}
	}
	assertBad("trojan entry_node", createChainRequest{
		Name: "bad-proto", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		EntryNode:         &createNodeRequest{Protocol: shared.ProtocolTrojan},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	}, "VLESS")
	assertBad("单跳", createChainRequest{
		Name: "bad-single", Hops: []chainHopRef{{ServerID: cID}},
		EntryNode:         &createNodeRequest{Protocol: shared.ProtocolVLESS},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	}, "多跳")
	assertBad("vmess 出口", createChainRequest{
		Name: "bad-exit", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		EntryNode:         &createNodeRequest{Protocol: shared.ProtocolVLESS},
		Node:              createNodeRequest{Protocol: shared.ProtocolVMess},
		TrafficMultiplier: "1.000",
	}, "")
}

// TestEditChainHy2KeepPortHop 验证 hy2 链原样编辑（含既有跳跃段）不被自身占用误判：
// 出口节点段行与出口共享端点段行均按 chain 归属排除（excludeChainID）。
func TestEditChainHy2KeepPortHop(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)

	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-edit", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s", code, env.Code)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.ChainRevisionByID(ctx, chain.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	// 前端编辑回填：从已发布快照恢复原样表单（含分配好的 port_hop 与混淆/证书参数）。
	var vc shared.VirtualConfig
	if err := json.Unmarshal(revision.Snapshot.ServiceConfig, &vc); err != nil {
		t.Fatal(err)
	}
	if vc.PortHop == "" {
		t.Fatal("自动分配的跳跃段应已落入出口配置")
	}
	editBody, _ := json.Marshal(editChainRequest{ChainID: chain.ID, Name: "hy2-edit",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node: createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: vc.PortHop,
			ObfsPassword: vc.ObfsPassword, TLSDomain: vc.TLSDomain, UpMbps: vc.UpMbps, DownMbps: vc.DownMbps},
		TrafficMultiplier: "1.000"})
	editRecorder := httptest.NewRecorder()
	serverAPI.handleEditChain(editRecorder, httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(editBody)))
	editEnv := decodeRPC(t, editRecorder)
	if editRecorder.Code != http.StatusOK || editEnv.Code != shared.CodeAccepted {
		t.Fatalf("edit = %d %s %s（不应与自身跳跃段冲突）", editRecorder.Code, editEnv.Code, editEnv.Message)
	}
	// 编辑后仍挂同一出口共享监听。
	edited, err := st.ChainByID(ctx, chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := st.DesiredChainRevision(ctx, edited.ID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Snapshot.ServiceEndpointID != chain.ServiceEndpointID {
		t.Fatalf("编辑后 service_endpoint_id = %d, want 沿用 %d",
			desired.Snapshot.ServiceEndpointID, chain.ServiceEndpointID)
	}
}

// TestDeleteChainReconcilesHy2ServiceEndpoint 验证删链释放 hy2 出口共享监听引用
// （评审移交①）：删除引用链后 reconcile 出口共享端点（重算 clients / 保留监听）。
func TestDeleteChainReconcilesHy2ServiceEndpoint(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-del", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s", code, env.Code)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.ServiceEndpointID == 0 {
		t.Fatal("hy2 链应挂出口共享监听")
	}
	before, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil {
		t.Fatal(err)
	}

	delBody, _ := json.Marshal(map[string]any{"chain_id": dto.ID})
	rec := httptest.NewRecorder()
	serverAPI.handleDeleteChain(rec, httptest.NewRequest("POST", "/api/chain/delete", bytes.NewReader(delBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	after, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("删链后应补一次出口共享端点 reconcile: before=%d after=%d", len(before), len(after))
	}
	var payload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(after[len(after)-1].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EndpointID != chain.ServiceEndpointID {
		t.Fatalf("reconcile 端点 = %d, want %d", payload.EndpointID, chain.ServiceEndpointID)
	}
}

// TestCreateChainHy2ExplicitSamePortHop 验证显式同段第二链 400（评审移交③）：
// 同机 hy2 链共享同一监听与段（首链 profile 为准，显式段并入即被忽略），因此显式
// 指定与既有保留段重叠的段视为冲突 → 400；留空自动分配的链并入端点，落库段以端点
// 段为准（终审修复 I-1：段收敛而非错开，否则逐跳绑定与出口 DNAT 分叉断链）。
func TestCreateChainHy2ExplicitSamePortHop(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)
	create := func(name, portHop string) (int, rpcEnvelope) {
		t.Helper()
		return postCreateChain(t, serverAPI, createChainRequest{
			Name: name, Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
			Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: portHop},
			TrafficMultiplier: "1.000",
		})
	}
	code, env := create("hy2-seg-a", "30000-30031")
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("首链 = %d %s %s", code, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain1, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev1, err := st.ChainRevisionByID(ctx, chain1.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc1 shared.VirtualConfig
	if err := json.Unmarshal(rev1.Snapshot.ServiceConfig, &vc1); err != nil {
		t.Fatal(err)
	}
	if vc1.PortHop != "30000-30031" {
		t.Fatalf("首链显式段 = %q, want 30000-30031", vc1.PortHop)
	}
	// 显式同段/重叠段第二链 → 400。
	for _, seg := range []string{"30000-30031", "30005-30020"} {
		code, env = create("hy2-seg-b", seg)
		if code != http.StatusOK || env.Code != shared.CodeInvalidArgument {
			t.Fatalf("显式重叠段 %s 应 400: %d %s %s", seg, code, env.Code, env.Message)
		}
	}
	// 留空自动分配 → 并入既有共享监听（首链 profile 为准，终审修复 I-1）：自动分配
	// 虽避开首链段给出新段，落库 ServiceConfig 必须以端点段为准，否则 dispatch 按新段
	// 绑定逐跳 forward 而出口 DNAT/订阅仅覆盖端点段 → 链必断。
	code, env = create("hy2-seg-c", "")
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("自动分配链 = %d %s %s", code, env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain3, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev3, err := st.ChainRevisionByID(ctx, chain3.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc3 shared.VirtualConfig
	if err := json.Unmarshal(rev3.Snapshot.ServiceConfig, &vc3); err != nil {
		t.Fatal(err)
	}
	if vc3.PortHop != "30000-30031" {
		t.Fatalf("并入链 ServiceConfig 段 = %q, want 端点段 30000-30031", vc3.PortHop)
	}
}

// TestEditChainEntryBlockWithoutEndpointPortConflict 验证 entryShared 门控收紧
// （评审移交②）：编辑把链改为 vless 出口但快照无入口端点（不会挂端点，入口为
// dokodemo 实监听）时，入口端口必须做冲突校验。
func TestEditChainEntryBlockWithoutEndpointPortConflict(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: "token-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, cID := server("a"), server("c")
	// 占用者：入口机 a 上 18443 的存量节点。
	occPort := 18443
	occCfg, _ := json.Marshal(buildVirtualConfig(createNodeRequest{Protocol: shared.ProtocolTrojan,
		Network: shared.NetworkWS, Security: shared.SecurityTLS, TLSDomain: "cdn.example.com"}))
	if _, err := st.InsertNode(ctx, "occ", aID, shared.ProtocolTrojan, &occPort, occCfg); err != nil {
		t.Fatal(err)
	}
	// 目标链：trojan 出口、无入口区块（快照 endpoint_id=0）。
	trojanReq := createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS,
		Security: shared.SecurityTLS, TLSDomain: "cdn.example.com"}
	if err := trojanReq.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(trojanReq))
	nodeID, _ := st.InsertNode(ctx, "tgt", cID, shared.ProtocolTrojan, nil, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 2096, Network: shared.NetworkWS})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	chainID, _ := st.InsertChain(ctx, "tgt")
	aHop, _ := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 0, "")
	cHop, _ := st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, nodeID, 0, "")
	if err := st.SetChainServiceNode(ctx, chainID, nodeID); err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "tgt", ServiceNodeID: nodeID, ServiceServerID: cID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct"},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester}

	// 编辑改 vless 出口 + 显式入口端口 18443：快照 endpoint_id=0 → 不会挂端点，
	// 入口端口按 dokodemo 实监听校验 → 与存量节点 18443 冲突 → 400。
	//（旧门控按"vless 出口即共享"跳过校验，本用例在收紧前应放行、收紧后 400。）
	vlessReq := createNodeRequest{Protocol: shared.ProtocolVLESS, ShortID: "0123456789abcdef",
		Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
		Fingerprint: shared.FingerprintChrome, Network: shared.NetworkTCP, Flow: shared.FlowVision}
	editBody, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "tgt",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: &occPort,
		Node: vlessReq, TrafficMultiplier: "1.000"})
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(editBody)))
	editEnv := decodeRPC(t, recorder)
	if recorder.Code != http.StatusOK || editEnv.Code != shared.CodeInvalidArgument {
		t.Fatalf("edit = %d %s %s（期望 400 端口冲突）", recorder.Code, editEnv.Code, editEnv.Message)
	}
}

// TestCreateChainHy2HopPortBinding 验证端到端 hy2 链各跳 forward 端口绑定段起点
//（评审 #3）：自动分配路径中间跳不再为 0——否则 dispatch 下发 HopPortEnd=段长-1，
// agent 自选端口后段 inbound 循环为空，端口跳跃在自动分配路径全断；显式入口端口时
// 入口跳保留用户端口（段随跳平移，目标按同号换算仍对齐）。
func TestCreateChainHy2HopPortBinding(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)
	bID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "mid-b", Address: "mid-b.example.com",
		BootstrapToken: "token-b", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-3hop", Hops: []chainHopRef{{ServerID: aID}, {ServerID: bID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s %s", code, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := st.ChainRevisionByID(ctx, chain.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc shared.VirtualConfig
	if err := json.Unmarshal(rev.Snapshot.ServiceConfig, &vc); err != nil {
		t.Fatal(err)
	}
	start, _, err := shared.ParsePortHop(vc.PortHop)
	if err != nil {
		t.Fatalf("自动分配应给出合法段: %q", vc.PortHop)
	}
	hops, err := st.ChainHops(ctx, chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 3 {
		t.Fatalf("hops = %d, want 3", len(hops))
	}
	for i, h := range hops[:2] {
		if h.ForwardPort != start {
			t.Fatalf("hop %d forward_port = %d, want 段起点 %d", i, h.ForwardPort, start)
		}
	}
	if hops[2].ForwardPort != 0 {
		t.Fatalf("出口跳无 forward 管道，forward_port 应为 0: %d", hops[2].ForwardPort)
	}
	// 快照同源（piece 哈希输入），编辑后经 ReplaceWorkingChainTopology 回写不漂移。
	for i, h := range rev.Snapshot.Hops[:2] {
		if h.ForwardPort != start {
			t.Fatalf("快照 hop %d forward_port = %d, want %d", i, h.ForwardPort, start)
		}
	}

	// 显式入口端口：入口跳保留用户端口，中间跳仍绑段起点。
	entryPort := 38443
	code, env = postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-3hop-explicit", Hops: []chainHopRef{{ServerID: aID}, {ServerID: bID}, {ServerID: cID}},
		EntryPort:         &entryPort,
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create explicit = %d %s %s", code, env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain2, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev2, err := st.ChainRevisionByID(ctx, chain2.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc2 shared.VirtualConfig
	if err := json.Unmarshal(rev2.Snapshot.ServiceConfig, &vc2); err != nil {
		t.Fatal(err)
	}
	start2, _, err := shared.ParsePortHop(vc2.PortHop)
	if err != nil {
		t.Fatalf("自动分配应给出合法段: %q", vc2.PortHop)
	}
	hops2, err := st.ChainHops(ctx, chain2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hops2[0].ForwardPort != entryPort {
		t.Fatalf("显式入口端口应保留: %d, want %d", hops2[0].ForwardPort, entryPort)
	}
	if hops2[1].ForwardPort != start2 {
		t.Fatalf("中间跳 forward_port = %d, want 段起点 %d", hops2[1].ForwardPort, start2)
	}
}

// TestCreateChainHy2SharedEndpointAdoptsPortHop 验证同出口机第二条 hy2 链并入既有
// 共享监听时段参数收敛（终审修复 I-1）：自动分配会避开首链段给出新段，但链并入端点
// （首链 profile 为准），落库 ServiceConfig 与逐跳 forward 绑定必须采用端点段——
// 否则 dispatch 按新段绑定逐跳 forward，而出口 DNAT/订阅 mport 仅覆盖端点段，链必断。
// 混淆密码等实现参数同样以端点为准。编辑路径同语义：回填端点段原样提交可放行
// （端点段即本链段，不算冲突），编辑后段仍收敛端点段。
func TestCreateChainHy2SharedEndpointAdoptsPortHop(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: "token-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// 两条链共用出口机 c，入口机不同（共享入口机会在段监听上真实冲突，不在本例范围）。
	aID, a2ID, cID := server("entry-a"), server("entry-a2"), server("exit-c")
	requester := &chainEditRequester{online: map[int64]bool{aID: true, a2ID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester}

	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-share-a", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "30000-30031"},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("首链 = %d %s %s", code, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain1, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev1, err := st.ChainRevisionByID(ctx, chain1.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc1 shared.VirtualConfig
	if err := json.Unmarshal(rev1.Snapshot.ServiceConfig, &vc1); err != nil {
		t.Fatal(err)
	}

	// 第二条链（自动分配会避开端点段产出新段）→ 并入端点，段/混淆以端点为准。
	code, env = postCreateChain(t, serverAPI, createChainRequest{
		Name: "hy2-share-b", Hops: []chainHopRef{{ServerID: a2ID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("第二链 = %d %s %s", code, env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain2, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chain2.ServiceEndpointID != chain1.ServiceEndpointID {
		t.Fatalf("同机 hy2 链应并入同一共享监听: %d vs %d", chain2.ServiceEndpointID, chain1.ServiceEndpointID)
	}
	rev2, err := st.ChainRevisionByID(ctx, chain2.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var vc2 shared.VirtualConfig
	if err := json.Unmarshal(rev2.Snapshot.ServiceConfig, &vc2); err != nil {
		t.Fatal(err)
	}
	if vc2.PortHop != "30000-30031" {
		t.Fatalf("并入链 ServiceConfig 段 = %q, want 端点段 30000-30031", vc2.PortHop)
	}
	if vc2.ObfsPassword != vc1.ObfsPassword {
		t.Fatalf("并入链混淆密码应以端点（首链）为准: %q vs %q", vc2.ObfsPassword, vc1.ObfsPassword)
	}
	// 逐跳 forward 绑定端点段起点（端到端 hy2，无入口区块）。
	hops2, err := st.ChainHops(ctx, chain2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops2) != 2 || hops2[0].ForwardPort != 30000 {
		t.Fatalf("第二链入口跳 forward_port 应绑定端点段起点 30000: %+v", hops2)
	}

	// 编辑路径同语义：回填端点段原样提交（段即本链共有占用，不应误判冲突），
	// 编辑后段仍收敛端点段。
	if err := st.PublishChainRevision(ctx, rev2.ID, false); err != nil {
		t.Fatal(err)
	}
	editBody, _ := json.Marshal(editChainRequest{ChainID: chain2.ID, Name: "hy2-share-b",
		Hops: []chainHopRef{{ServerID: a2ID}, {ServerID: cID}},
		Node: createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "30000-30031",
			ObfsPassword: vc2.ObfsPassword, TLSDomain: vc2.TLSDomain, UpMbps: vc2.UpMbps, DownMbps: vc2.DownMbps},
		TrafficMultiplier: "1.000"})
	editRecorder := httptest.NewRecorder()
	serverAPI.handleEditChain(editRecorder, httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(editBody)))
	editEnv := decodeRPC(t, editRecorder)
	if editRecorder.Code != http.StatusOK || editEnv.Code != shared.CodeAccepted {
		t.Fatalf("编辑并入链 = %d %s %s（端点段即本链段，不应误判冲突）",
			editRecorder.Code, editEnv.Code, editEnv.Message)
	}
	desired, err := st.DesiredChainRevision(ctx, chain2.ID)
	if err != nil {
		t.Fatal(err)
	}
	var vc2e shared.VirtualConfig
	if err := json.Unmarshal(desired.Snapshot.ServiceConfig, &vc2e); err != nil {
		t.Fatal(err)
	}
	if vc2e.PortHop != "30000-30031" {
		t.Fatalf("编辑后 ServiceConfig 段 = %q, want 端点段 30000-30031", vc2e.PortHop)
	}
}

// TestEditChainEntryBlockOnEndpointlessChainRejected 验证存量端到端链补勾入口协议
// 区块被拒绝（终审修复 I-2）：无端点链编辑携带 entry_node 时端点创建门控不挂端点，
// 但末段 transport 已半应用为 hy2 → 入口监听回收、订阅仍走端到端 → 断链；v1 直接
// 400。带入口区块的链编辑（携带/取消勾选）不受影响。
func TestEditChainEntryBlockOnEndpointlessChainRejected(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, cID := hy2ChainFixture(t)

	edit := func(chainID int64, hops []chainHopRef, node createNodeRequest, entry *createNodeRequest) (int, rpcEnvelope) {
		t.Helper()
		body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "edit",
			Hops: hops, Node: node, EntryNode: entry, TrafficMultiplier: "1.000"})
		recorder := httptest.NewRecorder()
		serverAPI.handleEditChain(recorder, httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body)))
		return recorder.Code, decodeRPC(t, recorder)
	}
	publish := func(chainID int64) {
		t.Helper()
		chain, err := st.ChainByID(ctx, chainID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, chain.DesiredRevisionID, false); err != nil {
			t.Fatal(err)
		}
	}

	// 存量端到端 hy2 链（无入口端点）：补勾入口区块 → 400。
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "e2e-hy2", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s %s", code, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	e2eChain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e2eChain.EndpointID != 0 {
		t.Fatalf("端到端链不应有入口端点: %d", e2eChain.EndpointID)
	}
	publish(e2eChain.ID)
	code, env = edit(e2eChain.ID, []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		createNodeRequest{Protocol: shared.ProtocolHysteria2}, &createNodeRequest{Protocol: shared.ProtocolVLESS})
	if code != http.StatusOK || env.Code != shared.CodeInvalidArgument {
		t.Fatalf("存量链补勾入口区块 = %d %s, want INVALID_ARGUMENT", code, env.Code)
	}
	if !strings.Contains(env.Message, "请新建链路") {
		t.Fatalf("错误信息 = %q, want 含「请新建链路」", env.Message)
	}

	// 对照：带入口区块的链——编辑携带 entry_node 仍放行；取消勾选（entry_node 省略）也放行。
	code, env = postCreateChain(t, serverAPI, createChainRequest{
		Name: "entry-hy2", Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		EntryNode:         &createNodeRequest{Protocol: shared.ProtocolVLESS},
		Node:              createNodeRequest{Protocol: shared.ProtocolHysteria2},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create entry = %d %s %s", code, env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	entryChain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entryChain.EndpointID == 0 {
		t.Fatal("带入口区块的链应有入口端点")
	}
	publish(entryChain.ID)
	code, env = edit(entryChain.ID, []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		createNodeRequest{Protocol: shared.ProtocolHysteria2}, &createNodeRequest{Protocol: shared.ProtocolVLESS})
	if code != http.StatusOK || env.Code != shared.CodeAccepted {
		t.Fatalf("带端点链编辑（勾选）= %d %s %s, want Accepted", code, env.Code, env.Message)
	}
	publish(entryChain.ID)
	code, env = edit(entryChain.ID, []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		createNodeRequest{Protocol: shared.ProtocolHysteria2}, nil)
	if code != http.StatusOK || env.Code != shared.CodeAccepted {
		t.Fatalf("带端点链编辑（取消勾选）= %d %s %s, want Accepted", code, env.Code, env.Message)
	}
}
