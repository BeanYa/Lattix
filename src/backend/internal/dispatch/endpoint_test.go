package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func createDirectSharedChain(t *testing.T, st *store.Store, serverID, endpointID int64, name string) store.InitialChainDeploymentResult {
	t.Helper()
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	deployment, err := st.CreateInitialChainDeployment(context.Background(), store.InitialChainDeployment{
		Name: name, ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpointID, ServiceUUID: "service-" + name,
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: serverID, Role: store.HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(context.Background(), deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func latestEndpointPayload(t *testing.T, st *store.Store) shared.ApplySharedEndpointPayload {
	t.Helper()
	commands, err := st.CommandsByType(context.Background(), shared.TypeApplySharedEndpoint)
	if err != nil || len(commands) == 0 {
		t.Fatalf("shared endpoint commands: len=%d err=%v", len(commands), err)
	}
	var payload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(commands[len(commands)-1].Data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestReconcileSharedEndpointGroupsUsersByChain(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	chainA := createDirectSharedChain(t, st, serverID, endpoint.ID, "a")
	chainB := createDirectSharedChain(t, st, serverID, endpoint.ID, "b")
	userA, _ := st.InsertUser(ctx, "user-a", "global-a", "sub-a", nil)
	userB, _ := st.InsertUser(ctx, "user-b", "global-b", "sub-b", nil)
	if _, _, err := st.SetUserChains(ctx, userA, []int64{chainA.ChainID, chainB.ChainID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userB, []int64{chainA.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if len(payload.Clients) != 3 || len(payload.Routes) != 2 {
		t.Fatalf("payload clients/routes = %d/%d", len(payload.Clients), len(payload.Routes))
	}
	usersByChain := map[int64]int{}
	for _, route := range payload.Routes {
		usersByChain[route.ChainID] = len(route.Users)
	}
	if usersByChain[chainA.ChainID] != 2 || usersByChain[chainB.ChainID] != 1 {
		t.Fatalf("route user counts = %+v", usersByChain)
	}

	if err := st.SetUserDisabled(ctx, userB, true); err != nil {
		t.Fatal(err)
	}
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload = latestEndpointPayload(t, st)
	if len(payload.Clients) != 2 {
		t.Fatalf("disabled user still present: %+v", payload.Clients)
	}
}

func TestPublishReconcilesPreviousAndNewSharedEndpoints(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	configB := json.RawMessage(`{"protocol":"vless","port":8443,"template":{}}`)
	endpointA, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", configA)
	endpointB, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 8443, "profile-b", configB)
	deployment := createDirectSharedChain(t, st, serverID, endpointA.ID, "move")
	userID, _ := st.InsertUser(ctx, "user", "global", "sub", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID}); err != nil {
		t.Fatal(err)
	}
	oldRevision, _ := st.PublishedChainRevision(ctx, deployment.ChainID)
	desiredSnapshot := oldRevision.Snapshot
	desiredSnapshot.EndpointID = endpointB.ID
	_, err = st.CreateChainRevision(ctx, deployment.ChainID, desiredSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	hops, _ := st.ChainHops(ctx, deployment.ChainID)
	node, _ := st.NodeByID(ctx, deployment.NodeID)
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	d.publishDesiredRevision(ctx, deployment.ChainID, hops, *node)

	commands, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(commands) != 2 {
		t.Fatalf("endpoint reconcile commands: len=%d err=%v", len(commands), err)
	}
	seen := map[int64]bool{}
	for _, command := range commands {
		var payload shared.ApplySharedEndpointPayload
		if err := json.Unmarshal(command.Data, &payload); err != nil {
			t.Fatal(err)
		}
		seen[payload.EndpointID] = true
	}
	if !seen[endpointA.ID] || !seen[endpointB.ID] {
		t.Fatalf("reconciled endpoints = %+v", seen)
	}
}
