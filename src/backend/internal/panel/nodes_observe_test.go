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

// nodeObserveFixture 准备一台直连服务器并落库一个 vless 节点（pending，供重试/删除复用）。
func nodeObserveFixture(t *testing.T, ctx context.Context) (*store.Store, int64, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "svc", Address: "svc.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	port := 3000
	nodeReq := createNodeRequest{Name: "n1", ServerID: serverID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeReq.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(nodeReq))
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, &port, config)
	if err != nil {
		t.Fatal(err)
	}
	return st, serverID, nodeID
}

func TestCreateNodeCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, serverID, _ := nodeObserveFixture(t, ctx)
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester, observes: progress.NewRegistry()}
	port := 3001
	node := createNodeRequest{Name: "n2", ServerID: serverID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "abcdef0123456789", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := node.normalize(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(node)
	env, rec := postGroupObserve(t, srv, srv.handleCreateNode, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "node.create")
}

func TestRetryNodeCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, serverID, nodeID := nodeObserveFixture(t, ctx)
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		NodeID int64 `json:"node_id"`
	}{NodeID: nodeID})
	env, rec := postGroupObserve(t, srv, srv.handleRetryNode, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("retry status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "node.retry")
}

func TestDeleteNodeCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, serverID, nodeID := nodeObserveFixture(t, ctx)
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	srv := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester, observes: progress.NewRegistry()}
	body, _ := json.Marshal(struct {
		NodeID int64 `json:"node_id"`
	}{NodeID: nodeID})
	env, rec := postGroupObserve(t, srv, srv.handleDeleteNode, string(body))
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("delete status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertChainNodeObserveDone(t, srv, env, "node.delete")
}
