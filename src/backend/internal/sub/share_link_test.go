package sub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// shareLinkChainFixture 建一条已发布修订的非共享入口链：入口 old.example.com:10001，
// 出口 vless 业务节点（realized 参数随快照发布）。
func shareLinkChainFixture(t *testing.T, ctx context.Context, st *store.Store) (chainID, nodeID int64) {
	t.Helper()
	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "old.example.com", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "tok-exit", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	nodeID, err = st.InsertNode(ctx, "exit-node", exitID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err = st.InsertChain(ctx, "share-chain")
	if err != nil {
		t.Fatal(err)
	}
	entryHopID, err := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 10001, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainServiceNode(ctx, chainID, nodeID); err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20001, Network: shared.NetworkTCP,
		PublicKey: "pub-key", ShortID: "ab", ServerName: "www.example.com"})
	published, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "share-chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 10001},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, published.ID, false); err != nil {
		t.Fatal(err)
	}
	return chainID, nodeID
}

func TestUserChainShareLinkMirrorsSubscription(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, nodeID := shareLinkChainFixture(t, ctx, st)
	userID, err := st.InsertUser(ctx, "user", "user-uuid-1111", "sub", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}

	srv := New(st, nil, nil)
	link, err := srv.UserChainShareLink(ctx, userID, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if link.Protocol != shared.ProtocolVLESS || link.Address != "old.example.com" || link.Port != 10001 {
		t.Fatalf("share link endpoint = %+v, want vless @ old.example.com:10001", link)
	}
	for _, want := range []string{"vless://user-uuid-1111@old.example.com:10001?", "pbk=pub-key", "security=reality"} {
		if !strings.Contains(link.Link, want) {
			t.Errorf("share link %q missing %q", link.Link, want)
		}
	}
}

func TestUserChainShareLinkRejectsUnassignedAndUnpublished(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, nodeID := shareLinkChainFixture(t, ctx, st)
	otherID, err := st.InsertChain(ctx, "unpublished")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "user-uuid-2222", "sub", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户未分配出口节点：不允许导出（与订阅一致）。
	srv := New(st, nil, nil)
	if _, err := srv.UserChainShareLink(ctx, userID, chainID); err == nil {
		t.Fatal("unassigned user chain share link = nil error, want error")
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.UserChainShareLink(ctx, userID, otherID); err == nil {
		t.Fatal("unpublished chain share link = nil error, want error")
	}
}

func TestUserNodeShareLink(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, exitNodeID := shareLinkChainFixture(t, ctx, st)
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "standalone", Address: "1.2.3.4", BootstrapToken: "tok-standalone", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolTrojan, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "direct-node", serverID, shared.ProtocolTrojan, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP,
		SNI: "sni.example.com", Fingerprint: shared.FingerprintChrome})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "user-uuid-3333", "sub", nil)
	if err != nil {
		t.Fatal(err)
	}

	srv := New(st, nil, nil)
	// 未分配：拒绝。
	if _, err := srv.UserNodeShareLink(ctx, userID, nodeID); err == nil {
		t.Fatal("unassigned node share link = nil error, want error")
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	link, err := srv.UserNodeShareLink(ctx, userID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if link.Protocol != shared.ProtocolTrojan || link.Address != "1.2.3.4" || link.Port != 8443 {
		t.Fatalf("node share link = %+v, want trojan @ 1.2.3.4:8443", link)
	}
	if !strings.HasPrefix(link.Link, "trojan://user-uuid-3333@1.2.3.4:8443?") {
		t.Errorf("node share link = %q", link.Link)
	}
	// 链路出口节点不单独导出。
	if _, err := srv.UserNodeShareLink(ctx, userID, exitNodeID); err == nil {
		t.Fatal("exit node share link = nil error, want error")
	}
}

func TestUserShareLinkRejectsInactiveUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, _ := shareLinkChainFixture(t, ctx, st)
	expired := time.Now().Add(-time.Hour)
	userID, err := st.InsertUser(ctx, "expired", "user-uuid-4444", "sub", &expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(st, nil, nil).UserChainShareLink(ctx, userID, chainID); err == nil {
		t.Fatal("expired user share link = nil error, want error")
	}
}
