package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
