package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// assertChainNodeObserveDone 断言 envelope 携带合法 observe_id 且观察已 done、
// 阶段为 db/publish/regenerate 三阶段（与分组观察的 reconcile 阶段不同）。
func assertChainNodeObserveDone(t *testing.T, srv *Server, env groupEnv, kind string) {
	t.Helper()
	if env.ObserveID == "" {
		t.Fatalf("envelope 未携带 observe_id: %+v", env)
	}
	if !shared.ValidMessageID(env.ObserveID) {
		t.Fatalf("observe_id 非法: %q", env.ObserveID)
	}
	obs, ok := srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 不存在", env.ObserveID)
	}
	if obs.Kind != kind {
		t.Fatalf("观察 kind = %s, want %s", obs.Kind, kind)
	}
	if obs.Status != progress.StatusDone {
		t.Fatalf("观察状态 = %s, want %s (obs = %+v)", obs.Status, progress.StatusDone, obs)
	}
	if len(obs.Stages) != 3 {
		t.Fatalf("stages = %+v, want 3 个阶段", obs.Stages)
	}
	for i, want := range []string{"db", "publish", "regenerate"} {
		if obs.Stages[i].Key != want {
			t.Fatalf("stage[%d].key = %s, want %s", i, obs.Stages[i].Key, want)
		}
	}
}

func TestCreateChainCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	node := createNodeRequest{Name: "a", ServerID: serverID, Protocol: shared.ProtocolVLESS,
		ShortID: "short-a", Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
		Fingerprint: shared.FingerprintChrome, Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := node.normalize(); err != nil {
		t.Fatal(err)
	}
	entryPort := 443
	body, _ := json.Marshal(createChainRequest{Name: "a",
		Hops: []chainHopRef{{ServerID: serverID}}, Entry: chainHopRef{ServerID: serverID},
		Exit: chainHopRef{ServerID: serverID}, Node: node, EntryPort: &entryPort,
		TrafficMultiplier: "1.000"})
	env, rec := postGroupObserve(t, srv, srv.handleCreateChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "chain.create")
}

func TestEditChainCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, _, chainID, _, _, nodeRequest, _ := chainEditFixture(t, ctx, st)
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	entryPort := 1000
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: &entryPort,
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	env, rec := postGroupObserve(t, srv, srv.handleEditChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeAccepted {
		t.Fatalf("edit status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "chain.edit")
}

func TestDeleteChainCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, _, chainID, _, _, _, _ := chainEditFixture(t, ctx, st)
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		ChainID int64 `json:"chain_id"`
	}{ChainID: chainID})
	env, rec := postGroupObserve(t, srv, srv.handleDeleteChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("delete status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "chain.delete")
}

func TestRetryChainCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, nodeID, chainID, aHop, _, _, config := chainEditFixture(t, ctx, st)
	failChainAfterEdit(t, ctx, st, chainID, nodeID, aID, cID, aHop, 2000, config)
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		ChainID int64 `json:"chain_id"`
	}{ChainID: chainID})
	env, rec := postGroupObserve(t, srv, srv.handleRetryChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("retry status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "chain.retry")
}

func TestForcePublishChainCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, nodeID, chainID, aHop, _, _, config := chainEditFixture(t, ctx, st)
	failChainAfterEdit(t, ctx, st, chainID, nodeID, aID, cID, aHop, 2000, config)
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		ChainID int64 `json:"chain_id"`
	}{ChainID: chainID})
	env, rec := postGroupObserve(t, srv, srv.handleForcePublishChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("force publish status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "chain.force_publish")
}

// TestForcePublishChainFailureMarksObserveFailed 覆盖业务失败路径的观察收口：
// 失败链尚无生效参数时强制发布返回错误，观察必须落 failed（而非 defer Close 的 done）。
func TestForcePublishChainFailureMarksObserveFailed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "a", Address: "a.example.com", BootstrapToken: "token-a", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	cID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "c", Address: "c.example.com", BootstrapToken: "token-c", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	port := 3000
	nodeRequest := createNodeRequest{Name: "chain", ServerID: cID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeRequest.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(nodeRequest))
	nodeID, _ := st.InsertNode(ctx, "chain", cID, shared.ProtocolVLESS, &port, config)
	chainID, _ := st.InsertChain(ctx, "chain")
	aHop, _ := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 1000, "")
	_, _ = st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, nodeID, port, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		ChainID int64 `json:"chain_id"`
	}{ChainID: chainID})
	env, rec := postGroupObserve(t, srv, srv.handleForcePublishChain, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeInvalidArgument {
		t.Fatalf("force publish 应失败: status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	if env.ObserveID == "" {
		t.Fatalf("失败响应未携带 observe_id: %+v", env)
	}
	obs, ok := srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 不存在", env.ObserveID)
	}
	if obs.Status != progress.StatusFailed {
		t.Fatalf("观察状态 = %s, want %s (obs = %+v)", obs.Status, progress.StatusFailed, obs)
	}
	if obs.Error == "" {
		t.Fatalf("失败观察应携带错误信息: %+v", obs)
	}
}
