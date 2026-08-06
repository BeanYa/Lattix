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
	serverID, err := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
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
