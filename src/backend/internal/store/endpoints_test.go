package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lattix/shared"
)

func TestSharedEndpointReuseAndAssignmentIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || !created {
		t.Fatalf("create endpoint: created=%v err=%v", created, err)
	}
	reused, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || created || reused.ID != endpoint.ID {
		t.Fatalf("reuse endpoint: got=%+v created=%v err=%v", reused, created, err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", config); !errors.Is(err, ErrEndpointConflict) {
		t.Fatalf("incompatible endpoint error = %v", err)
	}
	second, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 8443, "profile-a", config)
	if err != nil || !created || second.ID == endpoint.ID || second.Port != 8443 {
		t.Fatalf("explicit second port: endpoint=%+v created=%v err=%v", second, created, err)
	}

	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	added, removed, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 1 || len(removed) != 0 {
		t.Fatalf("first assignment: added=%+v removed=%+v err=%v", added, removed, err)
	}
	firstUUID := added[0].AccessUUID
	added, removed, err = st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 0 || len(removed) != 0 {
		t.Fatalf("idempotent assignment: added=%+v removed=%+v err=%v", added, removed, err)
	}
	assignments, _ := st.UserChainAssignments(ctx, userID)
	if len(assignments) != 1 || assignments[0].AccessUUID != firstUUID {
		t.Fatalf("assignment credential changed: %+v", assignments)
	}
}

func TestAccessTrafficIsAttributedOnceToUserAndChain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	endpoint, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1500,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil {
		t.Fatal(err)
	}
	identity := "access:" + jsonNumber(added[0].ID)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	counters := []TrafficCounterSnapshot{
		{User: identity, Up: 100, Down: 200},
		{User: "tunnel:service-uuid", Up: 100, Down: 200},
		{EndpointID: endpoint.ID, Up: 100, Down: 200},
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1", counters, now); err != nil {
		t.Fatal(err)
	}
	traffic, _ := st.UserTraffic(ctx, "user-uuid")
	if traffic.Up != 100 || traffic.Down != 200 {
		t.Fatalf("user traffic = %+v", traffic)
	}
	totals, _ := st.ChainTrafficTotals(ctx, deployment.ChainID)
	if len(totals) != 1 || totals[0].HopID != 0 || totals[0].RawUp != 100 || totals[0].EffectiveUp != 150 {
		t.Fatalf("chain traffic = %+v", totals)
	}
	var endpointUp, endpointDown int64
	if err := st.db.QueryRow(`SELECT up, down FROM endpoint_traffic_totals WHERE endpoint_id=?`, endpoint.ID).
		Scan(&endpointUp, &endpointDown); err != nil || endpointUp != 100 || endpointDown != 200 {
		t.Fatalf("endpoint traffic = %d/%d err=%v", endpointUp, endpointDown, err)
	}
}

func TestValidateAssignableChainsRejectsLegacyChain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, err := st.InsertChain(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ValidateAssignableChains(ctx, []int64{chainID}); err == nil {
		t.Fatal("legacy chain unexpectedly assignable")
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
