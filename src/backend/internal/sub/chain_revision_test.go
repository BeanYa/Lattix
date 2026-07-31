package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestSubscriptionUsesPublishedRevisionWhileEditApplies(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	entryID, _ := st.CreateServer(ctx, "published-entry", "old.example.com", "tok-entry", store.MachineTypeDirect, "", "", "US", "")
	desiredEntryID, _ := st.CreateServer(ctx, "desired-entry", "new.example.com", "tok-new", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "tok-exit", store.MachineTypeDirect, "", "", "JP", "")

	publishedConfig, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	desiredConfig, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolSocks, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "published-node", exitID, shared.ProtocolVLESS, nil, publishedConfig)
	chainID, _ := st.InsertChain(ctx, "published-chain")
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 10001, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)

	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20001, Network: shared.NetworkTCP,
		PublicKey: "published-key", ShortID: "published-short", ServerName: "published.example.com"})
	published, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "published-chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: publishedConfig, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 10001},
			{HopID: exitHopID, ServerID: exitID, Role: store.HopRoleExit, ForwardPort: 20001},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, published.ID, false); err != nil {
		t.Fatal(err)
	}

	desiredHopID, _ := st.NextChainHopID(ctx, chainID, desiredEntryID)
	desired, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "desired-chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: desiredConfig, TrafficMultiplierMilli: 2000,
		Hops: []store.ChainRevisionHop{
			{HopID: desiredHopID, ServerID: desiredEntryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 10002},
			{HopID: exitHopID, ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceWorkingChainTopology(ctx, desired, shared.ProtocolSocks, nil, true); err != nil {
		t.Fatal(err)
	}

	chain, _ := st.ChainByID(ctx, chainID)
	item, err := New(st, nil, nil).chainSubscriptionItem(httptest.NewRequest("GET", "/sub/test", nil), *chain, map[int64]bool{nodeID: true})
	if err != nil {
		t.Fatal(err)
	}
	if item.node.Name != "published-chain" || item.node.Protocol != shared.ProtocolVLESS {
		t.Fatalf("subscription node = name %q protocol %q", item.node.Name, item.node.Protocol)
	}
	if item.node.ServerAddress != "old.example.com" || item.rc.Port != 10001 || item.rc.PublicKey != "published-key" {
		t.Fatalf("subscription endpoint/config = address %q realized %+v", item.node.ServerAddress, item.rc)
	}
	compiled, err := New(st, nil, nil).compileNodes(ctx, []proxyItem{*item}, "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0].CountryCode != "JP" {
		t.Fatalf("compiled chain region = %+v, want exit country JP", compiled)
	}
}

func TestSharedEndpointSubscriptionUsesAssignmentCredential(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "token-entry", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "token-exit", store.MachineTypeDirect, "", "", "JP", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "shared", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "global-user-uuid", "sub", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := st.UserByID(ctx, userID)
	items := New(st, nil, nil).subscriptionItems(httptest.NewRequest("GET", "/sub/sub", nil), user, nil)
	if len(items) != 1 || items[0].credential != added[0].AccessUUID || items[0].credential == user.UUID {
		t.Fatalf("subscription credential = %+v", items)
	}
	if items[0].node.ServerAddress != "entry.example.com" || items[0].node.ServerID != exitID {
		t.Fatalf("shared subscription endpoint = address %q region server %d", items[0].node.ServerAddress, items[0].node.ServerID)
	}
	compiled, err := New(st, nil, nil).compileNodes(ctx, items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0].CountryCode != "JP" ||
		compiled[0].Clash.UUID != added[0].AccessUUID || compiled[0].Singbox.UUID != added[0].AccessUUID ||
		!strings.Contains(compiled[0].QuanX, added[0].AccessUUID) {
		t.Fatalf("compiled shared endpoint = %+v", compiled)
	}
	links, err := renderLinks(items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(links)))
	if err != nil || !strings.Contains(string(decoded), added[0].AccessUUID) {
		t.Fatalf("shared links credential = %q, err %v", decoded, err)
	}
	for label, lookup := range map[string]func(context.Context) ([]int64, error){
		"chain": func(ctx context.Context) ([]int64, error) {
			return st.SubscriptionUserIDsForChain(ctx, deployment.ChainID)
		},
		"endpoint": func(ctx context.Context) ([]int64, error) { return st.SubscriptionUserIDsForEndpoint(ctx, endpoint.ID) },
		"entry":    func(ctx context.Context) ([]int64, error) { return st.SubscriptionUserIDsForServer(ctx, entryID) },
		"exit":     func(ctx context.Context) ([]int64, error) { return st.SubscriptionUserIDsForServer(ctx, exitID) },
	} {
		ids, err := lookup(ctx)
		if err != nil || len(ids) != 1 || ids[0] != userID {
			t.Fatalf("%s affected users = %v, err %v", label, ids, err)
		}
	}
	if err := st.SetUserDisabled(ctx, userID, true); err != nil {
		t.Fatal(err)
	}
	disabled, _ := st.UserByID(ctx, userID)
	if items := New(st, nil, nil).subscriptionItems(httptest.NewRequest("GET", "/sub/sub", nil), disabled, nil); len(items) != 0 {
		t.Fatalf("disabled user subscription contains %d items", len(items))
	}
}
