package panel

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

type fakeAutoRequester struct {
	online map[int64]bool
	sent   []shared.Envelope
}

func (f *fakeAutoRequester) Send(_ context.Context, _ int64, env shared.Envelope) error {
	f.sent = append(f.sent, env)
	return nil
}

func (f *fakeAutoRequester) IsOnline(serverID int64) bool {
	return f.online[serverID]
}

// setupAutoTestChain 创建一条 2 跳已发布 active 链路（Entry: A1 [备选 A2], Exit: B1 [备选 B2]）。
func setupAutoTestChain(t *testing.T) (*Server, *store.Store, *fakeAutoRequester, int64, int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel-auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	req := &fakeAutoRequester{online: make(map[int64]bool)}
	srv, err := New(st, req, Config{AdminUser: "admin", AdminPass: "test-password-123"})
	if err != nil {
		t.Fatal(err)
	}

	createSrv := func(alias, addr string) int64 {
		id, cerr := st.CreateServer(ctx, store.ServerDraft{
			Alias:          alias,
			Address:        addr,
			BootstrapToken: "tok-" + alias,
			MachineType:    store.MachineTypeDirect,
			CountryCode:    "CN",
			Location:       alias,
		})
		if cerr != nil {
			t.Fatal(cerr)
		}
		req.online[id] = true
		return id
	}

	a1 := createSrv("Entry-A1", "203.0.113.1")
	a2 := createSrv("Entry-A2", "203.0.113.2")
	b1 := createSrv("Exit-B1", "198.51.100.1")
	b2 := createSrv("Exit-B2", "198.51.100.2")

	vc := buildVirtualConfig(createNodeRequest{
		Name:     "AutoChain",
		ServerID: b1,
		Protocol: shared.ProtocolVLESS,
		Network:  "tcp",
		Security: shared.SecurityReality,
	})
	vcJSON, _ := json.Marshal(vc)
	ep, _, err := st.EnsureSharedEndpoint(ctx, a1, shared.ProtocolVLESS, 10443, "hash-a1", vcJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointApplying(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, ep.ID, json.RawMessage(`{"port":10443}`)); err != nil {
		t.Fatal(err)
	}

	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name:                   "AutoChain",
		ServiceServerID:        b1,
		ServiceProtocol:        shared.ProtocolVLESS,
		ServiceConfig:          vcJSON,
		EndpointID:             ep.ID,
		ServiceUUID:            "11111111-1111-1111-1111-111111111111",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: a1, Role: store.HopRoleEntry, Transport: "direct"},
			{ServerID: b1, Role: store.HopRoleExit, Transport: ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, dep.ChainID, store.ChainStatusApplying, "", store.ChainStatusPending); err != nil {
		t.Fatal(err)
	}
	for _, h := range dep.Hops {
		_ = st.SetChainHopStatus(ctx, h.HopID, store.HopStatusActive, "")
	}
	if err := st.PublishChainRevision(ctx, dep.RevisionID, false, store.ChainStatusApplying); err != nil {
		t.Fatal(err)
	}

	if _, err := st.ReplaceChainHopGraph(ctx, dep.ChainID, 0, []store.ChainHopGraphNode{
		{Kind: store.HopGraphKindServer, ServerID: a1, IsRoot: true, ListenPort: 10443},
		{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectSelf, Action: store.TriggerActionBanSuspected},
		{Kind: store.HopGraphKindServer, ServerID: a2, ListenPort: 10444},
	}, []store.ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceChainHopGraph(ctx, dep.ChainID, 1, []store.ChainHopGraphNode{
		{Kind: store.HopGraphKindServer, ServerID: b1, IsRoot: true, ListenPort: 20443},
		{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectPrev, Action: store.TriggerActionSegmentTimeout},
		{Kind: store.HopGraphKindServer, ServerID: b2, ListenPort: 20444},
	}, []store.ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}); err != nil {
		t.Fatal(err)
	}

	return srv, st, req, dep.ChainID, a1, a2, b1, b2
}
