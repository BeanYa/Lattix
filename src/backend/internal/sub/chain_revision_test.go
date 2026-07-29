package sub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "tok-exit", store.MachineTypeDirect, "", "", "US", "")

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
}
